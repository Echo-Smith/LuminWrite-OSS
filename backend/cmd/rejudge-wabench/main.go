// Command rejudge-wabench is an idempotent maintenance entry that re-runs the
// blind judge against the FROZEN output_text of a finished WABench run for
// cases whose review is missing or invalid (judge infrastructure failures:
// corrupt judge JSON, invalid enum values, exhausted repair retries).
//
// It only inserts new review rows whose evidence carries the "rejudge"
// metadata marker; it never rewrites wabench_outputs and never touches the
// original run reviews. Re-running it is safe: outputs that already have a
// rejudge review are skipped, and outputs that fail again stay on the
// worklist for the next invocation.
//
// Usage:
//
//	go run ./cmd/rejudge-wabench --run-id ablation-run-ablation-A-baseline
//	go run ./cmd/rejudge-wabench --run-id <runID> --dry-run   # list worklist only
//
// Environment (same LLM contract as cmd/run-ablation):
//
//	DATABASE_URL / TEST_DATABASE_URL   Postgres DSN
//	LLM_API_KEY                        required
//	LLM_BASE_URL / LLM_MODEL           endpoint + judge model overrides
//	LLM_TIMEOUT_SECONDS                per-request timeout (set generously for long outputs)
//	LLM_DISABLE_THINKING               =1 to suppress the thinking parameter
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/services"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
)

func main() {
	runID := flag.String("run-id", "", "WABench run_id whose judge-failed cases should be rejudged (required)")
	dryRun := flag.Bool("dry-run", false, "only list the rejudge worklist, do not call the judge or write reviews")
	flag.Parse()
	_ = godotenv.Load()

	if *runID == "" {
		log.Fatal("--run-id is required (e.g. --run-id ablation-run-ablation-A-baseline)")
	}

	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		dbURL = os.Getenv("DATABASE_URL")
	}
	if dbURL == "" {
		dbURL = "postgres://postgres:postgres@localhost:5432/writing_agent_v2?sslmode=disable"
	}
	db, err := database.NewPostgres(dbURL, 5, 2)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()

	repo := database.NewWABenchRepo(db)

	// Safety: never rejudge a run that is still pending/running — frozen
	// output rows must be final before reviews are appended.
	run, err := repo.GetRun(context.Background(), *runID)
	if err != nil {
		log.Fatalf("load run %s: %v", *runID, err)
	}
	if run.Status != "completed" && run.Status != "failed" {
		log.Fatalf("run %s has status %q; only completed/failed runs can be rejudged", *runID, run.Status)
	}

	candidates, err := repo.ListRejudgeableOutputs(context.Background(), *runID)
	if err != nil {
		log.Fatalf("list rejudgeable outputs: %v", err)
	}
	fmt.Printf("run %s (status=%s): %d rejudgeable output(s)\n", *runID, run.Status, len(candidates))
	for _, item := range candidates {
		fmt.Printf("  output=%s case=%s chars=%d failures=%d\n",
			item.OutputID, item.CaseID, len([]rune(item.OutputText)), len(item.Failures))
	}
	if *dryRun || len(candidates) == 0 {
		return
	}

	judge := newJudgeFromEnv()
	svc := services.NewWABenchRejudgeService(repo, judge)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Hour)
	defer cancel()
	outcomes, err := svc.RejudgeJudgeFailures(ctx, *runID)
	if err != nil {
		log.Fatalf("rejudge run %s: %v", *runID, err)
	}

	rejudged, failed := 0, 0
	for _, outcome := range outcomes {
		switch outcome.Status {
		case "rejudged":
			rejudged++
		default:
			failed++
		}
		detail, _ := json.Marshal(outcome.Detail)
		fmt.Printf("%s output=%s case=%s score=%.1f detail=%s\n",
			outcome.Status, outcome.OutputID, outcome.CaseID, outcome.WeightedScore, string(detail))
	}
	fmt.Printf("done: %d rejudged, %d still failed\n", rejudged, failed)
	if failed > 0 {
		os.Exit(1)
	}
}

// newJudgeFromEnv mirrors cmd/run-ablation's LLM construction (fail-closed on
// LLM_API_KEY, generous timeout for long completions).
func newJudgeFromEnv() *services.LLMWABenchJudge {
	apiKey := os.Getenv("LLM_API_KEY")
	if apiKey == "" {
		log.Fatal("LLM_API_KEY must be set in the environment")
	}
	llmTimeout := 120 * time.Second
	if raw := os.Getenv("LLM_TIMEOUT_SECONDS"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			log.Fatalf("invalid LLM_TIMEOUT_SECONDS %q: want positive integer seconds", raw)
		}
		llmTimeout = time.Duration(n) * time.Second
	}
	llm := tools.NewLLMClient(
		envDefault("LLM_BASE_URL", "https://api.xiaomimimo.com/v1"),
		apiKey,
		envDefault("LLM_MODEL", "mimo-v2.5"),
		32768, 0.7, llmTimeout,
	)
	if os.Getenv("LLM_DISABLE_THINKING") == "1" {
		llm.DisableThinkingParam()
		log.Println("thinking parameter suppressed (LLM_DISABLE_THINKING=1)")
	}
	return services.NewLLMWABenchJudge(llm)
}

func envDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/services"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
)

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:postgres@localhost:5432/writing_agent_v2?sslmode=disable"
	}

	db, err := database.NewPostgres(dbURL, 10, 4)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()

	repo := database.NewWABenchRepo(db)

	// 凭据只从环境注入：LLM_API_KEY 必填（fail-closed），绝不定死在源码里。
	apiKey := os.Getenv("LLM_API_KEY")
	if apiKey == "" {
		log.Fatal("LLM_API_KEY must be set in the environment")
	}
	llm := tools.NewLLMClient(
		envDefault("LLM_BASE_URL", "https://api.xiaomimimo.com/v1"),
		apiKey,
		envDefault("LLM_MODEL", "mimo-v2.5"),
		32768, 0.7, 120*time.Second,
	)

	adapter := services.NewHarnessWABenchExecutor(llm, nil, nil, profile.NewLoader(), nil, nil)
	judge := services.NewLLMWABenchJudge(llm)
	svc := services.NewWABenchEvaluationService(repo, adapter, judge)

	runIDs := []string{
		"ablation-run-ablation-A-baseline",
		"ablation-run-ablation-B-context",
		"ablation-run-ablation-C-context-memory",
		"ablation-run-ablation-D-full",
	}
	if rid := os.Getenv("RUN_ID"); rid != "" {
		runIDs = []string{rid}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()

	var wg sync.WaitGroup
	errs := make([]error, len(runIDs))
	for i, rid := range runIDs {
		wg.Add(1)
		go func(i int, rid string) {
			defer wg.Done()
			fmt.Printf("Starting run %s...\n", rid)
			if err := svc.ExecuteRun(ctx, rid); err != nil {
				errs[i] = err
				fmt.Printf("FAILED %s: %v\n", rid, err)
			} else {
				fmt.Printf("COMPLETED %s\n", rid)
			}
		}(i, rid)
	}
	wg.Wait()
	failed := false
	for i, err := range errs {
		if err != nil {
			failed = true
			fmt.Printf("  %s: %v\n", runIDs[i], err)
		}
	}
	if failed {
		os.Exit(1)
	}
	fmt.Println("ALL RUNS COMPLETED")
}

func envDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

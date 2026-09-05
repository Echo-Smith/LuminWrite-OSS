package main

// memory-forget runs the ProjectMemory forgetting sweep (roadmap §12, V2.9
// item 12) for one project. Default mode is a dry-run preview that counts
// what the policy would forget without touching anything; --apply executes
// the sweep and writes the append-only forgetting log. Canon facts are never
// in scope — the policy has no branch that touches them.
//
//	memory-forget -policy-dump
//	memory-forget -project prj_x [-apply] [-policy file.json] [-operator id] [-log N]
//
// DATABASE_URL is required for every mode except -policy-dump.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/projectmemory"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

func main() {
	project := flag.String("project", "", "project id (prj_*) to sweep")
	apply := flag.Bool("apply", false, "apply the sweep; default is a dry-run preview")
	policyPath := flag.String("policy", "", "optional forget policy JSON (default: the shipped policy)")
	operator := flag.String("operator", "", "operator identity recorded on the sweep")
	logRows := flag.Int("log", 0, "print the N most recent forgetting log rows for the project")
	dumpPolicy := flag.Bool("policy-dump", false, "print the shipped policy JSON and hash, then exit")
	flag.Parse()
	if err := run(context.Background(), *project, *apply, *policyPath, *operator, *logRows, *dumpPolicy); err != nil {
		fmt.Fprintln(os.Stderr, "memory-forget:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, project string, apply bool, policyPath, operator string, logRows int, dumpPolicy bool) error {
	policy := projectmemory.DefaultForgetPolicy()
	if strings.TrimSpace(policyPath) != "" {
		payload, err := os.ReadFile(policyPath)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(payload, &policy); err != nil {
			return fmt.Errorf("decode policy: %w", err)
		}
	}
	hash, err := policy.Hash()
	if err != nil {
		return err
	}
	if dumpPolicy {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(policy); err != nil {
			return err
		}
		fmt.Printf("policy_version=%d policy_hash=%s\n", projectmemory.ForgetPolicyVersion, hash)
		return nil
	}
	if strings.TrimSpace(project) == "" {
		return fmt.Errorf("--project is required (or use -policy-dump)")
	}
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	db, err := database.NewPostgres(databaseURL, 3, 1)
	if err != nil {
		return err
	}
	defer db.Close()
	store, err := writingstore.New(db)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if !apply {
		summary, err := store.PreviewForgettingPolicy(ctx, project, policy, now)
		if err != nil {
			return err
		}
		return report(summary, "preview", hash)
	}
	actorID := strings.TrimSpace(operator)
	if actorID == "" {
		actorID = "memory-forget-cli"
	}
	summary, err := store.ApplyForgettingPolicy(ctx, project, policy, now,
		writingstore.Actor{Type: writingstore.ActorPolicy, ID: actorID})
	if err != nil {
		return err
	}
	if err := report(summary, "applied", hash); err != nil {
		return err
	}
	if logRows > 0 {
		return printLog(ctx, store, project, logRows)
	}
	return nil
}

func report(summary writingstore.ForgettingSummary, mode, hash string) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	fmt.Printf("forgetting %s: sweep=%s policy_version=%d policy_hash=%s\n", mode, summary.SweepID, summary.PolicyVersion, hash)
	return encoder.Encode(summary)
}

func printLog(ctx context.Context, store *writingstore.Store, project string, limit int) error {
	records, err := store.ListForgettingLog(ctx, project, limit)
	if err != nil {
		return err
	}
	for _, record := range records {
		fmt.Printf("%s %s %s %s: %s -> %s rule=%s by=%s/%s\n", record.AppliedAt.Format(time.RFC3339),
			record.SweepID, record.ObjectTable, record.ObjectID, record.FromStatus, record.ToStatus,
			record.Rule, record.AppliedByType, record.AppliedByID)
	}
	return nil
}

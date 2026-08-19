package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/config"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
)

type importSummary struct {
	SchemaVersion   string         `json:"schema_version"`
	Partition       string         `json:"partition"`
	SuitesProcessed int            `json:"suites_processed"`
	CasesProcessed  int            `json:"cases_processed"`
	WarningCounts   map[string]int `json:"warning_counts"`
	StartedAt       time.Time      `json:"started_at"`
	CompletedAt     time.Time      `json:"completed_at"`
}

func main() {
	partition := flag.String("partition", "development", "WABench partition for imported legacy suites")
	apply := flag.Bool("apply", false, "required acknowledgement before writing migration candidates")
	flag.Parse()
	if !*apply {
		fmt.Fprintln(os.Stderr, "refusing to write without --apply; legacy rows remain unchanged")
		os.Exit(2)
	}

	cfg := config.Load()
	db, err := database.NewPostgres(cfg.Database.URL, cfg.Database.MaxOpenConns, cfg.Database.MaxIdleConns)
	if err != nil {
		fail(err)
	}
	defer db.Close()
	if err := database.Migrate(db); err != nil {
		fail(fmt.Errorf("apply WABench migration: %w", err))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	report, err := database.NewWABenchRepo(db).ImportLegacyEvaluations(ctx, database.LegacyImportOptions{
		Partition: *partition, Visibility: "private", PrivacyLevel: "private",
	})
	if err != nil {
		fail(err)
	}

	summary := importSummary{
		SchemaVersion: report.SchemaVersion, Partition: report.Partition,
		SuitesProcessed: report.SuitesProcessed, CasesProcessed: report.CasesProcessed,
		WarningCounts: map[string]int{}, StartedAt: report.StartedAt, CompletedAt: report.CompletedAt,
	}
	for _, warning := range report.Warnings {
		summary.WarningCounts[warning.Code]++
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(summary); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "wabench import failed:", err)
	os.Exit(1)
}

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/config"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
)

func readJSON(path string, target interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func readJSONL[T any](path string) ([]T, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	result := []T{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		var item T
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, scanner.Err()
}

func main() {
	batchPath := flag.String("batch", "", "frozen regression batch JSON")
	casesPath := flag.String("cases", "", "portable WABench cases JSONL")
	fixturesPath := flag.String("fixtures", "", "portable WABench fixtures JSONL")
	privateHoldoutPath := flag.String("private-holdout", "", "private redacted Holdout JSONL; validated in memory and never copied into WABench tables")
	apply := flag.Bool("apply", false, "required acknowledgement before database writes")
	flag.Parse()
	if !*apply || *batchPath == "" {
		fmt.Fprintln(os.Stderr, "--apply and --batch are required")
		os.Exit(2)
	}
	var batch database.WABenchFrozenBatch
	if err := readJSON(*batchPath, &batch); err != nil {
		fail(err)
	}
	cfg := config.Load()
	db, err := database.NewPostgres(cfg.Database.URL, cfg.Database.MaxOpenConns, cfg.Database.MaxIdleConns)
	if err != nil {
		fail(err)
	}
	defer db.Close()
	if err := database.Migrate(db); err != nil {
		fail(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	repo := database.NewWABenchRepo(db)
	var result *database.WABenchBatchImportResult
	if batch.Visibility == "private" {
		if *privateHoldoutPath == "" {
			fail(fmt.Errorf("--private-holdout is required for a private batch"))
		}
		rows, readErr := readJSONL[database.WABenchPrivateHoldoutRecord](*privateHoldoutPath)
		if readErr != nil {
			fail(readErr)
		}
		result, err = repo.ImportFrozenPrivateBatch(ctx, batch, rows)
	} else {
		if *casesPath == "" || *fixturesPath == "" {
			fail(fmt.Errorf("--cases and --fixtures are required for a public batch"))
		}
		cases, readErr := readJSONL[database.WABenchPortableCase](*casesPath)
		if readErr != nil {
			fail(readErr)
		}
		fixtures, readErr := readJSONL[database.WABenchPortableFixture](*fixturesPath)
		if readErr != nil {
			fail(readErr)
		}
		result, err = repo.ImportFrozenPublicBatch(ctx, batch, cases, fixtures)
	}
	if err != nil {
		fail(err)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		fail(err)
	}
}

func fail(err error) { fmt.Fprintln(os.Stderr, "WABench batch import failed:", err); os.Exit(1) }

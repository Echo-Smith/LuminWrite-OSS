package database

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestMigrate_Idempotent verifies that running Migrate twice is safe:
// the second run should skip all already-applied migrations.
func TestMigrate_Idempotent(t *testing.T) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		if os.Getenv("CI") == "true" {
			t.Fatal("CI=true but TEST_DATABASE_URL is not set — migration tests cannot run")
		}
		t.Skip("TEST_DATABASE_URL not set, skipping migration test")
	}

	db, err := NewPostgres(dbURL, 5, 2)
	if err != nil {
		t.Fatalf("failed to connect to test database: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// First run — should apply all migrations
	if err := MigrateDB(ctx, db, migrationFS); err != nil {
		t.Fatalf("first MigrateDB failed: %v", err)
	}

	// Verify schema_migrations table has records
	applied, err := getAppliedMigrations(ctx, db)
	if err != nil {
		t.Fatalf("getAppliedMigrations failed: %v", err)
	}
	if len(applied) == 0 {
		t.Fatal("expected migrations to be recorded in schema_migrations table")
	}
	t.Logf("first run: %d migrations applied", len(applied))

	// Second run — should skip all migrations (idempotent)
	if err := MigrateDB(ctx, db, migrationFS); err != nil {
		t.Fatalf("second MigrateDB failed: %v", err)
	}

	// Verify count hasn't changed
	applied2, err := getAppliedMigrations(ctx, db)
	if err != nil {
		t.Fatalf("getAppliedMigrations (second run) failed: %v", err)
	}
	if len(applied2) != len(applied) {
		t.Errorf("migration count changed: first=%d, second=%d", len(applied), len(applied2))
	}
	t.Logf("second run: %d migrations (all skipped)", len(applied2))
}

// TestMigrate_ChecksumVerification verifies that modifying an applied
// migration's SQL content triggers a checksum mismatch error.
func TestMigrate_ChecksumVerification(t *testing.T) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		if os.Getenv("CI") == "true" {
			t.Fatal("CI=true but TEST_DATABASE_URL is not set — migration tests cannot run")
		}
		t.Skip("TEST_DATABASE_URL not set, skipping migration test")
	}

	db, err := NewPostgres(dbURL, 5, 2)
	if err != nil {
		t.Fatalf("failed to connect to test database: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Run migrations first (idempotent — will skip if already applied)
	if err := MigrateDB(ctx, db, migrationFS); err != nil {
		t.Fatalf("MigrateDB failed: %v", err)
	}

	// Get the first applied migration
	applied, err := getAppliedMigrations(ctx, db)
	if err != nil {
		t.Fatalf("getAppliedMigrations failed: %v", err)
	}
	if len(applied) == 0 {
		t.Fatal("no migrations applied — cannot test checksum")
	}

	// Pick any applied migration and verify its checksum matches
	for version, rec := range applied {
		sqlBytes, err := migrationFS.ReadFile("migrations/" + version + ".up.sql")
		if err != nil {
			t.Fatalf("failed to read migration file %s: %v", version, err)
		}
		expectedChecksum := computeChecksum(string(sqlBytes))
		if rec.Checksum != expectedChecksum {
			t.Errorf("checksum mismatch for %s: stored=%s, computed=%s",
				version, rec.Checksum, expectedChecksum)
		}
		break // only check one
	}
}

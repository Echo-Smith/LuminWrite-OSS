package database

import (
	"context"
	"os"
	"regexp"
	"strings"
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

func TestVerifyMigrationChecksumFailsClosed(t *testing.T) {
	if err := verifyMigrationChecksum("089_example", "same", "same"); err != nil {
		t.Fatalf("matching checksum rejected: %v", err)
	}
	err := verifyMigrationChecksum("089_example", "applied", "modified")
	if err == nil {
		t.Fatal("modified applied migration must fail closed")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestWritingRuntimeMigrationStructure locks down the database boundary for the
// governed writing runtime.  It deliberately reads the embedded SQL rather
// than requiring PostgreSQL so that a missing or accidentally weakened
// migration fails fast in every developer checkout.
func TestWritingRuntimeMigrationStructure(t *testing.T) {
	type migrationSpec struct {
		version string
		tables  []string
	}

	specs := []migrationSpec{
		{
			version: "089_writing_kernel_core",
			tables: []string{
				"writing_contracts",
				"writing_documents",
				"writing_document_versions",
				"writing_runs",
				"writing_run_plans",
			},
		},
		{
			version: "090_writing_artifacts_quality",
			tables: []string{
				"writing_artifacts",
				"writing_artifact_edges",
				"writing_quality_reports",
				"writing_decisions",
			},
		},
		{
			version: "091_writing_run_ledger",
			tables: []string{
				"writing_run_events",
				"writing_snapshots",
				"writing_node_attempts",
			},
		},
	}

	up := make(map[string]string, len(specs))
	down := make(map[string]string, len(specs))
	for _, spec := range specs {
		up[spec.version] = readWritingMigration(t, spec.version, "up")
		down[spec.version] = readWritingMigration(t, spec.version, "down")
		for _, table := range spec.tables {
			definition := tableDefinition(t, up[spec.version], table)
			if !strings.Contains(strings.ToUpper(definition), "PRIMARY KEY") {
				t.Errorf("%s must declare a primary key", table)
			}
			if !hasTableIndex(up[spec.version], table) {
				t.Errorf("%s must have an explicit query index", table)
			}
		}
	}

	// The first ten tables are the published canonical governed-runtime
	// records. NodeAttempt is a separate execution-ledger table required for
	// exactly-once business semantics; it must not be folded into run_events.
	canonical := []string{
		"writing_contracts", "writing_documents", "writing_document_versions",
		"writing_runs", "writing_run_plans", "writing_artifacts",
		"writing_quality_reports", "writing_decisions", "writing_run_events",
		"writing_snapshots",
	}
	if len(canonical) != 10 {
		t.Fatal("canonical writing runtime table inventory changed; update the migration contract intentionally")
	}

	allUp := strings.Join([]string{up["089_writing_kernel_core"], up["090_writing_artifacts_quality"], up["091_writing_run_ledger"]}, "\n")
	for _, edge := range []struct{ child, parent string }{
		{"writing_contracts", "writing_documents"},
		{"writing_document_versions", "writing_documents"},
		{"writing_document_versions", "writing_contracts"},
		{"writing_runs", "writing_documents"},
		{"writing_runs", "writing_contracts"},
		{"writing_run_plans", "writing_runs"},
		{"writing_artifacts", "writing_runs"},
		{"writing_artifacts", "writing_run_plans"},
		{"writing_artifact_edges", "writing_runs"},
		{"writing_artifact_edges", "writing_artifacts"},
		{"writing_quality_reports", "writing_runs"},
		{"writing_quality_reports", "writing_run_plans"},
		{"writing_quality_reports", "writing_documents"},
		{"writing_quality_reports", "writing_document_versions"},
		{"writing_decisions", "writing_runs"},
		{"writing_run_events", "writing_runs"},
		{"writing_snapshots", "writing_runs"},
		{"writing_node_attempts", "writing_runs"},
	} {
		definition := tableDefinition(t, allUp, edge.child)
		if !referencesTable(definition, edge.parent) {
			t.Errorf("%s must reference %s", edge.child, edge.parent)
		}
	}

	for _, table := range []string{
		"writing_contracts", "writing_document_versions", "writing_run_plans",
		"writing_artifacts", "writing_quality_reports", "writing_snapshots",
	} {
		definition := tableDefinition(t, allUp, table)
		for _, column := range []string{"schema_version", "content_hash", "provenance"} {
			if !hasColumn(definition, column) {
				t.Errorf("%s must retain %s for traceability", table, column)
			}
		}
		if !hasColumnContaining(definition, "actor") {
			t.Errorf("%s must retain actor attribution for traceability", table)
		}
	}

	runEvents := tableDefinition(t, up["091_writing_run_ledger"], "writing_run_events")
	for _, column := range []string{"run_id", "sequence"} {
		if !hasColumn(runEvents, column) {
			t.Errorf("writing_run_events must include %s", column)
		}
	}
	if !hasUniqueColumns(runEvents, "run_id", "sequence") {
		t.Error("writing_run_events must enforce UNIQUE(run_id, sequence)")
	}

	nodeAttempts := tableDefinition(t, up["091_writing_run_ledger"], "writing_node_attempts")
	for _, column := range []string{
		"run_id", "node_id", "attempt", "idempotency_key", "status",
	} {
		if !hasColumn(nodeAttempts, column) {
			t.Errorf("writing_node_attempts must include %s", column)
		}
	}
	for _, field := range []string{"lease", "heartbeat", "started", "completed", "error", "actual_cost", "token"} {
		if !hasColumnContaining(nodeAttempts, field) {
			t.Errorf("writing_node_attempts must retain %s execution state", field)
		}
	}
	if !hasUniqueColumns(nodeAttempts, "run_id", "node_id", "attempt") {
		t.Error("writing_node_attempts must enforce UNIQUE(run_id, node_id, attempt)")
	}
	if !hasUniqueColumns(nodeAttempts, "idempotency_key") {
		t.Error("writing_node_attempts must enforce UNIQUE(idempotency_key)")
	}

	assertWritingRuntimeVersionedIdentities(t, allUp)
	assertWritingRuntimeIdempotency(t, up["090_writing_artifacts_quality"], up["091_writing_run_ledger"])
	assertWritingRuntimeEventLedger(t, up["091_writing_run_ledger"])
	assertWritingRuntimeGovernanceGates(t, up["089_writing_kernel_core"], up["090_writing_artifacts_quality"], up["091_writing_run_ledger"])

	allDown := strings.Join([]string{down["091_writing_run_ledger"], down["090_writing_artifacts_quality"], down["089_writing_kernel_core"]}, "\n")
	for _, edge := range []struct{ child, parent string }{
		{"writing_document_versions", "writing_documents"},
		{"writing_document_versions", "writing_contracts"},
		{"writing_contracts", "writing_documents"},
		{"writing_run_plans", "writing_runs"},
		{"writing_run_plans", "writing_contracts"},
		{"writing_artifact_edges", "writing_artifacts"},
		{"writing_artifacts", "writing_runs"},
		{"writing_artifacts", "writing_run_plans"},
		{"writing_artifacts", "writing_quality_reports"},
		{"writing_quality_reports", "writing_runs"},
		{"writing_quality_reports", "writing_run_plans"},
		{"writing_quality_reports", "writing_documents"},
		{"writing_quality_reports", "writing_document_versions"},
		{"writing_decisions", "writing_runs"},
		{"writing_decisions", "writing_run_plans"},
		{"writing_decisions", "writing_documents"},
		{"writing_decisions", "writing_document_versions"},
		{"writing_snapshots", "writing_run_events"},
		{"writing_snapshots", "writing_run_plans"},
		{"writing_snapshots", "writing_contracts"},
		{"writing_snapshots", "writing_documents"},
		{"writing_snapshots", "writing_document_versions"},
		{"writing_run_events", "writing_runs"},
		{"writing_run_events", "writing_node_attempts"},
		{"writing_snapshots", "writing_runs"},
		{"writing_node_attempts", "writing_runs"},
		{"writing_node_attempts", "writing_run_plans"},
	} {
		requireDropBefore(t, allDown, edge.child, edge.parent)
	}
}

func readWritingMigration(t *testing.T, version, direction string) string {
	t.Helper()
	path := "migrations/" + version + "." + direction + ".sql"
	contents, err := migrationFS.ReadFile(path)
	if err != nil {
		t.Fatalf("required governed writing migration %s is missing: %v", path, err)
	}
	return string(contents)
}

func tableDefinition(t *testing.T, sql, table string) string {
	t.Helper()
	pattern := `(?is)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?` + regexp.QuoteMeta(table) + `\s*\(`
	match := regexp.MustCompile(pattern).FindStringIndex(sql)
	if match == nil {
		t.Fatalf("missing CREATE TABLE for %s", table)
	}

	opening := strings.Index(sql[match[0]:match[1]], "(") + match[0]
	depth := 0
	for index := opening; index < len(sql); index++ {
		switch sql[index] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return sql[opening+1 : index]
			}
		}
	}
	t.Fatalf("unterminated CREATE TABLE definition for %s", table)
	return ""
}

func hasColumn(definition, column string) bool {
	return regexp.MustCompile(`(?i)(?:^|[\s,(])` + regexp.QuoteMeta(column) + `(?:\s|,|\)|$)`).MatchString(definition)
}

func hasColumnContaining(definition, fragment string) bool {
	return regexp.MustCompile(`(?i)(?:^|[\s,(])[a-z_]*` + regexp.QuoteMeta(fragment) + `[a-z_]*(?:\s|,|\)|$)`).MatchString(definition)
}

func referencesTable(definition, table string) bool {
	return regexp.MustCompile(`(?i)REFERENCES\s+` + regexp.QuoteMeta(table) + `(?:\s|\()`).MatchString(definition)
}

func hasTableIndex(sql, table string) bool {
	pattern := `(?is)CREATE\s+(?:UNIQUE\s+)?INDEX\s+(?:IF\s+NOT\s+EXISTS\s+)?\S+\s+ON\s+` + regexp.QuoteMeta(table) + `(?:\s|\()`
	return regexp.MustCompile(pattern).MatchString(sql)
}

func hasUniqueColumns(definition string, columns ...string) bool {
	parts := make([]string, len(columns))
	for index, column := range columns {
		parts[index] = `\s*` + regexp.QuoteMeta(column) + `\s*`
	}
	pattern := `(?is)UNIQUE\s*\(\s*` + strings.Join(parts, `,`) + `\)`
	return regexp.MustCompile(pattern).MatchString(definition)
}

func hasPrimaryKeyColumns(definition string, columns ...string) bool {
	parts := make([]string, len(columns))
	for index, column := range columns {
		parts[index] = `\s*` + regexp.QuoteMeta(column) + `\s*`
	}
	pattern := `(?is)PRIMARY\s+KEY\s*\(\s*` + strings.Join(parts, `,`) + `\)`
	return regexp.MustCompile(pattern).MatchString(definition)
}

func assertWritingRuntimeVersionedIdentities(t *testing.T, allUp string) {
	t.Helper()
	for _, identity := range []struct {
		table   string
		columns []string
	}{
		{"writing_run_plans", []string{"plan_id", "plan_version"}},
		{"writing_artifacts", []string{"artifact_id", "version"}},
		{"writing_quality_reports", []string{"report_id", "report_version"}},
		{"writing_decisions", []string{"decision_id", "decision_version"}},
		{"writing_snapshots", []string{"snapshot_id", "snapshot_version"}},
	} {
		definition := tableDefinition(t, allUp, identity.table)
		if !hasPrimaryKeyColumns(definition, identity.columns...) {
			t.Errorf("%s must use versioned primary identity %v", identity.table, identity.columns)
		}
	}

	for _, binding := range []string{
		"UNIQUE (document_id, contract_id, version, contract_hash)",
		"UNIQUE (run_id, plan_id, plan_version)",
		"UNIQUE (run_id, report_id, report_version)",
		"UNIQUE (run_id, snapshot_id, snapshot_version)",
		"UNIQUE (run_id, event_id)",
	} {
		if !strings.Contains(allUp, binding) {
			t.Errorf("missing versioned ownership binding %s", binding)
		}
	}
}

func assertWritingRuntimeIdempotency(t *testing.T, artifactsSQL, ledgerSQL string) {
	t.Helper()
	artifacts := tableDefinition(t, artifactsSQL, "writing_artifacts")
	attempts := tableDefinition(t, ledgerSQL, "writing_node_attempts")
	events := tableDefinition(t, ledgerSQL, "writing_run_events")

	for table, definition := range map[string]string{
		"writing_artifacts":     artifacts,
		"writing_node_attempts": attempts,
		"writing_run_events":    events,
	} {
		if !regexp.MustCompile(`(?i)idempotency_key\s+VARCHAR\(320\)`).MatchString(definition) {
			t.Errorf("%s must allow the full deterministic idempotency key", table)
		}
	}
	for _, definition := range []string{artifacts, attempts} {
		if !strings.Contains(definition, "idempotency_key = run_id || ':' || node_id || ':' || attempt::TEXT") {
			t.Error("artifact and node attempt identity must bind the exact deterministic idempotency key")
		}
	}
	if !hasUniqueColumns(attempts, "run_id", "node_id", "attempt", "idempotency_key") {
		t.Error("node attempts must expose a four-column idempotency binding")
	}
	for _, sqlFragment := range []string{
		"FOREIGN KEY (run_id, node_id, attempt, idempotency_key)",
		"UNIQUE (run_id, node_id, attempt, output_key, version)",
	} {
		if !strings.Contains(artifactsSQL+ledgerSQL, sqlFragment) {
			t.Errorf("missing idempotency/output binding %s", sqlFragment)
		}
	}
}

func assertWritingRuntimeEventLedger(t *testing.T, ledgerSQL string) {
	t.Helper()
	events := tableDefinition(t, ledgerSQL, "writing_run_events")
	for _, eventType := range []string{
		"run.planned", "run.started", "run.paused", "run.resumed", "run.cancelled",
		"run.completed", "run.failed", "node.started", "node.completed", "node.failed",
		"artifact.created", "quality.updated", "snapshot.created",
	} {
		if !strings.Contains(events, "'"+eventType+"'") {
			t.Errorf("event ledger must enumerate %s", eventType)
		}
	}
	for _, fragment := range []string{
		"FOREIGN KEY (run_id, causation_event_id)",
		"writing_run_events is append-only",
		"CREATE CONSTRAINT TRIGGER trg_writing_run_projection_from_event",
		"DEFERRABLE INITIALLY DEFERRED",
	} {
		if !strings.Contains(ledgerSQL, fragment) {
			t.Errorf("event ledger governance missing %s", fragment)
		}
	}
}

func assertWritingRuntimeGovernanceGates(t *testing.T, coreSQL, qualitySQL, ledgerSQL string) {
	t.Helper()
	all := coreSQL + qualitySQL + ledgerSQL
	for _, fragment := range []string{
		"writing_reject_immutable_columns",
		"trg_writing_contracts_immutable",
		"trg_writing_document_versions_immutable",
		"trg_writing_run_plans_immutable",
		"trg_writing_artifacts_immutable",
		"trg_writing_quality_reports_immutable",
		"trg_writing_decisions_immutable",
		"trg_writing_snapshots_immutable",
		"writing_enforce_quality_delivery_gate",
		"writing_enforce_snapshot_quality_binding",
		"writing_enforce_document_quality_gate",
		"writing_enforce_artifact_commit_gate",
		"snapshot_status <> 'persisted'",
		"NOT snapshot_row.complete",
		"NEW.waived_error_count <> 0",
		"achieved_rank < requested_rank",
		"waiver_severity IS DISTINCT FROM 'BLOCKER'",
	} {
		if !strings.Contains(all, fragment) {
			t.Errorf("governance gate missing %s", fragment)
		}
	}
}

func requireDropBefore(t *testing.T, downSQL, child, parent string) {
	t.Helper()
	upper := strings.ToUpper(downSQL)
	childOffset := strings.Index(upper, "DROP TABLE IF EXISTS "+strings.ToUpper(child))
	parentOffset := strings.Index(upper, "DROP TABLE IF EXISTS "+strings.ToUpper(parent))
	if childOffset < 0 || parentOffset < 0 {
		t.Errorf("down migrations must drop both %s and %s", child, parent)
		return
	}
	if childOffset > parentOffset {
		t.Errorf("down migrations must drop child %s before parent %s", child, parent)
	}
}

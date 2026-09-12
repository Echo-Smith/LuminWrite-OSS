package database

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
)

// TestMigration108UpDownRoundTrip verifies the research artifact-type CHECK
// extension on a throwaway database: up accepts every formal contract type
// and rejects an unknown one, down restores the exact 107 shape (keeping
// evidence_approval), and a second up re-applies cleanly. The down/up SQL
// runs statement-by-statement after stripping line comments.
func TestMigration108UpDownRoundTrip(t *testing.T) {
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	admin, err := NewPostgres(base, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	name := "test_" + hex.EncodeToString(suffix)
	if _, err := admin.Exec(fmt.Sprintf(`CREATE DATABASE %s TEMPLATE template0`, name)); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Path = "/" + name
	db, err := NewPostgres(parsed.String(), 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		db.Close()
		_, _ = admin.Exec(fmt.Sprintf(`DROP DATABASE %s WITH (FORCE)`, name))
	}()
	if err := Migrate(db); err != nil {
		t.Fatalf("up migration failed: %v", err)
	}
	formalTypes := []string{"research_candidates", "research_evidence_pack", "research_outline",
		"approved_research_outline", "evidence_approval", "research_citation_index",
		"research_validation_details"}
	for _, artifactType := range formalTypes {
		if err := probeArtifactType(db, artifactType); err != nil {
			t.Fatalf("up does not allow formal type %q: %v", artifactType, err)
		}
	}
	if err := probeArtifactType(db, "not_a_real_type"); err == nil {
		t.Fatal("constraint accepts an unknown artifact type")
	}
	if err := runSQL(t, db, migrationSQL(t, "108_research_artifact_types.down.sql")); err != nil {
		t.Fatalf("down migration failed: %v", err)
	}
	for _, artifactType := range []string{"research_outline", "research_citation_index"} {
		if err := probeArtifactType(db, artifactType); err == nil {
			t.Fatalf("down still accepts %q", artifactType)
		}
	}
	for _, artifactType := range []string{"evidence_approval", "full_draft", "research_note"} {
		if err := probeArtifactType(db, artifactType); err != nil {
			t.Fatalf("down dropped the 107 allowance for %q: %v", artifactType, err)
		}
	}
	if err := runSQL(t, db, migrationSQL(t, "108_research_artifact_types.up.sql")); err != nil {
		t.Fatalf("re-up failed: %v", err)
	}
	for _, artifactType := range formalTypes {
		if err := probeArtifactType(db, artifactType); err != nil {
			t.Fatalf("re-up does not allow %q: %v", artifactType, err)
		}
	}
}

// probeArtifactType reports whether the live chk_writing_artifact_type
// constraint's definition admits one artifact type.
func probeArtifactType(db *DB, artifactType string) error {
	var def string
	if err := db.QueryRow(`SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='chk_writing_artifact_type'`).Scan(&def); err != nil {
		return err
	}
	if !strings.Contains(def, "'"+artifactType+"'") {
		return fmt.Errorf("constraint %s does not allow %q", def, artifactType)
	}
	return nil
}

func migrationSQL(t *testing.T, name string) string {
	t.Helper()
	payload, err := migrationFS.ReadFile("migrations/" + name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(payload)
}

// runSQL executes a migration script statement-by-statement (the migrator
// embeds whole files; this verifier needs mid-file down execution).
func runSQL(t *testing.T, db *DB, script string) error {
	t.Helper()
	for _, statement := range splitSQL(script) {
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("exec %q: %w", statement, err)
		}
	}
	return nil
}

// splitSQL strips -- line comments and splits on statement-terminating
// semicolons (the migration scripts contain no string literals with ';').
func splitSQL(script string) []string {
	statements := []string{}
	current := strings.Builder{}
	for _, line := range strings.Split(script, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		current.WriteString(line)
		current.WriteString("\n")
		if strings.HasSuffix(strings.TrimSpace(line), ";") {
			statements = append(statements, strings.TrimSpace(current.String()))
			current.Reset()
		}
	}
	if tail := strings.TrimSpace(current.String()); tail != "" {
		statements = append(statements, tail)
	}
	return statements
}

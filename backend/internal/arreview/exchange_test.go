package arreview

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareProjectWritesFrozenInputs(t *testing.T) {
	exchange, err := NewExchange(filepath.Join(t.TempDir(), "projects"))
	if err != nil {
		t.Fatalf("NewExchange: %v", err)
	}
	projectID := "lb-" + repeat('0', 32)
	corpus := []byte(`{"sources":[]}`)
	spec := []byte(`{"schema_version":"lumin-review-spec/1"}`)
	dir, err := exchange.PrepareProject(projectID, corpus, spec)
	if err != nil {
		t.Fatalf("PrepareProject: %v", err)
	}
	roundTrip, err := exchange.ReadArtifact(projectID, "02_literature/REVIEW_CORPUS.json")
	if err != nil {
		t.Fatalf("ReadArtifact corpus: %v", err)
	}
	if string(roundTrip) != string(corpus) {
		t.Fatal("corpus round trip differs")
	}
	if _, err := exchange.ReadArtifact(projectID, "REVIEW_SPEC.json"); err != nil {
		t.Fatalf("ReadArtifact spec: %v", err)
	}
	_ = dir
}

func TestPrepareProjectIsIdempotentAndConflictsFailClosed(t *testing.T) {
	exchange, err := NewExchange(filepath.Join(t.TempDir(), "projects"))
	if err != nil {
		t.Fatalf("NewExchange: %v", err)
	}
	projectID := "lb-" + repeat('1', 32)
	corpus := []byte(`{"sources":[1]}`)
	spec := []byte(`{}`)
	if _, err := exchange.PrepareProject(projectID, corpus, spec); err != nil {
		t.Fatalf("first prepare: %v", err)
	}
	// Identical inputs: reuse must succeed untouched.
	if _, err := exchange.PrepareProject(projectID, corpus, spec); err != nil {
		t.Fatalf("idempotent prepare must succeed: %v", err)
	}
	// Different inputs under the same content-derived id: tampering.
	different := []byte(`{"sources":[2]}`)
	_, err = exchange.PrepareProject(projectID, different, spec)
	if !errors.Is(err, ErrExchangeConflict) {
		t.Fatalf("expected exchange conflict, got %v", err)
	}
}

func TestReadArtifactRejectsPathEscapes(t *testing.T) {
	exchange, err := NewExchange(filepath.Join(t.TempDir(), "projects"))
	if err != nil {
		t.Fatalf("NewExchange: %v", err)
	}
	projectID := "lb-" + repeat('2', 32)
	if _, err := exchange.PrepareProject(projectID, []byte(`{}`), []byte(`{}`)); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	escapes := []string{
		"../other/REVIEW_CORPUS.json",
		"02_literature/../../REVIEW_CORPUS.json",
		"/etc/passwd",
		"",
		"some\\windows\\path",
	}
	for _, attempt := range escapes {
		if _, err := exchange.ReadArtifact(projectID, attempt); err == nil {
			t.Fatalf("escape path %q must be rejected", attempt)
		}
	}
	if _, err := exchange.ReadArtifact("not-a-surrogate-id", "REVIEW_SPEC.json"); err == nil {
		t.Fatal("non-surrogate project id must be rejected")
	}
	if _, err := exchange.ReadArtifact(projectID, "05_writing/MISSING.md"); err == nil {
		t.Fatal("missing artifact must error")
	}
}

func TestNewExchangeCreatesRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nested", "exchange")
	exchange, err := NewExchange(root)
	if err != nil {
		t.Fatalf("NewExchange: %v", err)
	}
	if _, err := os.Stat(exchange.Root()); err != nil {
		t.Fatalf("root not created: %v", err)
	}
	if _, err := NewExchange("  "); err == nil {
		t.Fatal("empty root must be rejected")
	}
}

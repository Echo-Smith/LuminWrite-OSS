package agent_test

// Architecture freeze (docs: internal/agent/doc.go).
//
// internal/agent is a transitional compatibility package: the governed
// runtime is the production path, and the legacy harness survives only
// for the consumers listed below. This test fails CI when a NEW file
// imports the package, so the dependency set can only shrink.
//
// When WABench migrates to RunCore and editorial converges on the
// capability executor contract, remove their entries from the allowlist
// here — the test then enforces the next shrink.

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// agentImportPath is the frozen package under test.
const agentImportPath = "github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/agent"

// allowedAgentImporters lists the only files permitted to import
// internal/agent, with the reason each still needs it. Anything else
// importing the package fails this test — including new files.
var allowedAgentImporters = map[string]string{
	"internal/writingruntime/executor_adapters.go": "governed HarnessCoreNodeRunner adapts RunCore behind HarnessCoreInvoker",
	"internal/services/wabench_v2_adapter.go":      "WABench V2 runs candidates through RunCore (no lifecycle ownership)",
	"internal/editorial/experiment_runner.go":      "editorial experiments (converging on capability executors)",
}

func TestAgentPackageFrozenBoundary(t *testing.T) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatalf("locate repository root: %v", err)
	}

	var violations []string
	err = filepath.WalkDir(filepath.Join(repoRoot, "internal"), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			// The frozen package itself (and its tests) may reference its
			// own symbols; imports of the package path are what we guard.
			if entry.Name() == "agent" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		rel, relErr := filepath.Rel(repoRoot, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if _, allowed := allowedAgentImporters[rel]; allowed {
			return nil
		}
		importsAgent, importErr := fileImportsAgent(path)
		if importErr != nil {
			return importErr
		}
		if importsAgent {
			violations = append(violations, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan imports: %v", err)
	}
	if len(violations) > 0 {
		t.Fatalf("internal/agent is frozen (see internal/agent/doc.go): new importers found — %s\n"+
			"Allowed importers only: writingruntime/executor_adapters.go, services/wabench_v2_adapter.go, editorial/experiment_runner.go.\n"+
			"Route new execution through the governed runtime's capability executors instead.",
			strings.Join(violations, ", "))
	}

	// The allowlist must never silently rot: every entry must still exist
	// and still import the package, otherwise the shrink is incomplete.
	for rel := range allowedAgentImporters {
		path := filepath.Join(repoRoot, rel)
		importsAgent, importErr := fileImportsAgent(path)
		if importErr != nil {
			t.Fatalf("check allowlisted importer %s: %v", rel, importErr)
		}
		if !importsAgent {
			t.Fatalf("allowlisted importer %s no longer imports internal/agent — remove it from the freeze allowlist", rel)
		}
	}
}

// findRepoRoot walks up from the test's working directory until it finds
// the go.mod that owns this module.
func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}

func fileImportsAgent(path string) (bool, error) {
	fileSet := token.NewFileSet()
	parsed, err := parser.ParseFile(fileSet, path, nil, parser.ImportsOnly)
	if err != nil {
		return false, err
	}
	for _, spec := range parsed.Imports {
		if strings.Trim(spec.Path.Value, `"`) == agentImportPath {
			return true, nil
		}
	}
	return false, nil
}

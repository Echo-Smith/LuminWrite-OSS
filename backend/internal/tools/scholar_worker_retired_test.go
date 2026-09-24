package tools

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Scholar worker retirement guard (WP5 OSS governance).
//
// The research-review path executes the scholar operations in-process
// (backend/internal/scholar; PDF parsing via the docreader sidecar). The
// former private-network Python worker (services/scholar-worker, package
// lumin_scholar) is retired and must not drift back into the repository:
// no service definition, no build context, no smoke script driving it, no
// env-template comment implying it exists. SCHOLAR_WORKER_URL /
// SCHOLAR_WORKER_TOKEN are intentionally kept — they are the research-path
// enablement signal and the required configuration guard (see
// internal/scholar/client.go) — so this guard matches only the lowercase
// component identifiers, never the uppercase variable names.
//
// Whitelisting policy: exact repository-relative paths only, and only for
// dated historical records (plans / reviews / releases / specs) that are
// kept verbatim as the audit trail of the worker era. Never whitelist a
// directory, a glob, or anything under code, compose, scripts or env
// templates.

// retiredScholarPatterns are the retired component identifiers. Case
// matters: SCHOLAR_WORKER_URL / SCHOLAR_WORKER_TOKEN are live variables.
var retiredScholarPatterns = []string{
	"scholar-worker",
	"lumin_scholar",
}

// retiredScholarWhitelist lists the exact files allowed to mention the
// retired worker: the dated historical records kept verbatim, plus this
// guard itself (its doc comment names the patterns it enforces).
var retiredScholarWhitelist = map[string]bool{
	"docs/plans/2026-09-07-research-review-integration.md":   true,
	"docs/reviews/2026-09-08-research-review.md":             true,
	"docs/releases/2026-09-08-research-review-acceptance.md": true,
	"specs/research-review/design.md":                        true,
	"backend/internal/tools/scholar_worker_retired_test.go":  true,
}

// retiredScholarSkipDirs are never scanned: VCS internals, dependency trees
// and build output are not repository content.
var retiredScholarSkipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	".venv":        true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
	"coverage":     true,
}

func TestRepoHasNoRetiredScholarWorkerReferences(t *testing.T) {
	repoRoot := findOSEnvRoot(t)

	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(repoRoot, path)
		if relErr != nil {
			return relErr
		}
		if d.IsDir() {
			if rel != "." && retiredScholarSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || retiredScholarWhitelist[rel] {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		// Skip binary files (a NUL byte in the content is the heuristic).
		if bytes.IndexByte(data, 0) >= 0 {
			return nil
		}
		for _, pattern := range retiredScholarPatterns {
			if strings.Contains(string(data), pattern) {
				t.Errorf("retired scholar worker reference in %s: %q — "+
					"the worker is gone; research runs in-process via backend/internal/scholar "+
					"(update the text, or whitelist only a dated historical record by exact path)",
					rel, pattern)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk repository: %v", err)
	}
}

package arreview

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// ErrExchangeConflict reports a pre-existing exchange directory whose frozen
// inputs differ from what the content-addressed id promises. Because ids are
// derived from input hashes, this can only mean external tampering or a hash
// collision; both must fail closed.
var ErrExchangeConflict = errors.New("arreview: exchange directory conflicts with its content-derived id")

var relPathPattern = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

// Exchange manages the shared volume between the Go host and the sidecar
// (single-machine PoC transport; a cross-machine deployment needs the upload
// API called out in the assessment doc and is out of scope).
//
// Layout per surrogate project:
//
//	{root}/{project_id}/REVIEW_SPEC.json            (genre profile)
//	{root}/{project_id}/02_literature/REVIEW_CORPUS.json (frozen corpus)
//	{root}/{project_id}/05_writing/…                (sidecar outputs)
//
// The sidecar resolves every request path inside the project directory and
// refuses escapes; this side re-checks containment anyway — defense in depth
// across a trust boundary.
type Exchange struct {
	root string
}

// NewExchange creates the exchange root (the volume mount point) if needed.
func NewExchange(root string) (*Exchange, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("%w: exchange root is empty", ErrProtocol)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("%w: exchange root %q: %v", ErrProtocol, root, err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("arreview: create exchange root %q: %w", abs, err)
	}
	return &Exchange{root: abs}, nil
}

// Root is the exchange root directory.
func (e *Exchange) Root() string { return e.root }

// PrepareProject materializes the frozen inputs for one surrogate project.
// It is idempotent: an existing directory with byte-identical inputs is
// reused untouched (the reconciliation path depends on this), and a
// directory with different contents fails closed.
func (e *Exchange) PrepareProject(projectID string, corpusJSON, specJSON []byte) (string, error) {
	if !ValidProjectID(projectID) {
		return "", fmt.Errorf("%w: project id %q is not a surrogate id", ErrProtocol, projectID)
	}
	projectDir := filepath.Join(e.root, projectID)
	corpusPath := filepath.Join(projectDir, "02_literature", "REVIEW_CORPUS.json")
	specPath := filepath.Join(projectDir, "REVIEW_SPEC.json")

	if _, err := os.Stat(projectDir); err == nil {
		for _, pair := range []struct {
			path     string
			expected []byte
		}{{corpusPath, corpusJSON}, {specPath, specJSON}} {
			existing, readErr := os.ReadFile(pair.path)
			if readErr != nil {
				return "", fmt.Errorf("%w: %s exists but %s is missing or unreadable: %v",
					ErrExchangeConflict, projectDir, pair.path, readErr)
			}
			if sha256.Sum256(existing) != sha256.Sum256(pair.expected) {
				return "", fmt.Errorf("%w: %s content differs from derived hash", ErrExchangeConflict, pair.path)
			}
		}
		return projectDir, nil
	}

	if err := os.MkdirAll(filepath.Dir(corpusPath), 0o755); err != nil {
		return "", fmt.Errorf("arreview: create project dirs: %w", err)
	}
	// 0644: the sidecar container runs as a different unprivileged user and
	// reads these files across the shared volume.
	if err := os.WriteFile(corpusPath, corpusJSON, 0o644); err != nil {
		return "", fmt.Errorf("arreview: write corpus: %w", err)
	}
	if err := os.WriteFile(specPath, specJSON, 0o644); err != nil {
		return "", fmt.Errorf("arreview: write review spec: %w", err)
	}
	return projectDir, nil
}

// ReadArtifact reads one sidecar output file (a project-relative path as
// returned in the record's artifacts map), refusing any path that escapes
// the project directory.
func (e *Exchange) ReadArtifact(projectID, relPath string) ([]byte, error) {
	target, err := e.ArtifactPath(projectID, relPath)
	if err != nil {
		return nil, err
	}
	payload, err := os.ReadFile(target)
	if err != nil {
		return nil, fmt.Errorf("arreview: read sidecar artifact %q: %w", relPath, err)
	}
	return payload, nil
}

// ArtifactPath resolves and contains one project-relative path.
func (e *Exchange) ArtifactPath(projectID, relPath string) (string, error) {
	if !ValidProjectID(projectID) {
		return "", fmt.Errorf("%w: project id %q is not a surrogate id", ErrProtocol, projectID)
	}
	if relPath == "" || !relPathPattern.MatchString(relPath) || strings.Contains(relPath, "..") {
		return "", fmt.Errorf("%w: artifact path %q is not a safe project-relative path", ErrProtocol, relPath)
	}
	projectDir := filepath.Join(e.root, projectID)
	target := filepath.Join(projectDir, filepath.FromSlash(path.Clean(relPath)))
	contained, err := filepath.Rel(e.root, target)
	if err != nil || strings.HasPrefix(contained, "..") {
		return "", fmt.Errorf("%w: artifact path %q escapes the exchange root", ErrProtocol, relPath)
	}
	if !strings.HasPrefix(target, projectDir+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: artifact path %q escapes the project directory", ErrProtocol, relPath)
	}
	return target, nil
}

// HashContent is the canonical content hash used for artifact import (the
// same sha256:<hex64> envelope the writing store uses).
func HashContent(payload []byte) string {
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}

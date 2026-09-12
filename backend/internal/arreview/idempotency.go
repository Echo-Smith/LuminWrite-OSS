package arreview

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// GeneratorVersion identifies the exact sidecar build a key was derived for.
// Bumping it rotates keys, so an upgraded sidecar never silently replays a
// cached run produced by different prompts or code (design.md §8).
const GeneratorVersion = "ar012-review@68888a7-lumin.1"

var (
	hashPattern  = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	projectIDPat = regexp.MustCompile(`^lb-[0-9a-f]{32}$`)
)

// IdempotencyInput is everything that must distinguish one sidecar run from
// another. The derivation mirrors design.md §8: owner + contract hash +
// evidence pack hash + approved outline hash + generator version + mode.
// Two jobs with identical inputs converge on the same surrogate project and
// key, so a timed-out job reconciles into the sidecar's cached record instead
// of re-running a 650s pipeline; two jobs with different frozen inputs can
// never collide.
type IdempotencyInput struct {
	Owner               string
	ContractHash        string
	EvidencePackHash    string
	ApprovedOutlineHash string
	GeneratorVersion    string
	Mode                RunMode
}

func (in IdempotencyInput) validate() error {
	switch {
	case strings.TrimSpace(in.Owner) == "":
		return fmt.Errorf("%w: owner is required", ErrProtocol)
	case !hashPattern.MatchString(in.ContractHash):
		return fmt.Errorf("%w: contract hash must be sha256:<64 hex>", ErrProtocol)
	case !hashPattern.MatchString(in.EvidencePackHash):
		return fmt.Errorf("%w: evidence pack hash must be sha256:<64 hex>", ErrProtocol)
	case !hashPattern.MatchString(in.ApprovedOutlineHash):
		return fmt.Errorf("%w: approved outline hash must be sha256:<64 hex>", ErrProtocol)
	case strings.TrimSpace(in.GeneratorVersion) == "":
		return fmt.Errorf("%w: generator version is required", ErrProtocol)
	case in.Mode != ModeGenerateOnly && in.Mode != ModeCompareOnly && in.Mode != ModeGenerateAndCompare:
		return fmt.Errorf("%w: unsupported run mode %q", ErrProtocol, in.Mode)
	}
	return nil
}

func (in IdempotencyInput) digest() (string, error) {
	if err := in.validate(); err != nil {
		return "", err
	}
	// \x1f (unit separator) keeps field boundaries unambiguous even if a
	// caller-supplied owner contains colons or dashes.
	canonical := strings.Join([]string{
		"arreview/1",
		in.Owner,
		in.ContractHash,
		in.EvidencePackHash,
		in.ApprovedOutlineHash,
		in.GeneratorVersion,
		string(in.Mode),
	}, "\x1f")
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:]), nil
}

// Digest returns the canonical "sha256:<hex64>" input digest. Callers persist
// it as the job's InputHash so the replay identity is auditable end to end.
func (in IdempotencyInput) Digest() (string, error) {
	hexDigest, err := in.digest()
	if err != nil {
		return "", err
	}
	return "sha256:" + hexDigest, nil
}

// SurrogateProjectID is the content-addressed project id sent to the sidecar.
// It matches the upstream project_id pattern (lowercase, digits, dash,
// underscore; <= 64 chars) and doubles as the exchange directory name, so
// each distinct frozen input set gets its own isolated working directory.
func (in IdempotencyInput) SurrogateProjectID() (string, error) {
	digest, err := in.digest()
	if err != nil {
		return "", err
	}
	return "lb-" + digest[:32], nil
}

// IdempotencyKey satisfies the upstream key pattern (^[A-Za-z0-9._:-]+$,
// 8..200 chars) and is scoped per surrogate project by the sidecar.
func (in IdempotencyInput) IdempotencyKey() (string, error) {
	digest, err := in.digest()
	if err != nil {
		return "", err
	}
	return "ar012-" + digest, nil
}

// ValidProjectID reports whether id is one of our surrogate ids.
func ValidProjectID(id string) bool { return projectIDPat.MatchString(id) }

package projectmemory

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ClaimSupportThreshold is how many distinct evidence citations a claim needs
// before it flips from open to supported. Support is advisory context for the
// committing user, never an auto-promotion: canon writes stay user-only.
const ClaimSupportThreshold = 2

// MaxEntityAliases caps the alias list of one entity birth certificate.
const MaxEntityAliases = 12

// entityKinds is the closed non-fiction entity set (docs/18 §18.10). There is
// no character-card concept; evolving truth lives in facts, not on entities.
var entityKinds = map[string]bool{
	"person":       true,
	"organization": true,
	"place":        true,
	"concept":      true,
	"term":         true,
	"product":      true,
	"event":        true,
	"work":         true,
}

// EntityKinds returns the sorted entity kind names.
func EntityKinds() []string {
	names := make([]string, 0, len(entityKinds))
	for name := range entityKinds {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ValidEntityKind reports whether kind is in the closed entity set.
func ValidEntityKind(kind string) bool {
	return entityKinds[strings.ToLower(strings.TrimSpace(kind))]
}

// EvidenceHash folds a citation into one idempotent key: a run id alone, or
// the sorted source refs, hash to the same value on every re-recording.
func EvidenceHash(sourceRunID string, sourceRefs []string) string {
	payload := "project-memory-evidence-v1\x00"
	if strings.TrimSpace(sourceRunID) != "" {
		payload += "run:" + strings.TrimSpace(sourceRunID)
	} else {
		refs := make([]string, 0, len(sourceRefs))
		for _, ref := range sourceRefs {
			if trimmed := strings.TrimSpace(ref); trimmed != "" {
				refs = append(refs, trimmed)
			}
		}
		sort.Strings(refs)
		payload += "refs:" + strings.Join(refs, "\x00")
	}
	sum := sha256.Sum256([]byte(payload))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Claim is a pending statement in the corroboration lane: the same triple
// shape as a fact, a mutable status, and an evidence ledger. Content never
// changes; only what supports it does.
type Claim struct {
	ClaimID           string    `json:"claim_id"`
	BatchID           string    `json:"batch_id"`
	ProjectID         string    `json:"project_id"`
	Subject           string    `json:"subject"`
	Predicate         string    `json:"predicate"`
	Object            string    `json:"object"`
	AsOf              time.Time `json:"as_of"`
	RaisedByType      string    `json:"raised_by_type"`
	RaisedByID        string    `json:"raised_by_id,omitempty"`
	SourceRunID       string    `json:"source_run_id,omitempty"`
	SourceRefs        []string  `json:"source_refs"`
	VocabularyVersion int       `json:"vocabulary_version"`
	ExtendedPredicate bool      `json:"extended_predicate"`
	ContentHash       string    `json:"content_hash"`
	Status            string    `json:"status"`
	PromotedFactID    string    `json:"promoted_fact_id,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
}

// ClaimEvidence is one corroboration citation of a claim.
type ClaimEvidence struct {
	EvidenceID     string    `json:"evidence_id"`
	ClaimID        string    `json:"claim_id"`
	EvidenceHash   string    `json:"evidence_hash"`
	SourceRunID    string    `json:"source_run_id,omitempty"`
	SourceRefs     []string  `json:"source_refs"`
	RecordedByType string    `json:"recorded_by_type"`
	RecordedByID   string    `json:"recorded_by_id,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// Entity is a birth certificate: immutable identity, mutable lifecycle
// status. Candidate rows come from any actor; promotion to the live registry
// is user-only.
type Entity struct {
	EntityID       string    `json:"entity_id"`
	ProjectID      string    `json:"project_id"`
	EntityKind     string    `json:"entity_kind"`
	CanonicalName  string    `json:"canonical_name"`
	Aliases        []string  `json:"aliases"`
	Description    string    `json:"description"`
	SourceRunID    string    `json:"source_run_id,omitempty"`
	SourceRefs     []string  `json:"source_refs"`
	Status         string    `json:"status"`
	RaisedByType   string    `json:"raised_by_type"`
	RaisedByID     string    `json:"raised_by_id,omitempty"`
	PromotedByType string    `json:"promoted_by_type,omitempty"`
	PromotedByID   string    `json:"promoted_by_id,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// ValidateClaim mechanically checks a claim before staging. It reuses the
// candidate rules: triples must be complete, in-vocabulary (or x- extended),
// provenance-carrying, and timestamped.
func ValidateClaim(claim *Claim) ([]string, error) {
	candidate := Candidate{ProjectID: claim.ProjectID, Subject: claim.Subject, Predicate: claim.Predicate,
		Object: claim.Object, AsOf: claim.AsOf, SourceRunID: claim.SourceRunID, SourceRefs: claim.SourceRefs,
		VocabularyVersion: claim.VocabularyVersion}
	warnings, err := ValidateCandidate(&candidate)
	if err != nil {
		return nil, err
	}
	claim.Subject, claim.Predicate, claim.Object = candidate.Subject, candidate.Predicate, candidate.Object
	claim.VocabularyVersion, claim.ExtendedPredicate = candidate.VocabularyVersion, candidate.ExtendedPredicate
	return warnings, nil
}

// ValidateEntity checks an entity birth certificate: closed kind set, a
// non-empty canonical name, and a bounded, self-consistent alias list.
func ValidateEntity(entity *Entity) ([]string, error) {
	if strings.TrimSpace(entity.ProjectID) == "" {
		return nil, fmt.Errorf("project id required")
	}
	if !ValidEntityKind(entity.EntityKind) {
		return nil, fmt.Errorf("entity kind %q is outside the closed set %v", entity.EntityKind, EntityKinds())
	}
	entity.EntityKind = strings.ToLower(strings.TrimSpace(entity.EntityKind))
	entity.CanonicalName = strings.TrimSpace(entity.CanonicalName)
	if entity.CanonicalName == "" {
		return nil, fmt.Errorf("canonical name is required")
	}
	seen := map[string]bool{}
	aliases := []string{}
	for _, alias := range entity.Aliases {
		normalized := strings.TrimSpace(alias)
		if normalized == "" {
			continue
		}
		if normalized == entity.CanonicalName {
			return nil, fmt.Errorf("alias %q duplicates the canonical name", normalized)
		}
		// Alias variants differing only by case are noise; keep the first
		// original spelling.
		folded := strings.ToLower(normalized)
		if seen[folded] {
			continue
		}
		seen[folded] = true
		aliases = append(aliases, normalized)
	}
	if len(aliases) > MaxEntityAliases {
		return nil, fmt.Errorf("entity carries %d aliases, the cap is %d", len(aliases), MaxEntityAliases)
	}
	entity.Aliases = aliases
	if len(entity.SourceRefs) == 0 && strings.TrimSpace(entity.SourceRunID) == "" {
		return nil, fmt.Errorf("provenance required: source_refs or source_run_id")
	}
	return nil, nil
}

// Package projectmemory implements the ProjectMemory layer: project-scoped
// canonical facts with validity intervals, separated from UserMemory,
// SourceEvidence, and DocumentState (docs/18). M1 scope: the controlled
// vocabulary, candidate staging with mechanical validation, and the
// user-only commit gate. Entities, terminology, decisions, and the
// through-line land in M2+.
package projectmemory

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

// VocabularyVersion pins the controlled predicate set. Bumping it changes
// hash inputs for every derived fact, so it must be treated like a policy
// change: new version, new content hashes, never an in-place edit.
const VocabularyVersion = 1

// ExtendedPrefix marks user-defined extensions outside the controlled set.
const ExtendedPrefix = "x-"

// vocabulary is the controlled predicate set. Facts outside it must use the
// x- extension namespace and are accepted with a warning, never silently.
var vocabulary = map[string]bool{
	"identity":     true,
	"location":     true,
	"possession":   true,
	"goal":         true,
	"state":        true,
	"relationship": true,
	"style_rule":   true,
	"constraint":   true,
}

// Vocabulary returns the sorted canonical predicate names of the current
// vocabulary version.
func Vocabulary() []string {
	names := make([]string, 0, len(vocabulary))
	for name := range vocabulary {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// VocabularySize reports how many canonical predicates are registered.
func VocabularySize() int {
	return len(vocabulary)
}

// ValidPredicate reports whether name is part of the controlled vocabulary.
func ValidPredicate(name string) bool {
	return vocabulary[strings.ToLower(strings.TrimSpace(name))]
}

// NormalizeSubject canonicalizes a subject/object key: trimmed, case-folded,
// inner whitespace runs collapsed to single spaces, so "林然 " and
// "  林然" collapse to one key. Interior spacing is preserved, not removed.
func NormalizeSubject(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}

// singleValuedPredicates can hold only one active fact per (subject,
// predicate): a new object supersedes the previous state. Multi-valued
// predicates (relationship, style_rule, constraint, x- extensions) keep
// coexisting active facts — knowing two people is not a state change.
var singleValuedPredicates = map[string]bool{
	"identity":   true,
	"location":   true,
	"possession": true,
	"goal":       true,
	"state":      true,
}

// SingleValued reports whether a predicate follows the one-active-fact rule.
// Extended predicates are multi-valued by default: their semantics are not
// governed by the vocabulary, so the store must not auto-invalidate them.
func SingleValued(predicate string) bool {
	return singleValuedPredicates[strings.ToLower(strings.TrimSpace(predicate))]
}

// Candidate is a staged memory proposal from any actor. Models may only
// produce candidates; nothing here is canon until a user commits it.
type Candidate struct {
	CandidateID       string    `json:"candidate_id"`
	BatchID           string    `json:"batch_id"`
	ProjectID         string    `json:"project_id"`
	Subject           string    `json:"subject"`
	Predicate         string    `json:"predicate"`
	Object            string    `json:"object"`
	AsOf              time.Time `json:"as_of"`
	SourceRunID       string    `json:"source_run_id,omitempty"`
	SourceRefs        []string  `json:"source_refs"`
	VocabularyVersion int       `json:"vocabulary_version"`
	ExtendedPredicate bool      `json:"extended_predicate"`
	Warnings          []string  `json:"warnings"`
	Status            string    `json:"status"`
	SubmittedByType   string    `json:"submitted_by_type"`
	SubmittedByID     string    `json:"submitted_by_id,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
}

// Fact is a canonical project fact: one validity interval, never mutated
// truth. A state change inserts a successor and stamps the predecessor's
// ValidTo/SupersededBy.
type Fact struct {
	FactID            string     `json:"fact_id"`
	ProjectID         string     `json:"project_id"`
	Subject           string     `json:"subject"`
	Predicate         string     `json:"predicate"`
	Object            string     `json:"object"`
	ValidFrom         time.Time  `json:"valid_from"`
	ValidTo           *time.Time `json:"valid_to,omitempty"`
	SupersededBy      string     `json:"superseded_by,omitempty"`
	SourceRunID       string     `json:"source_run_id,omitempty"`
	SourceRefs        []string   `json:"source_refs"`
	VocabularyVersion int        `json:"vocabulary_version"`
	ExtendedPredicate bool       `json:"extended_predicate"`
	ContentHash       string     `json:"content_hash"`
	CandidateID       string     `json:"candidate_id,omitempty"`
	CommittedByType   string     `json:"committed_by_type"`
	CommittedByID     string     `json:"committed_by_id,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
}

// ContentKey is the hash input for one fact content triple: normalized
// subject, canonical predicate, normalized object, and the vocabulary
// version. Two candidates with the same key are the same fact; relationship
// facts fold their endpoint pair lexicographically so ("a","b") and
// ("b","a") are one fact.
func ContentKey(projectID, subject, predicate, object string, vocabularyVersion int) string {
	canonical := strings.ToLower(strings.TrimSpace(predicate))
	foldedSubject, foldedObject := NormalizeSubject(subject), NormalizeSubject(object)
	if canonical == "relationship" && foldedSubject > foldedObject {
		foldedSubject, foldedObject = foldedObject, foldedSubject
	}
	payload := strings.Join([]string{"project-memory-v1", projectID, foldedSubject, canonical, foldedObject, fmt.Sprint(vocabularyVersion)}, "\x00")
	sum := sha256.Sum256([]byte(payload))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ValidateCandidate mechanically checks a candidate before it may be staged:
// triple completeness, vocabulary membership (with the x- extension escape),
// provenance presence, and the as-of timestamp. The returned warnings are
// persisted with the row; they are advisory, not blocking. The candidate is
// normalized in place (canonical predicate, folded subject/object) so the
// caller stages exactly what the store will compare.
func ValidateCandidate(candidate *Candidate) ([]string, error) {
	if strings.TrimSpace(candidate.ProjectID) == "" {
		return nil, fmt.Errorf("project id required")
	}
	if strings.TrimSpace(candidate.Subject) == "" || strings.TrimSpace(candidate.Predicate) == "" || strings.TrimSpace(candidate.Object) == "" {
		return nil, fmt.Errorf("subject, predicate, and object are required")
	}
	if candidate.AsOf.IsZero() {
		return nil, fmt.Errorf("as_of is required")
	}
	if len(candidate.SourceRefs) == 0 && strings.TrimSpace(candidate.SourceRunID) == "" {
		return nil, fmt.Errorf("provenance required: source_refs or source_run_id")
	}
	warnings := []string{}
	canonical := strings.ToLower(strings.TrimSpace(candidate.Predicate))
	if ValidPredicate(canonical) {
		candidate.Predicate = canonical
	} else {
		if !strings.HasPrefix(canonical, ExtendedPrefix) {
			return nil, fmt.Errorf("predicate %q is outside the controlled vocabulary; use a canonical predicate or the %q extension namespace", candidate.Predicate, ExtendedPrefix)
		}
		candidate.Predicate = canonical
		candidate.ExtendedPredicate = true
		warnings = append(warnings, "extended_predicate:"+candidate.Predicate)
	}
	if candidate.Predicate == "relationship" && NormalizeSubject(candidate.Subject) == NormalizeSubject(candidate.Object) {
		return nil, fmt.Errorf("relationship endpoints must differ")
	}
	if candidate.VocabularyVersion == 0 {
		candidate.VocabularyVersion = VocabularyVersion
	}
	if candidate.VocabularyVersion != VocabularyVersion {
		return nil, fmt.Errorf("candidate carries vocabulary v%d, store speaks v%d", candidate.VocabularyVersion, VocabularyVersion)
	}
	candidate.Subject = NormalizeSubject(candidate.Subject)
	candidate.Object = NormalizeSubject(candidate.Object)
	return warnings, nil
}

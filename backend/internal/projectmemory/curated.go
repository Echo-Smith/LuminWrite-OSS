package projectmemory

import (
	"fmt"
	"strings"
	"time"
)

// Terminology is a project usage directive: the canonical term, its
// definition, accepted aliases, and forbidden spellings. The compiler ranks
// it above generic style rules.
type Terminology struct {
	TerminologyID  string    `json:"terminology_id"`
	ProjectID      string    `json:"project_id"`
	Term           string    `json:"term"`
	Definition     string    `json:"definition"`
	Aliases        []string  `json:"aliases"`
	Forbidden      []string  `json:"forbidden"`
	SourceRunID    string    `json:"source_run_id,omitempty"`
	SourceRefs     []string  `json:"source_refs"`
	Status         string    `json:"status"`
	RaisedByType   string    `json:"raised_by_type"`
	RaisedByID     string    `json:"raised_by_id,omitempty"`
	PromotedByType string    `json:"promoted_by_type,omitempty"`
	PromotedByID   string    `json:"promoted_by_id,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// Decision is a settled call with its rationale. Revisiting one means
// promoting a successor that supersedes the old row — the old row is never
// edited.
type Decision struct {
	DecisionID     string     `json:"decision_id"`
	ProjectID      string     `json:"project_id"`
	Statement      string     `json:"statement"`
	Rationale      string     `json:"rationale"`
	DecidedAt      *time.Time `json:"decided_at,omitempty"`
	Supersedes     string     `json:"supersedes,omitempty"`
	SourceRunID    string     `json:"source_run_id,omitempty"`
	SourceRefs     []string   `json:"source_refs"`
	Status         string     `json:"status"`
	RaisedByType   string     `json:"raised_by_type"`
	RaisedByID     string     `json:"raised_by_id,omitempty"`
	PromotedByType string     `json:"promoted_by_type,omitempty"`
	PromotedByID   string     `json:"promoted_by_id,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

// OpenQuestion is one unanswered project question. Open questions land in the
// compiler's resident layer so long sessions cannot silently drop them; an
// answer may link the fact that settled it.
type OpenQuestion struct {
	QuestionID     string     `json:"question_id"`
	ProjectID      string     `json:"project_id"`
	Question       string     `json:"question"`
	Context        string     `json:"context"`
	Answer         string     `json:"answer"`
	AnsweredFactID string     `json:"answered_fact_id,omitempty"`
	Status         string     `json:"status"`
	RaisedByType   string     `json:"raised_by_type"`
	RaisedByID     string     `json:"raised_by_id,omitempty"`
	AnsweredByType string     `json:"answered_by_type,omitempty"`
	AnsweredByID   string     `json:"answered_by_id,omitempty"`
	AnsweredAt     *time.Time `json:"answered_at,omitempty"`
	SourceRunID    string     `json:"source_run_id,omitempty"`
	SourceRefs     []string   `json:"source_refs"`
	CreatedAt      time.Time  `json:"created_at"`
}

// Thread is one durable project line in the domain-neutral through-line
// ledger: an argument chain or narrative thread that must survive every
// session. Resident threads are never dropped by the compiler; resolution
// links the fact that settled the line.
type Thread struct {
	ThreadID       string    `json:"thread_id"`
	ProjectID      string    `json:"project_id"`
	Label          string    `json:"label"`
	Summary        string    `json:"summary"`
	Resident       bool      `json:"resident"`
	ResolvedFactID string    `json:"resolved_fact_id,omitempty"`
	Status         string    `json:"status"`
	SourceRunID    string    `json:"source_run_id,omitempty"`
	SourceRefs     []string  `json:"source_refs"`
	RaisedByType   string    `json:"raised_by_type"`
	RaisedByID     string    `json:"raised_by_id,omitempty"`
	PromotedByType string    `json:"promoted_by_type,omitempty"`
	PromotedByID   string    `json:"promoted_by_id,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// normalizeList trims, drops empties, and dedupes case-insensitively,
// preserving first spelling. Returns an error past the alias cap.
func normalizeList(values []string, cap int) ([]string, error) {
	seen := map[string]bool{}
	kept := []string{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		folded := strings.ToLower(trimmed)
		if seen[folded] {
			continue
		}
		seen[folded] = true
		kept = append(kept, trimmed)
	}
	if len(kept) > cap {
		return nil, fmt.Errorf("list carries %d entries, the cap is %d", len(kept), cap)
	}
	return kept, nil
}

// requireProvenance enforces the M1 rule across curated objects: something
// must say where a proposal came from.
func requireProvenance(sourceRunID string, sourceRefs []string) error {
	if len(sourceRefs) == 0 && strings.TrimSpace(sourceRunID) == "" {
		return fmt.Errorf("provenance required: source_refs or source_run_id")
	}
	return nil
}

// ValidateTerminology checks a terminology entry: a non-empty term, bounded
// alias/forbidden lists (case-insensitive dedupe), and no overlap between
// them.
func ValidateTerminology(entry *Terminology) error {
	if strings.TrimSpace(entry.ProjectID) == "" {
		return fmt.Errorf("project id required")
	}
	entry.Term = strings.TrimSpace(entry.Term)
	if entry.Term == "" {
		return fmt.Errorf("term is required")
	}
	aliases, err := normalizeList(entry.Aliases, MaxAliases)
	if err != nil {
		return err
	}
	forbidden, err := normalizeList(entry.Forbidden, MaxAliases)
	if err != nil {
		return err
	}
	seen := map[string]bool{strings.ToLower(entry.Term): true}
	for _, alias := range aliases {
		if seen[strings.ToLower(alias)] {
			return fmt.Errorf("alias %q duplicates the term or an earlier alias", alias)
		}
		seen[strings.ToLower(alias)] = true
	}
	for _, banned := range forbidden {
		if seen[strings.ToLower(banned)] {
			return fmt.Errorf("forbidden spelling %q collides with the term or an alias", banned)
		}
	}
	entry.Aliases, entry.Forbidden = aliases, forbidden
	return requireProvenance(entry.SourceRunID, entry.SourceRefs)
}

// ValidateDecision checks a decision proposal: a non-empty statement.
func ValidateDecision(decision *Decision) error {
	if strings.TrimSpace(decision.ProjectID) == "" {
		return fmt.Errorf("project id required")
	}
	decision.Statement = strings.TrimSpace(decision.Statement)
	if decision.Statement == "" {
		return fmt.Errorf("statement is required")
	}
	return requireProvenance(decision.SourceRunID, decision.SourceRefs)
}

// ValidateOpenQuestion checks a raised question: a non-empty question line.
func ValidateOpenQuestion(question *OpenQuestion) error {
	if strings.TrimSpace(question.ProjectID) == "" {
		return fmt.Errorf("project id required")
	}
	question.Question = strings.TrimSpace(question.Question)
	if question.Question == "" {
		return fmt.Errorf("question is required")
	}
	return requireProvenance(question.SourceRunID, question.SourceRefs)
}

// ValidateThread checks a thread proposal: a non-empty label.
func ValidateThread(thread *Thread) error {
	if strings.TrimSpace(thread.ProjectID) == "" {
		return fmt.Errorf("project id required")
	}
	thread.Label = strings.TrimSpace(thread.Label)
	if thread.Label == "" {
		return fmt.Errorf("label is required")
	}
	return requireProvenance(thread.SourceRunID, thread.SourceRefs)
}

package writingstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/projectmemory"
)

// ProjectRecord is a ProjectMemory scope: facts and candidates hang off a
// project, and documents may reference one via writing_documents.project_id.
type ProjectRecord struct {
	ProjectID   string         `json:"project_id"`
	OwnerUserID string         `json:"owner_user_id"`
	Title       string         `json:"title"`
	Status      string         `json:"status"`
	Metadata    map[string]any `json:"metadata"`
	Actor       Actor          `json:"actor"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

// CreateProject registers a new project scope.
func (s *Store) CreateProject(ctx context.Context, record ProjectRecord) error {
	if err := validateID(record.ProjectID, "prj_", "project_id"); err != nil {
		return err
	}
	if strings.TrimSpace(record.OwnerUserID) == "" {
		return fmt.Errorf("%w: owner_user_id is required", ErrInvalidRecord)
	}
	if err := record.Actor.Validate(); err != nil {
		return err
	}
	if record.Status == "" {
		record.Status = "active"
	}
	if record.Status != "active" && record.Status != "archived" {
		return fmt.Errorf("%w: unsupported project status %q", ErrInvalidRecord, record.Status)
	}
	if record.Metadata == nil {
		record.Metadata = map[string]any{}
	}
	metadata, err := json.Marshal(record.Metadata)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO writing_projects (project_id, owner_user_id, title, status, metadata, created_by_type, created_by_id, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,COALESCE(NULLIF($8, TIMESTAMP 'epoch'), NOW()))
	`, record.ProjectID, record.OwnerUserID, record.Title, record.Status, metadata,
		record.Actor.Type, record.Actor.ID, record.CreatedAt)
	if err != nil {
		return fmt.Errorf("create project: %w", err)
	}
	return nil
}

// GetProject loads one project scope.
func (s *Store) GetProject(ctx context.Context, projectID string) (ProjectRecord, error) {
	var record ProjectRecord
	var metadata []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT project_id, owner_user_id::text, title, status, metadata, created_by_type, COALESCE(created_by_id,''), created_at, updated_at
		FROM writing_projects WHERE project_id=$1
	`, projectID).Scan(&record.ProjectID, &record.OwnerUserID, &record.Title, &record.Status, &metadata,
		&record.Actor.Type, &record.Actor.ID, &record.CreatedAt, &record.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectRecord{}, ErrNotFound
	}
	if err != nil {
		return ProjectRecord{}, fmt.Errorf("get project: %w", err)
	}
	if err := json.Unmarshal(metadata, &record.Metadata); err != nil {
		return ProjectRecord{}, err
	}
	return record, nil
}

// SetDocumentProject attaches a document to a project scope. The FK enforces
// an existing project; a missing document reports ErrNotFound.
func (s *Store) SetDocumentProject(ctx context.Context, documentID, projectID string) error {
	if err := validateID(documentID, "doc_", "document_id"); err != nil {
		return err
	}
	if err := validateID(projectID, "prj_", "project_id"); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE writing_documents SET project_id=$2 WHERE document_id=$1`, documentID, projectID)
	if err != nil {
		return fmt.Errorf("set document project: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return fmt.Errorf("%w: document %s", ErrNotFound, documentID)
	}
	return nil
}

// StageMemoryCandidates stages a batch of validated candidates in one
// transaction. Validation happens per candidate before insert; a batch with
// any invalid candidate fails whole, so partial lanes cannot leak. Any actor
// may stage — that is the only lane models get.
func (s *Store) StageMemoryCandidates(ctx context.Context, candidates []projectmemory.Candidate) error {
	if len(candidates) == 0 {
		return fmt.Errorf("%w: candidate batch is empty", ErrInvalidRecord)
	}
	batchID := candidates[0].BatchID
	for i := range candidates {
		if err := validateID(candidates[i].CandidateID, "cand_", "candidate_id"); err != nil {
			return err
		}
		if err := validateID(candidates[i].BatchID, "bat_", "batch_id"); err != nil {
			return err
		}
		if candidates[i].BatchID != batchID {
			return fmt.Errorf("%w: batch mixes batch ids %q and %q", ErrInvalidRecord, batchID, candidates[i].BatchID)
		}
		warnings, err := projectmemory.ValidateCandidate(&candidates[i])
		if err != nil {
			return fmt.Errorf("%w: candidate %s: %v", ErrInvalidRecord, candidates[i].CandidateID, err)
		}
		candidates[i].Warnings = warnings
		if candidates[i].Status == "" {
			candidates[i].Status = "staged"
		}
		if candidates[i].Status != "staged" {
			return fmt.Errorf("%w: candidate %s must stage as staged", ErrInvalidRecord, candidates[i].CandidateID)
		}
		if candidates[i].SubmittedByType == "" {
			return fmt.Errorf("%w: candidate %s requires submitted_by_type", ErrInvalidRecord, candidates[i].CandidateID)
		}
		if candidates[i].CreatedAt.IsZero() {
			candidates[i].CreatedAt = time.Now().UTC()
		}
	}
	return s.InTransaction(ctx, func(tx *Tx) error {
		for i := range candidates {
			refs, err := json.Marshal(candidates[i].SourceRefs)
			if err != nil {
				return err
			}
			warnings, err := json.Marshal(candidates[i].Warnings)
			if err != nil {
				return err
			}
			if _, err := tx.tx.ExecContext(ctx, `
				INSERT INTO project_memory_candidates
				(candidate_id, batch_id, project_id, subject, predicate, object, as_of, source_run_id, source_refs,
				 vocabulary_version, extended_predicate, warnings, status, submitted_by_type, submitted_by_id, created_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),$9,$10,$11,$12,$13,$14,NULLIF($15,''),$16)
			`, candidates[i].CandidateID, candidates[i].BatchID, candidates[i].ProjectID, candidates[i].Subject,
				candidates[i].Predicate, candidates[i].Object, candidates[i].AsOf, candidates[i].SourceRunID, refs,
				candidates[i].VocabularyVersion, candidates[i].ExtendedPredicate, warnings, candidates[i].Status,
				candidates[i].SubmittedByType, candidates[i].SubmittedByID, candidates[i].CreatedAt); err != nil {
				return fmt.Errorf("stage candidate %s: %w", candidates[i].CandidateID, err)
			}
		}
		return nil
	})
}

// ListStagedMemoryCandidates returns the staged candidates of one batch in
// insertion order, or all staged candidates of a project when batchID is "".
func (s *Store) ListStagedMemoryCandidates(ctx context.Context, projectID, batchID string) ([]projectmemory.Candidate, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, fmt.Errorf("%w: project id required", ErrInvalidRecord)
	}
	query := `
		SELECT candidate_id, batch_id, project_id, subject, predicate, object, as_of, COALESCE(source_run_id,''), source_refs,
		 vocabulary_version, extended_predicate, warnings, status, submitted_by_type, COALESCE(submitted_by_id,''), created_at
		FROM project_memory_candidates
		WHERE project_id=$1 AND status='staged'
	`
	args := []any{projectID}
	if batchID != "" {
		query += ` AND batch_id=$2`
		args = append(args, batchID)
	}
	query += ` ORDER BY created_at, candidate_id`
	return scanMemoryCandidates(s.db.QueryContext(ctx, query, args...))
}

// scanMemoryCandidates drains a candidate result set.
func scanMemoryCandidates(rows *sql.Rows, err error) ([]projectmemory.Candidate, error) {
	if err != nil {
		return nil, fmt.Errorf("list memory candidates: %w", err)
	}
	defer rows.Close()
	candidates := []projectmemory.Candidate{}
	for rows.Next() {
		var candidate projectmemory.Candidate
		var refs, warnings []byte
		if err := rows.Scan(&candidate.CandidateID, &candidate.BatchID, &candidate.ProjectID, &candidate.Subject,
			&candidate.Predicate, &candidate.Object, &candidate.AsOf, &candidate.SourceRunID, &refs,
			&candidate.VocabularyVersion, &candidate.ExtendedPredicate, &warnings, &candidate.Status,
			&candidate.SubmittedByType, &candidate.SubmittedByID, &candidate.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(refs, &candidate.SourceRefs); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(warnings, &candidate.Warnings); err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	return candidates, rows.Err()
}

// CommitMemoryCandidate promotes one staged candidate into a canonical fact.
// The gate is HITL-only: the committing actor must be a user. The fact's
// validity interval opens at the candidate's as-of instant. Re-committing a
// fact whose active content key already matches is an idempotent no-op that
// still closes the candidate, so replayed requests cannot duplicate canon.
func (s *Store) CommitMemoryCandidate(ctx context.Context, candidateID string, actor Actor, factID string) (projectmemory.Fact, bool, error) {
	if err := validateID(factID, "fact_", "fact_id"); err != nil {
		return projectmemory.Fact{}, false, err
	}
	if err := actor.Validate(); err != nil {
		return projectmemory.Fact{}, false, err
	}
	if actor.Type != ActorUser {
		return projectmemory.Fact{}, false, fmt.Errorf("%w: project memory commit requires a user actor, got %q", ErrInvalidRecord, actor.Type)
	}
	var fact projectmemory.Fact
	committed := false
	err := s.InTransaction(ctx, func(tx *Tx) error {
		var candidate projectmemory.Candidate
		var refs, warnings []byte
		err := tx.tx.QueryRowContext(ctx, `
			SELECT candidate_id, batch_id, project_id, subject, predicate, object, as_of, COALESCE(source_run_id,''), source_refs,
			 vocabulary_version, extended_predicate, warnings, status, submitted_by_type, COALESCE(submitted_by_id,''), created_at
			FROM project_memory_candidates WHERE candidate_id=$1
		`, candidateID).Scan(&candidate.CandidateID, &candidate.BatchID, &candidate.ProjectID, &candidate.Subject,
			&candidate.Predicate, &candidate.Object, &candidate.AsOf, &candidate.SourceRunID, &refs,
			&candidate.VocabularyVersion, &candidate.ExtendedPredicate, &warnings, &candidate.Status,
			&candidate.SubmittedByType, &candidate.SubmittedByID, &candidate.CreatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: candidate %s", ErrNotFound, candidateID)
		}
		if err != nil {
			return fmt.Errorf("load candidate: %w", err)
		}
		if err := json.Unmarshal(refs, &candidate.SourceRefs); err != nil {
			return err
		}
		if candidate.Status != "staged" {
			return fmt.Errorf("%w: candidate %s is %q, only staged candidates can be committed", ErrConflict, candidateID, candidate.Status)
		}
		contentHash := projectmemory.ContentKey(candidate.ProjectID, candidate.Subject, candidate.Predicate, candidate.Object, candidate.VocabularyVersion)
		var existingID string
		err = tx.tx.QueryRowContext(ctx, `
			SELECT fact_id FROM project_facts WHERE project_id=$1 AND content_hash=$2 AND valid_to IS NULL
		`, candidate.ProjectID, contentHash).Scan(&existingID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("probe active fact: %w", err)
		}
		if err == nil {
			// Same content already canon: close the candidate, return the
			// existing fact unchanged.
			if _, err := tx.tx.ExecContext(ctx, `
				UPDATE project_memory_candidates SET status='committed' WHERE candidate_id=$1
			`, candidateID); err != nil {
				return fmt.Errorf("close duplicate candidate: %w", err)
			}
			fact, err = scanFact(s.db.QueryRowContext(ctx, factColumns+` FROM project_facts WHERE fact_id=$1`, existingID))
			return err
		}
		fact = projectmemory.Fact{FactID: factID, ProjectID: candidate.ProjectID,
			Subject: candidate.Subject, Predicate: candidate.Predicate, Object: candidate.Object,
			ValidFrom: candidate.AsOf, SourceRunID: candidate.SourceRunID, SourceRefs: candidate.SourceRefs,
			VocabularyVersion: candidate.VocabularyVersion, ExtendedPredicate: candidate.ExtendedPredicate,
			ContentHash: contentHash, CandidateID: candidate.CandidateID,
			CommittedByType: string(actor.Type), CommittedByID: actor.ID, CreatedAt: time.Now().UTC()}
		// Single-valued predicates follow the one-active-fact rule: the new
		// state supersedes the previous one, and cannot predate it (the closed
		// interval must still satisfy valid_to > valid_from). Multi-valued
		// predicates keep coexisting active facts.
		superseded := []string{}
		if projectmemory.SingleValued(candidate.Predicate) {
			predecessors, err := tx.tx.QueryContext(ctx, `
				SELECT fact_id, valid_from FROM project_facts
				WHERE project_id=$1 AND subject=$2 AND predicate=$3 AND valid_to IS NULL AND content_hash<>$4
				ORDER BY valid_from
			`, candidate.ProjectID, candidate.Subject, candidate.Predicate, contentHash)
			if err != nil {
				return fmt.Errorf("probe predecessors: %w", err)
			}
			for predecessors.Next() {
				var predecessorID string
				var validFrom time.Time
				if err := predecessors.Scan(&predecessorID, &validFrom); err != nil {
					predecessors.Close()
					return err
				}
				if !candidate.AsOf.After(validFrom) {
					predecessors.Close()
					return fmt.Errorf("%w: state change %s is at or before the active fact %s valid_from", ErrConflict, candidateID, predecessorID)
				}
				superseded = append(superseded, predecessorID)
			}
			if err := predecessors.Err(); err != nil {
				predecessors.Close()
				return err
			}
			predecessors.Close()
		}
		refsJSON, err := json.Marshal(fact.SourceRefs)
		if err != nil {
			return err
		}
		if _, err := tx.tx.ExecContext(ctx, `
			INSERT INTO project_facts
			(fact_id, project_id, subject, predicate, object, valid_from, source_run_id, source_refs,
			 vocabulary_version, extended_predicate, content_hash, candidate_id, committed_by_type, committed_by_id, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,$9,$10,$11,$12,$13,$14,$15)
		`, fact.FactID, fact.ProjectID, fact.Subject, fact.Predicate, fact.Object, fact.ValidFrom,
			fact.SourceRunID, refsJSON, fact.VocabularyVersion, fact.ExtendedPredicate, fact.ContentHash,
			fact.CandidateID, fact.CommittedByType, fact.CommittedByID, fact.CreatedAt); err != nil {
			return fmt.Errorf("insert fact: %w", err)
		}
		for _, predecessorID := range superseded {
			if _, err := tx.tx.ExecContext(ctx, `
				UPDATE project_facts SET valid_to=$2, superseded_by=$3 WHERE fact_id=$1
			`, predecessorID, fact.ValidFrom, fact.FactID); err != nil {
				return fmt.Errorf("supersede predecessor %s: %w", predecessorID, err)
			}
		}
		if _, err := tx.tx.ExecContext(ctx, `
			UPDATE project_memory_candidates SET status='committed' WHERE candidate_id=$1
		`, candidateID); err != nil {
			return fmt.Errorf("close candidate: %w", err)
		}
		committed = true
		return nil
	})
	if err != nil {
		return projectmemory.Fact{}, false, err
	}
	return fact, committed, nil
}

// RejectMemoryCandidate marks a staged candidate rejected. The gate is
// HITL-only, mirroring commit.
func (s *Store) RejectMemoryCandidate(ctx context.Context, candidateID string, actor Actor) error {
	if err := actor.Validate(); err != nil {
		return err
	}
	if actor.Type != ActorUser {
		return fmt.Errorf("%w: project memory rejection requires a user actor, got %q", ErrInvalidRecord, actor.Type)
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE project_memory_candidates SET status='rejected'
		WHERE candidate_id=$1 AND status='staged'
	`, candidateID)
	if err != nil {
		return fmt.Errorf("reject candidate: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return fmt.Errorf("%w: staged candidate %s", ErrNotFound, candidateID)
	}
	return nil
}

const factColumns = `
	SELECT fact_id, project_id, subject, predicate, object, valid_from, valid_to, COALESCE(superseded_by,''),
	 COALESCE(source_run_id,''), source_refs, vocabulary_version, extended_predicate, content_hash,
	 COALESCE(candidate_id,''), committed_by_type, COALESCE(committed_by_id,''), created_at
`

// scanFact reads one fact row.
func scanFact(row *sql.Row) (projectmemory.Fact, error) {
	var fact projectmemory.Fact
	var refs []byte
	var validTo sql.NullTime
	err := row.Scan(&fact.FactID, &fact.ProjectID, &fact.Subject, &fact.Predicate, &fact.Object, &fact.ValidFrom, &validTo,
		&fact.SupersededBy, &fact.SourceRunID, &refs, &fact.VocabularyVersion, &fact.ExtendedPredicate, &fact.ContentHash,
		&fact.CandidateID, &fact.CommittedByType, &fact.CommittedByID, &fact.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return projectmemory.Fact{}, ErrNotFound
	}
	if err != nil {
		return projectmemory.Fact{}, fmt.Errorf("get fact: %w", err)
	}
	if validTo.Valid {
		fact.ValidTo = &validTo.Time
	}
	if err := json.Unmarshal(refs, &fact.SourceRefs); err != nil {
		return projectmemory.Fact{}, err
	}
	return fact, nil
}

// GetFact loads one canonical fact.
func (s *Store) GetFact(ctx context.Context, factID string) (projectmemory.Fact, error) {
	return scanFact(s.db.QueryRowContext(ctx, factColumns+` FROM project_facts WHERE fact_id=$1`, factID))
}

// SupersedeFact closes an active fact's validity interval and links its
// successor. It is the only write path to valid_to/superseded_by, and it is
// HITL-only: interval invalidation is a canon mutation, not bookkeeping.
func (s *Store) SupersedeFact(ctx context.Context, factID, successorID string, validTo time.Time, actor Actor) error {
	if err := validateID(successorID, "fact_", "successor fact_id"); err != nil {
		return err
	}
	if err := actor.Validate(); err != nil {
		return err
	}
	if actor.Type != ActorUser {
		return fmt.Errorf("%w: project memory supersede requires a user actor, got %q", ErrInvalidRecord, actor.Type)
	}
	if validTo.IsZero() {
		return fmt.Errorf("%w: valid_to is required", ErrInvalidRecord)
	}
	// Interval sanity is checked in SQL: the closed interval must still satisfy
	// valid_to > valid_from, matching the schema constraint.
	result, err := s.db.ExecContext(ctx, `
		UPDATE project_facts SET valid_to=$2, superseded_by=$3
		WHERE fact_id=$1 AND valid_to IS NULL AND valid_from < $2
	`, factID, validTo, successorID)
	if err != nil {
		return fmt.Errorf("supersede fact: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return fmt.Errorf("%w: active fact %s (or invalid interval)", ErrNotFound, factID)
	}
	return nil
}

// ListActiveFacts returns the current canon of one project, optionally
// filtered to one subject.
func (s *Store) ListActiveFacts(ctx context.Context, projectID, subject string) ([]projectmemory.Fact, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, fmt.Errorf("%w: project id required", ErrInvalidRecord)
	}
	query := factColumns + ` FROM project_facts WHERE project_id=$1 AND valid_to IS NULL`
	args := []any{projectID}
	if subject != "" {
		query += ` AND subject=$2`
		args = append(args, projectmemory.NormalizeSubject(subject))
	}
	query += ` ORDER BY subject, predicate, object`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list active facts: %w", err)
	}
	defer rows.Close()
	facts := []projectmemory.Fact{}
	for rows.Next() {
		var fact projectmemory.Fact
		var refs []byte
		var validTo sql.NullTime
		if err := rows.Scan(&fact.FactID, &fact.ProjectID, &fact.Subject, &fact.Predicate, &fact.Object, &fact.ValidFrom, &validTo,
			&fact.SupersededBy, &fact.SourceRunID, &refs, &fact.VocabularyVersion, &fact.ExtendedPredicate, &fact.ContentHash,
			&fact.CandidateID, &fact.CommittedByType, &fact.CommittedByID, &fact.CreatedAt); err != nil {
			return nil, err
		}
		if validTo.Valid {
			fact.ValidTo = &validTo.Time
		}
		if err := json.Unmarshal(refs, &fact.SourceRefs); err != nil {
			return nil, err
		}
		facts = append(facts, fact)
	}
	return facts, rows.Err()
}

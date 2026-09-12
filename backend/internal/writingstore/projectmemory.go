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

// marshalStringSlice marshals a string slice as a JSON array, normalizing a
// nil slice to [] so the jsonb_typeof='array' column checks never see null.
func marshalStringSlice(values []string) ([]byte, error) {
	if values == nil {
		return []byte("[]"), nil
	}
	return json.Marshal(values)
}

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

// DocumentProjectID resolves the project scope a document is attached to.
// ErrNotFound covers both the missing document and a document without a
// project — callers treat both as "no project scope".
func (s *Store) DocumentProjectID(ctx context.Context, documentID string) (string, error) {
	if err := validateID(documentID, "doc_", "document_id"); err != nil {
		return "", err
	}
	var projectID sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT project_id FROM writing_documents WHERE document_id=$1`, documentID).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("document project: %w", err)
	}
	if !projectID.Valid {
		return "", ErrNotFound
	}
	return projectID.String, nil
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
			refs, err := marshalStringSlice(candidates[i].SourceRefs)
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
		superseded, err := insertFactWithSupersede(ctx, tx, fact)
		if err != nil {
			return err
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

// insertFactWithSupersede inserts a canon fact and resolves the single-valued
// predecessor rule: single-valued predicates follow the one-active-fact rule
// (the new state supersedes the previous one and cannot predate it, keeping
// valid_to > valid_from); multi-valued predicates keep coexisting active
// facts. Returns the predecessor ids whose intervals the caller must close.
func insertFactWithSupersede(ctx context.Context, tx *Tx, fact projectmemory.Fact) ([]string, error) {
	superseded := []string{}
	if projectmemory.SingleValued(fact.Predicate) {
		predecessors, err := tx.tx.QueryContext(ctx, `
			SELECT fact_id, valid_from FROM project_facts
			WHERE project_id=$1 AND subject=$2 AND predicate=$3 AND valid_to IS NULL AND content_hash<>$4
			ORDER BY valid_from
		`, fact.ProjectID, fact.Subject, fact.Predicate, fact.ContentHash)
		if err != nil {
			return nil, fmt.Errorf("probe predecessors: %w", err)
		}
		for predecessors.Next() {
			var predecessorID string
			var validFrom time.Time
			if err := predecessors.Scan(&predecessorID, &validFrom); err != nil {
				predecessors.Close()
				return nil, err
			}
			if !fact.ValidFrom.After(validFrom) {
				predecessors.Close()
				return nil, fmt.Errorf("%w: state change %s is at or before the active fact %s valid_from", ErrConflict, fact.CandidateID, predecessorID)
			}
			superseded = append(superseded, predecessorID)
		}
		if err := predecessors.Err(); err != nil {
			predecessors.Close()
			return nil, err
		}
		predecessors.Close()
	}
	refsJSON, err := marshalStringSlice(fact.SourceRefs)
	if err != nil {
		return nil, err
	}
	if _, err := tx.tx.ExecContext(ctx, `
		INSERT INTO project_facts
		(fact_id, project_id, subject, predicate, object, valid_from, source_run_id, source_refs,
		 vocabulary_version, extended_predicate, content_hash, candidate_id, committed_by_type, committed_by_id, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,$9,$10,$11,$12,$13,$14,$15)
	`, fact.FactID, fact.ProjectID, fact.Subject, fact.Predicate, fact.Object, fact.ValidFrom,
		fact.SourceRunID, refsJSON, fact.VocabularyVersion, fact.ExtendedPredicate, fact.ContentHash,
		fact.CandidateID, fact.CommittedByType, fact.CommittedByID, fact.CreatedAt); err != nil {
		return nil, fmt.Errorf("insert fact: %w", err)
	}
	return superseded, nil
}

// StageMemoryClaims stages a batch of claims into the corroboration lane.
// Any actor may raise a claim; the batch fails whole on any invalid item.
// Re-raising a triple that already has an open/supported claim in the
// project is rejected: corroboration accumulates on the existing claim, so
// duplicates cannot fragment the evidence ledger.
func (s *Store) StageMemoryClaims(ctx context.Context, claims []projectmemory.Claim) error {
	if len(claims) == 0 {
		return fmt.Errorf("%w: claim batch is empty", ErrInvalidRecord)
	}
	batchID := claims[0].BatchID
	for i := range claims {
		if err := validateID(claims[i].ClaimID, "claim_", "claim_id"); err != nil {
			return err
		}
		if err := validateID(claims[i].BatchID, "bat_", "batch_id"); err != nil {
			return err
		}
		if claims[i].BatchID != batchID {
			return fmt.Errorf("%w: batch mixes batch ids %q and %q", ErrInvalidRecord, batchID, claims[i].BatchID)
		}
		if claims[i].RaisedByType == "" {
			return fmt.Errorf("%w: claim %s requires raised_by_type", ErrInvalidRecord, claims[i].ClaimID)
		}
		warnings, err := projectmemory.ValidateClaim(&claims[i])
		if err != nil {
			return fmt.Errorf("%w: claim %s: %v", ErrInvalidRecord, claims[i].ClaimID, err)
		}
		_ = warnings
		if claims[i].Status == "" {
			claims[i].Status = "open"
		}
		if claims[i].Status != "open" {
			return fmt.Errorf("%w: claim %s must stage as open", ErrInvalidRecord, claims[i].ClaimID)
		}
		if claims[i].CreatedAt.IsZero() {
			claims[i].CreatedAt = time.Now().UTC()
		}
		claims[i].ContentHash = projectmemory.ContentKey(claims[i].ProjectID, claims[i].Subject, claims[i].Predicate, claims[i].Object, claims[i].VocabularyVersion)
	}
	return s.InTransaction(ctx, func(tx *Tx) error {
		for i := range claims {
			refs, err := marshalStringSlice(claims[i].SourceRefs)
			if err != nil {
				return err
			}
			var existingID string
			err = tx.tx.QueryRowContext(ctx, `
				SELECT claim_id FROM project_claims WHERE project_id=$1 AND content_hash=$2 AND status IN ('open','supported')
			`, claims[i].ProjectID, claims[i].ContentHash).Scan(&existingID)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("probe open claim: %w", err)
			}
			if err == nil {
				return fmt.Errorf("%w: claim %s duplicates open claim %s", ErrConflict, claims[i].ClaimID, existingID)
			}
			if _, err := tx.tx.ExecContext(ctx, `
				INSERT INTO project_claims
				(claim_id, batch_id, project_id, subject, predicate, object, as_of, raised_by_type, raised_by_id,
				 source_run_id, source_refs, vocabulary_version, extended_predicate, content_hash, status, created_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''),NULLIF($10,''),$11,$12,$13,$14,$15,$16)
			`, claims[i].ClaimID, claims[i].BatchID, claims[i].ProjectID, claims[i].Subject, claims[i].Predicate,
				claims[i].Object, claims[i].AsOf, claims[i].RaisedByType, claims[i].RaisedByID,
				claims[i].SourceRunID, refs, claims[i].VocabularyVersion, claims[i].ExtendedPredicate,
				claims[i].ContentHash, claims[i].Status, claims[i].CreatedAt); err != nil {
				return fmt.Errorf("stage claim %s: %w", claims[i].ClaimID, err)
			}
			// The raising run is the claim's first evidence citation.
			if _, err := tx.tx.ExecContext(ctx, `
				INSERT INTO project_claim_evidence
				(evidence_id, claim_id, evidence_hash, source_run_id, source_refs, recorded_by_type, recorded_by_id)
				VALUES ($1,$2,$3,NULLIF($4,''),$5,$6,NULLIF($7,''))
			`, StableID("evd_", claims[i].ClaimID, claims[i].ContentHash, "raise"), claims[i].ClaimID,
				projectmemory.EvidenceHash(claims[i].SourceRunID, claims[i].SourceRefs),
				claims[i].SourceRunID, refs, claims[i].RaisedByType, claims[i].RaisedByID); err != nil {
				return fmt.Errorf("record raising evidence %s: %w", claims[i].ClaimID, err)
			}
		}
		return nil
	})
}

// CorroborateMemoryClaim records one evidence citation. Any actor may
// corroborate — gathering sources is machine work. Recording is idempotent
// per evidence hash; when distinct citations reach the support threshold the
// claim flips from open to supported. Support never auto-promotes.
func (s *Store) CorroborateMemoryClaim(ctx context.Context, claimID, evidenceID string, sourceRunID string, sourceRefs []string, actor Actor) (projectmemory.Claim, error) {
	if err := validateID(evidenceID, "evd_", "evidence_id"); err != nil {
		return projectmemory.Claim{}, err
	}
	if err := actor.Validate(); err != nil {
		return projectmemory.Claim{}, err
	}
	if len(sourceRefs) == 0 && strings.TrimSpace(sourceRunID) == "" {
		return projectmemory.Claim{}, fmt.Errorf("%w: evidence requires source_refs or source_run_id", ErrInvalidRecord)
	}
	claim := projectmemory.Claim{}
	err := s.InTransaction(ctx, func(tx *Tx) error {
		var refs []byte
		var promotedAt sql.NullTime
		err := tx.tx.QueryRowContext(ctx, `
			SELECT claim_id, batch_id, project_id, subject, predicate, object, as_of, raised_by_type, COALESCE(raised_by_id,''),
			 COALESCE(source_run_id,''), source_refs, vocabulary_version, extended_predicate, content_hash, status,
			 COALESCE(promoted_fact_id,''), promoted_at, created_at
			FROM project_claims WHERE claim_id=$1
		`, claimID).Scan(&claim.ClaimID, &claim.BatchID, &claim.ProjectID, &claim.Subject, &claim.Predicate, &claim.Object,
			&claim.AsOf, &claim.RaisedByType, &claim.RaisedByID, &claim.SourceRunID, &refs, &claim.VocabularyVersion,
			&claim.ExtendedPredicate, &claim.ContentHash, &claim.Status, &claim.PromotedFactID, &promotedAt, &claim.CreatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: claim %s", ErrNotFound, claimID)
		}
		if err != nil {
			return fmt.Errorf("load claim: %w", err)
		}
		if err := json.Unmarshal(refs, &claim.SourceRefs); err != nil {
			return err
		}
		if claim.Status != "open" && claim.Status != "supported" {
			return fmt.Errorf("%w: claim %s is %q and no longer accepts evidence", ErrConflict, claimID, claim.Status)
		}
		refsJSON, err := marshalStringSlice(sourceRefs)
		if err != nil {
			return err
		}
		evidenceHash := projectmemory.EvidenceHash(sourceRunID, sourceRefs)
		result, err := tx.tx.ExecContext(ctx, `
			INSERT INTO project_claim_evidence
			(evidence_id, claim_id, evidence_hash, source_run_id, source_refs, recorded_by_type, recorded_by_id)
			VALUES ($1,$2,$3,NULLIF($4,''),$5,$6,NULLIF($7,''))
			ON CONFLICT (claim_id, evidence_hash) DO NOTHING
		`, evidenceID, claimID, evidenceHash, sourceRunID, refsJSON, actor.Type, actor.ID)
		if err != nil {
			return fmt.Errorf("record evidence: %w", err)
		}
		if rows, _ := result.RowsAffected(); rows == 1 {
			var citations int
			if err := tx.tx.QueryRowContext(ctx, `
				SELECT COUNT(*) FROM project_claim_evidence WHERE claim_id=$1
			`, claimID).Scan(&citations); err != nil {
				return fmt.Errorf("count evidence: %w", err)
			}
			if claim.Status == "open" && int64(citations) >= projectmemory.ClaimSupportThreshold {
				if _, err := tx.tx.ExecContext(ctx, `
					UPDATE project_claims SET status='supported', updated_at=NOW() WHERE claim_id=$1
				`, claimID); err != nil {
					return fmt.Errorf("support claim: %w", err)
				}
				claim.Status = "supported"
			}
		}
		return nil
	})
	if err != nil {
		return projectmemory.Claim{}, err
	}
	return claim, nil
}

// CommitMemoryClaim promotes a claim into a canonical fact. The gate is
// HITL-only (user actor); support status is advisory context, so committing
// an open claim is allowed — the user judges, corroboration informs. The
// fact insertion reuses the candidate commit machinery, including idempotent
// handling of an already-canon triple.
func (s *Store) CommitMemoryClaim(ctx context.Context, claimID string, actor Actor, factID string) (projectmemory.Fact, bool, error) {
	if err := validateID(factID, "fact_", "fact_id"); err != nil {
		return projectmemory.Fact{}, false, err
	}
	if err := actor.Validate(); err != nil {
		return projectmemory.Fact{}, false, err
	}
	if actor.Type != ActorUser {
		return projectmemory.Fact{}, false, fmt.Errorf("%w: project memory claim promotion requires a user actor, got %q", ErrInvalidRecord, actor.Type)
	}
	var fact projectmemory.Fact
	var claim projectmemory.Claim
	err := s.InTransaction(ctx, func(tx *Tx) error {
		var refs []byte
		err := tx.tx.QueryRowContext(ctx, `
			SELECT claim_id, batch_id, project_id, subject, predicate, object, as_of, raised_by_type, COALESCE(raised_by_id,''),
			 COALESCE(source_run_id,''), source_refs, vocabulary_version, extended_predicate, content_hash, status,
			 COALESCE(promoted_fact_id,''), created_at
			FROM project_claims WHERE claim_id=$1
		`, claimID).Scan(&claim.ClaimID, &claim.BatchID, &claim.ProjectID, &claim.Subject, &claim.Predicate, &claim.Object,
			&claim.AsOf, &claim.RaisedByType, &claim.RaisedByID, &claim.SourceRunID, &refs, &claim.VocabularyVersion,
			&claim.ExtendedPredicate, &claim.ContentHash, &claim.Status, &claim.PromotedFactID, &claim.CreatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: claim %s", ErrNotFound, claimID)
		}
		if err != nil {
			return fmt.Errorf("load claim: %w", err)
		}
		if err := json.Unmarshal(refs, &claim.SourceRefs); err != nil {
			return err
		}
		if claim.Status != "open" && claim.Status != "supported" {
			return fmt.Errorf("%w: claim %s is %q, only open or supported claims can be promoted", ErrConflict, claimID, claim.Status)
		}
		contentHash := projectmemory.ContentKey(claim.ProjectID, claim.Subject, claim.Predicate, claim.Object, claim.VocabularyVersion)
		var existingID string
		err = tx.tx.QueryRowContext(ctx, `
			SELECT fact_id FROM project_facts WHERE project_id=$1 AND content_hash=$2 AND valid_to IS NULL
		`, claim.ProjectID, contentHash).Scan(&existingID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("probe active fact: %w", err)
		}
		if err == nil {
			if _, err := tx.tx.ExecContext(ctx, `
				UPDATE project_claims SET status='promoted', promoted_fact_id=$2, promoted_at=NOW(), updated_at=NOW() WHERE claim_id=$1
			`, claimID, existingID); err != nil {
				return fmt.Errorf("link promoted claim: %w", err)
			}
			fact, err = scanFact(s.db.QueryRowContext(ctx, factColumns+` FROM project_facts WHERE fact_id=$1`, existingID))
			return err
		}
		// Insert the canon fact with the same single-valued supersede rules
		// as the candidate commit path.
		fact = projectmemory.Fact{FactID: factID, ProjectID: claim.ProjectID,
			Subject: claim.Subject, Predicate: claim.Predicate, Object: claim.Object,
			ValidFrom: claim.AsOf, SourceRunID: claim.SourceRunID, SourceRefs: claim.SourceRefs,
			VocabularyVersion: claim.VocabularyVersion, ExtendedPredicate: claim.ExtendedPredicate,
			ContentHash: contentHash, CandidateID: "",
			CommittedByType: string(actor.Type), CommittedByID: actor.ID, CreatedAt: time.Now().UTC()}
		superseded, err := insertFactWithSupersede(ctx, tx, fact)
		if err != nil {
			return err
		}
		for _, predecessorID := range superseded {
			if _, err := tx.tx.ExecContext(ctx, `
				UPDATE project_facts SET valid_to=$2, superseded_by=$3 WHERE fact_id=$1
			`, predecessorID, fact.ValidFrom, fact.FactID); err != nil {
				return fmt.Errorf("supersede predecessor %s: %w", predecessorID, err)
			}
		}
		if _, err := tx.tx.ExecContext(ctx, `
			UPDATE project_claims SET status='promoted', promoted_fact_id=$2, promoted_at=NOW(), updated_at=NOW() WHERE claim_id=$1
		`, claimID, fact.FactID); err != nil {
			return fmt.Errorf("close promoted claim: %w", err)
		}
		return nil
	})
	if err != nil {
		return projectmemory.Fact{}, false, err
	}
	return fact, true, nil
}

// RejectMemoryClaim closes a claim as rejected. HITL-only, mirroring commit.
func (s *Store) RejectMemoryClaim(ctx context.Context, claimID string, actor Actor) error {
	if err := actor.Validate(); err != nil {
		return err
	}
	if actor.Type != ActorUser {
		return fmt.Errorf("%w: project memory claim rejection requires a user actor, got %q", ErrInvalidRecord, actor.Type)
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE project_claims SET status='rejected', updated_at=NOW()
		WHERE claim_id=$1 AND status IN ('open','supported')
	`, claimID)
	if err != nil {
		return fmt.Errorf("reject claim: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return fmt.Errorf("%w: rejectable claim %s", ErrNotFound, claimID)
	}
	return nil
}

// ListMemoryClaims returns the claims of one project, optionally filtered by
// status (pass "" for all).
func (s *Store) ListMemoryClaims(ctx context.Context, projectID, status string) ([]projectmemory.Claim, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, fmt.Errorf("%w: project id required", ErrInvalidRecord)
	}
	query := `
		SELECT claim_id, batch_id, project_id, subject, predicate, object, as_of, raised_by_type, COALESCE(raised_by_id,''),
		 COALESCE(source_run_id,''), source_refs, vocabulary_version, extended_predicate, content_hash, status,
		 COALESCE(promoted_fact_id,''), created_at
		FROM project_claims WHERE project_id=$1
	`
	args := []any{projectID}
	if status != "" {
		query += ` AND status=$2`
		args = append(args, status)
	}
	query += ` ORDER BY created_at, claim_id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list claims: %w", err)
	}
	defer rows.Close()
	claims := []projectmemory.Claim{}
	for rows.Next() {
		var claim projectmemory.Claim
		var refs []byte
		if err := rows.Scan(&claim.ClaimID, &claim.BatchID, &claim.ProjectID, &claim.Subject, &claim.Predicate, &claim.Object,
			&claim.AsOf, &claim.RaisedByType, &claim.RaisedByID, &claim.SourceRunID, &refs, &claim.VocabularyVersion,
			&claim.ExtendedPredicate, &claim.ContentHash, &claim.Status, &claim.PromotedFactID, &claim.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(refs, &claim.SourceRefs); err != nil {
			return nil, err
		}
		claims = append(claims, claim)
	}
	return claims, rows.Err()
}

// StageMemoryEntity stages an entity birth certificate into the candidate
// pool. Any actor may raise entities; the (project, kind, canonical name)
// identity must not collide with a live entity.
func (s *Store) StageMemoryEntity(ctx context.Context, entity *projectmemory.Entity) error {
	if err := validateID(entity.EntityID, "ent_", "entity_id"); err != nil {
		return err
	}
	if entity.RaisedByType == "" {
		return fmt.Errorf("%w: entity %s requires raised_by_type", ErrInvalidRecord, entity.EntityID)
	}
	if _, err := projectmemory.ValidateEntity(entity); err != nil {
		return fmt.Errorf("%w: entity %s: %v", ErrInvalidRecord, entity.EntityID, err)
	}
	if entity.Status == "" {
		entity.Status = "candidate"
	}
	if entity.Status != "candidate" {
		return fmt.Errorf("%w: entity %s must stage as candidate", ErrInvalidRecord, entity.EntityID)
	}
	if entity.CreatedAt.IsZero() {
		entity.CreatedAt = time.Now().UTC()
	}
	aliases, err := marshalStringSlice(entity.Aliases)
	if err != nil {
		return err
	}
	refs, err := marshalStringSlice(entity.SourceRefs)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO project_entities
		(entity_id, project_id, entity_kind, canonical_name, aliases, description, source_run_id, source_refs,
		 status, raised_by_type, raised_by_id, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,$9,$10,NULLIF($11,''),$12)
	`, entity.EntityID, entity.ProjectID, entity.EntityKind, entity.CanonicalName, aliases, entity.Description,
		entity.SourceRunID, refs, entity.Status, entity.RaisedByType, entity.RaisedByID, entity.CreatedAt)
	if err != nil {
		return fmt.Errorf("stage entity: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return fmt.Errorf("%w: entity %s", ErrConflict, entity.EntityID)
	}
	return nil
}

// PromoteMemoryEntity moves a candidate entity into the live registry.
// HITL-only, mirroring canon commits.
func (s *Store) PromoteMemoryEntity(ctx context.Context, entityID string, actor Actor) error {
	if err := actor.Validate(); err != nil {
		return err
	}
	if actor.Type != ActorUser {
		return fmt.Errorf("%w: entity promotion requires a user actor, got %q", ErrInvalidRecord, actor.Type)
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE project_entities SET status='promoted', promoted_by_type=$2, promoted_by_id=NULLIF($3,''),
		 promoted_at=NOW(), updated_at=NOW()
		WHERE entity_id=$1 AND status='candidate'
	`, entityID, actor.Type, actor.ID)
	if err != nil {
		return fmt.Errorf("promote entity: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return fmt.Errorf("%w: candidate entity %s", ErrNotFound, entityID)
	}
	return nil
}

// ArchiveMemoryEntity retires an entity, freeing its (project, kind, name)
// identity for re-registration. HITL-only.
func (s *Store) ArchiveMemoryEntity(ctx context.Context, entityID string, actor Actor) error {
	if err := actor.Validate(); err != nil {
		return err
	}
	if actor.Type != ActorUser {
		return fmt.Errorf("%w: entity archival requires a user actor, got %q", ErrInvalidRecord, actor.Type)
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE project_entities SET status='archived', updated_at=NOW()
		WHERE entity_id=$1 AND status IN ('candidate','promoted')
	`, entityID)
	if err != nil {
		return fmt.Errorf("archive entity: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return fmt.Errorf("%w: archivable entity %s", ErrNotFound, entityID)
	}
	return nil
}

// ListMemoryEntities returns the entities of one project, optionally
// filtered by kind and status (pass "" for all).
func (s *Store) ListMemoryEntities(ctx context.Context, projectID, entityKind, status string) ([]projectmemory.Entity, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, fmt.Errorf("%w: project id required", ErrInvalidRecord)
	}
	query := `
		SELECT entity_id, project_id, entity_kind, canonical_name, aliases, description, COALESCE(source_run_id,''),
		 source_refs, status, raised_by_type, COALESCE(raised_by_id,''), COALESCE(promoted_by_type,''),
		 COALESCE(promoted_by_id,''), created_at
		FROM project_entities WHERE project_id=$1
	`
	args := []any{projectID}
	if entityKind != "" {
		args = append(args, entityKind)
		query += fmt.Sprintf(` AND entity_kind=$%d`, len(args))
	}
	if status != "" {
		args = append(args, status)
		query += fmt.Sprintf(` AND status=$%d`, len(args))
	}
	query += ` ORDER BY created_at, entity_id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list entities: %w", err)
	}
	defer rows.Close()
	entities := []projectmemory.Entity{}
	for rows.Next() {
		var entity projectmemory.Entity
		var aliases, refs []byte
		if err := rows.Scan(&entity.EntityID, &entity.ProjectID, &entity.EntityKind, &entity.CanonicalName, &aliases,
			&entity.Description, &entity.SourceRunID, &refs, &entity.Status, &entity.RaisedByType, &entity.RaisedByID,
			&entity.PromotedByType, &entity.PromotedByID, &entity.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(aliases, &entity.Aliases); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(refs, &entity.SourceRefs); err != nil {
			return nil, err
		}
		entities = append(entities, entity)
	}
	return entities, rows.Err()
}

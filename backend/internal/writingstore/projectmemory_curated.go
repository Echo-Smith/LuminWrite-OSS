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

// M2.5 curated project state: terminology, decisions, open questions, and
// threads. All four share the governance pattern — any actor stages a
// candidate, promotion and lifecycle closures are user-only, content columns
// are immutable at the database level.

// StageTerminology stages a terminology proposal.
func (s *Store) StageTerminology(ctx context.Context, entry *projectmemory.Terminology) error {
	if err := validateID(entry.TerminologyID, "term_", "terminology_id"); err != nil {
		return err
	}
	if entry.RaisedByType == "" {
		return fmt.Errorf("%w: terminology %s requires raised_by_type", ErrInvalidRecord, entry.TerminologyID)
	}
	if err := projectmemory.ValidateTerminology(entry); err != nil {
		return fmt.Errorf("%w: terminology %s: %v", ErrInvalidRecord, entry.TerminologyID, err)
	}
	entry.Status = "candidate"
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	aliases, err := marshalStringSlice(entry.Aliases)
	if err != nil {
		return err
	}
	forbidden, err := marshalStringSlice(entry.Forbidden)
	if err != nil {
		return err
	}
	refs, err := marshalStringSlice(entry.SourceRefs)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO project_terminology
		(terminology_id, project_id, term, definition, aliases, forbidden, source_run_id, source_refs,
		 status, raised_by_type, raised_by_id, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,'candidate',$9,NULLIF($10,''),$11)
	`, entry.TerminologyID, entry.ProjectID, entry.Term, entry.Definition, aliases, forbidden,
		entry.SourceRunID, refs, entry.RaisedByType, entry.RaisedByID, entry.CreatedAt)
	if err != nil {
		return fmt.Errorf("stage terminology: %w", err)
	}
	return nil
}

// PromoteTerminology moves a candidate entry into the active glossary. The
// partial unique index enforces one live entry per (project, term); archiving
// frees the term for re-registration. HITL-only.
func (s *Store) PromoteTerminology(ctx context.Context, terminologyID string, actor Actor) error {
	if err := actor.Validate(); err != nil {
		return err
	}
	if actor.Type != ActorUser {
		return fmt.Errorf("%w: terminology promotion requires a user actor, got %q", ErrInvalidRecord, actor.Type)
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE project_terminology SET status='active', promoted_by_type=$2, promoted_by_id=NULLIF($3,''),
		 promoted_at=NOW(), updated_at=NOW()
		WHERE terminology_id=$1 AND status='candidate'
	`, terminologyID, actor.Type, actor.ID)
	if err != nil {
		return fmt.Errorf("promote terminology: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return fmt.Errorf("%w: candidate terminology %s", ErrNotFound, terminologyID)
	}
	return nil
}

// ArchiveTerminology retires an entry, freeing its term. HITL-only.
func (s *Store) ArchiveTerminology(ctx context.Context, terminologyID string, actor Actor) error {
	if err := actor.Validate(); err != nil {
		return err
	}
	if actor.Type != ActorUser {
		return fmt.Errorf("%w: terminology archival requires a user actor, got %q", ErrInvalidRecord, actor.Type)
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE project_terminology SET status='archived', updated_at=NOW()
		WHERE terminology_id=$1 AND status IN ('candidate','active')
	`, terminologyID)
	if err != nil {
		return fmt.Errorf("archive terminology: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return fmt.Errorf("%w: archivable terminology %s", ErrNotFound, terminologyID)
	}
	return nil
}

// ListTerminology returns the glossary of one project, optionally filtered by
// status (pass "" for all).
func (s *Store) ListTerminology(ctx context.Context, projectID, status string) ([]projectmemory.Terminology, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, fmt.Errorf("%w: project id required", ErrInvalidRecord)
	}
	query := `
		SELECT terminology_id, project_id, term, definition, aliases, forbidden, COALESCE(source_run_id,''),
		 source_refs, status, raised_by_type, COALESCE(raised_by_id,''), created_at
		FROM project_terminology WHERE project_id=$1
	`
	args := []any{projectID}
	if status != "" {
		args = append(args, status)
		query += fmt.Sprintf(` AND status=$%d`, len(args))
	}
	query += ` ORDER BY created_at, terminology_id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list terminology: %w", err)
	}
	defer rows.Close()
	entries := []projectmemory.Terminology{}
	for rows.Next() {
		var entry projectmemory.Terminology
		var aliases, forbidden, refs []byte
		if err := rows.Scan(&entry.TerminologyID, &entry.ProjectID, &entry.Term, &entry.Definition, &aliases,
			&forbidden, &entry.SourceRunID, &refs, &entry.Status, &entry.RaisedByType, &entry.RaisedByID,
			&entry.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(aliases, &entry.Aliases); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(forbidden, &entry.Forbidden); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(refs, &entry.SourceRefs); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// StageDecision stages a decision proposal. An optional supersedes target is
// only linked at promotion time, when the target must still be active.
func (s *Store) StageDecision(ctx context.Context, decision *projectmemory.Decision) error {
	if err := validateID(decision.DecisionID, "dec_", "decision_id"); err != nil {
		return err
	}
	if decision.RaisedByType == "" {
		return fmt.Errorf("%w: decision %s requires raised_by_type", ErrInvalidRecord, decision.DecisionID)
	}
	if err := projectmemory.ValidateDecision(decision); err != nil {
		return fmt.Errorf("%w: decision %s: %v", ErrInvalidRecord, decision.DecisionID, err)
	}
	decision.Status = "candidate"
	if decision.CreatedAt.IsZero() {
		decision.CreatedAt = time.Now().UTC()
	}
	refs, err := marshalStringSlice(decision.SourceRefs)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO project_decisions
		(decision_id, project_id, statement, rationale, decided_at, supersedes, source_run_id, source_refs,
		 status, raised_by_type, raised_by_id, created_at)
		VALUES ($1,$2,$3,$4,$5,NULLIF($6,''),NULLIF($7,''),$8,'candidate',$9,NULLIF($10,''),$11)
	`, decision.DecisionID, decision.ProjectID, decision.Statement, decision.Rationale, decision.DecidedAt,
		decision.Supersedes, decision.SourceRunID, refs, decision.RaisedByType, decision.RaisedByID, decision.CreatedAt)
	if err != nil {
		return fmt.Errorf("stage decision: %w", err)
	}
	return nil
}

// PromoteDecision activates a decision. HITL-only. When the proposal carries
// a supersedes link, the target must still be active and is closed in the
// same transaction — a decision cannot supersede an already-superseded one.
func (s *Store) PromoteDecision(ctx context.Context, decisionID string, actor Actor) error {
	if err := actor.Validate(); err != nil {
		return err
	}
	if actor.Type != ActorUser {
		return fmt.Errorf("%w: decision promotion requires a user actor, got %q", ErrInvalidRecord, actor.Type)
	}
	return s.InTransaction(ctx, func(tx *Tx) error {
		var supersedes sql.NullString
		err := tx.tx.QueryRowContext(ctx, `
			SELECT COALESCE(supersedes,'') FROM project_decisions WHERE decision_id=$1 AND status='candidate'
		`, decisionID).Scan(&supersedes)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: candidate decision %s", ErrNotFound, decisionID)
		}
		if err != nil {
			return fmt.Errorf("load decision: %w", err)
		}
		if target := supersedes.String; target != "" {
			result, err := tx.tx.ExecContext(ctx, `
				UPDATE project_decisions SET status='superseded', updated_at=NOW()
				WHERE decision_id=$1 AND status='active'
			`, target)
			if err != nil {
				return fmt.Errorf("supersede predecessor: %w", err)
			}
			if rows, _ := result.RowsAffected(); rows == 0 {
				return fmt.Errorf("%w: active decision %s to supersede", ErrConflict, target)
			}
		}
		if _, err := tx.tx.ExecContext(ctx, `
			UPDATE project_decisions SET status='active', promoted_by_type=$2, promoted_by_id=NULLIF($3,''),
			 promoted_at=NOW(), updated_at=NOW()
			WHERE decision_id=$1 AND status='candidate'
		`, decisionID, actor.Type, actor.ID); err != nil {
			return fmt.Errorf("promote decision: %w", err)
		}
		return nil
	})
}

// ListDecisions returns the decisions of one project, optionally filtered by
// status (pass "" for all).
func (s *Store) ListDecisions(ctx context.Context, projectID, status string) ([]projectmemory.Decision, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, fmt.Errorf("%w: project id required", ErrInvalidRecord)
	}
	query := `
		SELECT decision_id, project_id, statement, rationale, decided_at, COALESCE(supersedes,''),
		 COALESCE(source_run_id,''), source_refs, status, raised_by_type, COALESCE(raised_by_id,''), created_at
		FROM project_decisions WHERE project_id=$1
	`
	args := []any{projectID}
	if status != "" {
		args = append(args, status)
		query += fmt.Sprintf(` AND status=$%d`, len(args))
	}
	query += ` ORDER BY created_at, decision_id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list decisions: %w", err)
	}
	defer rows.Close()
	decisions := []projectmemory.Decision{}
	for rows.Next() {
		var decision projectmemory.Decision
		var refs []byte
		var decidedAt sql.NullTime
		if err := rows.Scan(&decision.DecisionID, &decision.ProjectID, &decision.Statement, &decision.Rationale,
			&decidedAt, &decision.Supersedes, &decision.SourceRunID, &refs, &decision.Status,
			&decision.RaisedByType, &decision.RaisedByID, &decision.CreatedAt); err != nil {
			return nil, err
		}
		if decidedAt.Valid {
			decision.DecidedAt = &decidedAt.Time
		}
		if err := json.Unmarshal(refs, &decision.SourceRefs); err != nil {
			return nil, err
		}
		decisions = append(decisions, decision)
	}
	return decisions, rows.Err()
}

// RaiseOpenQuestion records one unanswered question. Any actor may raise —
// surfacing unknowns is machine work; closing them is a human judgment.
func (s *Store) RaiseOpenQuestion(ctx context.Context, question *projectmemory.OpenQuestion) error {
	if err := validateID(question.QuestionID, "qu_", "question_id"); err != nil {
		return err
	}
	if question.RaisedByType == "" {
		return fmt.Errorf("%w: question %s requires raised_by_type", ErrInvalidRecord, question.QuestionID)
	}
	if err := projectmemory.ValidateOpenQuestion(question); err != nil {
		return fmt.Errorf("%w: question %s: %v", ErrInvalidRecord, question.QuestionID, err)
	}
	question.Status = "open"
	if question.CreatedAt.IsZero() {
		question.CreatedAt = time.Now().UTC()
	}
	refs, err := marshalStringSlice(question.SourceRefs)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO project_open_questions
		(question_id, project_id, question, context, source_run_id, source_refs, status, raised_by_type, raised_by_id, created_at)
		VALUES ($1,$2,$3,$4,NULLIF($5,''),$6,'open',$7,NULLIF($8,''),$9)
	`, question.QuestionID, question.ProjectID, question.Question, question.Context,
		question.SourceRunID, refs, question.RaisedByType, question.RaisedByID, question.CreatedAt)
	if err != nil {
		return fmt.Errorf("raise question: %w", err)
	}
	return nil
}

// AnswerOpenQuestion closes a question with an answer, optionally linking the
// fact that settled it. HITL-only.
func (s *Store) AnswerOpenQuestion(ctx context.Context, questionID, answer, answeredFactID string, actor Actor) error {
	if err := actor.Validate(); err != nil {
		return err
	}
	if actor.Type != ActorUser {
		return fmt.Errorf("%w: question answering requires a user actor, got %q", ErrInvalidRecord, actor.Type)
	}
	if strings.TrimSpace(answer) == "" && strings.TrimSpace(answeredFactID) == "" {
		return fmt.Errorf("%w: answer requires answer text or answered_fact_id", ErrInvalidRecord)
	}
	if answeredFactID != "" {
		if err := validateID(answeredFactID, "fact_", "answered_fact_id"); err != nil {
			return err
		}
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE project_open_questions SET status='answered', answer=$2, answered_fact_id=NULLIF($3,''),
		 answered_by_type=$4, answered_by_id=NULLIF($5,''), answered_at=NOW(), updated_at=NOW()
		WHERE question_id=$1 AND status='open'
	`, questionID, answer, answeredFactID, actor.Type, actor.ID)
	if err != nil {
		return fmt.Errorf("answer question: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return fmt.Errorf("%w: open question %s", ErrNotFound, questionID)
	}
	return nil
}

// DropOpenQuestion closes a question without an answer — it no longer merits
// tracking. HITL-only.
func (s *Store) DropOpenQuestion(ctx context.Context, questionID string, actor Actor) error {
	if err := actor.Validate(); err != nil {
		return err
	}
	if actor.Type != ActorUser {
		return fmt.Errorf("%w: question dropping requires a user actor, got %q", ErrInvalidRecord, actor.Type)
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE project_open_questions SET status='dropped', updated_at=NOW()
		WHERE question_id=$1 AND status='open'
	`, questionID)
	if err != nil {
		return fmt.Errorf("drop question: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return fmt.Errorf("%w: open question %s", ErrNotFound, questionID)
	}
	return nil
}

// ListOpenQuestions returns the questions of one project, optionally filtered
// by status (pass "" for all).
func (s *Store) ListOpenQuestions(ctx context.Context, projectID, status string) ([]projectmemory.OpenQuestion, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, fmt.Errorf("%w: project id required", ErrInvalidRecord)
	}
	query := `
		SELECT question_id, project_id, question, context, answer, COALESCE(answered_fact_id,''), status,
		 raised_by_type, COALESCE(raised_by_id,''), COALESCE(answered_by_type,''), COALESCE(answered_by_id,''),
		 answered_at, COALESCE(source_run_id,''), source_refs, created_at
		FROM project_open_questions WHERE project_id=$1
	`
	args := []any{projectID}
	if status != "" {
		args = append(args, status)
		query += fmt.Sprintf(` AND status=$%d`, len(args))
	}
	query += ` ORDER BY created_at, question_id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list questions: %w", err)
	}
	defer rows.Close()
	questions := []projectmemory.OpenQuestion{}
	for rows.Next() {
		var question projectmemory.OpenQuestion
		var refs []byte
		var answeredAt sql.NullTime
		if err := rows.Scan(&question.QuestionID, &question.ProjectID, &question.Question, &question.Context,
			&question.Answer, &question.AnsweredFactID, &question.Status, &question.RaisedByType, &question.RaisedByID,
			&question.AnsweredByType, &question.AnsweredByID, &answeredAt, &question.SourceRunID, &refs,
			&question.CreatedAt); err != nil {
			return nil, err
		}
		if answeredAt.Valid {
			question.AnsweredAt = &answeredAt.Time
		}
		if err := json.Unmarshal(refs, &question.SourceRefs); err != nil {
			return nil, err
		}
		questions = append(questions, question)
	}
	return questions, rows.Err()
}

// StageThread stages a thread proposal in the through-line ledger. Resident
// threads are compiler-resident: never dropped, own budget section.
func (s *Store) StageThread(ctx context.Context, thread *projectmemory.Thread) error {
	if err := validateID(thread.ThreadID, "thr_", "thread_id"); err != nil {
		return err
	}
	if thread.RaisedByType == "" {
		return fmt.Errorf("%w: thread %s requires raised_by_type", ErrInvalidRecord, thread.ThreadID)
	}
	if err := projectmemory.ValidateThread(thread); err != nil {
		return fmt.Errorf("%w: thread %s: %v", ErrInvalidRecord, thread.ThreadID, err)
	}
	thread.Status = "candidate"
	if thread.CreatedAt.IsZero() {
		thread.CreatedAt = time.Now().UTC()
	}
	refs, err := marshalStringSlice(thread.SourceRefs)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO project_threads
		(thread_id, project_id, label, summary, resident, source_run_id, source_refs, status,
		 raised_by_type, raised_by_id, created_at)
		VALUES ($1,$2,$3,$4,$5,NULLIF($6,''),$7,'candidate',$8,NULLIF($9,''),$10)
	`, thread.ThreadID, thread.ProjectID, thread.Label, thread.Summary, thread.Resident,
		thread.SourceRunID, refs, thread.RaisedByType, thread.RaisedByID, thread.CreatedAt)
	if err != nil {
		return fmt.Errorf("stage thread: %w", err)
	}
	return nil
}

// PromoteThread moves a candidate thread into the active ledger. HITL-only.
func (s *Store) PromoteThread(ctx context.Context, threadID string, actor Actor) error {
	if err := actor.Validate(); err != nil {
		return err
	}
	if actor.Type != ActorUser {
		return fmt.Errorf("%w: thread promotion requires a user actor, got %q", ErrInvalidRecord, actor.Type)
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE project_threads SET status='active', promoted_by_type=$2, promoted_by_id=NULLIF($3,''),
		 promoted_at=NOW(), updated_at=NOW()
		WHERE thread_id=$1 AND status='candidate'
	`, threadID, actor.Type, actor.ID)
	if err != nil {
		return fmt.Errorf("promote thread: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return fmt.Errorf("%w: candidate thread %s", ErrNotFound, threadID)
	}
	return nil
}

// ResolveThread closes an active thread as settled, optionally linking the
// fact that resolved it. HITL-only.
func (s *Store) ResolveThread(ctx context.Context, threadID, resolvedFactID string, actor Actor) error {
	if err := actor.Validate(); err != nil {
		return err
	}
	if actor.Type != ActorUser {
		return fmt.Errorf("%w: thread resolution requires a user actor, got %q", ErrInvalidRecord, actor.Type)
	}
	if resolvedFactID != "" {
		if err := validateID(resolvedFactID, "fact_", "resolved_fact_id"); err != nil {
			return err
		}
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE project_threads SET status='resolved', resolved_fact_id=NULLIF($2,''), updated_at=NOW()
		WHERE thread_id=$1 AND status='active'
	`, threadID, resolvedFactID)
	if err != nil {
		return fmt.Errorf("resolve thread: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return fmt.Errorf("%w: active thread %s", ErrNotFound, threadID)
	}
	return nil
}

// ArchiveThread retires a thread in any live state. HITL-only.
func (s *Store) ArchiveThread(ctx context.Context, threadID string, actor Actor) error {
	if err := actor.Validate(); err != nil {
		return err
	}
	if actor.Type != ActorUser {
		return fmt.Errorf("%w: thread archival requires a user actor, got %q", ErrInvalidRecord, actor.Type)
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE project_threads SET status='archived', updated_at=NOW()
		WHERE thread_id=$1 AND status IN ('candidate','active')
	`, threadID)
	if err != nil {
		return fmt.Errorf("archive thread: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return fmt.Errorf("%w: archivable thread %s", ErrNotFound, threadID)
	}
	return nil
}

// ListThreads returns the threads of one project, optionally filtered by
// status (pass "" for all).
func (s *Store) ListThreads(ctx context.Context, projectID, status string) ([]projectmemory.Thread, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, fmt.Errorf("%w: project id required", ErrInvalidRecord)
	}
	query := `
		SELECT thread_id, project_id, label, summary, resident, COALESCE(resolved_fact_id,''),
		 COALESCE(source_run_id,''), source_refs, status, raised_by_type, COALESCE(raised_by_id,''), created_at
		FROM project_threads WHERE project_id=$1
	`
	args := []any{projectID}
	if status != "" {
		args = append(args, status)
		query += fmt.Sprintf(` AND status=$%d`, len(args))
	}
	query += ` ORDER BY created_at, thread_id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list threads: %w", err)
	}
	defer rows.Close()
	threads := []projectmemory.Thread{}
	for rows.Next() {
		var thread projectmemory.Thread
		var refs []byte
		if err := rows.Scan(&thread.ThreadID, &thread.ProjectID, &thread.Label, &thread.Summary, &thread.Resident,
			&thread.ResolvedFactID, &thread.SourceRunID, &refs, &thread.Status, &thread.RaisedByType,
			&thread.RaisedByID, &thread.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(refs, &thread.SourceRefs); err != nil {
			return nil, err
		}
		threads = append(threads, thread)
	}
	return threads, rows.Err()
}

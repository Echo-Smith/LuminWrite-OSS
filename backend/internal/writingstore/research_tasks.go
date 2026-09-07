package writingstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Research sub-task persistence (design.md §6). One row per
// (run, node, task_key, input_hash): the durable ledger that lets a research
// node survive restarts by reusing only hash-verified completed sub-task
// outputs, and that fences late workers through conditional lease updates.

const (
	ResearchTaskPending       = "pending"
	ResearchTaskClaimed       = "claimed"
	ResearchTaskRunning       = "running"
	ResearchTaskSucceeded     = "succeeded"
	ResearchTaskFailed        = "failed"
	ResearchTaskOutcomeUnknwn = "outcome_unknown"
	ResearchTaskCancelled     = "cancelled"
)

type ResearchTask struct {
	ID               int64          `json:"id"`
	OwnerUserID      string         `json:"owner_user_id"`
	RunID            string         `json:"run_id"`
	NodeID           string         `json:"node_id"`
	TaskKey          string         `json:"task_key"`
	Phase            string         `json:"phase"`
	InputHash        string         `json:"input_hash"`
	Status           string         `json:"status"`
	Attempt          int            `json:"attempt"`
	LeaseOwner       string         `json:"lease_owner,omitempty"`
	LeaseExpiresAt   time.Time      `json:"lease_expires_at,omitempty"`
	OutputArtifactID string         `json:"output_artifact_id,omitempty"`
	OutputHash       string         `json:"output_hash,omitempty"`
	ErrorCode        string         `json:"error_code,omitempty"`
	RetryAfter       time.Time      `json:"retry_after,omitempty"`
	Usage            map[string]any `json:"usage,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

type CreateResearchTask struct {
	OwnerUserID string
	RunID       string
	NodeID      string
	TaskKey     string
	Phase       string
	InputHash   string
}

// ClaimResearchTask leases the next runnable task of one node for the named
// worker. Status and attempt are advanced inside the caller's transaction
// with a conditional UPDATE so two workers can never hold the same task.
func (tx *Tx) ClaimResearchTask(ctx context.Context, runID, nodeID, worker string, ttl time.Duration, now time.Time) (ResearchTask, bool, error) {
	if worker == "" || ttl <= 0 {
		return ResearchTask{}, false, fmt.Errorf("%w: research task claim requires a worker and lease ttl", ErrInvalidRecord)
	}
	row := tx.tx.QueryRowContext(ctx, `
		UPDATE writing_research_tasks SET
			status='running', attempt=attempt+1, lease_owner=$1, lease_expires_at=$2,
			updated_at=$3
		WHERE id = (
			SELECT id FROM writing_research_tasks
			WHERE run_id=$4 AND node_id=$5
			  AND (
			    (status IN ('pending','failed') AND (retry_after IS NULL OR retry_after <= $3))
			    -- Expired leases are reclaimable: the previous worker lost its
			    -- fence. outcome_unknown is deliberately not claimable — an
			    -- unknown upstream outcome may re-bill on retry and needs an
			    -- explicit operator/owner decision instead.
			    OR (status = 'running' AND lease_expires_at <= $3)
			  )
			ORDER BY updated_at, id
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		RETURNING id, owner_user_id, run_id, node_id, task_key, phase, input_hash,
		          status, attempt, lease_owner, lease_expires_at, output_artifact_id,
		          output_hash, error_code, retry_after, usage_json, created_at, updated_at
	`, worker, now.Add(ttl), now, runID, nodeID)
	task, err := scanResearchTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ResearchTask{}, false, nil
	}
	if err != nil {
		return ResearchTask{}, false, fmt.Errorf("claim research task: %w", err)
	}
	return task, true, nil
}

// RenewResearchTaskLease extends one task's lease. The conditional WHERE is
// the fencing token: after a lease expiry hands the task to another worker
// (or the outcome is terminal), the old owner cannot renew.
func (tx *Tx) RenewResearchTaskLease(ctx context.Context, taskID int64, worker string, ttl time.Duration, now time.Time) (bool, error) {
	result, err := tx.tx.ExecContext(ctx, `
		UPDATE writing_research_tasks SET lease_expires_at=$1, updated_at=$2
		WHERE id=$3 AND lease_owner=$4 AND status IN ('claimed','running')
	`, now.Add(ttl), now, taskID, worker)
	if err != nil {
		return false, fmt.Errorf("renew research task lease: %w", err)
	}
	rows, _ := result.RowsAffected()
	return rows == 1, nil
}

type ResearchTaskCompletion struct {
	OutputArtifactID string
	OutputHash       string
	Usage            map[string]any
}

// CompleteResearchTask marks a task succeeded. A succeeded row must carry the
// committed artifact id and content hash: cache reuse verifies the hash
// before trusting the output (design.md §6). The live-lease condition is the
// fence: an expired worker's late result can never overwrite the state a
// reclaiming worker already advanced — the late owner must renew or lose.
func (tx *Tx) CompleteResearchTask(ctx context.Context, taskID int64, worker string, completion ResearchTaskCompletion, now time.Time) error {
	if strings.TrimSpace(completion.OutputArtifactID) == "" || !sha256Pattern.MatchString(completion.OutputHash) {
		return fmt.Errorf("%w: succeeded research task requires output_artifact_id and output_hash", ErrInvalidRecord)
	}
	usage, err := marshalNullableJSON(completion.Usage, "research task usage")
	if err != nil {
		return err
	}
	result, err := tx.tx.ExecContext(ctx, `
		UPDATE writing_research_tasks SET status='succeeded', output_artifact_id=$1,
		 output_hash=$2, usage_json=COALESCE($3, usage_json), lease_owner=NULL,
		 lease_expires_at=NULL, error_code=NULL, updated_at=$4
		WHERE id=$5 AND lease_owner=$6 AND status IN ('claimed','running')
		  AND lease_expires_at > $4
	`, completion.OutputArtifactID, completion.OutputHash, usage, now, taskID, worker)
	if err != nil {
		return fmt.Errorf("complete research task: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("%w: research task %d is not owned by %s (fenced)", ErrConflict, taskID, worker)
	}
	return nil
}

type ResearchTaskFailure struct {
	// OutcomeUnknown marks calls whose acceptance by the upstream worker is
	// unknown (deadline, dropped connection): the row is parked inspectably
	// and never auto-recycled, because a retry may re-bill (design.md §7).
	OutcomeUnknown bool
	ErrorCode      string
	RetryAfter     time.Time
	Usage          map[string]any
}

func (tx *Tx) FailResearchTask(ctx context.Context, taskID int64, worker string, failure ResearchTaskFailure, now time.Time) error {
	if strings.TrimSpace(failure.ErrorCode) == "" {
		return fmt.Errorf("%w: failed research task requires an error code", ErrInvalidRecord)
	}
	usage, err := marshalNullableJSON(failure.Usage, "research task usage")
	if err != nil {
		return err
	}
	status := ResearchTaskFailed
	retryAfter := any(nil)
	if failure.OutcomeUnknown {
		status = ResearchTaskOutcomeUnknwn
	} else if !failure.RetryAfter.IsZero() {
		retryAfter = failure.RetryAfter
	}
	result, err := tx.tx.ExecContext(ctx, `
		UPDATE writing_research_tasks SET status=$1, error_code=$2, retry_after=$3,
		 usage_json=COALESCE($4, usage_json), lease_owner=NULL, lease_expires_at=NULL,
		 updated_at=$5
		WHERE id=$6 AND lease_owner=$7 AND status IN ('claimed','running')
	`, status, failure.ErrorCode, retryAfter, usage, now, taskID, worker)
	if err != nil {
		return fmt.Errorf("fail research task: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("%w: research task %d is not owned by %s (fenced)", ErrConflict, taskID, worker)
	}
	return nil
}

// GetResearchTask loads one task row.
func (s *Store) GetResearchTask(ctx context.Context, taskID int64) (ResearchTask, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, owner_user_id, run_id, node_id, task_key, phase, input_hash,
		       status, attempt, lease_owner, lease_expires_at, output_artifact_id,
		       output_hash, error_code, retry_after, usage_json, created_at, updated_at
		FROM writing_research_tasks WHERE id=$1
	`, taskID)
	task, err := scanResearchTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ResearchTask{}, ErrNotFound
	}
	if err != nil {
		return ResearchTask{}, fmt.Errorf("get research task: %w", err)
	}
	return task, nil
}

// ListResearchTasks lists a run's sub-task ledger ordered by node and key.
func (s *Store) ListResearchTasks(ctx context.Context, runID, ownerUserID string) ([]ResearchTask, error) {
	if err := validateID(runID, "run_", "run_id"); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, owner_user_id, run_id, node_id, task_key, phase, input_hash,
		       status, attempt, lease_owner, lease_expires_at, output_artifact_id,
		       output_hash, error_code, retry_after, usage_json, created_at, updated_at
		FROM writing_research_tasks
		WHERE run_id=$1 AND owner_user_id=$2
		ORDER BY node_id, id
	`, runID, ownerUserID)
	if err != nil {
		return nil, fmt.Errorf("list research tasks: %w", err)
	}
	defer rows.Close()
	tasks := []ResearchTask{}
	for rows.Next() {
		task, err := scanResearchTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

// EnsureResearchTask inserts one sub-task row, first-writer-wins on the
// natural key: identical inputs converge on one ledger row so idempotent
// replays reuse rather than duplicate.
func (tx *Tx) EnsureResearchTask(ctx context.Context, task CreateResearchTask, now time.Time) (ResearchTask, error) {
	if err := validateID(task.RunID, "run_", "run_id"); err != nil {
		return ResearchTask{}, err
	}
	if strings.TrimSpace(task.OwnerUserID) == "" {
		return ResearchTask{}, fmt.Errorf("%w: research task owner_user_id is required", ErrInvalidRecord)
	}
	if strings.TrimSpace(task.NodeID) == "" || strings.TrimSpace(task.TaskKey) == "" ||
		strings.TrimSpace(task.Phase) == "" || !sha256Pattern.MatchString(task.InputHash) {
		return ResearchTask{}, fmt.Errorf("%w: incomplete research task identity", ErrInvalidRecord)
	}
	result, err := tx.tx.ExecContext(ctx, `
		INSERT INTO writing_research_tasks (
			owner_user_id, run_id, node_id, task_key, phase, input_hash, status, attempt, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,'pending',0,$7,$7)
		ON CONFLICT (run_id, node_id, task_key, input_hash) DO NOTHING
	`, task.OwnerUserID, task.RunID, task.NodeID, task.TaskKey, task.Phase, task.InputHash, now)
	if err != nil {
		return ResearchTask{}, fmt.Errorf("insert research task: %w", err)
	}
	if inserted, _ := result.RowsAffected(); inserted == 1 {
		return tx.loadResearchTaskByIdentity(ctx, task)
	}
	return tx.loadResearchTaskByIdentity(ctx, task)
}

func (tx *Tx) loadResearchTaskByIdentity(ctx context.Context, task CreateResearchTask) (ResearchTask, error) {
	row := tx.tx.QueryRowContext(ctx, `
		SELECT id, owner_user_id, run_id, node_id, task_key, phase, input_hash,
		       status, attempt, lease_owner, lease_expires_at, output_artifact_id,
		       output_hash, error_code, retry_after, usage_json, created_at, updated_at
		FROM writing_research_tasks
		WHERE run_id=$1 AND node_id=$2 AND task_key=$3 AND input_hash=$4
	`, task.RunID, task.NodeID, task.TaskKey, task.InputHash)
	loaded, err := scanResearchTask(row)
	if err != nil {
		return ResearchTask{}, fmt.Errorf("load research task identity: %w", err)
	}
	return loaded, nil
}

type researchTaskScanner interface{ Scan(dest ...any) error }

func scanResearchTask(row researchTaskScanner) (ResearchTask, error) {
	var task ResearchTask
	var leaseOwner, outputArtifactID, outputHash, errorCode sql.NullString
	var leaseExpiresAt, retryAfter sql.NullTime
	var usage []byte
	if err := row.Scan(&task.ID, &task.OwnerUserID, &task.RunID, &task.NodeID, &task.TaskKey,
		&task.Phase, &task.InputHash, &task.Status, &task.Attempt, &leaseOwner,
		&leaseExpiresAt, &outputArtifactID, &outputHash, &errorCode, &retryAfter,
		&usage, &task.CreatedAt, &task.UpdatedAt); err != nil {
		return ResearchTask{}, err
	}
	task.LeaseOwner, task.OutputArtifactID, task.OutputHash, task.ErrorCode = leaseOwner.String, outputArtifactID.String, outputHash.String, errorCode.String
	task.LeaseExpiresAt, task.RetryAfter = leaseExpiresAt.Time, retryAfter.Time
	if len(usage) > 0 {
		var decoded map[string]any
		if err := json.Unmarshal(usage, &decoded); err != nil {
			return ResearchTask{}, fmt.Errorf("decode research task usage: %w", err)
		}
		task.Usage = decoded
	}
	return task, nil
}

func marshalNullableJSON(value map[string]any, field string) (any, error) {
	if value == nil {
		return nil, nil
	}
	return marshalJSON(value, field)
}

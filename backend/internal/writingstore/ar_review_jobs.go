package writingstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// AR-012 candidate evaluation jobs (specs/research-review/ar012-sidecar.md).
// One row per (owner, frozen input set): the durable record a worker needs to
// execute, reconcile after a lost synchronous response, and audit. Candidate
// artifacts live in the content-addressed blob store and are referenced by
// hash only — the run's artifact ledger and the document delivery path are
// never touched (A18 isolation).
const (
	ArReviewJobPending       = "pending"
	ArReviewJobRunning       = "running"
	ArReviewJobCompleted     = "completed"
	ArReviewJobFailed        = "failed"
	ArReviewJobOutcomeUnknwn = "outcome_unknown"
	ArReviewJobCancelled     = "cancelled"
)

// ArReviewArtifactRef points at one imported sidecar output by content hash.
type ArReviewArtifactRef struct {
	Kind        string `json:"kind"`
	ContentHash string `json:"content_hash"`
	MediaType   string `json:"media_type"`
	Size        int    `json:"size"`
	SidecarPath string `json:"sidecar_path"`
}

// ArReviewJob is one evaluation job's durable state.
type ArReviewJob struct {
	ID                  string                `json:"id"`
	OwnerUserID         string                `json:"owner_user_id"`
	RunID               string                `json:"run_id"`
	Status              string                `json:"status"`
	CancelRequested     bool                  `json:"cancel_requested"`
	SurrogateProjectID  string                `json:"surrogate_project_id"`
	IdempotencyKey      string                `json:"idempotency_key"`
	CentralQuestion     string                `json:"central_question"`
	ContractHash        string                `json:"contract_hash"`
	EvidencePackHash    string                `json:"evidence_pack_hash"`
	ApprovedOutlineHash string                `json:"approved_outline_hash"`
	GeneratorVersion    string                `json:"generator_version"`
	InputHash           string                `json:"input_hash"`
	RemoteRunID         string                `json:"remote_run_id,omitempty"`
	ArtifactRefs        []ArReviewArtifactRef `json:"artifact_refs"`
	Usage               map[string]any        `json:"usage"`
	CorpusWarnings      []string              `json:"corpus_warnings"`
	ErrorCode           string                `json:"error_code,omitempty"`
	ErrorMessage        string                `json:"error_message,omitempty"`
	LeaseOwner          string                `json:"lease_owner,omitempty"`
	CreatedAt           time.Time             `json:"created_at"`
	UpdatedAt           time.Time             `json:"updated_at"`
	CompletedAt         *time.Time            `json:"completed_at,omitempty"`
}

// ErrArReviewJobReplayed reports that an identical (owner, input hash) job
// already exists; the caller returns the stored job instead of re-running.
var ErrArReviewJobReplayed = errors.New("writingstore: ar review job replayed")

// CreateArReviewJob inserts a new pending job. When a job with the same
// (owner, input hash) already exists it returns the existing row wrapped in
// ErrArReviewJobReplayed so callers can serve an honest idempotent replay.
func (s *Store) CreateArReviewJob(ctx context.Context, job ArReviewJob) (ArReviewJob, error) {
	var stored ArReviewJob
	err := s.InTransaction(ctx, func(tx *Tx) error {
		var err error
		stored, err = tx.CreateArReviewJob(ctx, job)
		return err
	})
	if err != nil && !errors.Is(err, ErrArReviewJobReplayed) {
		return ArReviewJob{}, err
	}
	// On replay, stored is the pre-existing job and err is the sentinel the
	// caller needs to distinguish an idempotent replay from a fresh insert.
	return stored, err
}

func (tx *Tx) CreateArReviewJob(ctx context.Context, job ArReviewJob) (ArReviewJob, error) {
	if job.OwnerUserID == "" || job.RunID == "" {
		return ArReviewJob{}, fmt.Errorf("%w: ar review job requires owner and run", ErrInvalidRecord)
	}
	if job.SurrogateProjectID == "" || job.IdempotencyKey == "" || job.InputHash == "" {
		return ArReviewJob{}, fmt.Errorf("%w: ar review job requires derived identity", ErrInvalidRecord)
	}
	if job.ID == "" {
		job.ID = StableID("arjob_", job.OwnerUserID, job.InputHash)
	}
	refs, err := marshalJSON(orDefaultSlice(job.ArtifactRefs), "artifact_refs")
	if err != nil {
		return ArReviewJob{}, err
	}
	usage, err := marshalJSON(orDefaultMap(job.Usage), "usage")
	if err != nil {
		return ArReviewJob{}, err
	}
	warnings, err := marshalJSON(orDefaultStrings(job.CorpusWarnings), "corpus_warnings")
	if err != nil {
		return ArReviewJob{}, err
	}
	row := tx.tx.QueryRowContext(ctx, `
		INSERT INTO writing_ar_review_jobs
			(id, owner_user_id, run_id, status, surrogate_project_id, idempotency_key,
			 central_question, contract_hash, evidence_pack_hash, approved_outline_hash,
			 generator_version, input_hash, artifact_refs, usage, corpus_warnings)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		ON CONFLICT (owner_user_id, input_hash) DO NOTHING
		RETURNING id, status, created_at, updated_at`,
		job.ID, job.OwnerUserID, job.RunID, ArReviewJobPending, job.SurrogateProjectID,
		job.IdempotencyKey, job.CentralQuestion, job.ContractHash, job.EvidencePackHash,
		job.ApprovedOutlineHash, job.GeneratorVersion, job.InputHash,
		refs, usage, warnings)
	err = row.Scan(&job.ID, &job.Status, &job.CreatedAt, &job.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		existing, getErr := tx.getArReviewJobByIdentity(ctx, job.OwnerUserID, job.InputHash)
		if getErr != nil {
			return ArReviewJob{}, getErr
		}
		return existing, ErrArReviewJobReplayed
	}
	if err != nil {
		return ArReviewJob{}, fmt.Errorf("create ar review job: %w", err)
	}
	return job, nil
}

func (tx *Tx) getArReviewJobByIdentity(ctx context.Context, owner, inputHash string) (ArReviewJob, error) {
	row := tx.tx.QueryRowContext(ctx, `SELECT `+arReviewJobColumns()+` FROM writing_ar_review_jobs WHERE owner_user_id=$1 AND input_hash=$2`, owner, inputHash)
	return scanArReviewJob(row)
}

// GetArReviewJob loads one job with owner-first scoping: a job the caller
// does not own is indistinguishable from a missing one.
func (s *Store) GetArReviewJob(ctx context.Context, ownerUserID, jobID string) (ArReviewJob, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+arReviewJobColumns()+` FROM writing_ar_review_jobs WHERE id=$1 AND owner_user_id=$2`, jobID, ownerUserID)
	job, err := scanArReviewJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ArReviewJob{}, ErrNotFound
	}
	return job, err
}

// LatestArReviewJobForRun returns the run's newest job (any status), or
// ErrNotFound when the run has none.
func (s *Store) LatestArReviewJobForRun(ctx context.Context, ownerUserID, runID string) (ArReviewJob, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+arReviewJobColumns()+` FROM writing_ar_review_jobs WHERE owner_user_id=$1 AND run_id=$2 ORDER BY created_at DESC, id DESC LIMIT 1`, ownerUserID, runID)
	job, err := scanArReviewJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ArReviewJob{}, ErrNotFound
	}
	return job, err
}

// ClaimPendingArReviewJob leases the oldest pending job for the named worker
// with a conditional UPDATE, so two workers can never hold the same job.
func (s *Store) ClaimPendingArReviewJob(ctx context.Context, worker string, ttl time.Duration, now time.Time) (ArReviewJob, bool, error) {
	var claimed ArReviewJob
	err := s.InTransaction(ctx, func(tx *Tx) error {
		row := tx.tx.QueryRowContext(ctx, `
			UPDATE writing_ar_review_jobs SET
				status='running', lease_owner=$1, lease_expires_at=$2, updated_at=$3
			WHERE id = (
				SELECT id FROM writing_ar_review_jobs
				WHERE status='pending'
				ORDER BY created_at, id
				LIMIT 1
				FOR UPDATE SKIP LOCKED
			)
			RETURNING `+arReviewJobColumns(),
			worker, now.Add(ttl), now)
		var err error
		claimed, err = scanArReviewJob(row)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	})
	if err != nil {
		return ArReviewJob{}, false, err
	}
	if claimed.ID == "" {
		return ArReviewJob{}, false, nil
	}
	return claimed, true, nil
}

// SetArReviewJobRemoteRun records the sidecar's run id as soon as it is known
// (even mid-flight) so reconciliation can find the record after a timeout.
func (s *Store) SetArReviewJobRemoteRun(ctx context.Context, jobID, remoteRunID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE writing_ar_review_jobs SET remote_run_id=$2, updated_at=$3 WHERE id=$1`,
		jobID, remoteRunID, time.Now().UTC())
	return err
}

// FinishArReviewJob commits a terminal state with the job's final artifacts.
// The cancel flag wins inside the same statement: if the owner requested
// cancellation while the worker was importing, the row lands as cancelled
// and the sidecar refs are discarded. A row that is already terminal refuses
// the update with ErrConflict — terminal states are immutable.
func (s *Store) FinishArReviewJob(ctx context.Context, jobID, status, errorCode, errorMessage string, refs []ArReviewArtifactRef, usage map[string]any, warnings []string, remoteRunID string) error {
	if status != ArReviewJobCompleted && status != ArReviewJobFailed && status != ArReviewJobOutcomeUnknwn && status != ArReviewJobCancelled {
		return fmt.Errorf("%w: terminal ar review job status %q", ErrInvalidRecord, status)
	}
	return s.InTransaction(ctx, func(tx *Tx) error {
		refsJSON, err := marshalJSON(orDefaultSlice(refs), "artifact_refs")
		if err != nil {
			return err
		}
		usageJSON, err := marshalJSON(orDefaultMap(usage), "usage")
		if err != nil {
			return err
		}
		warnJSON, err := marshalJSON(orDefaultStrings(warnings), "corpus_warnings")
		if err != nil {
			return err
		}
		var finalStatus string
		err = tx.tx.QueryRowContext(ctx, `
			UPDATE writing_ar_review_jobs SET
				status = CASE WHEN cancel_requested THEN $10 ELSE $2 END,
				error_code=$3, error_message=$4,
				artifact_refs = CASE WHEN cancel_requested THEN '[]'::jsonb ELSE $5 END,
				usage=$6, corpus_warnings=$7,
				remote_run_id=COALESCE(NULLIF($8,''), remote_run_id),
				completed_at=$9, updated_at=$9
			WHERE id=$1 AND status IN ('pending','running')
			RETURNING status`,
			jobID, status, errorCode, errorMessage, refsJSON, usageJSON, warnJSON,
			remoteRunID, time.Now().UTC(), ArReviewJobCancelled).Scan(&finalStatus)
		if errors.Is(err, sql.ErrNoRows) {
			// Either a concurrent worker finished it first or the row is in
			// another terminal state; both make this finish a no-op conflict.
			return fmt.Errorf("%w: ar review job %s already terminal", ErrConflict, jobID)
		}
		if err != nil {
			return fmt.Errorf("finish ar review job: %w", err)
		}
		_ = finalStatus
		return nil
	})
}

// RequestArReviewJobCancel marks a pending job cancelled, or flags a running
// job so the worker discards its import when the synchronous call returns.
// The sidecar itself cannot be cancelled — the flag is honest about that.
func (s *Store) RequestArReviewJobCancel(ctx context.Context, ownerUserID, jobID string) (ArReviewJob, error) {
	job, err := s.GetArReviewJob(ctx, ownerUserID, jobID)
	if err != nil {
		return ArReviewJob{}, err
	}
	switch job.Status {
	case ArReviewJobPending:
		_, err = s.db.ExecContext(ctx,
			`UPDATE writing_ar_review_jobs SET status=$3, completed_at=$4, updated_at=$4 WHERE id=$1 AND owner_user_id=$2 AND status='pending'`,
			jobID, ownerUserID, ArReviewJobCancelled, time.Now().UTC())
		if err != nil {
			return ArReviewJob{}, err
		}
	case ArReviewJobRunning:
		_, err = s.db.ExecContext(ctx,
			`UPDATE writing_ar_review_jobs SET cancel_requested=TRUE, updated_at=$3 WHERE id=$1 AND owner_user_id=$2`,
			jobID, ownerUserID, time.Now().UTC())
		if err != nil {
			return ArReviewJob{}, err
		}
	default:
		return ArReviewJob{}, fmt.Errorf("%w: ar review job %s is terminal", ErrInvalidRecord, job.Status)
	}
	return s.GetArReviewJob(ctx, ownerUserID, jobID)
}

// StuckArReviewJobs lists running jobs whose lease expired (worker died) and
// outcome_unknown jobs not reconciled since unknownCutoff — both need
// attention from the worker loop, neither is blindly re-runnable.
func (s *Store) StuckArReviewJobs(ctx context.Context, now, unknownCutoff time.Time, limit int) ([]ArReviewJob, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+arReviewJobColumns()+` FROM writing_ar_review_jobs WHERE (status='running' AND lease_expires_at <= $1) OR (status='outcome_unknown' AND updated_at <= $2) ORDER BY updated_at, id LIMIT $3`, now, unknownCutoff, limit)
	if err != nil {
		return nil, fmt.Errorf("scan stuck ar review jobs: %w", err)
	}
	defer rows.Close()
	jobs := []ArReviewJob{}
	for rows.Next() {
		job, err := scanArReviewJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

// TouchArReviewJob defers the next reconciliation scan after the sidecar was
// still running (the row keeps its outcome_unknown status, only the scan
// watermark moves).
func (s *Store) TouchArReviewJob(ctx context.Context, jobID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE writing_ar_review_jobs SET updated_at=$2 WHERE id=$1`,
		jobID, time.Now().UTC())
	return err
}

func arReviewJobColumns() string {
	return `id, owner_user_id, run_id, status, cancel_requested, surrogate_project_id, idempotency_key, central_question, contract_hash, evidence_pack_hash, approved_outline_hash, generator_version, input_hash, remote_run_id, artifact_refs, usage, corpus_warnings, error_code, error_message, lease_owner, created_at, updated_at, completed_at`
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanArReviewJob(row rowScanner) (ArReviewJob, error) {
	var job ArReviewJob
	var refs, usage, warnings []byte
	var cancelRequested bool
	var leaseOwner sql.NullString
	var completedAt sql.NullTime
	var remoteRunID, errorCode, errorMessage string
	err := row.Scan(&job.ID, &job.OwnerUserID, &job.RunID, &job.Status, &cancelRequested,
		&job.SurrogateProjectID, &job.IdempotencyKey, &job.CentralQuestion, &job.ContractHash,
		&job.EvidencePackHash, &job.ApprovedOutlineHash, &job.GeneratorVersion, &job.InputHash,
		&remoteRunID, &refs, &usage, &warnings, &errorCode, &errorMessage,
		&leaseOwner, &job.CreatedAt, &job.UpdatedAt, &completedAt)
	if err != nil {
		return ArReviewJob{}, err
	}
	job.CancelRequested = cancelRequested
	job.RemoteRunID = remoteRunID
	job.ErrorCode = errorCode
	job.ErrorMessage = errorMessage
	if leaseOwner.Valid {
		job.LeaseOwner = leaseOwner.String
	}
	if completedAt.Valid {
		job.CompletedAt = &completedAt.Time
	}
	if len(refs) > 0 {
		_ = json.Unmarshal(refs, &job.ArtifactRefs)
	}
	if len(usage) > 0 {
		_ = json.Unmarshal(usage, &job.Usage)
	}
	if len(warnings) > 0 {
		_ = json.Unmarshal(warnings, &job.CorpusWarnings)
	}
	return job, nil
}

func orDefaultSlice(refs []ArReviewArtifactRef) []ArReviewArtifactRef {
	if refs == nil {
		return []ArReviewArtifactRef{}
	}
	return refs
}

func orDefaultMap(usage map[string]any) map[string]any {
	if usage == nil {
		return map[string]any{}
	}
	return usage
}

func orDefaultStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

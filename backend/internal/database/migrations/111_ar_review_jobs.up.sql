-- 111: AR-012 candidate evaluation jobs (T10). One row per post-run
-- evaluation request: a frozen evidence pack + approved outline dispatched to
-- the AR-012 sidecar for an isolated comparison candidate (specs/research-
-- review/ar012-sidecar.md).
--
-- The job row is the durable record that lets a worker survive restarts:
-- (owner_user_id, input_hash) is unique, so an identical replay returns the
-- original job instead of re-running a 650s sidecar pipeline; the lease
-- columns fence concurrent workers exactly like writing_research_tasks; and
-- outcome_unknown is deliberately not claimable — an unknown sidecar outcome
-- may re-bill on retry and needs reconciliation, never a blind re-dispatch.
--
-- Candidate artifacts are stored content-addressed in the existing
-- writing_artifact_content blobs and referenced by hash from artifact_refs;
-- no rows are written to writing_artifacts or the attempt ledger, so a
-- candidate can never enter the document delivery path (A18).

CREATE TABLE IF NOT EXISTS writing_ar_review_jobs (
    id TEXT PRIMARY KEY,
    owner_user_id TEXT NOT NULL,
    run_id TEXT NOT NULL,
    status TEXT NOT NULL,
    cancel_requested BOOLEAN NOT NULL DEFAULT FALSE,
    surrogate_project_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    central_question TEXT NOT NULL DEFAULT '',
    contract_hash TEXT NOT NULL,
    evidence_pack_hash TEXT NOT NULL,
    approved_outline_hash TEXT NOT NULL,
    generator_version TEXT NOT NULL,
    input_hash TEXT NOT NULL,
    remote_run_id TEXT NOT NULL DEFAULT '',
    artifact_refs JSONB NOT NULL DEFAULT '{}'::jsonb,
    usage JSONB NOT NULL DEFAULT '{}'::jsonb,
    corpus_warnings JSONB NOT NULL DEFAULT '[]'::jsonb,
    error_code TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    lease_owner TEXT NOT NULL DEFAULT '',
    lease_expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    CONSTRAINT chk_ar_review_job_status CHECK (status IN (
        'pending', 'running', 'completed', 'failed',
        'outcome_unknown', 'cancelled'
    ))
);

-- Replay identity: one job per owner + frozen input set, forever.
CREATE UNIQUE INDEX IF NOT EXISTS idx_ar_review_jobs_replay
    ON writing_ar_review_jobs (owner_user_id, input_hash);

-- Worker scan: pending jobs in submission order.
CREATE INDEX IF NOT EXISTS idx_ar_review_jobs_pending
    ON writing_ar_review_jobs (created_at, id) WHERE status = 'pending';

-- Reconciliation scan: outcome-unknown jobs whose sidecar may have finished
-- after the synchronous response was lost.
CREATE INDEX IF NOT EXISTS idx_ar_review_jobs_unknown
    ON writing_ar_review_jobs (updated_at, id) WHERE status = 'outcome_unknown';

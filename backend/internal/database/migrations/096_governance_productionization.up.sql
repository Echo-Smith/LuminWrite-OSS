CREATE TABLE writing_shadow_contents (
    content_key VARCHAR(768) PRIMARY KEY,
    policy_hash VARCHAR(71) NOT NULL,
    run_id VARCHAR(128) NOT NULL REFERENCES writing_runs(run_id) ON DELETE CASCADE,
    media_type VARCHAR(160) NOT NULL,
    content_hash VARCHAR(71) NOT NULL,
    body BYTEA NOT NULL,
    stored_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,

    CONSTRAINT chk_writing_shadow_policy_hash CHECK (policy_hash ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT chk_writing_shadow_content_hash CHECK (content_hash ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT chk_writing_shadow_run_id CHECK (run_id ~ '^run_[A-Za-z0-9_-]+$'),
    CONSTRAINT chk_writing_shadow_body CHECK (octet_length(body) > 0),
    CONSTRAINT chk_writing_shadow_expiry CHECK (expires_at > stored_at)
);

CREATE INDEX idx_writing_shadow_policy_run ON writing_shadow_contents(policy_hash, run_id, stored_at DESC);
CREATE INDEX idx_writing_shadow_expiry ON writing_shadow_contents(expires_at);

CREATE TABLE writing_rollout_approvals (
    approval_id VARCHAR(128) PRIMARY KEY,
    policy_hash VARCHAR(71) NOT NULL,
    policy_version INTEGER NOT NULL,
    activation_key VARCHAR(160) NOT NULL,
    target_mode VARCHAR(24) NOT NULL,
    approved_by VARCHAR(160) NOT NULL,
    reason TEXT NOT NULL,
    evidence_health JSONB NOT NULL,
    evidence_cutoff TIMESTAMPTZ NOT NULL,
    evidence_last_recorded_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_writing_rollout_approval_id CHECK (approval_id ~ '^approval_[A-Za-z0-9_-]+$'),
    CONSTRAINT chk_writing_rollout_approval_hash CHECK (policy_hash ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT chk_writing_rollout_approval_version CHECK (policy_version >= 1),
    CONSTRAINT chk_writing_rollout_approval_mode CHECK (target_mode = 'allowlist'),
    CONSTRAINT chk_writing_rollout_approval_health CHECK (jsonb_typeof(evidence_health) = 'object'),
    CONSTRAINT chk_writing_rollout_approval_expiry CHECK (expires_at > created_at)
);

CREATE INDEX idx_writing_rollout_approval_lookup
    ON writing_rollout_approvals(policy_hash, policy_version, activation_key, created_at DESC);

CREATE FUNCTION writing_reject_rollout_approval_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'writing_rollout_approvals is append-only; % is forbidden', TG_OP
        USING ERRCODE = 'integrity_constraint_violation';
END;
$$;

CREATE TRIGGER trg_writing_rollout_approvals_append_only
BEFORE UPDATE OR DELETE ON writing_rollout_approvals
FOR EACH ROW EXECUTE FUNCTION writing_reject_rollout_approval_mutation();

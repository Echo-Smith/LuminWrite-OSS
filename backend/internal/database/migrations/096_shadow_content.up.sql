CREATE TABLE writing_shadow_content (
    shadow_key VARCHAR(768) PRIMARY KEY,
    policy_hash VARCHAR(71) NOT NULL,
    run_id VARCHAR(128) NOT NULL,
    media_type VARCHAR(255) NOT NULL,
    body BYTEA NOT NULL,
    content_hash VARCHAR(71) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT chk_shadow_policy_hash CHECK (policy_hash ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT chk_shadow_content_hash CHECK (content_hash ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT chk_shadow_run_id CHECK (run_id ~ '^run_[A-Za-z0-9._-]+$'),
    CONSTRAINT chk_shadow_body_nonempty CHECK (octet_length(body) > 0),
    CONSTRAINT chk_shadow_expiry CHECK (expires_at > created_at)
);

CREATE INDEX idx_writing_shadow_content_expiry
    ON writing_shadow_content(expires_at);

CREATE INDEX idx_writing_shadow_content_created
    ON writing_shadow_content(created_at);

CREATE INDEX idx_writing_shadow_content_policy_run
    ON writing_shadow_content(policy_hash, run_id);

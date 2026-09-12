-- 102: ProjectMemory M3 — context envelope persistence. One compiled,
-- deterministic envelope per (run, node) attempt: the envelope hash pins the
-- exact bytes the model will see, and the metadata records what the compiler
-- had to trim or could not supply, so evidence review can answer "what did
-- the model actually see".

CREATE TABLE project_context_envelopes (
    envelope_id      VARCHAR(128) PRIMARY KEY,
    run_id           VARCHAR(128) NOT NULL REFERENCES writing_runs(run_id) ON DELETE CASCADE,
    node_id          VARCHAR(128) NOT NULL,
    attempt          INTEGER NOT NULL,
    project_id       VARCHAR(128) REFERENCES writing_projects(project_id) ON DELETE SET NULL,
    compiler_version INTEGER NOT NULL,
    envelope_hash    VARCHAR(71) NOT NULL,
    payload          JSONB NOT NULL,
    missing          JSONB NOT NULL DEFAULT '[]'::jsonb,
    trimmed          JSONB NOT NULL DEFAULT '[]'::jsonb,
    diagnostics      JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_project_context_envelopes_id CHECK (envelope_id ~ '^env_[A-Za-z0-9_-]+$'),
    CONSTRAINT chk_project_context_envelopes_run CHECK (run_id ~ '^run_[A-Za-z0-9_-]+$'),
    CONSTRAINT chk_project_context_envelopes_hash CHECK (envelope_hash ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT chk_project_context_envelopes_payload CHECK (jsonb_typeof(payload) = 'object'),
    CONSTRAINT chk_project_context_envelopes_missing CHECK (jsonb_typeof(missing) = 'array'),
    CONSTRAINT chk_project_context_envelopes_trimmed CHECK (jsonb_typeof(trimmed) = 'array'),
    CONSTRAINT chk_project_context_envelopes_diagnostics CHECK (jsonb_typeof(diagnostics) = 'array'),
    CONSTRAINT uk_project_context_envelopes_attempt UNIQUE (run_id, node_id, attempt, envelope_hash)
);

CREATE INDEX idx_project_context_envelopes_run
    ON project_context_envelopes(run_id, node_id, attempt);

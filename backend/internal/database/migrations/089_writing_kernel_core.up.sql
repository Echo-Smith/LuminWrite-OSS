-- 089: Governed writing kernel core.
-- These tables are the canonical source of truth for new governed runs.
-- Legacy agent_traces/editorial tables remain compatibility projections only.

CREATE FUNCTION writing_reject_immutable_columns()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    column_name TEXT;
BEGIN
    FOREACH column_name IN ARRAY TG_ARGV LOOP
        IF to_jsonb(OLD)->column_name IS DISTINCT FROM to_jsonb(NEW)->column_name THEN
            RAISE EXCEPTION 'immutable column %.% cannot be changed', TG_TABLE_NAME, column_name
                USING ERRCODE = 'integrity_constraint_violation';
        END IF;
    END LOOP;
    RETURN NEW;
END;
$$;

CREATE TABLE writing_documents (
    document_id            VARCHAR(128) PRIMARY KEY,
    owner_user_id          UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title                  TEXT NOT NULL DEFAULT '',
    status                 VARCHAR(24) NOT NULL DEFAULT 'active',
    current_version        INTEGER NOT NULL DEFAULT 0,
    current_version_id     VARCHAR(128),
    metadata               JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_by_type        VARCHAR(16) NOT NULL,
    created_by_id          VARCHAR(128),
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_writing_documents_id CHECK (document_id ~ '^doc_[A-Za-z0-9_-]+$'),
    CONSTRAINT chk_writing_documents_status CHECK (status IN ('active', 'archived', 'deleted')),
    CONSTRAINT chk_writing_documents_version CHECK (current_version >= 0),
    CONSTRAINT chk_writing_documents_metadata CHECK (jsonb_typeof(metadata) = 'object'),
    CONSTRAINT chk_writing_documents_actor CHECK (created_by_type IN ('user', 'system', 'model', 'worker', 'validator', 'policy', 'capability'))
);

CREATE TABLE writing_contracts (
    contract_id            VARCHAR(128) NOT NULL,
    version                INTEGER NOT NULL,
    document_id            VARCHAR(128) NOT NULL REFERENCES writing_documents(document_id) ON DELETE CASCADE,
    schema_version         VARCHAR(32) NOT NULL DEFAULT 'lcp/1.0',
    contract_hash          VARCHAR(71) NOT NULL,
    content_hash           VARCHAR(71) NOT NULL,
    contract_payload       JSONB NOT NULL,
    confirmation_status    VARCHAR(24) NOT NULL DEFAULT 'draft',
    confirmed_by_type      VARCHAR(16),
    confirmed_by_id        VARCHAR(128),
    confirmed_at           TIMESTAMPTZ,
    provenance             JSONB NOT NULL DEFAULT '{}'::jsonb,
    source_refs            JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_by_type        VARCHAR(16) NOT NULL,
    created_by_id          VARCHAR(128),
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (contract_id, version),
    CONSTRAINT uk_writing_contract_document_version UNIQUE (document_id, version),
    CONSTRAINT uk_writing_contract_document_identity UNIQUE (document_id, contract_id, version),
    CONSTRAINT uk_writing_contract_hash_identity UNIQUE (document_id, contract_id, version, contract_hash),
    CONSTRAINT chk_writing_contract_id CHECK (contract_id ~ '^ctr_[A-Za-z0-9_-]+$'),
    CONSTRAINT chk_writing_contract_version CHECK (version >= 1),
    CONSTRAINT chk_writing_contract_schema CHECK (schema_version = 'lcp/1.0'),
    CONSTRAINT chk_writing_contract_hash CHECK (contract_hash ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT chk_writing_contract_content_hash CHECK (content_hash ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT chk_writing_contract_hash_binding CHECK (contract_hash = content_hash),
    CONSTRAINT chk_writing_contract_payload CHECK (jsonb_typeof(contract_payload) = 'object'),
    CONSTRAINT chk_writing_contract_confirmation CHECK (confirmation_status IN ('draft', 'confirmed', 'superseded')),
    CONSTRAINT chk_writing_contract_confirmed_actor CHECK (
        (confirmation_status = 'confirmed' AND confirmed_by_type IN ('user', 'system') AND confirmed_at IS NOT NULL) OR
        (confirmation_status <> 'confirmed')
    ),
    CONSTRAINT chk_writing_contract_provenance CHECK (jsonb_typeof(provenance) = 'object'),
    CONSTRAINT chk_writing_contract_sources CHECK (jsonb_typeof(source_refs) = 'array'),
    CONSTRAINT chk_writing_contract_actor CHECK (created_by_type IN ('user', 'system', 'model', 'worker', 'validator', 'policy', 'capability'))
);

CREATE TABLE writing_document_versions (
    version_id             VARCHAR(128) PRIMARY KEY,
    document_id            VARCHAR(128) NOT NULL REFERENCES writing_documents(document_id) ON DELETE CASCADE,
    version                INTEGER NOT NULL,
    base_version_id        VARCHAR(128),
    contract_id            VARCHAR(128) NOT NULL,
    contract_version       INTEGER NOT NULL,
    schema_version         VARCHAR(32) NOT NULL DEFAULT 'lcp/1.0',
    content_hash           VARCHAR(71) NOT NULL,
    version_hash           VARCHAR(71) NOT NULL,
    document_ast           JSONB NOT NULL,
    quality_state          VARCHAR(32) NOT NULL DEFAULT 'candidate_draft',
    quality_report_id      VARCHAR(128),
    quality_report_version INTEGER,
    snapshot_manifest_id   VARCHAR(128),
    snapshot_version       INTEGER,
    provenance             JSONB NOT NULL DEFAULT '{}'::jsonb,
    source_refs            JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_by_type        VARCHAR(16) NOT NULL,
    created_by_id          VARCHAR(128),
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    accepted_at            TIMESTAMPTZ,
    verified_at            TIMESTAMPTZ,

    CONSTRAINT uk_writing_document_version UNIQUE (document_id, version),
    CONSTRAINT uk_writing_document_version_identity UNIQUE (document_id, version_id),
    CONSTRAINT uk_writing_document_version_full_identity UNIQUE (document_id, version_id, version),
    CONSTRAINT uk_writing_document_version_hash UNIQUE (document_id, version_hash),
    CONSTRAINT fk_writing_document_version_base FOREIGN KEY (document_id, base_version_id)
        REFERENCES writing_document_versions(document_id, version_id) DEFERRABLE INITIALLY DEFERRED,
    CONSTRAINT fk_writing_document_version_contract FOREIGN KEY (document_id, contract_id, contract_version)
        REFERENCES writing_contracts(document_id, contract_id, version),
    CONSTRAINT chk_writing_document_version_id CHECK (version_id ~ '^ver_[A-Za-z0-9_-]+$'),
    CONSTRAINT chk_writing_document_version_number CHECK (version >= 1),
    CONSTRAINT chk_writing_document_version_schema CHECK (schema_version = 'lcp/1.0'),
    CONSTRAINT chk_writing_document_content_hash CHECK (content_hash ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT chk_writing_document_version_hash CHECK (version_hash ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT chk_writing_document_ast CHECK (jsonb_typeof(document_ast) = 'object'),
    CONSTRAINT chk_writing_document_quality_state CHECK (quality_state IN (
        'candidate_draft', 'accepted_draft', 'verified_deliverable'
    )),
    CONSTRAINT chk_writing_document_quality_timestamps CHECK (
        (quality_state = 'candidate_draft') OR
        (quality_state = 'accepted_draft' AND accepted_at IS NOT NULL) OR
        (quality_state = 'verified_deliverable' AND accepted_at IS NOT NULL AND verified_at IS NOT NULL
            AND quality_report_id IS NOT NULL AND quality_report_version IS NOT NULL
            AND snapshot_manifest_id IS NOT NULL AND snapshot_version IS NOT NULL)
    ),
    CONSTRAINT chk_writing_document_provenance CHECK (jsonb_typeof(provenance) = 'object'),
    CONSTRAINT chk_writing_document_sources CHECK (jsonb_typeof(source_refs) = 'array'),
    CONSTRAINT chk_writing_document_actor CHECK (created_by_type IN ('user', 'system', 'model', 'worker', 'validator', 'policy', 'capability'))
);

ALTER TABLE writing_documents
    ADD CONSTRAINT fk_writing_documents_current_version
    FOREIGN KEY (document_id, current_version_id, current_version)
    REFERENCES writing_document_versions(document_id, version_id, version)
    ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE writing_runs (
    run_id                 VARCHAR(128) PRIMARY KEY,
    document_id            VARCHAR(128) NOT NULL REFERENCES writing_documents(document_id) ON DELETE CASCADE,
    contract_id            VARCHAR(128) NOT NULL,
    contract_version       INTEGER NOT NULL,
    contract_hash          VARCHAR(71) NOT NULL,
    base_version_id        VARCHAR(128),
    schema_version         VARCHAR(32) NOT NULL DEFAULT 'lcp/1.0',
    status                 VARCHAR(32) NOT NULL DEFAULT 'draft',
    approval_mode          VARCHAR(24) NOT NULL DEFAULT 'conditional',
    requested_assurance    VARCHAR(16) NOT NULL,
    achieved_assurance     VARCHAR(16),
    active_plan_id         VARCHAR(128),
    active_plan_version    INTEGER,
    last_event_sequence    BIGINT NOT NULL DEFAULT 0,
    last_snapshot_id       VARCHAR(128),
    last_snapshot_version  INTEGER,
    budget                 JSONB NOT NULL DEFAULT '{}'::jsonb,
    permissions            JSONB NOT NULL DEFAULT '[]'::jsonb,
    failure                JSONB,
    created_by_type        VARCHAR(16) NOT NULL,
    created_by_id          VARCHAR(128),
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at             TIMESTAMPTZ,
    completed_at           TIMESTAMPTZ,
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT uk_writing_run_document UNIQUE (run_id, document_id),
    CONSTRAINT uk_writing_run_contract UNIQUE (run_id, contract_id, contract_version),
    CONSTRAINT uk_writing_run_contract_hash UNIQUE (run_id, contract_id, contract_version, contract_hash),
    CONSTRAINT fk_writing_run_contract FOREIGN KEY (document_id, contract_id, contract_version, contract_hash)
        REFERENCES writing_contracts(document_id, contract_id, version, contract_hash),
    CONSTRAINT fk_writing_run_base_version FOREIGN KEY (document_id, base_version_id)
        REFERENCES writing_document_versions(document_id, version_id) DEFERRABLE INITIALLY DEFERRED,
    CONSTRAINT chk_writing_run_id CHECK (run_id ~ '^run_[A-Za-z0-9_-]+$'),
    CONSTRAINT chk_writing_run_contract_hash CHECK (contract_hash ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT chk_writing_run_schema CHECK (schema_version = 'lcp/1.0'),
    CONSTRAINT chk_writing_run_status CHECK (status IN (
        'draft', 'contract_confirmed', 'planning', 'planned', 'awaiting_approval',
        'running', 'pausing', 'paused', 'replanning', 'failed', 'cancelling',
        'cancelled', 'completed'
    )),
    CONSTRAINT chk_writing_run_approval CHECK (approval_mode IN ('conditional', 'always', 'auto')),
    CONSTRAINT chk_writing_run_requested_assurance CHECK (requested_assurance IN ('flexible', 'standard', 'sourced', 'strict')),
    CONSTRAINT chk_writing_run_achieved_assurance CHECK (achieved_assurance IS NULL OR achieved_assurance IN ('flexible', 'standard', 'sourced', 'strict')),
    CONSTRAINT chk_writing_run_sequence CHECK (last_event_sequence >= 0),
    CONSTRAINT chk_writing_run_snapshot_ref CHECK (
        (last_snapshot_id IS NULL AND last_snapshot_version IS NULL)
        OR (last_snapshot_id IS NOT NULL AND last_snapshot_version >= 1)
    ),
    CONSTRAINT chk_writing_run_budget CHECK (jsonb_typeof(budget) = 'object'),
    CONSTRAINT chk_writing_run_permissions CHECK (jsonb_typeof(permissions) = 'array'),
    CONSTRAINT chk_writing_run_actor CHECK (created_by_type IN ('user', 'system', 'model', 'worker', 'validator', 'policy', 'capability')),
    CONSTRAINT chk_writing_run_active_plan_ref CHECK (
        (active_plan_id IS NULL AND active_plan_version IS NULL)
        OR (active_plan_id IS NOT NULL AND active_plan_version >= 1)
    ),
    CONSTRAINT chk_writing_run_active_plan CHECK (
        status NOT IN ('awaiting_approval', 'running', 'pausing', 'paused', 'replanning', 'completed')
        OR (active_plan_id IS NOT NULL AND active_plan_version IS NOT NULL)
    )
);

CREATE TABLE writing_run_plans (
    plan_id                VARCHAR(128) NOT NULL,
    run_id                 VARCHAR(128) NOT NULL REFERENCES writing_runs(run_id) ON DELETE CASCADE,
    plan_version           INTEGER NOT NULL,
    contract_id            VARCHAR(128) NOT NULL,
    contract_version       INTEGER NOT NULL,
    schema_version         VARCHAR(32) NOT NULL DEFAULT 'lcp/1.0',
    intent_plan_hash       VARCHAR(71) NOT NULL,
    plan_hash              VARCHAR(71) NOT NULL,
    content_hash           VARCHAR(71) NOT NULL,
    trust_level            VARCHAR(2) NOT NULL,
    status                 VARCHAR(24) NOT NULL DEFAULT 'draft',
    intent_plan            JSONB NOT NULL,
    executable_plan        JSONB NOT NULL,
    strategy_decision      JSONB NOT NULL,
    static_validation      JSONB NOT NULL,
    static_validation_valid BOOLEAN NOT NULL DEFAULT FALSE,
    budget                 JSONB NOT NULL,
    permissions            JSONB NOT NULL,
    approval_required      BOOLEAN NOT NULL DEFAULT FALSE,
    approval_status        VARCHAR(24) NOT NULL DEFAULT 'not_required',
    approved_by_type       VARCHAR(16),
    approved_by_id         VARCHAR(128),
    approved_at            TIMESTAMPTZ,
    provenance             JSONB NOT NULL DEFAULT '{}'::jsonb,
    source_refs            JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_by_type        VARCHAR(16) NOT NULL,
    created_by_id          VARCHAR(128),
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (plan_id, plan_version),
    CONSTRAINT uk_writing_run_plan_version UNIQUE (run_id, plan_version),
    CONSTRAINT uk_writing_run_plan_full_identity UNIQUE (run_id, plan_id, plan_version),
    CONSTRAINT uk_writing_run_plan_hash UNIQUE (run_id, plan_hash),
    CONSTRAINT fk_writing_run_plan_contract FOREIGN KEY (run_id, contract_id, contract_version)
        REFERENCES writing_runs(run_id, contract_id, contract_version),
    CONSTRAINT chk_writing_run_plan_id CHECK (plan_id ~ '^plan_[A-Za-z0-9_-]+$'),
    CONSTRAINT chk_writing_run_plan_version CHECK (plan_version >= 1),
    CONSTRAINT chk_writing_run_plan_schema CHECK (schema_version = 'lcp/1.0'),
    CONSTRAINT chk_writing_run_plan_intent_hash CHECK (intent_plan_hash ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT chk_writing_run_plan_hash CHECK (plan_hash ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT chk_writing_run_plan_content_hash CHECK (content_hash ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT chk_writing_run_plan_trust CHECK (trust_level IN ('T1', 'T2', 'T3', 'T4')),
    CONSTRAINT chk_writing_run_plan_status CHECK (status IN ('draft', 'validated', 'approved', 'locked', 'superseded')),
    CONSTRAINT chk_writing_run_plan_t4 CHECK (trust_level <> 'T4' OR status IN ('draft', 'superseded')),
    CONSTRAINT chk_writing_run_plan_dispatchable CHECK (
        status NOT IN ('validated', 'approved', 'locked') OR static_validation_valid
    ),
    CONSTRAINT chk_writing_run_plan_payloads CHECK (
        jsonb_typeof(intent_plan) = 'object' AND jsonb_typeof(executable_plan) = 'object'
        AND jsonb_typeof(strategy_decision) = 'object' AND jsonb_typeof(static_validation) = 'object'
    ),
    CONSTRAINT chk_writing_run_plan_budget CHECK (jsonb_typeof(budget) = 'object'),
    CONSTRAINT chk_writing_run_plan_permissions CHECK (jsonb_typeof(permissions) = 'array'),
    CONSTRAINT chk_writing_run_plan_approval CHECK (
        approval_status IN ('not_required', 'pending', 'approved', 'rejected', 'expired')
        AND ((approval_required AND approval_status <> 'not_required') OR (NOT approval_required AND approval_status = 'not_required'))
        AND ((approval_status = 'approved' AND approved_by_type IN ('user', 'system') AND approved_at IS NOT NULL)
            OR approval_status <> 'approved')
    ),
    CONSTRAINT chk_writing_run_plan_provenance CHECK (jsonb_typeof(provenance) = 'object'),
    CONSTRAINT chk_writing_run_plan_sources CHECK (jsonb_typeof(source_refs) = 'array'),
    CONSTRAINT chk_writing_run_plan_actor CHECK (created_by_type IN ('user', 'system', 'model', 'worker', 'validator', 'policy', 'capability'))
);

ALTER TABLE writing_runs
    ADD CONSTRAINT fk_writing_runs_active_plan
    FOREIGN KEY (run_id, active_plan_id, active_plan_version)
    REFERENCES writing_run_plans(run_id, plan_id, plan_version)
    DEFERRABLE INITIALLY DEFERRED;

CREATE TRIGGER trg_writing_contracts_immutable
BEFORE UPDATE ON writing_contracts
FOR EACH ROW EXECUTE FUNCTION writing_reject_immutable_columns(
    'contract_id', 'version', 'document_id', 'schema_version', 'contract_hash',
    'content_hash', 'contract_payload', 'provenance', 'source_refs',
    'created_by_type', 'created_by_id', 'created_at'
);

CREATE TRIGGER trg_writing_document_versions_immutable
BEFORE UPDATE ON writing_document_versions
FOR EACH ROW EXECUTE FUNCTION writing_reject_immutable_columns(
    'version_id', 'document_id', 'version', 'base_version_id', 'contract_id',
    'contract_version', 'schema_version', 'content_hash', 'version_hash',
    'document_ast', 'provenance', 'source_refs', 'created_by_type',
    'created_by_id', 'created_at'
);

CREATE TRIGGER trg_writing_run_plans_immutable
BEFORE UPDATE ON writing_run_plans
FOR EACH ROW EXECUTE FUNCTION writing_reject_immutable_columns(
    'plan_id', 'run_id', 'plan_version', 'contract_id', 'contract_version',
    'schema_version', 'intent_plan_hash', 'plan_hash', 'content_hash',
    'trust_level', 'intent_plan', 'executable_plan', 'strategy_decision',
    'static_validation', 'static_validation_valid', 'budget', 'permissions',
    'provenance', 'source_refs', 'created_by_type', 'created_by_id', 'created_at'
);

CREATE INDEX idx_writing_documents_owner_updated ON writing_documents(owner_user_id, updated_at DESC);
CREATE INDEX idx_writing_contracts_document ON writing_contracts(document_id, version DESC);
CREATE INDEX idx_writing_document_versions_document ON writing_document_versions(document_id, version DESC);
CREATE INDEX idx_writing_runs_document_status ON writing_runs(document_id, status, updated_at DESC);
CREATE INDEX idx_writing_runs_status_updated ON writing_runs(status, updated_at);
CREATE INDEX idx_writing_run_plans_run ON writing_run_plans(run_id, plan_version DESC);

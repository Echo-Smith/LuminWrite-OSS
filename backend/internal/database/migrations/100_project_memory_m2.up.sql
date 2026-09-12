-- 100: ProjectMemory M2 — the claims corroboration lane and the entity
-- candidate pool (non-fiction positioning, docs/18 §18.10).
--
-- Claims are mutable workspace (open → supported → promoted/rejected), unlike
-- immutable canon facts: they accumulate evidence from independent sources,
-- and only a user actor promotes one into a fact. Content columns are still
-- immutable — a claim never changes what it says, only what supports it.
--
-- Entities are birth certificates only: immutable identity (kind, canonical
-- name, aliases), mutable status (candidate → promoted/archived). Evolving
-- truth lives in project_facts, never on the entity row.

CREATE TABLE project_claims (
    claim_id           VARCHAR(128) PRIMARY KEY,
    batch_id           VARCHAR(128) NOT NULL,
    project_id         VARCHAR(128) NOT NULL REFERENCES writing_projects(project_id) ON DELETE CASCADE,
    subject            TEXT NOT NULL,
    predicate          VARCHAR(128) NOT NULL,
    object             TEXT NOT NULL,
    as_of              TIMESTAMPTZ NOT NULL,
    raised_by_type     VARCHAR(16) NOT NULL,
    raised_by_id       VARCHAR(128),
    source_run_id      VARCHAR(128),
    source_refs        JSONB NOT NULL DEFAULT '[]'::jsonb,
    vocabulary_version INTEGER NOT NULL,
    extended_predicate BOOLEAN NOT NULL DEFAULT FALSE,
    content_hash       VARCHAR(71) NOT NULL,
    status             VARCHAR(16) NOT NULL DEFAULT 'open',
    promoted_fact_id   VARCHAR(128),
    promoted_at        TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_project_claims_id CHECK (claim_id ~ '^claim_[A-Za-z0-9_-]+$'),
    CONSTRAINT chk_project_claims_batch CHECK (batch_id ~ '^bat_[A-Za-z0-9_-]+$'),
    CONSTRAINT chk_project_claims_status CHECK (status IN ('open', 'supported', 'promoted', 'rejected')),
    CONSTRAINT chk_project_claims_actor CHECK (raised_by_type IN ('user', 'system', 'model', 'worker', 'validator', 'policy', 'capability')),
    CONSTRAINT chk_project_claims_refs CHECK (jsonb_typeof(source_refs) = 'array'),
    CONSTRAINT chk_project_claims_hash CHECK (content_hash ~ '^sha256:[0-9a-f]{64}$')
);

CREATE UNIQUE INDEX uk_project_claims_open_content
    ON project_claims(project_id, content_hash)
    WHERE status IN ('open', 'supported');

CREATE INDEX idx_project_claims_status
    ON project_claims(project_id, status);

CREATE TRIGGER trg_project_claims_immutable
BEFORE UPDATE ON project_claims
FOR EACH ROW EXECUTE FUNCTION writing_reject_immutable_columns(
    'claim_id', 'batch_id', 'project_id', 'subject', 'predicate', 'object',
    'as_of', 'raised_by_type', 'raised_by_id', 'source_run_id', 'source_refs',
    'vocabulary_version', 'extended_predicate', 'content_hash', 'created_at'
);

-- Evidence citations are idempotent per (claim, evidence hash): re-recording
-- the same source run cannot inflate corroboration.
CREATE TABLE project_claim_evidence (
    evidence_id      VARCHAR(128) PRIMARY KEY,
    claim_id         VARCHAR(128) NOT NULL REFERENCES project_claims(claim_id) ON DELETE CASCADE,
    evidence_hash    VARCHAR(71) NOT NULL,
    source_run_id    VARCHAR(128),
    source_refs      JSONB NOT NULL DEFAULT '[]'::jsonb,
    recorded_by_type VARCHAR(16) NOT NULL,
    recorded_by_id   VARCHAR(128),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_project_claim_evidence_id CHECK (evidence_id ~ '^evd_[A-Za-z0-9_-]+$'),
    CONSTRAINT chk_project_claim_evidence_hash CHECK (evidence_hash ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT chk_project_claim_evidence_refs CHECK (jsonb_typeof(source_refs) = 'array'),
    CONSTRAINT chk_project_claim_evidence_actor CHECK (recorded_by_type IN ('user', 'system', 'model', 'worker', 'validator', 'policy', 'capability')),
    CONSTRAINT uk_project_claim_evidence UNIQUE (claim_id, evidence_hash)
);

CREATE INDEX idx_project_claim_evidence_claim
    ON project_claim_evidence(claim_id);

CREATE TABLE project_entities (
    entity_id        VARCHAR(128) PRIMARY KEY,
    project_id       VARCHAR(128) NOT NULL REFERENCES writing_projects(project_id) ON DELETE CASCADE,
    entity_kind      VARCHAR(32) NOT NULL,
    canonical_name   TEXT NOT NULL,
    aliases          JSONB NOT NULL DEFAULT '[]'::jsonb,
    description      TEXT NOT NULL DEFAULT '',
    source_run_id    VARCHAR(128),
    source_refs      JSONB NOT NULL DEFAULT '[]'::jsonb,
    status           VARCHAR(16) NOT NULL DEFAULT 'candidate',
    raised_by_type   VARCHAR(16) NOT NULL,
    raised_by_id     VARCHAR(128),
    promoted_by_type VARCHAR(16),
    promoted_by_id   VARCHAR(128),
    promoted_at      TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_project_entities_id CHECK (entity_id ~ '^ent_[A-Za-z0-9_-]+$'),
    CONSTRAINT chk_project_entities_kind CHECK (entity_kind IN ('person', 'organization', 'place', 'concept', 'term', 'product', 'event', 'work')),
    CONSTRAINT chk_project_entities_status CHECK (status IN ('candidate', 'promoted', 'archived')),
    CONSTRAINT chk_project_entities_actor CHECK (raised_by_type IN ('user', 'system', 'model', 'worker', 'validator', 'policy', 'capability')),
    CONSTRAINT chk_project_entities_promoted_actor CHECK (promoted_by_type IS NULL OR promoted_by_type IN ('user', 'system', 'model', 'worker', 'validator', 'policy', 'capability')),
    CONSTRAINT chk_project_entities_refs CHECK (jsonb_typeof(source_refs) = 'array'),
    CONSTRAINT chk_project_entities_aliases CHECK (jsonb_typeof(aliases) = 'array')
);

-- One live identity per (project, kind, canonical name); archiving an entity
-- frees the name for re-registration.
CREATE UNIQUE INDEX uk_project_entities_active_name
    ON project_entities(project_id, entity_kind, canonical_name)
    WHERE status <> 'archived';

CREATE TRIGGER trg_project_entities_immutable
BEFORE UPDATE ON project_entities
FOR EACH ROW EXECUTE FUNCTION writing_reject_immutable_columns(
    'entity_id', 'project_id', 'entity_kind', 'canonical_name', 'aliases',
    'description', 'source_run_id', 'source_refs', 'raised_by_type',
    'raised_by_id', 'created_at'
);

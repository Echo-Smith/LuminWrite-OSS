-- 099: ProjectMemory M1 — project-scoped canonical facts with validity
-- intervals, a staged candidate lane, and a user-only (HITL) commit gate.
--
-- Design corrections grounded in the existing kernel (docs/18 §18.6):
--   * the kernel keys on writing_documents(doc_*) + owner_user_id; there is no
--     project concept yet, so projects become first-class (prj_*) and
--     documents gain an optional project_id
--   * actor discipline reuses the writing_documents actor vocabulary; canon
--     mutations are restricted to ActorUser at the store layer
--   * fact rows are append-only via the 089 immutable-columns trigger: only
--     valid_to / superseded_by may change after insert (interval invalidation,
--     never truth mutation)
--   * the predicate vocabulary is versioned in Go (projectmemory package) and
--     intentionally NOT hardcoded into a DB CHECK

CREATE TABLE writing_projects (
    project_id       VARCHAR(128) PRIMARY KEY,
    owner_user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title            TEXT NOT NULL DEFAULT '',
    status           VARCHAR(24) NOT NULL DEFAULT 'active',
    metadata         JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_by_type  VARCHAR(16) NOT NULL,
    created_by_id    VARCHAR(128),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_writing_projects_id CHECK (project_id ~ '^prj_[A-Za-z0-9_-]+$'),
    CONSTRAINT chk_writing_projects_status CHECK (status IN ('active', 'archived')),
    CONSTRAINT chk_writing_projects_metadata CHECK (jsonb_typeof(metadata) = 'object'),
    CONSTRAINT chk_writing_projects_actor CHECK (created_by_type IN ('user', 'system', 'model', 'worker', 'validator', 'policy', 'capability'))
);

ALTER TABLE writing_documents
    ADD COLUMN IF NOT EXISTS project_id VARCHAR(128) REFERENCES writing_projects(project_id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_writing_documents_project
    ON writing_documents(project_id)
    WHERE project_id IS NOT NULL;

-- Candidate lane: model/capability actors may only stage rows here; nothing
-- in this table is model-visible canon until a user commits it.
CREATE TABLE project_memory_candidates (
    candidate_id       VARCHAR(128) PRIMARY KEY,
    batch_id           VARCHAR(128) NOT NULL,
    project_id         VARCHAR(128) NOT NULL REFERENCES writing_projects(project_id) ON DELETE CASCADE,
    subject            TEXT NOT NULL,
    predicate          VARCHAR(128) NOT NULL,
    object             TEXT NOT NULL,
    as_of              TIMESTAMPTZ NOT NULL,
    source_run_id      VARCHAR(128),
    source_refs        JSONB NOT NULL DEFAULT '[]'::jsonb,
    vocabulary_version INTEGER NOT NULL,
    extended_predicate BOOLEAN NOT NULL DEFAULT FALSE,
    warnings           JSONB NOT NULL DEFAULT '[]'::jsonb,
    status             VARCHAR(16) NOT NULL DEFAULT 'staged',
    submitted_by_type  VARCHAR(16) NOT NULL,
    submitted_by_id    VARCHAR(128),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_project_memory_candidates_id CHECK (candidate_id ~ '^cand_[A-Za-z0-9_-]+$'),
    CONSTRAINT chk_project_memory_candidates_batch CHECK (batch_id ~ '^bat_[A-Za-z0-9_-]+$'),
    CONSTRAINT chk_project_memory_candidates_status CHECK (status IN ('staged', 'committed', 'rejected')),
    CONSTRAINT chk_project_memory_candidates_actor CHECK (submitted_by_type IN ('user', 'system', 'model', 'worker', 'validator', 'policy', 'capability')),
    CONSTRAINT chk_project_memory_candidates_refs CHECK (jsonb_typeof(source_refs) = 'array'),
    CONSTRAINT chk_project_memory_candidates_warnings CHECK (jsonb_typeof(warnings) = 'array')
);

CREATE INDEX IF NOT EXISTS idx_project_memory_candidates_staged
    ON project_memory_candidates(project_id, status)
    WHERE status = 'staged';

-- Canonical facts are validity-interval rows, never mutable truth: a state
-- change inserts a new row and stamps the previous row's valid_to +
-- superseded_by (the only two mutable columns). One active fact per
-- (project, content) triple is enforced by a partial unique index, which also
-- makes re-committing an identical triple idempotent.
CREATE TABLE project_facts (
    fact_id            VARCHAR(128) PRIMARY KEY,
    project_id         VARCHAR(128) NOT NULL REFERENCES writing_projects(project_id) ON DELETE CASCADE,
    subject            TEXT NOT NULL,
    predicate          VARCHAR(128) NOT NULL,
    object             TEXT NOT NULL,
    valid_from         TIMESTAMPTZ NOT NULL,
    valid_to           TIMESTAMPTZ,
    superseded_by      VARCHAR(128) REFERENCES project_facts(fact_id),
    source_run_id      VARCHAR(128),
    source_refs        JSONB NOT NULL DEFAULT '[]'::jsonb,
    vocabulary_version INTEGER NOT NULL,
    extended_predicate BOOLEAN NOT NULL DEFAULT FALSE,
    content_hash       VARCHAR(71) NOT NULL,
    candidate_id       VARCHAR(128),
    committed_by_type  VARCHAR(16) NOT NULL,
    committed_by_id    VARCHAR(128),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_project_facts_id CHECK (fact_id ~ '^fact_[A-Za-z0-9_-]+$'),
    CONSTRAINT chk_project_facts_actor CHECK (committed_by_type IN ('user', 'system', 'model', 'worker', 'validator', 'policy', 'capability')),
    CONSTRAINT chk_project_facts_hash CHECK (content_hash ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT chk_project_facts_refs CHECK (jsonb_typeof(source_refs) = 'array'),
    CONSTRAINT chk_project_facts_interval CHECK (valid_to IS NULL OR valid_to > valid_from)
);

CREATE UNIQUE INDEX uk_project_facts_active_content
    ON project_facts(project_id, content_hash)
    WHERE valid_to IS NULL;

CREATE INDEX IF NOT EXISTS idx_project_facts_subject
    ON project_facts(project_id, subject, predicate, valid_from DESC);

CREATE TRIGGER trg_project_facts_immutable
BEFORE UPDATE ON project_facts
FOR EACH ROW EXECUTE FUNCTION writing_reject_immutable_columns(
    'fact_id', 'project_id', 'subject', 'predicate', 'object', 'valid_from',
    'source_run_id', 'source_refs', 'vocabulary_version', 'extended_predicate',
    'content_hash', 'candidate_id', 'committed_by_type', 'committed_by_id'
);

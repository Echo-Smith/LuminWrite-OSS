-- 101: ProjectMemory M2.5 — curated project state that the context compiler
-- consumes: terminology (usage directives), decisions (settled calls with
-- rationale), open questions (the unanswered ledger), and threads (the
-- domain-neutral through-line: argument/narrative lines that must survive
-- every session, docs/18 §18.10).
--
-- All four follow the established governance pattern: any actor may stage a
-- candidate, promotion to the live set is user-only (HITL), and content
-- columns are immutable via the 089 trigger — lifecycle columns only.

CREATE TABLE project_terminology (
    terminology_id   VARCHAR(128) PRIMARY KEY,
    project_id       VARCHAR(128) NOT NULL REFERENCES writing_projects(project_id) ON DELETE CASCADE,
    term             TEXT NOT NULL,
    definition       TEXT NOT NULL DEFAULT '',
    aliases          JSONB NOT NULL DEFAULT '[]'::jsonb,
    forbidden        JSONB NOT NULL DEFAULT '[]'::jsonb,
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

    CONSTRAINT chk_project_terminology_id CHECK (terminology_id ~ '^term_[A-Za-z0-9_-]+$'),
    CONSTRAINT chk_project_terminology_status CHECK (status IN ('candidate', 'active', 'archived')),
    CONSTRAINT chk_project_terminology_actor CHECK (raised_by_type IN ('user', 'system', 'model', 'worker', 'validator', 'policy', 'capability')),
    CONSTRAINT chk_project_terminology_promoted_actor CHECK (promoted_by_type IS NULL OR promoted_by_type IN ('user', 'system', 'model', 'worker', 'validator', 'policy', 'capability')),
    CONSTRAINT chk_project_terminology_aliases CHECK (jsonb_typeof(aliases) = 'array'),
    CONSTRAINT chk_project_terminology_forbidden CHECK (jsonb_typeof(forbidden) = 'array'),
    CONSTRAINT chk_project_terminology_refs CHECK (jsonb_typeof(source_refs) = 'array')
);

CREATE UNIQUE INDEX uk_project_terminology_active_term
    ON project_terminology(project_id, term)
    WHERE status <> 'archived';

CREATE TRIGGER trg_project_terminology_immutable
BEFORE UPDATE ON project_terminology
FOR EACH ROW EXECUTE FUNCTION writing_reject_immutable_columns(
    'terminology_id', 'project_id', 'term', 'definition', 'aliases',
    'forbidden', 'source_run_id', 'source_refs', 'raised_by_type',
    'raised_by_id', 'created_at'
);

CREATE TABLE project_decisions (
    decision_id      VARCHAR(128) PRIMARY KEY,
    project_id       VARCHAR(128) NOT NULL REFERENCES writing_projects(project_id) ON DELETE CASCADE,
    statement        TEXT NOT NULL,
    rationale        TEXT NOT NULL DEFAULT '',
    decided_at       TIMESTAMPTZ,
    supersedes       VARCHAR(128) REFERENCES project_decisions(decision_id),
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

    CONSTRAINT chk_project_decisions_id CHECK (decision_id ~ '^dec_[A-Za-z0-9_-]+$'),
    CONSTRAINT chk_project_decisions_status CHECK (status IN ('candidate', 'active', 'superseded', 'archived')),
    CONSTRAINT chk_project_decisions_actor CHECK (raised_by_type IN ('user', 'system', 'model', 'worker', 'validator', 'policy', 'capability')),
    CONSTRAINT chk_project_decisions_promoted_actor CHECK (promoted_by_type IS NULL OR promoted_by_type IN ('user', 'system', 'model', 'worker', 'validator', 'policy', 'capability')),
    CONSTRAINT chk_project_decisions_refs CHECK (jsonb_typeof(source_refs) = 'array')
);

CREATE INDEX idx_project_decisions_active
    ON project_decisions(project_id, status)
    WHERE status = 'active';

CREATE TRIGGER trg_project_decisions_immutable
BEFORE UPDATE ON project_decisions
FOR EACH ROW EXECUTE FUNCTION writing_reject_immutable_columns(
    'decision_id', 'project_id', 'statement', 'rationale', 'decided_at',
    'supersedes', 'source_run_id', 'source_refs', 'raised_by_type',
    'raised_by_id', 'created_at'
);

CREATE TABLE project_open_questions (
    question_id      VARCHAR(128) PRIMARY KEY,
    project_id       VARCHAR(128) NOT NULL REFERENCES writing_projects(project_id) ON DELETE CASCADE,
    question         TEXT NOT NULL,
    context          TEXT NOT NULL DEFAULT '',
    answer           TEXT NOT NULL DEFAULT '',
    answered_fact_id VARCHAR(128) REFERENCES project_facts(fact_id),
    status           VARCHAR(16) NOT NULL DEFAULT 'open',
    raised_by_type   VARCHAR(16) NOT NULL,
    raised_by_id     VARCHAR(128),
    answered_by_type VARCHAR(16),
    answered_by_id   VARCHAR(128),
    answered_at      TIMESTAMPTZ,
    source_run_id    VARCHAR(128),
    source_refs      JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_project_open_questions_id CHECK (question_id ~ '^qu_[A-Za-z0-9_-]+$'),
    CONSTRAINT chk_project_open_questions_status CHECK (status IN ('open', 'answered', 'dropped')),
    CONSTRAINT chk_project_open_questions_actor CHECK (raised_by_type IN ('user', 'system', 'model', 'worker', 'validator', 'policy', 'capability')),
    CONSTRAINT chk_project_open_questions_answered_actor CHECK (answered_by_type IS NULL OR answered_by_type IN ('user', 'system', 'model', 'worker', 'validator', 'policy', 'capability')),
    CONSTRAINT chk_project_open_questions_refs CHECK (jsonb_typeof(source_refs) = 'array')
);

CREATE INDEX idx_project_open_questions_open
    ON project_open_questions(project_id, status)
    WHERE status = 'open';

CREATE TABLE project_threads (
    thread_id        VARCHAR(128) PRIMARY KEY,
    project_id       VARCHAR(128) NOT NULL REFERENCES writing_projects(project_id) ON DELETE CASCADE,
    label            TEXT NOT NULL,
    summary          TEXT NOT NULL DEFAULT '',
    resident         BOOLEAN NOT NULL DEFAULT TRUE,
    resolved_fact_id VARCHAR(128) REFERENCES project_facts(fact_id),
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

    CONSTRAINT chk_project_threads_id CHECK (thread_id ~ '^thr_[A-Za-z0-9_-]+$'),
    CONSTRAINT chk_project_threads_status CHECK (status IN ('candidate', 'active', 'resolved', 'archived')),
    CONSTRAINT chk_project_threads_actor CHECK (raised_by_type IN ('user', 'system', 'model', 'worker', 'validator', 'policy', 'capability')),
    CONSTRAINT chk_project_threads_promoted_actor CHECK (promoted_by_type IS NULL OR promoted_by_type IN ('user', 'system', 'model', 'worker', 'validator', 'policy', 'capability')),
    CONSTRAINT chk_project_threads_refs CHECK (jsonb_typeof(source_refs) = 'array')
);

CREATE INDEX idx_project_threads_resident
    ON project_threads(project_id, status)
    WHERE status = 'active' AND resident;

CREATE TRIGGER trg_project_threads_immutable
BEFORE UPDATE ON project_threads
FOR EACH ROW EXECUTE FUNCTION writing_reject_immutable_columns(
    'thread_id', 'project_id', 'label', 'summary', 'resident',
    'source_run_id', 'source_refs', 'raised_by_type',
    'raised_by_id', 'created_at'
);

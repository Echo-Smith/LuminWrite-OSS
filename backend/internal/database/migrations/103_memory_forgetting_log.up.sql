-- 103: ProjectMemory forgetting log (roadmap §12, V2.9 item 12). Every
-- lifecycle transition the forgetting sweep applies lands here, append-only:
-- what was forgotten, from which status, under which rule and policy hash, by
-- which actor. Canon facts never appear — the policy has no branch that
-- touches them (pinned by tests); the sweep SQL never references
-- project_facts.

CREATE TABLE project_memory_forgetting_log (
    log_id          VARCHAR(128) PRIMARY KEY,
    sweep_id        VARCHAR(128) NOT NULL,
    project_id      VARCHAR(128) NOT NULL REFERENCES writing_projects(project_id) ON DELETE CASCADE,
    object_table    VARCHAR(32) NOT NULL,
    object_id       VARCHAR(128) NOT NULL,
    from_status     VARCHAR(16) NOT NULL,
    to_status       VARCHAR(16) NOT NULL,
    rule            VARCHAR(32) NOT NULL,
    policy_version  INTEGER NOT NULL,
    policy_hash     VARCHAR(71) NOT NULL,
    applied_by_type VARCHAR(16) NOT NULL,
    applied_by_id   VARCHAR(128),
    applied_at      TIMESTAMPTZ NOT NULL,

    CONSTRAINT chk_forgetting_log_id CHECK (log_id ~ '^fgl_[A-Za-z0-9_-]+$'),
    CONSTRAINT chk_forgetting_log_sweep CHECK (sweep_id ~ '^swp_[A-Za-z0-9_-]+$'),
    CONSTRAINT chk_forgetting_log_table CHECK (object_table IN ('project_memory_candidates', 'project_claims', 'project_terminology', 'project_decisions', 'project_threads', 'project_entities')),
    CONSTRAINT chk_forgetting_log_rule CHECK (rule IN ('stale_candidate', 'claim_decay', 'cold_superseded')),
    CONSTRAINT chk_forgetting_log_hash CHECK (policy_hash ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT chk_forgetting_log_actor CHECK (applied_by_type IN ('user', 'system', 'model', 'worker', 'validator', 'policy', 'capability')),
    CONSTRAINT chk_forgetting_log_transition CHECK (from_status <> to_status)
);

CREATE INDEX IF NOT EXISTS idx_forgetting_log_sweep
    ON project_memory_forgetting_log(sweep_id);

CREATE INDEX IF NOT EXISTS idx_forgetting_log_object
    ON project_memory_forgetting_log(project_id, object_table, object_id);

CREATE INDEX IF NOT EXISTS idx_forgetting_log_project_time
    ON project_memory_forgetting_log(project_id, applied_at DESC);

-- Append-only: the log records history, it never rewrites it.
CREATE TRIGGER trg_forgetting_log_immutable
BEFORE UPDATE ON project_memory_forgetting_log
FOR EACH ROW EXECUTE FUNCTION writing_reject_immutable_columns(
    'log_id', 'sweep_id', 'project_id', 'object_table', 'object_id',
    'from_status', 'to_status', 'rule', 'policy_version', 'policy_hash',
    'applied_by_type', 'applied_by_id', 'applied_at'
);

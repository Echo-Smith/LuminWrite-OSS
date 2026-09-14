-- 087 down: 回滚 — 恢复 editorial_tasks 表，数据无法完全恢复
-- 注意：由于 up 迁移删除了 editorial_tasks 表，回滚需要重建表结构。
-- 已迁移到 agent_traces 的数据不会自动搬回。

-- 重建 editorial_tasks 表
CREATE TABLE IF NOT EXISTS editorial_tasks (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    title           VARCHAR(256) NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    owner_id        UUID REFERENCES users(id),
    assignee_type   VARCHAR(32) NOT NULL DEFAULT 'human',
    deadline        TIMESTAMPTZ,
    status          VARCHAR(32) NOT NULL DEFAULT 'draft',
    accept_criteria TEXT NOT NULL DEFAULT '',
    allowed_tools   TEXT[] DEFAULT '{}',
    token_budget    INTEGER NOT NULL DEFAULT 300000,
    token_used      INTEGER NOT NULL DEFAULT 0,
    priority        SMALLINT NOT NULL DEFAULT 3,
    tags            TEXT[] DEFAULT '{}',
    style_slug      VARCHAR(64) NOT NULL DEFAULT 'yinyue',
    conversation_id VARCHAR(64),
    created_by      UUID REFERENCES users(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_editorial_tasks_status ON editorial_tasks (status);
CREATE INDEX IF NOT EXISTS idx_editorial_tasks_owner ON editorial_tasks (owner_id);
CREATE INDEX IF NOT EXISTS idx_editorial_tasks_created ON editorial_tasks (created_at DESC);

-- 恢复 editorial_task_id 列
ALTER TABLE agent_traces ADD COLUMN IF NOT EXISTS editorial_task_id UUID REFERENCES editorial_tasks(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_traces_editorial_task
    ON agent_traces (editorial_task_id)
    WHERE editorial_task_id IS NOT NULL;

-- 恢复子表 FK: trace_id → task_id
ALTER TABLE editorial_artifacts DROP CONSTRAINT IF EXISTS editorial_artifacts_trace_id_fkey;
ALTER TABLE editorial_artifacts RENAME COLUMN trace_id TO task_id;
ALTER TABLE editorial_artifacts
    ADD CONSTRAINT editorial_artifacts_task_id_fkey
    FOREIGN KEY (task_id) REFERENCES editorial_tasks(id) ON DELETE CASCADE;

ALTER TABLE editorial_decisions DROP CONSTRAINT IF EXISTS editorial_decisions_trace_id_fkey;
ALTER TABLE editorial_decisions RENAME COLUMN trace_id TO task_id;
ALTER TABLE editorial_decisions
    ADD CONSTRAINT editorial_decisions_task_id_fkey
    FOREIGN KEY (task_id) REFERENCES editorial_tasks(id) ON DELETE CASCADE;

ALTER TABLE editorial_agent_run_events DROP CONSTRAINT IF EXISTS editorial_agent_run_events_trace_id_fkey;
ALTER TABLE editorial_agent_run_events RENAME COLUMN trace_id TO task_id;
ALTER TABLE editorial_agent_run_events
    ADD CONSTRAINT editorial_agent_run_events_task_id_fkey
    FOREIGN KEY (task_id) REFERENCES editorial_tasks(id) ON DELETE CASCADE;

ALTER TABLE editorial_agent_leases DROP CONSTRAINT IF EXISTS editorial_agent_leases_trace_id_fkey;
ALTER TABLE editorial_agent_leases RENAME COLUMN trace_id TO task_id;
ALTER TABLE editorial_agent_leases
    ADD CONSTRAINT editorial_agent_leases_task_id_fkey
    FOREIGN KEY (task_id) REFERENCES editorial_tasks(id) ON DELETE CASCADE;

-- 删除 agent_traces 新增的列
ALTER TABLE agent_traces DROP COLUMN IF EXISTS deadline;
ALTER TABLE agent_traces DROP COLUMN IF EXISTS accept_criteria;
ALTER TABLE agent_traces DROP COLUMN IF EXISTS allowed_tools;
ALTER TABLE agent_traces DROP COLUMN IF EXISTS token_budget;
ALTER TABLE agent_traces DROP COLUMN IF EXISTS priority;
ALTER TABLE agent_traces DROP COLUMN IF EXISTS tags;
ALTER TABLE agent_traces DROP COLUMN IF EXISTS conversation_id;
ALTER TABLE agent_traces DROP COLUMN IF EXISTS created_by;
ALTER TABLE agent_traces DROP COLUMN IF EXISTS editorial_status;
ALTER TABLE agent_traces DROP COLUMN IF EXISTS assignee_type;
ALTER TABLE agent_traces DROP COLUMN IF EXISTS updated_at;

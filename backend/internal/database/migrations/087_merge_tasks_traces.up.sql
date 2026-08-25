-- 087: 合并 editorial_tasks 到 agent_traces（两表合一）
--
-- 核心思路：agent_traces 是用户写作历史的统一入口，
-- editorial_tasks 是编辑部工作流的任务表。
-- 两者本质是 1:1 关系，合并后消除数据冗余和桥接代码。
--
-- 步骤：
-- 1. agent_traces 吸收 editorial_tasks 独有列
-- 2. 数据迁移：将 editorial_tasks 的数据合并到已有的 agent_traces（通过 editorial_task_id 关联）
--    对于没有对应 trace 的 task，创建新 trace
-- 3. 子表 FK 从 editorial_tasks(id) 改为 agent_traces(trace_id)
--    - editorial_artifacts.task_id → trace_id
--    - editorial_decisions.task_id → trace_id
--    - editorial_agent_run_events.task_id → trace_id
--    - editorial_agent_leases.task_id → trace_id
-- 4. 删除 editorial_task_id 列（不再需要关联）
-- 5. 删除 editorial_tasks 表

-- ─── 1. agent_traces 吸收 editorial_tasks 独有列 ──────────────

ALTER TABLE agent_traces ADD COLUMN IF NOT EXISTS description        TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_traces ADD COLUMN IF NOT EXISTS deadline           TIMESTAMPTZ;
ALTER TABLE agent_traces ADD COLUMN IF NOT EXISTS accept_criteria    TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_traces ADD COLUMN IF NOT EXISTS allowed_tools      TEXT[] DEFAULT '{}';
ALTER TABLE agent_traces ADD COLUMN IF NOT EXISTS token_budget       INTEGER NOT NULL DEFAULT 300000;
ALTER TABLE agent_traces ADD COLUMN IF NOT EXISTS priority           SMALLINT NOT NULL DEFAULT 3;
ALTER TABLE agent_traces ADD COLUMN IF NOT EXISTS tags               TEXT[] DEFAULT '{}';
ALTER TABLE agent_traces ADD COLUMN IF NOT EXISTS conversation_id    VARCHAR(64);
ALTER TABLE agent_traces ADD COLUMN IF NOT EXISTS created_by         UUID;
ALTER TABLE agent_traces ADD COLUMN IF NOT EXISTS editorial_status   VARCHAR(32) NOT NULL DEFAULT 'draft';
ALTER TABLE agent_traces ADD COLUMN IF NOT EXISTS assignee_type      VARCHAR(32) NOT NULL DEFAULT 'human';
ALTER TABLE agent_traces ADD COLUMN IF NOT EXISTS updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW();

-- ─── 2. 数据迁移 ──────────────────────────────────────────

-- 2a. 对于已有 agent_traces.editorial_task_id 关联的行，把 editorial_tasks 的数据搬过来
UPDATE agent_traces a
SET
    deadline = t.deadline,
    accept_criteria = COALESCE(t.accept_criteria, ''),
    allowed_tools = t.allowed_tools,
    token_budget = t.token_budget,
    priority = t.priority,
    tags = t.tags,
    conversation_id = t.conversation_id,
    created_by = t.created_by::uuid,
    editorial_status = t.status,
    assignee_type = t.assignee_type,
    updated_at = t.updated_at,
    -- 补充 editorial 独有信息到通用列
    token_usage = COALESCE(a.token_usage, '{}'::jsonb) || jsonb_build_object(
        'token_used', t.token_used,
        'token_budget', t.token_budget
    )
FROM editorial_tasks t
WHERE a.editorial_task_id = t.id;

-- 2b. 对于没有对应 trace 的 editorial_tasks 行，创建新 trace
-- （用 task.id 作为 trace_id，因为 trace_id 是 VARCHAR 可以存 UUID）
INSERT INTO agent_traces (
    trace_id, user_id, user_input, style_slug, mode, status,
    current_step, step_history, token_usage,
    deadline, accept_criteria, allowed_tools, token_budget, priority,
    tags, conversation_id, created_by, editorial_status, assignee_type,
    created_at, updated_at
)
SELECT
    t.id::text,                                    -- trace_id = task UUID
    t.owner_id,                                    -- user_id
    t.title,                                       -- user_input
    t.style_slug,                                  -- style_slug
    'editorial',                                   -- mode
    CASE t.status
        WHEN 'published' THEN 'completed'
        WHEN 'archived' THEN 'completed'
        WHEN 'draft' THEN 'idle'
        WHEN 'pending_approval' THEN 'idle'
        WHEN 'research' THEN 'running'
        WHEN 'writing' THEN 'running'
        WHEN 'review' THEN 'running'
        WHEN 'pending_publish' THEN 'running'
        ELSE 'completed'
    END,                                           -- status (mapped)
    '',                                            -- current_step
    '[]'::jsonb,                                   -- step_history
    jsonb_build_object('token_used', t.token_used, 'token_budget', t.token_budget),
    t.deadline,                                    -- deadline
    COALESCE(t.accept_criteria, ''),              -- accept_criteria
    t.allowed_tools,                               -- allowed_tools
    t.token_budget,                                -- token_budget
    t.priority,                                    -- priority
    t.tags,                                        -- tags
    t.conversation_id,                             -- conversation_id
    t.created_by,                                  -- created_by
    t.status,                                      -- editorial_status
    t.assignee_type,                               -- assignee_type
    t.created_at,                                  -- created_at
    t.updated_at                                   -- updated_at
FROM editorial_tasks t
WHERE NOT EXISTS (
    SELECT 1 FROM agent_traces a WHERE a.editorial_task_id = t.id
)
ON CONFLICT (trace_id) DO NOTHING;

-- ─── 3. 子表 FK 迁移 ──────────────────────────────────────

-- 3a. editorial_artifacts: task_id → trace_id (VARCHAR)
--     FK 从 editorial_tasks(id) 改为 agent_traces(trace_id)
ALTER TABLE editorial_artifacts DROP CONSTRAINT IF EXISTS editorial_artifacts_task_id_fkey;
ALTER TABLE editorial_artifacts RENAME COLUMN task_id TO trace_id;
ALTER TABLE editorial_artifacts
    ADD CONSTRAINT editorial_artifacts_trace_id_fkey
    FOREIGN KEY (trace_id) REFERENCES agent_traces(trace_id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS idx_artifacts_trace ON editorial_artifacts (trace_id);

-- 3b. editorial_decisions: task_id → trace_id
ALTER TABLE editorial_decisions DROP CONSTRAINT IF EXISTS editorial_decisions_task_id_fkey;
ALTER TABLE editorial_decisions RENAME COLUMN task_id TO trace_id;
ALTER TABLE editorial_decisions
    ADD CONSTRAINT editorial_decisions_trace_id_fkey
    FOREIGN KEY (trace_id) REFERENCES agent_traces(trace_id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS idx_decisions_trace ON editorial_decisions (trace_id);

-- 3c. editorial_agent_run_events: task_id → trace_id
ALTER TABLE editorial_agent_run_events DROP CONSTRAINT IF EXISTS editorial_agent_run_events_task_id_fkey;
ALTER TABLE editorial_agent_run_events RENAME COLUMN task_id TO trace_id;
ALTER TABLE editorial_agent_run_events
    ADD CONSTRAINT editorial_agent_run_events_trace_id_fkey
    FOREIGN KEY (trace_id) REFERENCES agent_traces(trace_id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS idx_agent_run_events_trace_id ON editorial_agent_run_events(trace_id);

-- 3d. editorial_agent_leases: task_id → trace_id
ALTER TABLE editorial_agent_leases DROP CONSTRAINT IF EXISTS editorial_agent_leases_task_id_fkey;
ALTER TABLE editorial_agent_leases RENAME COLUMN task_id TO trace_id;
ALTER TABLE editorial_agent_leases
    ADD CONSTRAINT editorial_agent_leases_trace_id_fkey
    FOREIGN KEY (trace_id) REFERENCES agent_traces(trace_id) ON DELETE CASCADE;
-- 重建 partial unique index（trace_id 替换 task_id）
DROP INDEX IF EXISTS idx_active_lease_unique;
CREATE UNIQUE INDEX idx_active_lease_unique
    ON editorial_agent_leases (trace_id, agent_role)
    WHERE status = 'active';
CREATE INDEX IF NOT EXISTS idx_agent_leases_trace ON editorial_agent_leases (trace_id);

-- ─── 4. 删除 editorial_task_id 列（不再需要） ──────────────

DROP INDEX IF EXISTS idx_traces_editorial_task;
ALTER TABLE agent_traces DROP COLUMN IF EXISTS editorial_task_id;

-- ─── 5. 更新 memory_store 中的 last_task_id / source_task_id 引用 ──
-- 这些表用 task_id 引用 editorial_tasks.id，现在应该引用 agent_traces.trace_id
-- 但由于这些是 VARCHAR 列且没有 FK 约束，数据格式不变（都是 UUID 字符串），
-- 只是语义上现在指向 agent_traces.trace_id。无需 DDL 变更。

-- ─── 6. 删除 editorial_tasks 表 ────────────────────────────
-- 最后一步：确认所有 FK 已迁移后删除原表
DROP TABLE IF EXISTS editorial_tasks;

-- ─── 7. 索引优化 ──────────────────────────────────────────
CREATE INDEX IF NOT EXISTS idx_traces_editorial_status ON agent_traces (editorial_status);
CREATE INDEX IF NOT EXISTS idx_traces_assignee ON agent_traces (assignee_type);
CREATE INDEX IF NOT EXISTS idx_traces_updated_at ON agent_traces (updated_at DESC);

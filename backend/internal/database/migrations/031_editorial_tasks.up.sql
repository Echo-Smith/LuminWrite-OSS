-- 编辑部任务表
CREATE TABLE IF NOT EXISTS editorial_tasks (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    title           VARCHAR(256) NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    owner_id        UUID REFERENCES users(id),
    assignee_type   VARCHAR(32) NOT NULL DEFAULT 'human', -- human | research_agent | writing_agent | review_agent
    deadline        TIMESTAMPTZ,
    status          VARCHAR(32) NOT NULL DEFAULT 'draft', -- draft | pending_approval | research | writing | review | pending_publish | published | archived
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

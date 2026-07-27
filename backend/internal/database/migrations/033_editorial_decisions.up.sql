-- 编辑部决策表
CREATE TABLE IF NOT EXISTS editorial_decisions (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    task_id         UUID NOT NULL REFERENCES editorial_tasks(id) ON DELETE CASCADE,
    type            VARCHAR(32) NOT NULL, -- approve_topic | select_angle | trust_source | accept_review | allow_rewrite | publish | escalate
    decided_by      UUID REFERENCES users(id),
    decided_by_type VARCHAR(32) NOT NULL, -- human | research_agent | writing_agent | review_agent | system
    status          VARCHAR(16) NOT NULL DEFAULT 'pending', -- pending | approved | rejected | escalated
    rationale       TEXT NOT NULL DEFAULT '',
    evidence        TEXT NOT NULL DEFAULT '',
    artifact_id     UUID REFERENCES editorial_artifacts(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    decided_at      TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_decisions_task ON editorial_decisions (task_id);
CREATE INDEX IF NOT EXISTS idx_decisions_status ON editorial_decisions (status);
CREATE INDEX IF NOT EXISTS idx_decisions_type ON editorial_decisions (task_id, type);

-- 编辑部交付物表
CREATE TABLE IF NOT EXISTS editorial_artifacts (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    task_id     UUID NOT NULL REFERENCES editorial_tasks(id) ON DELETE CASCADE,
    type        VARCHAR(32) NOT NULL, -- topic_card | research_brief | source_pack | fact_claims | outline | draft | review_report | revised_draft
    version     INTEGER NOT NULL DEFAULT 1,
    content     JSONB NOT NULL DEFAULT '{}',
    status      VARCHAR(16) NOT NULL DEFAULT 'draft', -- draft | submitted | approved | rejected | superseded
    produced_by VARCHAR(32) NOT NULL, -- research_agent | writing_agent | review_agent | human
    reviewed_by UUID REFERENCES users(id),
    review_note TEXT NOT NULL DEFAULT '',
    parent_id   UUID REFERENCES editorial_artifacts(id),
    token_cost  INTEGER NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uk_artifact_version UNIQUE (task_id, type, version)
);

CREATE INDEX IF NOT EXISTS idx_artifacts_task ON editorial_artifacts (task_id);
CREATE INDEX IF NOT EXISTS idx_artifacts_type ON editorial_artifacts (task_id, type);
CREATE INDEX IF NOT EXISTS idx_artifacts_status ON editorial_artifacts (status);

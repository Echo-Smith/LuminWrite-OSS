-- 038: Agent run events table for Event/Decision/Transition three-layer model
-- Events record objective facts about agent execution (completed/failed)
-- Decisions are only created when human/system choice is needed
-- Transitions reference the Event or Decision that caused them

CREATE TABLE IF NOT EXISTS editorial_agent_run_events (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id     UUID NOT NULL REFERENCES editorial_tasks(id) ON DELETE CASCADE,
    type        TEXT NOT NULL,  -- agent_run.completed | agent_run.failed | artifact.produced
    agent_role  TEXT NOT NULL,  -- research_agent | writing_agent | review_agent
    status      TEXT NOT NULL,  -- completed | failed
    artifact_id UUID REFERENCES editorial_artifacts(id) ON DELETE SET NULL,
    error       TEXT,
    metadata    JSONB DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_agent_run_events_task_id ON editorial_agent_run_events(task_id);
CREATE INDEX idx_agent_run_events_type ON editorial_agent_run_events(type);

-- 049: Link agent_traces to editorial_tasks for writing process traceability
-- Each writing session creates an editorial_task, and all intermediate
-- artifacts (search results, research brief, outline, draft, review, etc.)
-- are stored as editorial_artifacts linked to that task.

ALTER TABLE agent_traces ADD COLUMN IF NOT EXISTS editorial_task_id UUID REFERENCES editorial_tasks(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_traces_editorial_task
    ON agent_traces (editorial_task_id)
    WHERE editorial_task_id IS NOT NULL;

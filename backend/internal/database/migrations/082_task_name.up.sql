-- 082: Add task_name column to agent_traces
-- Stores the writing task name (extracted topic) for display in session list.
-- Priority: article_title > task_name > user_input (truncated) > "历史会话"

ALTER TABLE agent_traces ADD COLUMN IF NOT EXISTS task_name VARCHAR(128);

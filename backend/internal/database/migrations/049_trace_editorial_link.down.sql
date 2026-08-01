-- 049 down: Remove editorial_task_id column from agent_traces
DROP INDEX IF EXISTS idx_traces_editorial_task;
ALTER TABLE agent_traces DROP COLUMN IF EXISTS editorial_task_id;

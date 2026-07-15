-- 016 down: Remove user_deleted column
ALTER TABLE agent_traces DROP COLUMN IF EXISTS user_deleted;
DROP INDEX IF EXISTS idx_traces_user_deleted;

-- 016: User-facing session soft delete
-- Allows users to delete sessions from their sidebar without affecting admin trace history.

ALTER TABLE agent_traces ADD COLUMN IF NOT EXISTS user_deleted BOOLEAN NOT NULL DEFAULT FALSE;
CREATE INDEX IF NOT EXISTS idx_traces_user_deleted ON agent_traces (user_deleted) WHERE user_deleted = TRUE;

-- Rollback of 110_session_custom_title.

ALTER TABLE agent_traces DROP COLUMN IF EXISTS custom_title;

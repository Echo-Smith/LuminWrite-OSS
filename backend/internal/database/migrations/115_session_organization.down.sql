-- Rollback of 109_session_organization.

DROP INDEX IF EXISTS idx_folders_user;
DROP INDEX IF EXISTS idx_traces_archived;
DROP INDEX IF EXISTS idx_traces_folder;

ALTER TABLE agent_traces DROP COLUMN IF EXISTS archived_at;
ALTER TABLE agent_traces DROP COLUMN IF EXISTS folder_id;

DROP TABLE IF EXISTS session_folders;

-- 109: Session organization — folders, archive, and ownership hardening.
--
-- Adds:
--   1. session_folders  — user-created folders for organizing sessions.
--   2. agent_traces.folder_id   — optional folder membership (SET NULL on
--      folder delete so sessions survive folder removal).
--   3. agent_traces.archived_at — archive timestamp; NULL = 未归档.
--   4. Indexes for the sidebar's folder/archive filtered listings.
--
-- Backfill: legacy rows stay un-archived (archived_at IS NULL) and
-- un-foldered, which preserves current behavior exactly.

CREATE TABLE IF NOT EXISTS session_folders (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name        VARCHAR(64) NOT NULL,
    sort_order  INT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, name)
);

ALTER TABLE agent_traces
    ADD COLUMN IF NOT EXISTS folder_id UUID REFERENCES session_folders(id) ON DELETE SET NULL;
ALTER TABLE agent_traces
    ADD COLUMN IF NOT EXISTS archived_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_traces_folder ON agent_traces (folder_id) WHERE folder_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_traces_archived ON agent_traces (user_id, archived_at);
CREATE INDEX IF NOT EXISTS idx_folders_user ON session_folders (user_id, sort_order);

-- 041: Editorial knowledge lifecycle — candidate → active → archived → superseded
-- Extends the status field to support a full lifecycle for editorial knowledge entries.

-- Add superseded_by column for tracking knowledge replacement chain
ALTER TABLE editorial_knowledge
    ADD COLUMN IF NOT EXISTS superseded_by UUID REFERENCES editorial_knowledge(id);

-- Add archived_by column for audit trail
ALTER TABLE editorial_knowledge
    ADD COLUMN IF NOT EXISTS archived_reason TEXT NOT NULL DEFAULT '';

-- Add archived_at column
ALTER TABLE editorial_knowledge
    ADD COLUMN IF NOT EXISTS archived_at TIMESTAMPTZ;

-- Index for superseded_by lookups
CREATE INDEX IF NOT EXISTS idx_editorial_knowledge_superseded_by
    ON editorial_knowledge (superseded_by)
    WHERE superseded_by IS NOT NULL;

-- Update existing knowledge: keep current 'active' entries as 'active'
-- (no data migration needed — existing rows already have status='active')

-- Add index for status + scope queries (replaces partial index if exists)
CREATE INDEX IF NOT EXISTS idx_editorial_knowledge_status_scope
    ON editorial_knowledge (status, scope);

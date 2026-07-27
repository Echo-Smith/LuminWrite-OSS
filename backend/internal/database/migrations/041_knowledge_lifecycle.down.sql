-- 041 down: Remove knowledge lifecycle columns

DROP INDEX IF EXISTS idx_editorial_knowledge_status_scope;
DROP INDEX IF EXISTS idx_editorial_knowledge_superseded_by;

ALTER TABLE editorial_knowledge
    DROP COLUMN IF EXISTS archived_at;

ALTER TABLE editorial_knowledge
    DROP COLUMN IF EXISTS archived_reason;

ALTER TABLE editorial_knowledge
    DROP COLUMN IF EXISTS superseded_by;

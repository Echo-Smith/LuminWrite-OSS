-- Remove content_fingerprint unique index
DROP INDEX IF EXISTS uk_editorial_knowledge_fingerprint;
DROP INDEX IF EXISTS idx_editorial_knowledge_scope;

-- Remove added columns
ALTER TABLE editorial_knowledge
    DROP COLUMN IF EXISTS content_fingerprint,
    DROP COLUMN IF EXISTS scope,
    DROP COLUMN IF EXISTS source;

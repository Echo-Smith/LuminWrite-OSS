-- 039: Content fingerprint for editorial_knowledge deduplication
-- Replaces title + column_tag as the dedup key with a normalized content hash.
-- Also adds scope and source columns for provenance tracking.

ALTER TABLE editorial_knowledge
    ADD COLUMN IF NOT EXISTS content_fingerprint VARCHAR(64),
    ADD COLUMN IF NOT EXISTS scope VARCHAR(32) NOT NULL DEFAULT 'global',
    ADD COLUMN IF NOT EXISTS source VARCHAR(32) NOT NULL DEFAULT 'agent';

-- Backfill content_fingerprint for existing rows using md5 of normalized title + content
UPDATE editorial_knowledge
SET content_fingerprint = md5(lower(trim(both from title)) || '|' || lower(regexp_replace(content, '\s+', ' ', 'g')))
WHERE content_fingerprint IS NULL;

-- Add unique index on content_fingerprint (partial — only for non-null values)
CREATE UNIQUE INDEX IF NOT EXISTS uk_editorial_knowledge_fingerprint
    ON editorial_knowledge (content_fingerprint)
    WHERE content_fingerprint IS NOT NULL;

-- Index for scope-based queries
CREATE INDEX IF NOT EXISTS idx_editorial_knowledge_scope
    ON editorial_knowledge (scope, status);

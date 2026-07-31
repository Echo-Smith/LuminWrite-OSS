-- 048 down: Revert the FTS index fix

DROP INDEX IF EXISTS idx_kc_content_fts;

-- Recreate old BM25 index (broken, but matches original state)
CREATE INDEX idx_kc_bm25 ON knowledge_chunks
USING bm25 (content, title)
WITH (key_field = 'id');
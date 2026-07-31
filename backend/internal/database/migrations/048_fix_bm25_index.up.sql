-- 048: Fix chunk insertion and add FTS GIN index
--
-- Problem: paradedb BM25 index with UUID key_field causes "No key field defined" error.
--   This silently breaks all chunk insertion (the handler ignores AddChunk errors).
--
-- Solution:
--   1. Drop the problematic BM25 index entirely
--   2. Add a GIN index on to_tsvector('simple', content) for FTS
--   3. The search code will use PostgreSQL FTS as fallback

-- Drop the broken paradedb BM25 index
DROP INDEX IF EXISTS idx_kc_bm25;

-- Add GIN index for PostgreSQL FTS (used by ftsSearchInKB fallback)
CREATE INDEX IF NOT EXISTS idx_kc_content_fts
ON knowledge_chunks
USING gin (to_tsvector('simple', content));
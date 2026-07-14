-- 010: Rollback pgvector embedding column on knowledge_base

DROP INDEX IF EXISTS idx_kb_source_active;
DROP INDEX IF EXISTS idx_kb_embedding_hnsw;

ALTER TABLE knowledge_base DROP COLUMN IF EXISTS embedding_model;
ALTER TABLE knowledge_base DROP COLUMN IF EXISTS embedding_dim;
ALTER TABLE knowledge_base DROP COLUMN IF EXISTS embedding;

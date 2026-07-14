-- 010: Enable pgvector embedding column on knowledge_base

-- Add embedding column (vector type from pgvector extension, already created in 001)
ALTER TABLE knowledge_base ADD COLUMN IF NOT EXISTS embedding vector(1024);

-- Create HNSW index for fast approximate nearest neighbor search
CREATE INDEX IF NOT EXISTS idx_kb_embedding_hnsw
    ON knowledge_base USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);

-- Add metadata for embedding model used
ALTER TABLE knowledge_base ADD COLUMN IF NOT EXISTS embedding_model VARCHAR(64) DEFAULT '';
ALTER TABLE knowledge_base ADD COLUMN IF NOT EXISTS embedding_dim INTEGER DEFAULT 1024;

-- Index for source filtering (common query pattern)
CREATE INDEX IF NOT EXISTS idx_kb_source_active
    ON knowledge_base (source)
    WHERE embedding IS NOT NULL;

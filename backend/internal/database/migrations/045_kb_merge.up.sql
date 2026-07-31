-- 045: WeKnora 合并 — 知识库多租户 + 分块 + BM25 + GraphRAG
-- 本 migration 将 WeKnora 的核心数据结构内化到 V2 的 PostgreSQL 中

-- ─── 0. 启用 paradedb 扩展（BM25 全文检索）─────────────
-- paradedb 的扩展名为 pg_search（不是 pg_bm25）
CREATE EXTENSION IF NOT EXISTS pg_search;
CREATE EXTENSION IF NOT EXISTS vector;

-- ─── 1. 扩展 knowledge_base 表 — 增加多租户 + 分块配置 ────
ALTER TABLE knowledge_base
    ADD COLUMN IF NOT EXISTS user_id VARCHAR(64),           -- 多租户隔离（NULL = 全局共享）
    ADD COLUMN IF NOT EXISTS kb_id VARCHAR(64),              -- 知识库分组 ID（替代 WeKnora 的 KB ID）
    ADD COLUMN IF NOT EXISTS source_type VARCHAR(32) DEFAULT 'text',  -- text | file | url
    ADD COLUMN IF NOT EXISTS source_url VARCHAR(1024),       -- 原始 URL（source_type=url 时）
    ADD COLUMN IF NOT EXISTS file_name VARCHAR(256),          -- 原始文件名（source_type=file 时）
    ADD COLUMN IF NOT EXISTS file_size BIGINT,               -- 文件大小（字节）
    ADD COLUMN IF NOT EXISTS chunk_count INTEGER DEFAULT 0,  -- 分块数量
    ADD COLUMN IF NOT EXISTS status VARCHAR(16) DEFAULT 'active',  -- active | processing | failed
    ADD COLUMN IF NOT EXISTS parent_id UUID;                 -- 父条目 ID（分块指向原始文档）

-- 更新已有数据的 source 列（兼容旧数据）
UPDATE knowledge_base SET source_type = source WHERE source_type = 'text' AND source IS NOT NULL AND source != '';

-- 创建多租户索引
CREATE INDEX IF NOT EXISTS idx_kb_user_id ON knowledge_base (user_id);
CREATE INDEX IF NOT EXISTS idx_kb_user_status ON knowledge_base (user_id, status);
CREATE INDEX IF NOT EXISTS idx_kb_kb_id ON knowledge_base (kb_id);
CREATE INDEX IF NOT EXISTS idx_kb_parent ON knowledge_base (parent_id);
CREATE INDEX IF NOT EXISTS idx_kb_user_created ON knowledge_base (user_id, created_at DESC);

-- ─── 2. 知识分块表 ───────────────────────────────────────
-- 每个文档被切分为多个 chunk，每个 chunk 有独立的 embedding
-- 用于精细化检索（BM25 + Dense 都在 chunk 级别执行）
CREATE TABLE IF NOT EXISTS knowledge_chunks (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    doc_id          UUID NOT NULL REFERENCES knowledge_base(id) ON DELETE CASCADE,
    user_id         VARCHAR(64),              -- 冗余字段，加速 per-user 查询
    chunk_index     INTEGER NOT NULL,         -- 分块序号（0-based）
    content         TEXT NOT NULL,             -- 分块内容
    chunk_metadata  JSONB DEFAULT '{}',       -- 分块元数据（如页码、段落位置等）
    embedding       vector(1024),              -- 分块级 embedding (DashScope qwen3.7 = 1024维)
    embedding_model VARCHAR(64),
    embedding_dim   INTEGER,
    bm25_field      text,                      -- paradedb BM25 索引字段
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_kc_doc_id ON knowledge_chunks (doc_id);
CREATE INDEX IF NOT EXISTS idx_kc_user_id ON knowledge_chunks (user_id);
CREATE INDEX IF NOT EXISTS idx_kc_doc_chunk ON knowledge_chunks (doc_id, chunk_index);

-- Note: knowledge_chunks.title 列（分块标题，可选）
ALTER TABLE knowledge_chunks ADD COLUMN IF NOT EXISTS title VARCHAR(512);

-- paradedb BM25 索引（在 chunk 级别做全文检索）
-- 对 content 和 title 建立 BM25 索引，支持中文分词
-- BM25 索引 (paradedb pg_search)
-- paradedb v0.22.x 需要 key_field 参数指定主键
CREATE INDEX IF NOT EXISTS idx_kc_bm25 ON knowledge_chunks
USING bm25 (content, title)
WITH (key_field = 'id');

-- pgvector 索引（chunk 级向量检索）
CREATE INDEX IF NOT EXISTS idx_kc_embedding ON knowledge_chunks
    USING hnsw (embedding vector_cosine_ops) WITH (m = 16, ef_construction = 64);

-- ─── 3. 实体-关系图（GraphRAG）────────────────────────────
-- 复用 V2 已有的 memory_entities / memory_relations 表结构
-- 但为知识库创建独立的图索引表，避免与用户记忆混淆

CREATE TABLE IF NOT EXISTS kb_entities (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    doc_id          UUID REFERENCES knowledge_base(id) ON DELETE CASCADE,
    chunk_id        UUID REFERENCES knowledge_chunks(id) ON DELETE CASCADE,
    user_id         VARCHAR(64),
    entity_name     VARCHAR(256) NOT NULL,
    entity_type     VARCHAR(64),               -- person | organization | location | event | concept
    attributes      JSONB DEFAULT '{}',        -- 实体属性
    embedding       vector(1024),              -- 实体名 embedding（用于实体链接）
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_ke_doc ON kb_entities (doc_id);
CREATE INDEX IF NOT EXISTS idx_ke_chunk ON kb_entities (chunk_id);
CREATE INDEX IF NOT EXISTS idx_ke_user ON kb_entities (user_id);
CREATE INDEX IF NOT EXISTS idx_ke_name ON kb_entities (entity_name);
CREATE INDEX IF NOT EXISTS idx_ke_type ON kb_entities (entity_type);
CREATE INDEX IF NOT EXISTS idx_ke_embedding ON kb_entities
    USING hnsw (embedding vector_cosine_ops) WITH (m = 16, ef_construction = 64);

CREATE TABLE IF NOT EXISTS kb_relations (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    doc_id          UUID REFERENCES knowledge_base(id) ON DELETE CASCADE,
    source_entity_id UUID NOT NULL REFERENCES kb_entities(id) ON DELETE CASCADE,
    target_entity_id UUID NOT NULL REFERENCES kb_entities(id) ON DELETE CASCADE,
    relation_type   VARCHAR(64) NOT NULL,     -- Author | Alias | Member_of | Located_in | etc.
    attributes      JSONB DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_kr_doc ON kb_relations (doc_id);
CREATE INDEX IF NOT EXISTS idx_kr_source ON kb_relations (source_entity_id);
CREATE INDEX IF NOT EXISTS idx_kr_target ON kb_relations (target_entity_id);
CREATE INDEX IF NOT EXISTS idx_kr_type ON kb_relations (relation_type);

-- ─── 4. 知识库配置表（替代 WeKnora 的 config.yaml）─────────
CREATE TABLE IF NOT EXISTS kb_configs (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id         VARCHAR(64),              -- NULL = 全局默认配置
    kb_id           VARCHAR(64),              -- NULL = 该用户所有 KB
    chunk_size      INTEGER DEFAULT 512,
    chunk_overlap   INTEGER DEFAULT 50,
    split_markers   JSONB DEFAULT '["\n\n", "\n", "。"]',
    bm25_weight     DECIMAL(3,2) DEFAULT 0.3,
    dense_weight    DECIMAL(3,2) DEFAULT 0.5,
    graph_weight    DECIMAL(3,2) DEFAULT 0.2,
    rerank_enabled  BOOLEAN DEFAULT false,
    rerank_model    VARCHAR(64),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 全局默认配置
INSERT INTO kb_configs (user_id, kb_id)
    VALUES (NULL, NULL)
    ON CONFLICT DO NOTHING;

-- ─── 5. 迁移 user_materials 表 — 兼容 WeKnora 合并 ──────
-- 增加 doc_id 列，指向本地 knowledge_base 表（替代 weknora_doc_id）
ALTER TABLE user_materials
    ADD COLUMN IF NOT EXISTS doc_id UUID REFERENCES knowledge_base(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS chunk_count INTEGER DEFAULT 0;

-- 将 weknora_doc_id 列保留但标记为废弃（用于数据迁移参考）
COMMENT ON COLUMN user_materials.weknora_doc_id IS 'DEPRECATED: use doc_id instead';

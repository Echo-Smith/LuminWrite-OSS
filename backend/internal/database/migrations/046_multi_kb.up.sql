-- 046: 多知识库支持 — knowledge_bases 元数据表 + knowledge_chunks 加 kb_id

-- ─── 1. 知识库元数据表 ──────────────────────────────────
CREATE TABLE IF NOT EXISTS knowledge_bases (
    id          VARCHAR(64) PRIMARY KEY,             -- KB ID (UUID 或自定义 slug)
    name        VARCHAR(128) NOT NULL,               -- 显示名称
    description TEXT,                                -- 描述
    user_id     VARCHAR(64),                         -- 所属用户 (NULL = 全局共享)
    doc_count   INT DEFAULT 0,                       -- 文档数 (缓存)
    chunk_count INT DEFAULT 0,                       -- 分块数 (缓存)
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    updated_at  TIMESTAMPTZ DEFAULT NOW()
);

-- 默认 KB (兼容已有数据)
INSERT INTO knowledge_bases (id, name, description)
VALUES ('default', '默认知识库', '所有未分类知识的默认存储')
ON CONFLICT (id) DO NOTHING;

-- 将已有文档的 NULL kb_id 设为 'default'
UPDATE knowledge_base SET kb_id = 'default' WHERE kb_id IS NULL OR kb_id = '';

-- ─── 2. knowledge_chunks 加 kb_id（用于按 KB 检索）─────
ALTER TABLE knowledge_chunks ADD COLUMN IF NOT EXISTS kb_id VARCHAR(64);

-- 从父文档回填 kb_id
UPDATE knowledge_chunks kc
SET kb_id = COALESCE((SELECT kb_id FROM knowledge_base kb WHERE kb.id = kc.doc_id), 'default')
WHERE kc.kb_id IS NULL OR kc.kb_id = '';

-- 索引
CREATE INDEX IF NOT EXISTS idx_kc_kb_id ON knowledge_chunks (kb_id);
CREATE INDEX IF NOT EXISTS idx_kb_base_kb_id ON knowledge_base (kb_id);

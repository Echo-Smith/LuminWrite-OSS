-- 044: WeKnora 用户素材库 — 方案B（admin JWT + 每用户独立 KB）
-- 1. user_weknora_mapping: 用户 → WeKnora KB ID 映射
-- 2. user_materials: 用户素材本地元数据（原始内容存 WeKnora）
-- 3. topic_materials: 选题与素材的关联关系（自动 + 手动）

-- ─── 1. 用户-WeKnora KB 映射表 ─────────────────────────
CREATE TABLE IF NOT EXISTS user_weknora_mapping (
    user_id         VARCHAR(64) PRIMARY KEY,
    weknora_kb_id   VARCHAR(64) NOT NULL,
    weknora_tenant_id INTEGER,
    kb_name         VARCHAR(128),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_uwm_kb_id ON user_weknora_mapping (weknora_kb_id);

-- ─── 2. 用户素材本地元数据表 ─────────────────────────────
-- 原始文档存储在 WeKnora 中，此表仅保存元数据和索引信息
CREATE TABLE IF NOT EXISTS user_materials (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id         VARCHAR(64) NOT NULL,
    title           VARCHAR(512) NOT NULL,
    content_preview TEXT,          -- 前 500 字预览
    source_type     VARCHAR(32) NOT NULL DEFAULT 'text',  -- text | file | url
    source_url      VARCHAR(1024), -- 原始 URL（source_type=url 时）
    file_name       VARCHAR(256),  -- 原始文件名（source_type=file 时）
    file_size       BIGINT,        -- 文件大小（字节）
    weknora_doc_id  VARCHAR(64),   -- WeKnora 中的知识条目 ID
    weknora_kb_id   VARCHAR(64),   -- 所属 WeKnora KB ID
    metadata        JSONB DEFAULT '{}',
    status          VARCHAR(16) NOT NULL DEFAULT 'active',  -- active | processing | failed
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_um_user_id ON user_materials (user_id);
CREATE INDEX IF NOT EXISTS idx_um_user_status ON user_materials (user_id, status);
CREATE INDEX IF NOT EXISTS idx_um_weknora_doc ON user_materials (weknora_doc_id);
CREATE INDEX IF NOT EXISTS idx_um_created ON user_materials (user_id, created_at DESC);

-- ─── 3. 选题-素材关联表 ─────────────────────────────────
CREATE TABLE IF NOT EXISTS topic_materials (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    topic_id        UUID NOT NULL,
    material_id     UUID NOT NULL REFERENCES user_materials(id) ON DELETE CASCADE,
    user_id         VARCHAR(64) NOT NULL,
    association_type VARCHAR(16) NOT NULL DEFAULT 'manual',  -- manual | auto
    relevance_score DECIMAL(3,2),  -- 自动关联时的相关性分数 (0.00-1.00)
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_tm_topic ON topic_materials (topic_id);
CREATE INDEX IF NOT EXISTS idx_tm_material ON topic_materials (material_id);
CREATE INDEX IF NOT EXISTS idx_tm_user ON topic_materials (user_id);
CREATE UNIQUE INDEX IF NOT EXISTS uk_tm_topic_material ON topic_materials (topic_id, material_id);

-- 029_memory_entities_relations.up.sql

-- 实体记忆网络 — 长期记忆的图结构存储
-- 将用户偏好、话题、风格等建模为实体，实体之间的关系建模为边

-- ─── 实体表 ────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS memory_entities (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    -- 实体分类
    entity_type     VARCHAR(32) NOT NULL,            -- topic | style | preference | concept | person | tone | structure
    name            VARCHAR(256) NOT NULL,            -- 实体名称（如"短文风格"、"科技话题"）
    description     TEXT NOT NULL DEFAULT '',         -- 实体描述

    -- 语义检索
    embedding       vector(1024),

    -- 置信度与生命周期
    confidence      DECIMAL(3,2) NOT NULL DEFAULT 0.50,
    occurrence_count INTEGER NOT NULL DEFAULT 1,
    source_trace_id VARCHAR(64),

    -- 状态
    status          VARCHAR(16) NOT NULL DEFAULT 'active', -- active | superseded | archived
    superseded_by   UUID REFERENCES memory_entities(id),

    -- 时间
    first_seen      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- 约束：同一用户同一类型+名称只有一条活跃记录
    CONSTRAINT uk_user_entity UNIQUE (user_id, entity_type, name, status)
);

CREATE INDEX IF NOT EXISTS idx_entities_user_active
    ON memory_entities (user_id, status)
    WHERE status = 'active';

CREATE INDEX IF NOT EXISTS idx_entities_user_type
    ON memory_entities (user_id, entity_type, status);

CREATE INDEX IF NOT EXISTS idx_entities_embedding
    ON memory_entities
    USING hnsw (embedding vector_cosine_ops);

-- ─── 关系表 ────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS memory_relations (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    source_entity_id UUID NOT NULL REFERENCES memory_entities(id) ON DELETE CASCADE,
    target_entity_id UUID NOT NULL REFERENCES memory_entities(id) ON DELETE CASCADE,

    -- 关系类型
    relation_type   VARCHAR(32) NOT NULL,            -- prefers | dislikes | related_to | evolved_from | co_occurs_with | contrasts_with
    weight          DECIMAL(3,2) NOT NULL DEFAULT 0.50, -- 关系强度 0.0-1.0
    evidence_count  INTEGER NOT NULL DEFAULT 1,       -- 支持该关系的证据数量
    source_trace_id VARCHAR(64),

    -- 时间
    first_seen      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- 约束：同一用户同一关系类型+source+target 只有一条记录
    CONSTRAINT uk_user_relation UNIQUE (user_id, source_entity_id, target_entity_id, relation_type)
);

CREATE INDEX IF NOT EXISTS idx_relations_user
    ON memory_relations (user_id);

CREATE INDEX IF NOT EXISTS idx_relations_source
    ON memory_relations (source_entity_id);

CREATE INDEX IF NOT EXISTS idx_relations_target
    ON memory_relations (target_entity_id);

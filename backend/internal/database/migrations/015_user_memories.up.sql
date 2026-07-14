-- 015_user_memories.up.sql

-- 用户记忆表 — 三层记忆模型存储
CREATE TABLE IF NOT EXISTS user_memories (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    -- 分层
    tier            VARCHAR(16) NOT NULL,                    -- hard | pattern | feedback
    category        VARCHAR(32) NOT NULL,                    -- word_count | style | structure | tone | title | topic | argument

    -- 内容
    key             VARCHAR(128) NOT NULL,
    value           TEXT NOT NULL,
    embedding       vector(1024),                            -- 通义 text-embedding-v3 语义检索

    -- 置信度与生命周期
    confidence      DECIMAL(3,2) NOT NULL DEFAULT 0.50,
    occurrences     INTEGER NOT NULL DEFAULT 1,
    source_trace_id VARCHAR(64),

    -- 质量信号（泛化录用加权）
    quality_source  VARCHAR(32) NOT NULL DEFAULT '',         -- workbuddy_adopt | high_rating | user_copy | user_share | manual_approve | ''
    quality_weight  DECIMAL(3,2) NOT NULL DEFAULT 0.00,
    quality_score   DECIMAL(3,2) NOT NULL DEFAULT 0.00,      -- 预留：综合质量评分（多信号加权）

    -- 衰减控制（预留）
    memory_weight   DECIMAL(3,2) NOT NULL DEFAULT 1.00,      -- 预留：记忆权重（人工/算法调整）
    decay_at        TIMESTAMPTZ,                              -- 预留：自定义衰减起点（NULL=使用 last_seen）

    -- 来源追踪（预留）
    source_type     VARCHAR(32) NOT NULL DEFAULT 'auto',      -- auto | manual | import | migration

    -- 状态
    status          VARCHAR(16) NOT NULL DEFAULT 'active',   -- candidate | active | superseded | dismissed | archived
    superseded_by   UUID REFERENCES user_memories(id),

    -- 时间
    first_seen      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- 约束：同一用户同一 category+key 只能有一条 active/candidate
    CONSTRAINT uk_user_memory UNIQUE (user_id, category, key, status)
);

-- 检索索引
CREATE INDEX IF NOT EXISTS idx_memories_user_active
    ON user_memories (user_id, status)
    WHERE status IN ('active', 'candidate');

CREATE INDEX IF NOT EXISTS idx_memories_user_tier
    ON user_memories (user_id, tier, status)
    WHERE status IN ('active', 'candidate');

CREATE INDEX IF NOT EXISTS idx_memories_embedding
    ON user_memories
    USING hnsw (embedding vector_cosine_ops);

-- 会话级记忆关闭追踪（用户在某次写作中 dismiss 的记忆）
CREATE TABLE IF NOT EXISTS memory_session_dismissals (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    memory_id       UUID NOT NULL REFERENCES user_memories(id) ON DELETE CASCADE,
    session_id      VARCHAR(64) NOT NULL,
    dismissed_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT uk_session_dismissal UNIQUE (memory_id, session_id)
);

CREATE INDEX IF NOT EXISTS idx_dismissals_session ON memory_session_dismissals (session_id);

-- 028_conversation_messages.up.sql

-- 对话消息表 — 短期记忆存储
-- 存储每个会话中的用户/助手消息，支持语义检索和动态窗口
CREATE TABLE IF NOT EXISTS conversation_messages (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    conversation_id VARCHAR(64) NOT NULL,           -- 前端会话 ID，同一会话内的消息组成一段对话
    user_id         UUID REFERENCES users(id) ON DELETE CASCADE,
    trace_id        VARCHAR(64),                     -- 关联的 agent_traces.trace_id

    role            VARCHAR(16) NOT NULL,            -- user | assistant | system
    content         TEXT NOT NULL,
    content_type    VARCHAR(16) NOT NULL DEFAULT 'text', -- text | article | review | outline
    intent          VARCHAR(32) NOT NULL DEFAULT '',  -- writing | chat | polish | ...

    -- 语义检索
    embedding       vector(1024),

    -- Token 预算管理
    token_count     INTEGER NOT NULL DEFAULT 0,

    -- 元数据
    metadata        JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 检索索引
CREATE INDEX IF NOT EXISTS idx_conv_messages_conversation
    ON conversation_messages (conversation_id, created_at);

CREATE INDEX IF NOT EXISTS idx_conv_messages_user
    ON conversation_messages (user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_conv_messages_embedding
    ON conversation_messages
    USING hnsw (embedding vector_cosine_ops);

-- 清理旧对话消息（超过 30 天的自动清理）
CREATE INDEX IF NOT EXISTS idx_conv_messages_created_at
    ON conversation_messages (created_at);

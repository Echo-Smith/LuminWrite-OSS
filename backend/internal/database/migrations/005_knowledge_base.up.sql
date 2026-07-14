-- 005_knowledge_base.up.sql

CREATE TABLE IF NOT EXISTS knowledge_base (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    source          VARCHAR(32) NOT NULL,
    source_id       VARCHAR(256),
    title           VARCHAR(512) NOT NULL,
    content         TEXT NOT NULL,
    content_hash    VARCHAR(64) NOT NULL,
    -- embedding    vector(1024), -- uncomment when pgvector is installed
    metadata        JSONB DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT uk_kb_content_hash UNIQUE (content_hash)
);

CREATE INDEX IF NOT EXISTS idx_kb_source ON knowledge_base (source);
CREATE INDEX IF NOT EXISTS idx_kb_content_fts ON knowledge_base
    USING gin (to_tsvector('simple', content));

-- Sensitive words table
CREATE TABLE IF NOT EXISTS sensitive_words (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    word            VARCHAR(128) NOT NULL,
    category        VARCHAR(32) NOT NULL DEFAULT 'general',
    severity        VARCHAR(16) NOT NULL DEFAULT 'medium',
    is_active       BOOLEAN NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_sensitive_words_active ON sensitive_words (is_active) WHERE is_active = TRUE;
CREATE UNIQUE INDEX IF NOT EXISTS uk_sensitive_word ON sensitive_words (word, category);

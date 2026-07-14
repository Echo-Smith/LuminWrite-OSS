-- 003_users_topics.up.sql

CREATE TABLE IF NOT EXISTS users (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    uid             VARCHAR(64) UNIQUE NOT NULL,
    name            VARCHAR(128),
    email           VARCHAR(256),
    reputation      DECIMAL(5,2) NOT NULL DEFAULT 1.00,
    adoption_count  INTEGER NOT NULL DEFAULT 0,
    feedback_count  INTEGER NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_users_uid ON users (uid);

CREATE TABLE IF NOT EXISTS topics (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    title           VARCHAR(256) NOT NULL,
    description     TEXT,
    source          VARCHAR(64) NOT NULL DEFAULT 'system',
    source_uid      VARCHAR(64),
    platform        VARCHAR(32),
    hot_rank        INTEGER,
    raw_data        JSONB,
    fetched_at      TIMESTAMPTZ,
    status          VARCHAR(16) NOT NULL DEFAULT 'active',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_topics_source_status ON topics (source, status);
CREATE INDEX IF NOT EXISTS idx_topics_fetched_at ON topics (fetched_at DESC);

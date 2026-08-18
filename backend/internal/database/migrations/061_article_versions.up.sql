-- 061_article_versions.up.sql
-- 文章版本管理：每次用户编辑保存稿件时，将旧版本归档到此表。
-- agent_traces.article 始终保存最新版本，此表保存历史版本用于回溯。

CREATE TABLE IF NOT EXISTS article_versions (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    trace_id        VARCHAR(64) NOT NULL REFERENCES agent_traces(trace_id) ON DELETE CASCADE,
    user_id         UUID REFERENCES users(id),

    article         TEXT NOT NULL,
    article_title   TEXT,
    version_note    VARCHAR(256),

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_article_versions_trace_id ON article_versions (trace_id);
CREATE INDEX IF NOT EXISTS idx_article_versions_user_id ON article_versions (user_id);
CREATE INDEX IF NOT EXISTS idx_article_versions_created_at ON article_versions (created_at DESC);

-- Add user_deleted column if not exists (for user-level article visibility isolation)
-- This was added in 016 but let's ensure it exists
ALTER TABLE agent_traces ADD COLUMN IF NOT EXISTS user_deleted BOOLEAN NOT NULL DEFAULT FALSE;

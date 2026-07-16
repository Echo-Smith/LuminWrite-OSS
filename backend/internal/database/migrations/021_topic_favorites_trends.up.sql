-- 021_topic_favorites_trends.up.sql

-- 选题收藏表
CREATE TABLE IF NOT EXISTS topic_favorites (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id     VARCHAR(64) NOT NULL,
    topic_id    UUID NOT NULL REFERENCES topics(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(user_id, topic_id)
);

CREATE INDEX IF NOT EXISTS idx_topic_favorites_user ON topic_favorites (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_topic_favorites_topic ON topic_favorites (topic_id);

-- 选题热度趋势表 — 定时记录热搜排名变化
CREATE TABLE IF NOT EXISTS topic_trends (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    topic_id    UUID NOT NULL REFERENCES topics(id) ON DELETE CASCADE,
    hot_rank    INTEGER,
    platform    VARCHAR(32),
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_topic_trends_topic_time ON topic_trends (topic_id, recorded_at DESC);
CREATE INDEX IF NOT EXISTS idx_topic_trends_recorded ON topic_trends (recorded_at DESC);

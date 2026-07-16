-- 023_topic_recommendations.up.sql

-- AI 推荐选题缓存表 — 按 user_id 缓存 LLM 生成的推荐结果，带 TTL
-- 避免每次进入选题中心都重新调用 LLM（开销大、延迟高）
-- 策略：首次请求生成并写入缓存，后续请求直接读缓存，用户手动"换一批"时强制重新生成
CREATE TABLE IF NOT EXISTS topic_recommendations (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id         VARCHAR(64) NOT NULL,
    recommendations JSONB NOT NULL,                    -- LLM 生成的推荐结果数组
    generated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(user_id)
);

CREATE INDEX IF NOT EXISTS idx_topic_recs_user ON topic_recommendations (user_id, generated_at DESC);

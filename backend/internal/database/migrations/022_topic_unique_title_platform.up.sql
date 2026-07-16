-- 022_topic_unique_title_platform.up.sql

-- Add unique constraint on (title, platform) for hot topic upsert
-- Allows ON CONFLICT (title, platform) DO UPDATE for batch hot topic sync
-- Note: PostgreSQL treats NULLs as distinct, so rows with NULL platform won't conflict
CREATE UNIQUE INDEX IF NOT EXISTS idx_topics_title_platform
    ON topics (title, platform);

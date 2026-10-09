-- 124 回滚
-- 124 rollback: drop the full-text fetch flag.
ALTER TABLE rss_subscriptions
    DROP COLUMN IF EXISTS fetch_full_text;

-- 124: RSS full-text fetch flag (摘要源原文抓取回退).
--
-- fetch_full_text: 订阅级开关（默认关）。开启后，feed 只给摘要的条目
-- 回退抓取原文（经 URLImporter：fetch → 抽取 → 分块 → embedding），
-- 抓取失败降级用摘要、仍不足最小区间则跳过。全文抓取比摘要重一个量级，
-- 因此按订阅显式开启而非全局默认。
ALTER TABLE rss_subscriptions
    ADD COLUMN IF NOT EXISTS fetch_full_text BOOLEAN NOT NULL DEFAULT FALSE;

COMMENT ON COLUMN rss_subscriptions.fetch_full_text IS 'When true, summary-only items fall back to fetching the original article body';

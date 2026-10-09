-- 123: RSS subscriptions (个人订阅源 → 素材文件夹).
--
-- rss_subscriptions — 用户级订阅源。语义：一个订阅源 = 一个自动更新的
-- 素材文件夹（target_folder_id → material_folders）。抓取由全局 cron 任务
-- rss_fetch 驱动：任务内按 last_fetched_at 选到期源，逐源拉取 → 解析 →
-- 按 guid/link 去重 → 限量写入 knowledge_base + user_materials（source_type
-- 'rss'）。etag/last_modified 做 HTTP 条件 GET；fail_count 连续失败退避。
--
-- 密钥/隐私：feed_url 是用户提交的任意 URL，抓取侧必须做 SSRF 防护
-- （scheme 白名单 + 私网/环回/链路本地地址阻断 + 大小/超时限制）。
CREATE TABLE rss_subscriptions (
    id               UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id          UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    feed_url         TEXT NOT NULL,
    title            VARCHAR(256) NOT NULL DEFAULT '',
    site_url         TEXT NOT NULL DEFAULT '',
    description      TEXT NOT NULL DEFAULT '',
    target_folder_id UUID REFERENCES material_folders(id) ON DELETE SET NULL,
    max_items_per_tick INT NOT NULL DEFAULT 3,
    etag             VARCHAR(256) NOT NULL DEFAULT '',
    last_modified    VARCHAR(128) NOT NULL DEFAULT '',
    last_fetched_at  TIMESTAMPTZ,
    last_item_at     TIMESTAMPTZ,
    fail_count       INT NOT NULL DEFAULT 0,
    last_error       TEXT NOT NULL DEFAULT '',
    is_active        BOOLEAN NOT NULL DEFAULT TRUE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uk_rss_subscriptions_user_feed UNIQUE (user_id, feed_url),
    CONSTRAINT chk_rss_subscriptions_feed_url CHECK (feed_url ~ '^https?://'),
    CONSTRAINT chk_rss_subscriptions_max_items CHECK (max_items_per_tick BETWEEN 1 AND 20)
);

CREATE INDEX IF NOT EXISTS idx_rss_subscriptions_due
    ON rss_subscriptions (last_fetched_at NULLS FIRST)
    WHERE is_active;

COMMENT ON TABLE rss_subscriptions IS 'Per-user RSS/Atom feeds; items land as materials (source_type=rss) in target_folder_id';

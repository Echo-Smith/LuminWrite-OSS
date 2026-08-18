-- 060: Web Push subscriptions
-- Stores browser push notification subscriptions per user.
-- Each subscription corresponds to one browser/device.

CREATE TABLE IF NOT EXISTS push_subscriptions (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id       VARCHAR(64) NOT NULL,          -- links to users.uid
    endpoint      TEXT NOT NULL,                  -- browser push service endpoint URL
    p256dh_key    TEXT NOT NULL,                  -- client public key (base64url)
    auth_secret   TEXT NOT NULL,                  -- auth secret (base64url)
    user_agent    TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT uk_push_endpoint UNIQUE (endpoint)
);

CREATE INDEX IF NOT EXISTS idx_push_subs_user ON push_subscriptions (user_id);

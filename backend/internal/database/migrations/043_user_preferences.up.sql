-- 043: User preferences — per-user key-value settings (cloud-synced)
-- Replaces localStorage for settings that must follow the user account

CREATE TABLE IF NOT EXISTS user_preferences (
    user_id    VARCHAR(64)  NOT NULL,
    key        VARCHAR(64)  NOT NULL,
    value      TEXT         NOT NULL,
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, key)
);

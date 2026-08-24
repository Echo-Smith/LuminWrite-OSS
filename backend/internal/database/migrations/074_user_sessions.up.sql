-- 074: User Sessions — track active login sessions for multi-device management
--
-- Stores every JWT session issued at login time, enabling:
--   - List active devices/sessions per user (GET /api/v2/auth/sessions)
--   - Revoke individual sessions (device kick/offline)
--   - "Logout all" functionality
-- The JWT payload includes a `jti` (session ID) that maps to this table.

CREATE TABLE IF NOT EXISTS user_sessions (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id         VARCHAR(64) NOT NULL,          -- links to users.id
    jti             VARCHAR(64) NOT NULL,           -- JWT session ID (unique per token)
    device_name     VARCHAR(128) NOT NULL DEFAULT '',  -- e.g. "Chrome 120 / macOS"
    device_type     VARCHAR(32)  NOT NULL DEFAULT '',  -- desktop | mobile | tablet
    ip_address      VARCHAR(64)  NOT NULL DEFAULT '',
    user_agent      TEXT          NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    last_active_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    expires_at      TIMESTAMPTZ  NOT NULL,
    revoked         BOOLEAN      NOT NULL DEFAULT FALSE,
    revoked_at      TIMESTAMPTZ,

    CONSTRAINT uk_user_sessions_jti UNIQUE (jti)
);

CREATE INDEX IF NOT EXISTS idx_user_sessions_user ON user_sessions (user_id) WHERE revoked = FALSE;
CREATE INDEX IF NOT EXISTS idx_user_sessions_expires ON user_sessions (expires_at) WHERE revoked = FALSE;

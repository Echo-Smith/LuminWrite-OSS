-- 059: Admin Audit Log
-- Tracks who did what, when, and what changed.

CREATE TABLE IF NOT EXISTS admin_audit_logs (
    id          BIGSERIAL PRIMARY KEY,
    actor_id    VARCHAR(128) NOT NULL,
    actor_role  VARCHAR(32)  NOT NULL DEFAULT 'admin',
    action      VARCHAR(64)  NOT NULL,
    resource    VARCHAR(64)  NOT NULL,
    resource_id VARCHAR(128),
    detail      TEXT         NOT NULL DEFAULT '',
    changes     JSONB        NOT NULL DEFAULT '{}'::jsonb,
    ip_address  VARCHAR(64),
    user_agent  TEXT,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_audit_logs_actor ON admin_audit_logs (actor_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_logs_resource ON admin_audit_logs (resource, resource_id);
CREATE INDEX IF NOT EXISTS idx_audit_logs_action ON admin_audit_logs (action);
CREATE INDEX IF NOT EXISTS idx_audit_logs_created ON admin_audit_logs (created_at DESC);

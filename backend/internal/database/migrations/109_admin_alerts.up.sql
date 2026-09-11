-- 109: Admin alerts — ops patrol findings
-- Stores alerts raised by the background ops patrol worker (rule-based
-- health checks), with acknowledge/resolve state for the admin panel.

CREATE TABLE IF NOT EXISTS admin_alerts (
    id               UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    severity         VARCHAR(16) NOT NULL,                   -- info | warning | critical
    check_type       VARCHAR(64) NOT NULL,                   -- e.g. write_failure_rate, cron_failure
    fingerprint      VARCHAR(160) NOT NULL,                  -- dedupe key: check_type + resource id
    title            VARCHAR(256) NOT NULL,
    detail           TEXT NOT NULL DEFAULT '',
    evidence         JSONB NOT NULL DEFAULT '{}'::jsonb,
    status           VARCHAR(16) NOT NULL DEFAULT 'pending', -- pending | acked | resolved
    acknowledged_by  TEXT NOT NULL DEFAULT '',
    acknowledged_at  TIMESTAMPTZ,
    resolved_at      TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_admin_alerts_status_created ON admin_alerts (status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_admin_alerts_fingerprint ON admin_alerts (fingerprint, created_at DESC);

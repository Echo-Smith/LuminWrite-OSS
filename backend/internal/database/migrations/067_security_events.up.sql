-- 067: Security Events — persistent log for prompt injection interceptions
--
-- Stores every prompt injection detection event (from SanitizeExternalContent
-- and SanitizeUserInput) so that security teams can:
--   - Query historical interception trends
--   - Identify attack patterns by source/type
--   - Correlate with session traces
--   - Export for compliance audits

CREATE TABLE IF NOT EXISTS security_events (
    id              BIGSERIAL PRIMARY KEY,
    event_type      VARCHAR(32) NOT NULL,                 -- external_content | user_input
    source          VARCHAR(256) NOT NULL DEFAULT '',     -- e.g. "search_result[0].snippet", "user_input"
    pattern_count   INTEGER NOT NULL DEFAULT 1,            -- number of injection patterns matched
    -- Context for forensic analysis (optional, best-effort)
    trace_id        VARCHAR(64) NOT NULL DEFAULT '',
    user_id         VARCHAR(128) NOT NULL DEFAULT '',
    session_id      VARCHAR(64) NOT NULL DEFAULT '',
    -- Pattern categories matched (JSON array, e.g. ["direct_override","identity_override"])
    pattern_types   JSONB NOT NULL DEFAULT '[]'::jsonb,
    -- Snippet of the intercepted content (truncated for storage)
    content_snippet TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Indexes for common query patterns
CREATE INDEX IF NOT EXISTS idx_security_events_time ON security_events(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_security_events_type ON security_events(event_type);
CREATE INDEX IF NOT EXISTS idx_security_events_source ON security_events(source);
CREATE INDEX IF NOT EXISTS idx_security_events_trace ON security_events(trace_id) WHERE trace_id != '';
CREATE INDEX IF NOT EXISTS idx_security_events_user ON security_events(user_id) WHERE user_id != '';

-- Retention: auto-cleanup events older than 90 days via a scheduled job
-- (the cron scheduler can handle this with a DELETE WHERE created_at < NOW() - INTERVAL '90 days')

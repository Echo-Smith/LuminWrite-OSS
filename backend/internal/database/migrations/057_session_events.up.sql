-- 057: Session Event Log — append-only event log for replay/fork/telemetry
--
-- Each row represents a single discrete event in the agent execution lifecycle.
-- Events are never updated or deleted — only appended.
-- This enables:
--   - Session replay (reconstruct UI from events)
--   - Fork from any step (re-run with different parameters)
--   - Telemetry & evaluation data export
--   - Audit trail for compliance

CREATE TABLE IF NOT EXISTS session_events (
    id              BIGSERIAL PRIMARY KEY,
    trace_id        VARCHAR(64) NOT NULL,
    seq             INTEGER NOT NULL,          -- monotonic sequence within a trace
    event_type      VARCHAR(64) NOT NULL,       -- step.start, step.complete, stream.delta, paused, resumed, error, completed, cancelled, etc.
    step            VARCHAR(64),                -- step name (if applicable)
    data            JSONB DEFAULT '{}',         -- event payload (result, duration, error message, etc.)
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE(trace_id, seq)
);

CREATE INDEX IF NOT EXISTS idx_session_events_trace ON session_events (trace_id, seq);
CREATE INDEX IF NOT EXISTS idx_session_events_type ON session_events (event_type);
CREATE INDEX IF NOT EXISTS idx_session_events_created ON session_events (created_at DESC);

-- 004_agent_traces.up.sql

CREATE TABLE IF NOT EXISTS agent_traces (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    trace_id        VARCHAR(64) UNIQUE NOT NULL,
    user_id         UUID REFERENCES users (id),
    session_id      VARCHAR(64),

    user_input      TEXT NOT NULL,
    style_slug      VARCHAR(64),
    mode            VARCHAR(16) NOT NULL DEFAULT 'auto',

    status          VARCHAR(16) NOT NULL DEFAULT 'running',
    current_step    VARCHAR(64),
    step_history     JSONB DEFAULT '[]',

    article         TEXT,
    review_result   JSONB,

    token_usage     JSONB DEFAULT '{}',
    duration_ms     INTEGER,
    error           TEXT,

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at    TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_traces_user_id ON agent_traces (user_id);
CREATE INDEX IF NOT EXISTS idx_traces_status ON agent_traces (status);
CREATE INDEX IF NOT EXISTS idx_traces_created_at ON agent_traces (created_at DESC);

CREATE TABLE IF NOT EXISTS feedback_segments (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    trace_id        VARCHAR(64) NOT NULL,
    user_id         UUID REFERENCES users (id),

    segment_type    VARCHAR(16) NOT NULL,
    segment_index   INTEGER,
    segment_text    TEXT,

    rating          INTEGER NOT NULL,
    feedback_type   VARCHAR(32),
    comment         TEXT,

    user_reputation DECIMAL(5,2) NOT NULL DEFAULT 1.00,
    is_adopted      BOOLEAN NOT NULL DEFAULT FALSE,
    adopted_source  VARCHAR(64),
    adopted_at      TIMESTAMPTZ,

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_feedback_trace_id ON feedback_segments (trace_id);
CREATE INDEX IF NOT EXISTS idx_feedback_user_id ON feedback_segments (user_id);

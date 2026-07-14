-- Feedback segments (per-segment user feedback)
CREATE TABLE IF NOT EXISTS feedback_segments (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    trace_id        VARCHAR(64) NOT NULL,
    user_id         UUID,

    segment_type    VARCHAR(16) NOT NULL,           -- title | paragraph | sentence | overall
    segment_index   INTEGER,
    segment_text    TEXT,

    rating          INTEGER NOT NULL,               -- 1-5
    feedback_type   VARCHAR(32),                    -- good | bad | suggestion
    comment         TEXT,

    user_reputation DECIMAL(5,2) NOT NULL DEFAULT 1.00,
    is_adopted      BOOLEAN NOT NULL DEFAULT FALSE,
    adopted_at      TIMESTAMPTZ,
    adopted_source  VARCHAR(64),

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_feedback_trace_id ON feedback_segments (trace_id);
CREATE INDEX IF NOT EXISTS idx_feedback_user_id ON feedback_segments (user_id);
CREATE INDEX IF NOT EXISTS idx_feedback_type ON feedback_segments (feedback_type);
CREATE INDEX IF NOT EXISTS idx_feedback_adopted ON feedback_segments (is_adopted) WHERE is_adopted = TRUE;
CREATE INDEX IF NOT EXISTS idx_feedback_created ON feedback_segments (created_at DESC);

-- Feedback aggregation (style iteration)
CREATE TABLE IF NOT EXISTS feedback_aggregation (
    id                    UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    style_slug            VARCHAR(64) NOT NULL,
    profile_version       INTEGER NOT NULL,

    total_feedback        INTEGER NOT NULL DEFAULT 0,
    total_adopted         INTEGER NOT NULL DEFAULT 0,
    avg_rating            DECIMAL(3,2) NOT NULL DEFAULT 0.00,
    weighted_score        DECIMAL(5,2) NOT NULL DEFAULT 0.00,

    dimension_scores      JSONB DEFAULT '{}',
    segment_breakdown     JSONB DEFAULT '{}',
    improvement_suggestions TEXT,

    ready_for_iteration   BOOLEAN NOT NULL DEFAULT FALSE,
    iteration_threshold   INTEGER NOT NULL DEFAULT 30,

    period_start          TIMESTAMPTZ NOT NULL,
    period_end            TIMESTAMPTZ NOT NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT uk_feedback_agg UNIQUE (style_slug, profile_version, period_start)
);

-- Evaluation sets
CREATE TABLE IF NOT EXISTS evaluation_sets (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name            VARCHAR(128) NOT NULL,
    style_slug      VARCHAR(64) NOT NULL,
    description     TEXT,

    status          VARCHAR(16) NOT NULL DEFAULT 'draft',
    sample_count    INTEGER NOT NULL DEFAULT 0,

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_eval_sets_style ON evaluation_sets (style_slug);

-- Evaluation samples
CREATE TABLE IF NOT EXISTS evaluation_samples (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    set_id          UUID NOT NULL REFERENCES evaluation_sets (id) ON DELETE CASCADE,

    topic           VARCHAR(256) NOT NULL,
    input_prompt    TEXT NOT NULL,
    style_slug      VARCHAR(64) NOT NULL,

    expected_structure  JSONB,
    expected_keywords   TEXT[],
    expected_length     INTEGER,
    red_flags           TEXT[],
    annotator           VARCHAR(64),
    annotation_notes    TEXT,

    scoring_criteria    JSONB NOT NULL,
    status              VARCHAR(16) NOT NULL DEFAULT 'pending',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_eval_samples_set ON evaluation_samples (set_id);
CREATE INDEX IF NOT EXISTS idx_eval_samples_style ON evaluation_samples (style_slug);

-- Evaluation runs
CREATE TABLE IF NOT EXISTS evaluation_runs (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    set_id          UUID NOT NULL REFERENCES evaluation_sets (id),
    profile_slug    VARCHAR(64) NOT NULL,
    profile_version INTEGER NOT NULL,

    trigger_type    VARCHAR(32) NOT NULL,
    trigger_detail  TEXT,

    status          VARCHAR(16) NOT NULL DEFAULT 'pending',
    total_samples   INTEGER NOT NULL DEFAULT 0,
    completed_count INTEGER NOT NULL DEFAULT 0,
    results         JSONB DEFAULT '[]',

    overall_score   DECIMAL(5,2),
    dimension_scores JSONB DEFAULT '{}',

    started_at      TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_eval_runs_profile ON evaluation_runs (profile_slug, profile_version);
CREATE INDEX IF NOT EXISTS idx_eval_runs_set ON evaluation_runs (set_id);

-- 065: Self-Evolution Gate — configurable thresholds, eval results, and health monitoring
--
-- This migration adds:
-- 1. evolution_gate_configs: per-style configurable thresholds for auto-rollback
-- 2. Extends style_profile_candidates with eval result columns
-- 3. evolution_gate_events: audit trail for all gate decisions

-- ─── Gate Configurations ───────────────────────────────
CREATE TABLE IF NOT EXISTS evolution_gate_configs (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    style_slug      VARCHAR(64) NOT NULL UNIQUE,
    -- Eval gate thresholds
    min_eval_score      DECIMAL(3,2) NOT NULL DEFAULT 3.00,
    max_regression_drop DECIMAL(3,2) NOT NULL DEFAULT 0.30,
    -- Canary auto-rollback thresholds
    error_rate_threshold    DECIMAL(5,2) NOT NULL DEFAULT 10.00,
    min_sample_size         INTEGER NOT NULL DEFAULT 50,
    observation_window_min  INTEGER NOT NULL DEFAULT 10,
    auto_rollback_enabled   BOOLEAN NOT NULL DEFAULT TRUE,
    -- Promotion thresholds
    auto_promote_enabled    BOOLEAN NOT NULL DEFAULT FALSE,
    auto_promote_min_uptime DECIMAL(5,2) NOT NULL DEFAULT 99.00,
    auto_promote_window_min INTEGER NOT NULL DEFAULT 30,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Default config for all existing styles
INSERT INTO evolution_gate_configs (style_slug)
SELECT DISTINCT style_slug FROM style_profile_candidates
ON CONFLICT (style_slug) DO NOTHING;

-- ─── Extend candidates with eval results ─────────────────
ALTER TABLE style_profile_candidates
    ADD COLUMN IF NOT EXISTS eval_run_id UUID,
    ADD COLUMN IF NOT EXISTS eval_score DECIMAL(3,2),
    ADD COLUMN IF NOT EXISTS eval_passed BOOLEAN,
    ADD COLUMN IF NOT EXISTS eval_completed_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS eval_summary JSONB DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS rejected_reason TEXT,
    ADD COLUMN IF NOT EXISTS approved_by VARCHAR(128),
    ADD COLUMN IF NOT EXISTS approved_at TIMESTAMPTZ;

-- ─── Gate Events (audit trail) ──────────────────────────
CREATE TABLE IF NOT EXISTS evolution_gate_events (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    candidate_id    UUID NOT NULL REFERENCES style_profile_candidates(id),
    event_type      VARCHAR(32) NOT NULL,
    actor_id        VARCHAR(128) NOT NULL DEFAULT 'system',
    actor_type      VARCHAR(16) NOT NULL DEFAULT 'system',
    detail          TEXT NOT NULL DEFAULT '',
    metadata        JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_gate_events_candidate
    ON evolution_gate_events (candidate_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_gate_events_type
    ON evolution_gate_events (event_type);

-- ─── Canary health snapshots ───────────────────────────
CREATE TABLE IF NOT EXISTS canary_health_snapshots (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    candidate_id    UUID NOT NULL REFERENCES style_profile_candidates(id),
    style_slug      VARCHAR(64) NOT NULL,
    total_requests  INTEGER NOT NULL DEFAULT 0,
    new_version_hits INTEGER NOT NULL DEFAULT 0,
    old_version_hits INTEGER NOT NULL DEFAULT 0,
    error_count     INTEGER NOT NULL DEFAULT 0,
    error_rate      DECIMAL(5,2) NOT NULL DEFAULT 0,
    uptime_pct      DECIMAL(5,2) NOT NULL DEFAULT 100,
    triggered_rollback BOOLEAN NOT NULL DEFAULT FALSE,
    rollback_reason TEXT,
    captured_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_health_snap_candidate
    ON canary_health_snapshots (candidate_id, captured_at DESC);
CREATE INDEX IF NOT EXISTS idx_health_snap_style
    ON canary_health_snapshots (style_slug, captured_at DESC);

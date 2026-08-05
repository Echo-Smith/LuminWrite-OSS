-- 055: Self-Evolution Loop — candidate profiles and canary rollout tracking
CREATE TABLE IF NOT EXISTS style_profile_candidates (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    style_slug VARCHAR(64) NOT NULL,
    parent_version INTEGER NOT NULL DEFAULT 0,
    changes JSONB NOT NULL DEFAULT '{}',
    eval_baseline_id UUID,
    eval_candidate_id UUID,
    status VARCHAR(16) NOT NULL DEFAULT 'draft',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS canary_rollouts (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    style_slug VARCHAR(64) NOT NULL,
    version INTEGER NOT NULL,
    candidate_id UUID NOT NULL REFERENCES style_profile_candidates(id),
    percentage DECIMAL(3,2) NOT NULL DEFAULT 0,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    started_at TIMESTAMPTZ,
    ended_at TIMESTAMPTZ,
    rollback_reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_candidates_style ON style_profile_candidates (style_slug);
CREATE INDEX IF NOT EXISTS idx_rollouts_style ON canary_rollouts (style_slug);

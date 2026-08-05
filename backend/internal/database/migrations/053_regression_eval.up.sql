-- 053: Regression Eval Suite — store baseline scores and regression comparisons
CREATE TABLE IF NOT EXISTS eval_regression_baselines (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    style_slug VARCHAR(64) NOT NULL,
    set_id UUID NOT NULL,
    run_id UUID NOT NULL,
    overall_score DECIMAL(5,2) NOT NULL DEFAULT 0,
    dimension_scores JSONB NOT NULL DEFAULT '{}',
    snapshot JSONB NOT NULL DEFAULT '{}',
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uk_regression_baseline UNIQUE (style_slug, set_id)
);
CREATE INDEX IF NOT EXISTS idx_regression_baseline_style ON eval_regression_baselines (style_slug);
CREATE TABLE IF NOT EXISTS eval_regression_comparisons (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    style_slug VARCHAR(64) NOT NULL,
    set_id UUID NOT NULL,
    baseline_run_id UUID NOT NULL,
    candidate_run_id UUID NOT NULL,
    score_delta DECIMAL(5,2) NOT NULL DEFAULT 0,
    dimension_deltas JSONB NOT NULL DEFAULT '{}',
    regressions JSONB NOT NULL DEFAULT '[]',
    is_passing BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_regression_comparison_style ON eval_regression_comparisons (style_slug);

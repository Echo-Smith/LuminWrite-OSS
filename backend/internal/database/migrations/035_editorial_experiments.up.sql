-- 对照实验框架：同一选题跑 Pipeline / Harness / Editorial 三组对比
CREATE TABLE IF NOT EXISTS editorial_experiments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    style_slug TEXT NOT NULL DEFAULT 'yinyue',
    status TEXT NOT NULL DEFAULT 'pending', -- pending | running | completed | failed
    -- 三组结果（JSON）
    pipeline_result JSONB DEFAULT '{}',
    unified_result JSONB DEFAULT '{}',
    editorial_result JSONB DEFAULT '{}',
    -- 对比指标汇总
    summary JSONB DEFAULT '{}',
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_editorial_experiments_status ON editorial_experiments(status);
CREATE INDEX idx_editorial_experiments_created_at ON editorial_experiments(created_at DESC);

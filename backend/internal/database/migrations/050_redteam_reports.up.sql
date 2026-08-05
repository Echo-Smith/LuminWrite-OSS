-- Red-team evaluation reports
-- Stores results of adversarial security evaluations
CREATE TABLE IF NOT EXISTS redteam_reports (
    id            TEXT PRIMARY KEY,
    total_cases   INTEGER NOT NULL DEFAULT 0,
    passed_cases  INTEGER NOT NULL DEFAULT 0,
    failed_cases  INTEGER NOT NULL DEFAULT 0,
    pass_rate     DOUBLE PRECISION NOT NULL DEFAULT 0,
    results       JSONB NOT NULL DEFAULT '[]',
    category_summary JSONB NOT NULL DEFAULT '{}',
    system_prompt TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT 'completed',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at  TIMESTAMPTZ
);

-- Index for listing reports by creation time
CREATE INDEX IF NOT EXISTS idx_redteam_reports_created_at ON redteam_reports (created_at DESC);

-- Index for filtering by status
CREATE INDEX IF NOT EXISTS idx_redteam_reports_status ON redteam_reports (status);

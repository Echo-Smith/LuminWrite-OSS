-- 008: Admin management tables (model_configs, api_keys, token_usage, cron_jobs)

-- ─── model_configs ──────────────────────────────────────
CREATE TABLE IF NOT EXISTS model_configs (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    provider     VARCHAR(64)  NOT NULL,              -- deepseek | openai | qwen | claude
    model_name   VARCHAR(128) NOT NULL,              -- e.g. deepseek-v4-flash, gpt-4o
    display_name VARCHAR(128) NOT NULL DEFAULT '',
    base_url     TEXT         NOT NULL DEFAULT '',
    max_tokens   INT          NOT NULL DEFAULT 8192,
    temperature  REAL         NOT NULL DEFAULT 0.7,
    is_default   BOOLEAN      NOT NULL DEFAULT FALSE,
    is_active    BOOLEAN      NOT NULL DEFAULT TRUE,
    capabilities JSONB        NOT NULL DEFAULT '{}'::jsonb,  -- {"stream": true, "thinking": true, "vision": false}
    metadata     JSONB        NOT NULL DEFAULT '{}'::jsonb,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_model_configs_provider_model ON model_configs(provider, model_name);
CREATE INDEX IF NOT EXISTS idx_model_configs_active ON model_configs(is_active);
-- Ensure only one default per provider
CREATE UNIQUE INDEX IF NOT EXISTS idx_model_configs_default ON model_configs(provider) WHERE is_default = TRUE;

-- ─── api_keys ───────────────────────────────────────────
CREATE TABLE IF NOT EXISTS api_keys (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name         VARCHAR(128) NOT NULL,              -- human-readable label
    provider     VARCHAR(64)  NOT NULL,              -- deepseek | tavily | zhihu | ima | dashscope
    key_value    TEXT         NOT NULL,              -- encrypted at app layer
    base_url     TEXT         NOT NULL DEFAULT '',
    is_active    BOOLEAN      NOT NULL DEFAULT TRUE,
    last_used_at TIMESTAMPTZ,
    last_check   TIMESTAMPTZ,
    last_status  VARCHAR(16)  NOT NULL DEFAULT 'unknown', -- ok | fail | unknown
    last_error   TEXT,
    metadata     JSONB        NOT NULL DEFAULT '{}'::jsonb,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_api_keys_provider ON api_keys(provider);
CREATE INDEX IF NOT EXISTS idx_api_keys_active ON api_keys(is_active);

-- ─── token_usage ────────────────────────────────────────
CREATE TABLE IF NOT EXISTS token_usage (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    trace_id      VARCHAR(64),
    user_id       VARCHAR(128),
    model_name    VARCHAR(128) NOT NULL,
    provider      VARCHAR(64)  NOT NULL,
    prompt_tokens INT          NOT NULL DEFAULT 0,
    completion_tokens INT      NOT NULL DEFAULT 0,
    total_tokens  INT          NOT NULL DEFAULT 0,
    estimated_cost REAL        NOT NULL DEFAULT 0,   -- in CNY
    api_key_id    UUID REFERENCES api_keys(id) ON DELETE SET NULL,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_token_usage_trace ON token_usage(trace_id);
CREATE INDEX IF NOT EXISTS idx_token_usage_model ON token_usage(model_name);
CREATE INDEX IF NOT EXISTS idx_token_usage_created ON token_usage(created_at);
CREATE INDEX IF NOT EXISTS idx_token_usage_user ON token_usage(user_id);

-- ─── cron_jobs ──────────────────────────────────────────
CREATE TABLE IF NOT EXISTS cron_jobs (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name         VARCHAR(128) NOT NULL,
    description  TEXT         NOT NULL DEFAULT '',
    schedule     VARCHAR(64)  NOT NULL,              -- cron expression or interval string
    task_type    VARCHAR(64)  NOT NULL,              -- topic_fetch | feedback_aggregate | eval_run | cleanup | custom
    task_config  JSONB        NOT NULL DEFAULT '{}'::jsonb,
    is_active    BOOLEAN      NOT NULL DEFAULT TRUE,
    last_run_at  TIMESTAMPTZ,
    next_run_at  TIMESTAMPTZ,
    last_status  VARCHAR(16)  NOT NULL DEFAULT 'pending', -- success | failed | running | pending
    last_error   TEXT,
    run_count    INT          NOT NULL DEFAULT 0,
    fail_count   INT          NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cron_jobs_active ON cron_jobs(is_active);
CREATE INDEX IF NOT EXISTS idx_cron_jobs_next_run ON cron_jobs(next_run_at) WHERE is_active = TRUE;

-- ─── Default seed data ──────────────────────────────────

-- Default model configs
INSERT INTO model_configs (provider, model_name, display_name, base_url, max_tokens, temperature, is_default, is_active, capabilities)
VALUES
    ('deepseek', 'deepseek-v4-flash', 'DeepSeek V4 Flash', 'https://api.deepseek.com', 8192, 0.7, TRUE, TRUE,
     '{"stream": true, "thinking": true, "vision": false}'),
    ('deepseek', 'deepseek-v4-pro', 'DeepSeek V4 Pro', 'https://api.deepseek.com', 16384, 0.0, FALSE, TRUE,
     '{"stream": true, "thinking": true, "vision": false}'),
    ('qwen', 'qwen-plus', '通义千问 Plus', 'https://dashscope.aliyuncs.com/compatible-mode/v1', 8192, 0.7, FALSE, FALSE,
     '{"stream": true, "thinking": false, "vision": false}'),
    ('openai', 'gpt-4o', 'GPT-4o', 'https://api.openai.com/v1', 8192, 0.7, FALSE, FALSE,
     '{"stream": true, "thinking": false, "vision": true}')
ON CONFLICT (provider, model_name) DO NOTHING;

-- Default cron jobs
INSERT INTO cron_jobs (name, description, schedule, task_type, task_config, is_active)
VALUES
    ('热点选题拉取', '每小时从微博/知乎抓取热榜选题', '0 * * * *', 'topic_fetch',
     '{"sources": ["weibo", "zhihu"]}', TRUE),
    ('反馈聚合计算', '每天凌晨聚合用户反馈数据', '0 2 * * *', 'feedback_aggregate',
     '{"threshold": 30}', TRUE),
    ('Token 用量清理', '每天清理90天前的用量记录', '0 3 * * *', 'cleanup',
     '{"retention_days": 90}', FALSE)
ON CONFLICT DO NOTHING;

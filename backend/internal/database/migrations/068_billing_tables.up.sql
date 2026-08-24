-- 068: Billing tables — point-based economy
--
-- This migration creates the billing infrastructure:
-- 1. point_rates: admin-configurable rates for converting tokens to points
-- 2. user_point_balance: per-user point balance
-- 3. recharge_orders: payment orders for point purchases
-- 4. point_consumption_log: detailed consumption records
-- 5. subscription_plans: plan definitions with feature flags
-- 6. user_subscriptions: active subscriptions

-- ─── 1. 点数费率配置 ──────────────────────────────────
CREATE TABLE IF NOT EXISTS point_rates (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    model_name      VARCHAR(128) NOT NULL,              -- 模型名（* = 通配所有未配置的模型）
    task_type       VARCHAR(32) NOT NULL,               -- writing | editorial | memory | fact_check | search
    input_rate      DECIMAL(8,4) NOT NULL DEFAULT 0.001, -- 每输入 Token 消耗的点数
    output_rate     DECIMAL(8,4) NOT NULL DEFAULT 0.003, -- 每输出 Token 消耗的点数
    is_active       BOOLEAN NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uk_point_rates_model_task UNIQUE(model_name, task_type)
);

-- ─── 2. 用户点数余额 ───────────────────────────────────
CREATE TABLE IF NOT EXISTS user_point_balance (
    user_id             UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    balance             DECIMAL(12,2) NOT NULL DEFAULT 0,   -- 当前余额（点数）
    total_recharged     DECIMAL(12,2) NOT NULL DEFAULT 0,   -- 累计充值
    total_consumed      DECIMAL(12,2) NOT NULL DEFAULT 0,   -- 累计消费
    total_refunded      DECIMAL(12,2) NOT NULL DEFAULT 0,   -- 累计退还
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ─── 3. 充值订单 ───────────────────────────────────────
CREATE TABLE IF NOT EXISTS recharge_orders (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    amount          DECIMAL(10,2) NOT NULL,               -- 支付金额（元）
    point_amount    DECIMAL(12,2) NOT NULL,               -- 获得的点数
    payment_method  VARCHAR(32) NOT NULL DEFAULT 'manual', -- manual | wechat | alipay
    payment_url     TEXT,
    status          VARCHAR(16) NOT NULL DEFAULT 'pending', -- pending | paid | failed | expired
    paid_at         TIMESTAMPTZ,
    expires_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_recharge_orders_user ON recharge_orders(user_id, status);

-- ─── 4. 点数消费明细 ───────────────────────────────────
CREATE TABLE IF NOT EXISTS point_consumption_log (
    id                  UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id             UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    trace_id            VARCHAR(64),                     -- 关联写作 trace
    task_type           VARCHAR(32) NOT NULL,             -- writing | editorial | memory | fact_check
    model_name          VARCHAR(128),                     -- 使用的模型
    prompt_tokens       INT NOT NULL DEFAULT 0,          -- 底层 Token（审计用）
    completion_tokens   INT NOT NULL DEFAULT 0,
    input_rate          DECIMAL(8,4) NOT NULL DEFAULT 0,  -- 使用的费率快照
    output_rate         DECIMAL(8,4) NOT NULL DEFAULT 0,
    points_used         DECIMAL(12,2) NOT NULL,          -- 换算后消耗的点数
    balance_before      DECIMAL(12,2) NOT NULL,
    balance_after       DECIMAL(12,2) NOT NULL,
    metadata            JSONB DEFAULT '{}'::jsonb,       -- 额外信息（Agent 数、风格等）
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_consumption_user_date ON point_consumption_log(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_consumption_trace ON point_consumption_log(trace_id) WHERE trace_id IS NOT NULL;

-- ─── 5. 套餐定义 ───────────────────────────────────────
CREATE TABLE IF NOT EXISTS subscription_plans (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name            VARCHAR(64) NOT NULL UNIQUE,         -- free | creator | pro | enterprise
    display_name    VARCHAR(128) NOT NULL,
    price_monthly   DECIMAL(10,2) NOT NULL DEFAULT 0,   -- 月费（元）
    point_quota     DECIMAL(12,2) NOT NULL DEFAULT 0,    -- 月度点数额度
    features        JSONB NOT NULL DEFAULT '{}'::jsonb,   -- 功能开关矩阵
    is_active       BOOLEAN NOT NULL DEFAULT TRUE,
    is_popular      BOOLEAN NOT NULL DEFAULT FALSE,
    sort_order      INTEGER NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ─── 6. 用户订阅 ───────────────────────────────────────
CREATE TABLE IF NOT EXISTS user_subscriptions (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    plan_id         UUID NOT NULL REFERENCES subscription_plans(id),
    status          VARCHAR(16) NOT NULL DEFAULT 'active', -- active | expired | cancelled | suspended
    started_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at      TIMESTAMPTZ,                          -- NULL = 永不过期（免费套餐）
    auto_renew      BOOLEAN NOT NULL DEFAULT FALSE,
    cancelled_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_user_subscriptions_user ON user_subscriptions(user_id) WHERE status = 'active';
CREATE INDEX IF NOT EXISTS idx_user_subscriptions_expires ON user_subscriptions(expires_at) WHERE status = 'active';

-- ─── 全局倍率（单行配置表）────────────────────────────
CREATE TABLE IF NOT EXISTS billing_config (
    id              INT PRIMARY KEY DEFAULT 1,
    global_multiplier DECIMAL(4,2) NOT NULL DEFAULT 1.0,  -- 全局倍率，乘以费率表
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT single_row CHECK (id = 1)
);
INSERT INTO billing_config (id, global_multiplier) VALUES (1, 1.0) ON CONFLICT DO NOTHING;

-- ─── 默认费率种子数据 ──────────────────────────────────
INSERT INTO point_rates (model_name, task_type, input_rate, output_rate) VALUES
    ('*', 'writing',        0.001, 0.003),
    ('*', 'editorial',      0.001, 0.003),
    ('*', 'memory',         0.001, 0.002),
    ('*', 'fact_check',     0.001, 0.002),
    ('*', 'search',         0.0005, 0),
    ('deepseek-v4-pro', 'writing',   0.002, 0.006),
    ('deepseek-v4-pro', 'editorial', 0.002, 0.006)
ON CONFLICT (model_name, task_type) DO NOTHING;

-- ─── 默认套餐种子数据 ──────────────────────────────────
INSERT INTO subscription_plans (name, display_name, price_monthly, point_quota, features, is_active, is_popular, sort_order) VALUES
    ('free',       '免费版',   0,    500,    '{"editorial_mode":false,"max_agents":1,"memory_enabled":false,"custom_styles":false,"kb_doc_limit":0,"max_word_count":800,"daily_write_limit":3}', TRUE, FALSE, 1),
    ('creator',    '创作者版', 39,   10000,   '{"editorial_mode":false,"max_agents":1,"memory_enabled":true,"custom_styles":true,"kb_doc_limit":50,"max_word_count":3000,"daily_write_limit":0}', TRUE, TRUE, 2),
    ('pro',        '专业版',   99,   30000,   '{"editorial_mode":true,"max_agents":6,"memory_enabled":true,"custom_styles":true,"kb_doc_limit":500,"max_word_count":0,"daily_write_limit":0,"graphrag":true,"mcp_tools":10}', TRUE, FALSE, 3),
    ('enterprise', '企业版',   399,  100000,  '{"editorial_mode":true,"max_agents":99,"memory_enabled":true,"custom_styles":true,"kb_doc_limit":9999,"max_word_count":0,"daily_write_limit":0,"graphrag":true,"mcp_tools":99,"rbac":true,"api_access":true}', TRUE, FALSE, 4)
ON CONFLICT (name) DO NOTHING;

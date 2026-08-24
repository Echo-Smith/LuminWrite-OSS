-- 085: recharge_orders 扩展 — 支持支付渠道、订单类型、套餐关联
--
-- 新增字段：
--   trade_no     — 第三方交易号（支付宝 trade_no）
--   pay_channel  — 支付渠道（alipay | wechat | manual | redeem_code）
--   order_type   — 订单类型（recharge | subscription | upgrade）
--   plan_id      — 关联套餐ID（subscription/upgrade 时使用）
--   period       — 订阅周期（monthly | yearly）

ALTER TABLE recharge_orders
    ADD COLUMN IF NOT EXISTS trade_no    VARCHAR(128),
    ADD COLUMN IF NOT EXISTS pay_channel VARCHAR(32) NOT NULL DEFAULT 'manual',
    ADD COLUMN IF NOT EXISTS order_type  VARCHAR(32) NOT NULL DEFAULT 'recharge',
    ADD COLUMN IF NOT EXISTS plan_id     UUID REFERENCES subscription_plans(id),
    ADD COLUMN IF NOT EXISTS period      VARCHAR(16);

-- 为回调查询创建索引（按 trade_no 和 order status）
CREATE INDEX IF NOT EXISTS idx_recharge_orders_trade_no
    ON recharge_orders(trade_no) WHERE trade_no IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_recharge_orders_status
    ON recharge_orders(status, created_at DESC);

-- 迁移旧数据：按 payment_method 推断 pay_channel 和 order_type
UPDATE recharge_orders
SET pay_channel = payment_method,
    order_type = 'recharge'
WHERE pay_channel = 'manual' AND order_type = 'recharge';

-- 将 payment_method 列重命名为 pay_channel（保持兼容）
-- 注意：不删除旧列，应用层逐步切换到 pay_channel

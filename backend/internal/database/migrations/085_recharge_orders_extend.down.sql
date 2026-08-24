-- 085 down: 回滚 recharge_orders 扩展

DROP INDEX IF EXISTS idx_recharge_orders_trade_no;
DROP INDEX IF EXISTS idx_recharge_orders_status;

ALTER TABLE recharge_orders
    DROP COLUMN IF EXISTS trade_no,
    DROP COLUMN IF EXISTS pay_channel,
    DROP COLUMN IF EXISTS order_type,
    DROP COLUMN IF EXISTS plan_id,
    DROP COLUMN IF EXISTS period;

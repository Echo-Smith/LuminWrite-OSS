-- 083 down: 回滚双轨积分拆分

DROP INDEX IF EXISTS idx_user_point_balance_reset_at;

-- 将 paid_balance + plan_balance 合并回 balance
UPDATE user_point_balance
SET balance = plan_balance + paid_balance
WHERE plan_balance IS NOT NULL OR paid_balance IS NOT NULL;

ALTER TABLE user_point_balance
    DROP COLUMN IF EXISTS plan_balance,
    DROP COLUMN IF EXISTS paid_balance,
    DROP COLUMN IF EXISTS plan_quota,
    DROP COLUMN IF EXISTS plan_reset_at;

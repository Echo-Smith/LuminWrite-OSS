-- 083: user_point_balance 拆分双轨积分
--
-- 将单一 balance 拆分为：
--   plan_balance  — 套餐积分（注册月清零，每月重置为套餐额度）
--   paid_balance  — 充值积分（永久有效，充值/兑换码获得）
--   plan_quota    — 本月套餐总额度（展示用，重置时参考）
--   plan_reset_at — 下次重置时间（基于 users.created_at 的月度周期）
--
-- 旧 balance 数据全部迁入 paid_balance（现有用户积分均为注册赠送/兑换码，属永久积分）
-- balance 列保留为计算列（plan_balance + paid_balance），通过应用层维护

-- 1. 新增列
ALTER TABLE user_point_balance
    ADD COLUMN IF NOT EXISTS plan_balance  DECIMAL(12,2) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS paid_balance  DECIMAL(12,2) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS plan_quota    DECIMAL(12,2) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS plan_reset_at TIMESTAMPTZ;

-- 2. 数据迁移：旧 balance 全部转入 paid_balance
--    plan_balance 初始为 0（将在首次月度重置时充入免费版 500 积分）
UPDATE user_point_balance
SET paid_balance = balance,
    plan_balance = 0,
    plan_quota = 0
WHERE plan_balance = 0 AND paid_balance = 0 AND balance > 0;

-- 3. 设置 plan_reset_at 为所有已有用户的下一次注册月周年
--    基于 users.created_at 计算下一个周年日
--    注意：PostgreSQL 不支持 LAST_DAY()（MySQL 函数），用 DATE_TRUNC + INTERVAL 计算月末
UPDATE user_point_balance b
SET plan_reset_at = sub.next_reset
FROM (
    SELECT
        b2.user_id,
        -- 计算下一个注册月周年：下个月的同一天（或月末如果注册日 > 下月天数）
        CASE
            WHEN EXTRACT(DAY FROM u.created_at) > EXTRACT(DAY FROM (DATE_TRUNC('month', NOW() + INTERVAL '1 month') + INTERVAL '1 month - 1 day')) THEN
                -- 注册日是 31 号等，取下月最后一天
                DATE_TRUNC('month', NOW() + INTERVAL '1 month')::timestamptz
                  + INTERVAL '1 month - 1 day'
            ELSE
                DATE_TRUNC('month', NOW() + INTERVAL '1 month')::timestamptz
                  + (EXTRACT(DAY FROM u.created_at) - 1) * INTERVAL '1 day'
        END AS next_reset
    FROM user_point_balance b2
    JOIN users u ON b2.user_id = u.id
) sub
WHERE b.user_id = sub.user_id
  AND b.plan_reset_at IS NULL;

-- 4. 给所有未初始化 plan_balance 的用户充入免费版 500 积分
--    （仅当 plan_balance = 0 且 plan_quota = 0 时，表示从未经过月度重置）
UPDATE user_point_balance
SET plan_balance = 500,
    plan_quota = 500
WHERE plan_balance = 0 AND plan_quota = 0;

-- 5. 创建索引用于定时任务扫描
CREATE INDEX IF NOT EXISTS idx_user_point_balance_reset_at
    ON user_point_balance(plan_reset_at)
    WHERE plan_reset_at IS NOT NULL;

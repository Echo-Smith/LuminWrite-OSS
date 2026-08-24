-- 072: 为已有注册用户补发初始积分
--
-- 积分系统上线前注册的用户没有 user_point_balance 记录。
-- 为所有非游客用户（role != 'guest'）补发 500 积分。
-- 游客不获得积分，注册升级时才会初始化。

INSERT INTO user_point_balance (user_id, balance, total_recharged)
SELECT u.id, 500, 500
FROM users u
WHERE u.role != 'guest'
  AND NOT EXISTS (
      SELECT 1 FROM user_point_balance b WHERE b.user_id = u.id
  )
ON CONFLICT (user_id) DO NOTHING;

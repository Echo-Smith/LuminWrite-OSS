-- 072 down: 回滚补发积分（仅删除由补发创建的 500 余额记录）
-- 注意：只删除 balance=500 且 total_consumed=0 的记录，避免误删已消费的用户
DELETE FROM user_point_balance
WHERE balance = 500
  AND total_consumed = 0
  AND total_recharged = 500;

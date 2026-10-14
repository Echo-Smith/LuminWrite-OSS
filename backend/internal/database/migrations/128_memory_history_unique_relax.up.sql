-- 128_memory_history_unique_relax: 偏好历史解锁——status 移出唯一键。
--
-- 旧 uk_user_memory (user_id, category, key, status) 使同一维度至多
-- 存在一行 superseded、一行 candidate：偏好演化到第 3 代起，supersede
-- 与新建全部撞唯一键（仅在应用日志 WARN，静默冻结）。真正的业务不
-- 变量是"同一维度至多一条活记忆（active/candidate）"，改用 partial
-- unique index 表达；superseded/archived 历史行不再受限。
--
-- 迁移期修复：旧约束允许同维度 active + candidate 并存（对齐撤回在
-- uk 冲突降级路径下会产生此形态）。保留最近观测的一条，其余降级为
-- superseded 并挂上撤回链指向幸存者。

ALTER TABLE user_memories DROP CONSTRAINT IF EXISTS uk_user_memory;

WITH ranked AS (
    SELECT id,
           FIRST_VALUE(id) OVER (
               PARTITION BY user_id, category, key
               ORDER BY last_seen DESC, created_at DESC, id DESC
           ) AS winner_id,
           ROW_NUMBER() OVER (
               PARTITION BY user_id, category, key
               ORDER BY last_seen DESC, created_at DESC, id DESC
           ) AS rn
    FROM user_memories
    WHERE status IN ('active', 'candidate')
)
UPDATE user_memories u
SET status = 'superseded',
    superseded_by = COALESCE(u.superseded_by, r.winner_id),
    updated_at = NOW()
FROM ranked r
WHERE u.id = r.id
  AND r.rn > 1
  AND u.status IN ('active', 'candidate');

CREATE UNIQUE INDEX uk_user_memory_live
    ON user_memories (user_id, category, key)
    WHERE status IN ('active', 'candidate');

-- 075_email_verification.up.sql
-- 添加邮箱验证相关字段

-- 添加 password_updated_at 字段（用于密码重置时间追踪）
ALTER TABLE users ADD COLUMN IF NOT EXISTS password_updated_at TIMESTAMPTZ;

-- 为 email 列添加 UNIQUE 约束（允许 NULL，即未绑定邮箱的用户不受限制）
-- 先删除可能存在的重复 email（保留最早注册的）
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE constraint_name = 'users_email_key' AND table_name = 'users'
    ) THEN
        -- 约束已存在，无需操作
        NULL;
    ELSE
        -- 清理重复 email（保留最早注册的，用 id::text 绕过 UUID 不支持 MIN 的问题）
        DELETE FROM users
        WHERE email IS NOT NULL
          AND id NOT IN (
            SELECT (MIN(id::text)::uuid) FROM users WHERE email IS NOT NULL GROUP BY email
          );
        -- 添加 UNIQUE 约束
        ALTER TABLE users ADD CONSTRAINT users_email_key UNIQUE (email);
    END IF;
END $$;

-- 添加 email 索引（加速查找）
CREATE INDEX IF NOT EXISTS idx_users_email ON users (email) WHERE email IS NOT NULL;

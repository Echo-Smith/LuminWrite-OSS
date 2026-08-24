-- 075_email_verification.down.sql
-- 回滚邮箱验证相关字段

DROP INDEX IF EXISTS idx_users_email;
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_email_key;
ALTER TABLE users DROP COLUMN IF EXISTS password_updated_at;

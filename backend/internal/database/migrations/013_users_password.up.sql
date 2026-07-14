-- 013_users_password.up.sql

-- Add password_hash column to users table for bcrypt-based authentication
ALTER TABLE users ADD COLUMN IF NOT EXISTS password_hash VARCHAR(128);
ALTER TABLE users ADD COLUMN IF NOT EXISTS role VARCHAR(16) NOT NULL DEFAULT 'user';

-- Create index for username lookup
CREATE INDEX IF NOT EXISTS idx_users_name ON users (name);

-- 014_api_key_hash.up.sql

-- Add key_hash column for fast API key lookup when key_value is encrypted
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS key_hash VARCHAR(64);

-- Create index for hash-based lookup
CREATE INDEX IF NOT EXISTS idx_api_keys_hash ON api_keys (key_hash) WHERE is_active = TRUE;

-- 026 down: Revert inline API key storage

-- Remove api_key_encrypted from model_configs
ALTER TABLE model_configs DROP COLUMN IF EXISTS api_key_encrypted;

-- Remove category from api_keys
ALTER TABLE api_keys DROP COLUMN IF EXISTS category;

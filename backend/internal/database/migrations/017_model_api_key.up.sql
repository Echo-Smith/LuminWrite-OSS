-- 017: Link model_configs to api_keys
ALTER TABLE model_configs ADD COLUMN IF NOT EXISTS api_key_id UUID REFERENCES api_keys(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_model_configs_api_key ON model_configs(api_key_id) WHERE api_key_id IS NOT NULL;

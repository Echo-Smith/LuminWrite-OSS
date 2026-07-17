-- 026: Inline API key storage on model_configs
-- Moves LLM API keys from api_keys table into model_configs directly,
-- so each model config is self-contained (base_url + api_key + model_name).
-- The api_keys table is repurposed for MCP/service keys only.

-- 1. Add api_key_encrypted column to model_configs
ALTER TABLE model_configs ADD COLUMN IF NOT EXISTS api_key_encrypted TEXT NOT NULL DEFAULT '';

-- 2. Add category column to api_keys to distinguish LLM vs MCP keys
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS category VARCHAR(32) NOT NULL DEFAULT 'mcp';

-- 3. Mark existing api_keys that are linked to model_configs as 'llm' category
UPDATE api_keys SET category = 'llm'
WHERE id IN (SELECT api_key_id FROM model_configs WHERE api_key_id IS NOT NULL);

-- 4. Migrate linked api_keys key_value → model_configs.api_key_encrypted
UPDATE model_configs mc
SET api_key_encrypted = ak.key_value
FROM api_keys ak
WHERE mc.api_key_id = ak.id AND mc.api_key_encrypted = '';

-- 5. Also migrate by provider match for unlinked configs
UPDATE model_configs mc
SET api_key_encrypted = sub.key_value
FROM (
    SELECT DISTINCT ON (provider) provider, key_value
    FROM api_keys
    WHERE category = 'llm' AND is_active = TRUE
    ORDER BY provider, created_at DESC
) sub
WHERE mc.provider = sub.provider AND mc.api_key_encrypted = '';

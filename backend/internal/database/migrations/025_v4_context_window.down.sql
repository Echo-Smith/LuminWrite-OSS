-- 025 down: Revert max_tokens and capabilities to previous values

UPDATE model_configs
SET max_tokens = 8192,
    capabilities = '{"stream": true, "thinking": true, "vision": false}'
WHERE provider = 'deepseek' AND model_name = 'deepseek-v4-flash';

UPDATE model_configs
SET max_tokens = 16384,
    capabilities = '{"stream": true, "thinking": true, "vision": false}'
WHERE provider = 'deepseek' AND model_name = 'deepseek-v4-pro';

UPDATE model_configs
SET metadata = metadata - 'context_window'
WHERE provider = 'deepseek' AND model_name IN ('deepseek-v4-flash', 'deepseek-v4-pro');

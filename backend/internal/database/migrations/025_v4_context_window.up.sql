-- 025: Update model configs for V4 1M context window and 384K max output
-- DeepSeek V4 models support up to 1M input tokens and 384K output tokens.
-- We increase max_tokens to leverage this for long-form writing and full-text material injection.

UPDATE model_configs
SET max_tokens = 16384,
    capabilities = '{"stream": true, "thinking": true, "vision": false, "context_window": 1048576, "max_output": 384000}'
WHERE provider = 'deepseek' AND model_name = 'deepseek-v4-flash';

UPDATE model_configs
SET max_tokens = 32768,
    capabilities = '{"stream": true, "thinking": true, "vision": false, "context_window": 1048576, "max_output": 384000}'
WHERE provider = 'deepseek' AND model_name = 'deepseek-v4-pro';

-- Add metadata for context window info (used by frontend for display)
UPDATE model_configs
SET metadata = jsonb_set(
    COALESCE(metadata, '{}'::jsonb),
    '{context_window}',
    '1048576'
)
WHERE provider = 'deepseek' AND model_name IN ('deepseek-v4-flash', 'deepseek-v4-pro');

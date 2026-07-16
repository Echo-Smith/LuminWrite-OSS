-- 024: Update model names from deprecated deepseek-chat/deepseek-reasoner to v4-flash/v4-pro
-- deepseek-chat and deepseek-reasoner will be deprecated on 2026/07/24 23:59 CST

-- Update deepseek-chat → deepseek-v4-flash
UPDATE model_configs
SET model_name    = 'deepseek-v4-flash',
    display_name  = 'DeepSeek V4 Flash',
    base_url      = 'https://api.deepseek.com',
    capabilities  = '{"stream": true, "thinking": true, "vision": false}',
    updated_at    = NOW()
WHERE provider = 'deepseek' AND model_name = 'deepseek-chat';

-- Update deepseek-reasoner → deepseek-v4-pro
UPDATE model_configs
SET model_name    = 'deepseek-v4-pro',
    display_name  = 'DeepSeek V4 Pro',
    base_url      = 'https://api.deepseek.com',
    temperature   = 0.0,
    capabilities  = '{"stream": true, "thinking": true, "vision": false}',
    updated_at    = NOW()
WHERE provider = 'deepseek' AND model_name = 'deepseek-reasoner';

-- Insert v4-pro if neither old nor new name existed (edge case for fresh-ish DBs)
INSERT INTO model_configs (provider, model_name, display_name, base_url, max_tokens, temperature, is_default, is_active, capabilities)
SELECT 'deepseek', 'deepseek-v4-pro', 'DeepSeek V4 Pro', 'https://api.deepseek.com', 16384, 0.0, FALSE, TRUE,
       '{"stream": true, "thinking": true, "vision": false}'::jsonb
WHERE NOT EXISTS (
    SELECT 1 FROM model_configs WHERE provider = 'deepseek' AND model_name = 'deepseek-v4-pro'
);

-- 024 down: Revert model names back to deepseek-chat/deepseek-reasoner
-- (Not recommended — old names deprecated 2026/07/24)

UPDATE model_configs
SET model_name    = 'deepseek-chat',
    display_name  = 'DeepSeek Chat',
    base_url      = 'https://api.deepseek.com/v1',
    capabilities  = '{"stream": true, "thinking": false, "vision": false}',
    updated_at    = NOW()
WHERE provider = 'deepseek' AND model_name = 'deepseek-v4-flash';

UPDATE model_configs
SET model_name    = 'deepseek-reasoner',
    display_name  = 'DeepSeek Reasoner (R1)',
    base_url      = 'https://api.deepseek.com/v1',
    capabilities  = '{"stream": true, "thinking": true, "vision": false}',
    updated_at    = NOW()
WHERE provider = 'deepseek' AND model_name = 'deepseek-v4-pro';

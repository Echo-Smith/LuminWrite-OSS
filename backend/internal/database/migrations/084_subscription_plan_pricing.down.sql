-- 084 down: 回滚套餐定价变更

ALTER TABLE subscription_plans
    DROP COLUMN IF EXISTS price_yearly;

-- 恢复原始定价
UPDATE subscription_plans
SET price_monthly = 0, point_quota = 500,
    features = '{"editorial_mode":false,"max_agents":1,"memory_enabled":false,"custom_styles":false,"kb_doc_limit":0,"max_word_count":800,"daily_write_limit":3}'::jsonb,
    is_active = TRUE, is_popular = FALSE, sort_order = 1
WHERE name = 'free';

UPDATE subscription_plans
SET price_monthly = 39, point_quota = 10000,
    features = '{"editorial_mode":false,"max_agents":1,"memory_enabled":true,"custom_styles":true,"kb_doc_limit":50,"max_word_count":3000,"daily_write_limit":0}'::jsonb,
    is_active = TRUE, is_popular = TRUE, sort_order = 2
WHERE name = 'creator';

UPDATE subscription_plans
SET price_monthly = 99, point_quota = 30000,
    features = '{"editorial_mode":true,"max_agents":6,"memory_enabled":true,"custom_styles":true,"kb_doc_limit":500,"max_word_count":0,"daily_write_limit":0,"graphrag":true,"mcp_tools":10}'::jsonb,
    is_active = TRUE, is_popular = FALSE, sort_order = 3
WHERE name = 'pro';

UPDATE subscription_plans
SET price_monthly = 399, point_quota = 100000,
    features = '{"editorial_mode":true,"max_agents":99,"memory_enabled":true,"custom_styles":true,"kb_doc_limit":9999,"max_word_count":0,"daily_write_limit":0,"graphrag":true,"mcp_tools":99,"rbac":true,"api_access":true}'::jsonb,
    is_active = TRUE, is_popular = FALSE, sort_order = 4
WHERE name = 'enterprise';

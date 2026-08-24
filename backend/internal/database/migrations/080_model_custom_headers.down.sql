-- 080: Remove custom_headers from model_configs

ALTER TABLE model_configs DROP COLUMN IF EXISTS custom_headers;

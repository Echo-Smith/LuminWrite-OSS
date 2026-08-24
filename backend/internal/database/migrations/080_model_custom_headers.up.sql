-- 080: Model config custom headers
-- 为 model_configs 增加 custom_headers 字段，支持自定义 HTTP 请求头
-- 用于接入需要额外 header 的 OpenAI 兼容中转站/供应商

ALTER TABLE model_configs ADD COLUMN IF NOT EXISTS custom_headers JSONB NOT NULL DEFAULT '{}'::jsonb;

COMMENT ON COLUMN model_configs.custom_headers IS '自定义 HTTP 请求头，JSON 对象，如 {"X-API-Key": "xxx", "X-Request-Source": "luminbuddy"}';

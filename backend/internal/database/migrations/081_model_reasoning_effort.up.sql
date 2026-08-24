-- 081: Add reasoning_effort column to model_configs
-- 不同模型支持的思考深度不同，需要前端可配。
-- 有效值: "low" | "medium" | "high" | "max"
-- 为已有 deepseek 模型设置默认值 "high"

ALTER TABLE model_configs ADD COLUMN IF NOT EXISTS reasoning_effort VARCHAR(16) NOT NULL DEFAULT 'high';

COMMENT ON COLUMN model_configs.reasoning_effort IS '思考深度: low | medium | high | max (仅 thinking=true 时生效)';

-- 为已有 deepseek 模型设置合理的默认值
UPDATE model_configs SET reasoning_effort = 'high' WHERE provider = 'deepseek' AND model_name = 'deepseek-v4-flash';
UPDATE model_configs SET reasoning_effort = 'max' WHERE provider = 'deepseek' AND model_name = 'deepseek-v4-pro';

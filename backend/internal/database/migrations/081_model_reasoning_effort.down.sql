-- 081 down: Remove reasoning_effort column

ALTER TABLE model_configs DROP COLUMN IF EXISTS reasoning_effort;

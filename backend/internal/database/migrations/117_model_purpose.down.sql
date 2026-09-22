-- Rollback: Remove model purpose field

DROP INDEX IF EXISTS idx_model_configs_purpose_default;
DROP INDEX IF EXISTS idx_model_configs_purpose;

-- Restore original default index
CREATE UNIQUE INDEX IF NOT EXISTS idx_model_configs_default 
  ON model_configs(provider) WHERE is_default = TRUE;

ALTER TABLE model_configs DROP COLUMN IF EXISTS purpose;

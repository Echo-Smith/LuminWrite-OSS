-- 117: Add model purpose field to model_configs table
-- Purpose types: generation (default), verification, embedding

ALTER TABLE model_configs
ADD COLUMN purpose VARCHAR(32) NOT NULL DEFAULT 'generation';

-- Create index for purpose-based queries
CREATE INDEX IF NOT EXISTS idx_model_configs_purpose ON model_configs(purpose);

-- Allow multiple defaults per purpose (e.g., one default generation model, one default verification model)
DROP INDEX IF EXISTS idx_model_configs_default;
CREATE UNIQUE INDEX IF NOT EXISTS idx_model_configs_purpose_default 
  ON model_configs(provider, purpose) WHERE is_default = TRUE;

COMMENT ON COLUMN model_configs.purpose IS 'Model purpose: generation (content creation), verification (claim checking), embedding (vector search)';

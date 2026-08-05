-- 052: Artifact Versioning — checksum, provenance chain, retention policy
ALTER TABLE editorial_artifacts ADD COLUMN IF NOT EXISTS checksum VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE editorial_artifacts ADD COLUMN IF NOT EXISTS provenance JSONB NOT NULL DEFAULT '{}';
ALTER TABLE editorial_artifacts ADD COLUMN IF NOT EXISTS retention_until TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_artifacts_checksum ON editorial_artifacts (checksum) WHERE checksum != '';

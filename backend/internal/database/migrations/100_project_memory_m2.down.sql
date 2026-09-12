-- Remove ProjectMemory M2 tables. Immutable-columns triggers are dropped
-- before their tables.
DROP TRIGGER IF EXISTS trg_project_entities_immutable ON project_entities;
DROP TABLE IF EXISTS project_entities;
DROP TABLE IF EXISTS project_claim_evidence;
DROP TRIGGER IF EXISTS trg_project_claims_immutable ON project_claims;
DROP TABLE IF EXISTS project_claims;

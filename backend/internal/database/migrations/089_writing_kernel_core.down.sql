ALTER TABLE writing_runs DROP CONSTRAINT IF EXISTS fk_writing_runs_active_plan;
ALTER TABLE writing_documents DROP CONSTRAINT IF EXISTS fk_writing_documents_current_version;

DROP TABLE IF EXISTS writing_run_plans;
DROP TABLE IF EXISTS writing_runs;
DROP TABLE IF EXISTS writing_document_versions;
DROP TABLE IF EXISTS writing_contracts;
DROP TABLE IF EXISTS writing_documents;
DROP FUNCTION IF EXISTS writing_reject_immutable_columns();

ALTER TABLE writing_artifacts DROP CONSTRAINT IF EXISTS fk_writing_artifact_quality;
ALTER TABLE writing_document_versions DROP CONSTRAINT IF EXISTS fk_writing_document_version_quality;

DROP TABLE IF EXISTS writing_decisions;
DROP TABLE IF EXISTS writing_artifact_edges;
DROP TABLE IF EXISTS writing_artifacts;
DROP TABLE IF EXISTS writing_quality_reports;
DROP FUNCTION IF EXISTS writing_reject_artifact_edge_mutation();

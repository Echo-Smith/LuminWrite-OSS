DROP TRIGGER IF EXISTS trg_writing_completed_run_gate ON writing_runs;
DROP TRIGGER IF EXISTS trg_writing_run_projection_from_run ON writing_runs;
DROP TRIGGER IF EXISTS trg_writing_run_projection_from_event ON writing_run_events;
DROP TRIGGER IF EXISTS trg_writing_artifact_commit_gate ON writing_artifacts;
DROP TRIGGER IF EXISTS trg_writing_document_quality_gate ON writing_document_versions;
DROP TRIGGER IF EXISTS trg_writing_snapshot_quality_binding ON writing_snapshots;
DROP TRIGGER IF EXISTS trg_writing_quality_delivery_gate ON writing_quality_reports;

ALTER TABLE writing_runs DROP CONSTRAINT IF EXISTS fk_writing_run_last_snapshot;
ALTER TABLE writing_artifacts DROP CONSTRAINT IF EXISTS fk_writing_artifact_snapshot;
ALTER TABLE writing_document_versions DROP CONSTRAINT IF EXISTS fk_writing_document_version_snapshot;
ALTER TABLE writing_quality_reports DROP CONSTRAINT IF EXISTS fk_writing_quality_snapshot;
ALTER TABLE writing_snapshots DROP CONSTRAINT IF EXISTS fk_writing_snapshot_quality;
ALTER TABLE writing_artifacts DROP CONSTRAINT IF EXISTS fk_writing_artifact_attempt;

DROP TABLE IF EXISTS writing_snapshots;
DROP TABLE IF EXISTS writing_run_events;
DROP TABLE IF EXISTS writing_node_attempts;

DROP FUNCTION IF EXISTS writing_enforce_completed_run_gate();
DROP FUNCTION IF EXISTS writing_enforce_run_projection();
DROP FUNCTION IF EXISTS writing_enforce_artifact_commit_gate();
DROP FUNCTION IF EXISTS writing_enforce_document_quality_gate();
DROP FUNCTION IF EXISTS writing_enforce_snapshot_quality_binding();
DROP FUNCTION IF EXISTS writing_enforce_quality_delivery_gate();
DROP FUNCTION IF EXISTS writing_reject_run_event_mutation();

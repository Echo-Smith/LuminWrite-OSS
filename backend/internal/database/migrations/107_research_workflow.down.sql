-- 107 down: research-review workflow tables and CHECK extensions.
--
-- The two new tables are dropped with the migration. User evidence is NOT
-- torn down by design (design.md §9): the immutable evidence pack / decision
-- artifacts live in writing_artifacts + writing_artifact_contents, which this
-- down migration does NOT remove — dropping the two ledger tables here only
-- loses sub-task progress rows and the gate bookkeeping, while the durable
-- artifacts remain readable. A production rollback should prefer closing the
-- feature entry over running this down migration at all.

DROP INDEX IF EXISTS idx_writing_gate_decisions_owner;
DROP INDEX IF EXISTS idx_writing_gate_decisions_run;
DROP TABLE IF EXISTS writing_gate_decisions;

DROP INDEX IF EXISTS idx_writing_research_tasks_lease;
DROP INDEX IF EXISTS idx_writing_research_tasks_node;
DROP INDEX IF EXISTS idx_writing_research_tasks_owner_run;
DROP TABLE IF EXISTS writing_research_tasks;

-- Restore the pre-107 CHECK constraints exactly as 089/090/095 defined them.
ALTER TABLE writing_artifacts
    DROP CONSTRAINT chk_writing_artifact_type;

ALTER TABLE writing_artifacts
    ADD CONSTRAINT chk_writing_artifact_type CHECK (artifact_type IN (
        'contract', 'materials', 'brief', 'source_pack', 'research_note',
        'claim_map', 'outline', 'section_draft', 'full_draft',
        'review_report', 'revision_set', 'quality_report',
        'evidence_report', 'fact_report'
    ));

ALTER TABLE writing_run_events
    DROP CONSTRAINT chk_writing_event_type,
    DROP CONSTRAINT chk_writing_event_entity;

ALTER TABLE writing_run_events
    ADD CONSTRAINT chk_writing_event_type CHECK (event_type IN (
        'run.planned', 'run.started', 'run.paused', 'run.resumed', 'run.cancelled',
        'run.completed', 'run.failed', 'node.started', 'node.completed', 'node.failed',
        'artifact.created', 'quality.updated', 'snapshot.created', 'document.committed',
        'run.transitioned', 'run.transition_rejected', 'node.paused', 'node.cancelled',
        'runtime.route_decided', 'runtime.execution_observed', 'runtime.shadow_compared'
    )),
    ADD CONSTRAINT chk_writing_event_entity CHECK (entity_kind IN (
        'run', 'node', 'artifact', 'document_version', 'quality_report',
        'snapshot', 'rollout_evidence'
    ));

ALTER TABLE writing_contracts
    DROP CONSTRAINT chk_writing_contract_schema;

ALTER TABLE writing_contracts
    ADD CONSTRAINT chk_writing_contract_schema CHECK (schema_version = 'lcp/1.0');

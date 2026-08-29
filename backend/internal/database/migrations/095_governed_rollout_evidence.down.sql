-- The governed runtime records audit evidence in this table, and the design
-- forbids rollbacks from rewriting audit history. This downgrade therefore
-- refuses to run while governed evidence exists. Note the append-only
-- trigger on writing_run_events also forbids deleting the rows outright:
-- archival must go through the sanctioned evidence export path first.
DO $downgrade_guard$
DECLARE evidence_rows bigint;
BEGIN
    SELECT count(*) INTO evidence_rows
      FROM writing_run_events
     WHERE event_type IN ('runtime.route_decided', 'runtime.execution_observed', 'runtime.shadow_compared');
    IF evidence_rows > 0 THEN
        RAISE EXCEPTION 'cannot downgrade migration 095: % governed runtime evidence rows are still present; archive and remove them first', evidence_rows;
    END IF;
END
$downgrade_guard$;

ALTER TABLE writing_run_events
    DROP CONSTRAINT chk_writing_event_type,
    DROP CONSTRAINT chk_writing_event_node_attempt,
    DROP CONSTRAINT chk_writing_event_entity;

ALTER TABLE writing_run_events
    ADD CONSTRAINT chk_writing_event_type CHECK (event_type IN (
        'run.planned', 'run.started', 'run.paused', 'run.resumed', 'run.cancelled',
        'run.completed', 'run.failed', 'node.started', 'node.completed', 'node.failed',
        'artifact.created', 'quality.updated', 'snapshot.created', 'document.committed',
        'run.transitioned', 'run.transition_rejected', 'node.paused', 'node.cancelled'
    )),
    ADD CONSTRAINT chk_writing_event_node_attempt CHECK (
        (event_type IN (
            'node.started', 'node.completed', 'node.failed', 'node.paused',
            'node.cancelled', 'artifact.created'
        ) AND node_id IS NOT NULL AND attempt >= 1 AND idempotency_key IS NOT NULL)
        OR
        (event_type IN ('run.transitioned', 'run.transition_rejected')
            AND node_id IS NULL AND attempt IS NULL AND idempotency_key IS NOT NULL)
        OR
        (event_type NOT IN (
            'node.started', 'node.completed', 'node.failed', 'node.paused',
            'node.cancelled', 'artifact.created', 'run.transitioned',
            'run.transition_rejected'
        ) AND node_id IS NULL AND attempt IS NULL AND idempotency_key IS NULL)
    ),
    ADD CONSTRAINT chk_writing_event_entity CHECK (entity_kind IN (
        'run', 'node', 'artifact', 'document_version', 'quality_report', 'snapshot'
    ));

ALTER TABLE writing_run_events
    DROP CONSTRAINT chk_writing_event_type,
    DROP CONSTRAINT chk_writing_event_node_attempt,
    DROP CONSTRAINT chk_writing_event_entity;

ALTER TABLE writing_run_events
    ADD CONSTRAINT chk_writing_event_type CHECK (event_type IN (
        'run.planned', 'run.started', 'run.paused', 'run.resumed', 'run.cancelled',
        'run.completed', 'run.failed', 'node.started', 'node.completed', 'node.failed',
        'artifact.created', 'quality.updated', 'snapshot.created', 'document.committed',
        'run.transitioned', 'run.transition_rejected', 'node.paused', 'node.cancelled',
        'runtime.route_decided', 'runtime.execution_observed', 'runtime.shadow_compared'
    )),
    ADD CONSTRAINT chk_writing_event_node_attempt CHECK (
        (event_type IN (
            'node.started', 'node.completed', 'node.failed', 'node.paused',
            'node.cancelled', 'artifact.created', 'runtime.route_decided',
            'runtime.execution_observed', 'runtime.shadow_compared'
        ) AND node_id IS NOT NULL AND attempt >= 1 AND idempotency_key IS NOT NULL)
        OR
        (event_type IN ('run.transitioned', 'run.transition_rejected')
            AND node_id IS NULL AND attempt IS NULL AND idempotency_key IS NOT NULL)
        OR
        (event_type NOT IN (
            'node.started', 'node.completed', 'node.failed', 'node.paused',
            'node.cancelled', 'artifact.created', 'runtime.route_decided',
            'runtime.execution_observed', 'runtime.shadow_compared',
            'run.transitioned', 'run.transition_rejected'
        ) AND node_id IS NULL AND attempt IS NULL AND idempotency_key IS NULL)
    ),
    ADD CONSTRAINT chk_writing_event_entity CHECK (entity_kind IN (
        'run', 'node', 'artifact', 'document_version', 'quality_report',
        'snapshot', 'rollout_evidence'
    ));

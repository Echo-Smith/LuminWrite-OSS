ALTER TABLE writing_run_events
    DROP CONSTRAINT chk_writing_event_type;

ALTER TABLE writing_run_events
    ADD CONSTRAINT chk_writing_event_type CHECK (event_type IN (
        'run.planned', 'run.started', 'run.paused', 'run.resumed', 'run.cancelled',
        'run.completed', 'run.failed', 'node.started', 'node.completed', 'node.failed',
        'artifact.created', 'quality.updated', 'snapshot.created',
        'run.transitioned', 'run.transition_rejected', 'node.paused', 'node.cancelled'
    ));

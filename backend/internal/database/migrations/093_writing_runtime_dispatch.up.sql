-- 093: Close the plan/runtime vocabulary and persist governed transitions.

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
    DROP CONSTRAINT chk_writing_event_node_attempt;

ALTER TABLE writing_run_events
    ADD CONSTRAINT chk_writing_event_type CHECK (event_type IN (
        'run.planned', 'run.started', 'run.paused', 'run.resumed', 'run.cancelled',
        'run.completed', 'run.failed', 'run.transitioned', 'run.transition_rejected',
        'node.started', 'node.completed', 'node.failed', 'node.paused',
        'node.cancelled', 'artifact.created', 'quality.updated', 'snapshot.created'
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
    );

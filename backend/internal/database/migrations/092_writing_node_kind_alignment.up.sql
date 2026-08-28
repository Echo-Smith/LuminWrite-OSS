-- 092: Keep persisted NodeAttempt kinds aligned with the typed writing-plan IR.

ALTER TABLE writing_node_attempts
    DROP CONSTRAINT IF EXISTS chk_writing_attempt_kind;

ALTER TABLE writing_node_attempts
    ADD CONSTRAINT chk_writing_attempt_kind CHECK (node_kind IN (
        'sequence', 'parallel', 'map', 'reduce', 'condition', 'retry',
        'refine', 'human_gate', 'validate', 'fallback', 'action'
    ));

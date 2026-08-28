ALTER TABLE writing_node_attempts
    DROP CONSTRAINT IF EXISTS chk_writing_attempt_kind;

ALTER TABLE writing_node_attempts
    ADD CONSTRAINT chk_writing_attempt_kind CHECK (node_kind IN (
        'action', 'map', 'parallel', 'branch', 'validate', 'retry', 'refine'
    ));

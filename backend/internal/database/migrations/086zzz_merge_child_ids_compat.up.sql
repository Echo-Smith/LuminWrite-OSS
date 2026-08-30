-- 086zzz: Preserve referential integrity while preparing 087 child tables.
--
-- The editorial child tables use UUID task_id, while 087 renames that column
-- to trace_id and targets agent_traces.trace_id (VARCHAR). Convert losslessly
-- before 087, retaining temporary FKs through a generated text identity.

ALTER TABLE editorial_tasks
    ADD COLUMN IF NOT EXISTS legacy_trace_id VARCHAR(64)
    GENERATED ALWAYS AS (id::TEXT) STORED UNIQUE;

ALTER TABLE editorial_artifacts
    DROP CONSTRAINT IF EXISTS editorial_artifacts_task_id_fkey;
ALTER TABLE editorial_artifacts
    ALTER COLUMN task_id TYPE VARCHAR(64) USING task_id::TEXT;
ALTER TABLE editorial_artifacts
    ADD CONSTRAINT editorial_artifacts_task_id_fkey
    FOREIGN KEY (task_id) REFERENCES editorial_tasks(legacy_trace_id) ON DELETE CASCADE;

ALTER TABLE editorial_decisions
    DROP CONSTRAINT IF EXISTS editorial_decisions_task_id_fkey;
ALTER TABLE editorial_decisions
    ALTER COLUMN task_id TYPE VARCHAR(64) USING task_id::TEXT;
ALTER TABLE editorial_decisions
    ADD CONSTRAINT editorial_decisions_task_id_fkey
    FOREIGN KEY (task_id) REFERENCES editorial_tasks(legacy_trace_id) ON DELETE CASCADE;

ALTER TABLE editorial_agent_run_events
    DROP CONSTRAINT IF EXISTS editorial_agent_run_events_task_id_fkey;
ALTER TABLE editorial_agent_run_events
    ALTER COLUMN task_id TYPE VARCHAR(64) USING task_id::TEXT;
ALTER TABLE editorial_agent_run_events
    ADD CONSTRAINT editorial_agent_run_events_task_id_fkey
    FOREIGN KEY (task_id) REFERENCES editorial_tasks(legacy_trace_id) ON DELETE CASCADE;

ALTER TABLE editorial_agent_leases
    DROP CONSTRAINT IF EXISTS editorial_agent_leases_task_id_fkey;
ALTER TABLE editorial_agent_leases
    ALTER COLUMN task_id TYPE VARCHAR(64) USING task_id::TEXT;
ALTER TABLE editorial_agent_leases
    ADD CONSTRAINT editorial_agent_leases_task_id_fkey
    FOREIGN KEY (task_id) REFERENCES editorial_tasks(legacy_trace_id) ON DELETE CASCADE;

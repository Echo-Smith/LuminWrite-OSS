ALTER TABLE editorial_artifacts
    DROP CONSTRAINT IF EXISTS editorial_artifacts_task_id_fkey;
ALTER TABLE editorial_artifacts
    ALTER COLUMN task_id TYPE UUID USING task_id::UUID;
ALTER TABLE editorial_artifacts
    ADD CONSTRAINT editorial_artifacts_task_id_fkey
    FOREIGN KEY (task_id) REFERENCES editorial_tasks(id) ON DELETE CASCADE;

ALTER TABLE editorial_decisions
    DROP CONSTRAINT IF EXISTS editorial_decisions_task_id_fkey;
ALTER TABLE editorial_decisions
    ALTER COLUMN task_id TYPE UUID USING task_id::UUID;
ALTER TABLE editorial_decisions
    ADD CONSTRAINT editorial_decisions_task_id_fkey
    FOREIGN KEY (task_id) REFERENCES editorial_tasks(id) ON DELETE CASCADE;

ALTER TABLE editorial_agent_run_events
    DROP CONSTRAINT IF EXISTS editorial_agent_run_events_task_id_fkey;
ALTER TABLE editorial_agent_run_events
    ALTER COLUMN task_id TYPE UUID USING task_id::UUID;
ALTER TABLE editorial_agent_run_events
    ADD CONSTRAINT editorial_agent_run_events_task_id_fkey
    FOREIGN KEY (task_id) REFERENCES editorial_tasks(id) ON DELETE CASCADE;

ALTER TABLE editorial_agent_leases
    DROP CONSTRAINT IF EXISTS editorial_agent_leases_task_id_fkey;
ALTER TABLE editorial_agent_leases
    ALTER COLUMN task_id TYPE UUID USING task_id::UUID;
ALTER TABLE editorial_agent_leases
    ADD CONSTRAINT editorial_agent_leases_task_id_fkey
    FOREIGN KEY (task_id) REFERENCES editorial_tasks(id) ON DELETE CASCADE;

ALTER TABLE editorial_tasks
    DROP COLUMN IF EXISTS legacy_trace_id;

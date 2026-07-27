-- 040 down: Re-add the unique constraint (not recommended, but for completeness)
ALTER TABLE editorial_agent_leases
    ADD CONSTRAINT uk_active_lease UNIQUE (task_id, agent_role);

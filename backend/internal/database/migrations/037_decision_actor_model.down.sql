-- Reverse migration 037

-- Drop agent leases table
DROP TABLE IF EXISTS editorial_agent_leases;

-- Remove decision target status columns
ALTER TABLE editorial_decisions
    DROP COLUMN IF EXISTS approve_target_status;
ALTER TABLE editorial_decisions
    DROP COLUMN IF EXISTS reject_target_status;

-- Remove actor model columns and constraints
ALTER TABLE editorial_decisions
    DROP CONSTRAINT IF EXISTS chk_actor_human_user_id;
ALTER TABLE editorial_decisions
    DROP CONSTRAINT IF EXISTS chk_actor_agent_role;
ALTER TABLE editorial_decisions
    DROP COLUMN IF EXISTS actor_label;
ALTER TABLE editorial_decisions
    DROP COLUMN IF EXISTS actor_role;
ALTER TABLE editorial_decisions
    DROP COLUMN IF EXISTS actor_user_id;
ALTER TABLE editorial_decisions
    DROP COLUMN IF EXISTS actor_type;

-- P0-2: Fix UUID columns that store non-UUID identifiers (agent roles, "system", "experiment", etc.)
-- These columns were originally UUID REFERENCES users(id) but code writes string identifiers.

-- editorial_decisions.decided_by: stores "system", "review_agent", "experiment", user UUIDs, etc.
ALTER TABLE editorial_decisions
    ALTER COLUMN decided_by TYPE VARCHAR(64) USING decided_by::text;
ALTER TABLE editorial_decisions
    DROP CONSTRAINT IF EXISTS editorial_decisions_decided_by_fkey;

-- editorial_tasks.owner_id: stores user UUID or "experiment"
ALTER TABLE editorial_tasks
    ALTER COLUMN owner_id TYPE VARCHAR(64) USING owner_id::text;
ALTER TABLE editorial_tasks
    DROP CONSTRAINT IF EXISTS editorial_tasks_owner_id_fkey;

-- editorial_tasks.created_by: same as owner_id
ALTER TABLE editorial_tasks
    ALTER COLUMN created_by TYPE VARCHAR(64) USING created_by::text;
ALTER TABLE editorial_tasks
    DROP CONSTRAINT IF EXISTS editorial_tasks_created_by_fkey;

-- editorial_experiments.created_by: stores user UUID or "experiment"
ALTER TABLE editorial_experiments
    ALTER COLUMN created_by TYPE VARCHAR(64) USING created_by::text;

-- editorial_artifacts.reviewed_by: already VARCHAR(64) in migration 032,
-- but databases that ran an older version of 032 may still have UUID.
-- This ALTER is idempotent (safe to run on already-VARCHAR columns).
ALTER TABLE editorial_artifacts
    ALTER COLUMN reviewed_by TYPE VARCHAR(64) USING reviewed_by::text;

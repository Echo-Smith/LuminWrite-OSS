-- Reverse the type changes (columns go back to their original types if needed)
-- Note: This down migration is best-effort. If non-UUID strings were inserted,
-- casting back to UUID will fail. In practice, down migrations are rarely run on production data.

ALTER TABLE editorial_artifacts
    ALTER COLUMN reviewed_by TYPE VARCHAR(64) USING reviewed_by::text;

ALTER TABLE editorial_experiments
    ALTER COLUMN created_by TYPE UUID USING created_by::uuid;

ALTER TABLE editorial_tasks
    ALTER COLUMN created_by TYPE UUID USING created_by::uuid;

ALTER TABLE editorial_tasks
    ALTER COLUMN owner_id TYPE UUID USING owner_id::uuid;

ALTER TABLE editorial_decisions
    ALTER COLUMN decided_by TYPE UUID USING decided_by::uuid;

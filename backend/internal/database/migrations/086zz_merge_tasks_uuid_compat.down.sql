ALTER TABLE editorial_tasks
    DROP CONSTRAINT IF EXISTS editorial_tasks_owner_id_fkey,
    DROP CONSTRAINT IF EXISTS editorial_tasks_created_by_fkey;

ALTER TABLE editorial_tasks
    ALTER COLUMN owner_id TYPE VARCHAR(64) USING owner_id::TEXT,
    ALTER COLUMN created_by TYPE VARCHAR(64) USING created_by::TEXT;

UPDATE editorial_tasks AS task
SET owner_id = legacy.owner_id,
    created_by = legacy.created_by
FROM editorial_task_actor_legacy AS legacy
WHERE legacy.task_id = task.id;

DROP TABLE IF EXISTS editorial_task_actor_legacy;

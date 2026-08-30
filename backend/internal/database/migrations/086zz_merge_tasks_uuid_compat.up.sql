-- 086zz: Normalize legacy string actors before the untouched 087 merge.
--
-- Migration 036 widened owner_id/created_by to VARCHAR for agent actors while
-- 087 merges those fields into UUID columns. Preserve the exact legacy values
-- in a durable recovery table before converting representable identities.

CREATE TABLE IF NOT EXISTS editorial_task_actor_legacy (
    task_id UUID PRIMARY KEY,
    owner_id TEXT,
    created_by TEXT,
    captured_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO editorial_task_actor_legacy (task_id, owner_id, created_by)
SELECT id, owner_id, created_by
FROM editorial_tasks
ON CONFLICT (task_id) DO NOTHING;

UPDATE editorial_tasks AS task
SET tags = array_append(
        COALESCE(task.tags, '{}'::text[]),
        'legacy-owner:' || task.owner_id
    ),
    owner_id = NULL
WHERE task.owner_id IS NOT NULL
  AND (
      task.owner_id !~* '^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
      OR NOT EXISTS (SELECT 1 FROM users WHERE users.id::TEXT = task.owner_id)
  );

UPDATE editorial_tasks AS task
SET tags = array_append(
        COALESCE(task.tags, '{}'::text[]),
        'legacy-created-by:' || task.created_by
    ),
    created_by = NULL
WHERE task.created_by IS NOT NULL
  AND task.created_by !~* '^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$';

ALTER TABLE editorial_tasks
    ALTER COLUMN owner_id TYPE UUID USING owner_id::UUID,
    ALTER COLUMN created_by TYPE UUID USING created_by::UUID;

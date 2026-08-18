-- 062: Update yinyue KB auto-import max_pages from 1 to 10
-- This was previously changed in-place in 047 (which caused checksum mismatch on existing deployments).
-- Moving it to a separate migration so 047's checksum stays stable.

UPDATE cron_jobs
SET task_config = jsonb_set(task_config, '{max_pages}', '10'::jsonb),
    updated_at = NOW()
WHERE task_type = 'kb_auto_import'
  AND task_config->>'kb_id' = 'yinyue';

-- Revert max_pages back to 1
UPDATE cron_jobs
SET task_config = jsonb_set(task_config, '{max_pages}', '1'::jsonb),
    updated_at = NOW()
WHERE task_type = 'kb_auto_import'
  AND task_config->>'kb_id' = 'yinyue';

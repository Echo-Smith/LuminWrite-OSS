-- 047 down: 回滚预设评论风格 KB seed

-- 删除 cron job
DELETE FROM cron_jobs WHERE task_type = 'kb_auto_import' AND task_config->>'kb_id' = 'yinyue';

-- 注意：不删除已导入的知识库文档和 KB 记录，避免数据丢失
-- 如需完全回滚，手动执行：
-- DELETE FROM knowledge_base WHERE kb_id = 'yinyue';
-- DELETE FROM knowledge_bases WHERE id = 'yinyue';

-- 047: Seed 预设评论风格 knowledge base + daily auto-import cron job

-- ─── 1. 创建「预设评论风格」知识库 ──────────────────────────
INSERT INTO knowledge_bases (id, name, description)
VALUES ('yinyue', '预设评论风格', '杭州网「预设评论风格」时评专栏文章集 — 用于写作风格参考与知识检索')
ON CONFLICT (id) DO NOTHING;

-- ─── 2. 注册每日自动录入 cron job ──────────────────────
-- 每天 08:00 自动抓取栏目页最新文章并导入知识库
INSERT INTO cron_jobs (id, name, description, schedule, task_type, task_config, is_active, created_at, updated_at)
SELECT
    gen_random_uuid(),
    '预设评论风格每日自动录入',
    '每天抓取杭州网预设评论风格栏目最新文章，自动导入到 yinyue 知识库',
    '0 8 * * *',
    'kb_auto_import',
    jsonb_build_object(
        'url', 'https://www.hangzhou.com.cn/pinglun/node_152931.htm',
        'kb_id', 'yinyue',
        'max_pages', 10
    ),
    true,
    NOW(),
    NOW()
WHERE NOT EXISTS (
    SELECT 1 FROM cron_jobs
    WHERE task_type = 'kb_auto_import'
      AND task_config->>'kb_id' = 'yinyue'
);

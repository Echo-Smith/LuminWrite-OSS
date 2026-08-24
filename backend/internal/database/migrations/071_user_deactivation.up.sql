-- 071: 用户注销功能 — 修复 FK 约束以支持硬删除
--
-- 策略：
-- 1. agent_traces → ON DELETE CASCADE（用户注销时删除所有写作历史）
-- 2. feedback_segments → ON DELETE CASCADE（用户注销时删除所有反馈）
-- 3. article_versions → ON DELETE CASCADE（用户注销时删除历史版本）
-- 4. editorial_tasks.owner_id / created_by → ON DELETE SET NULL
--    （编辑部任务可能涉及多人，删除用户时保留任务但清除引用）
--    注意：migration 036 将 owner_id 和 created_by 从 UUID 改为 VARCHAR(64)，
--    只有列类型仍为 UUID 时才加 FK，VARCHAR 列跳过（无法引用 UUID 主键）
-- 5. editorial_decisions.decided_by → ON DELETE SET NULL
--    （决策记录是审计数据，不应随用户删除而丢失）
-- 6. user_preferences.user_id 是 VARCHAR(64) 而非 FK，手动清理
-- 7. passkey_credentials.user_id 是 VARCHAR(64) 而非 FK，手动清理
-- 8. user_weknora_mapping.user_id 是 VARCHAR(64) 而非 FK，手动清理
-- 9. user_materials.user_id 是 VARCHAR(64) 而非 FK，手动清理

-- ─── 1. agent_traces: 加 ON DELETE CASCADE ──────────────────
-- 原 FK: user_id UUID REFERENCES users(id) (无级联)
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE table_name = 'agent_traces' AND constraint_name = 'agent_traces_user_id_fkey'
    ) THEN
        ALTER TABLE agent_traces DROP CONSTRAINT agent_traces_user_id_fkey;
    END IF;
END $$;
ALTER TABLE agent_traces
    ADD CONSTRAINT agent_traces_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

-- ─── 2. feedback_segments: 加 ON DELETE CASCADE ─────────────
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE table_name = 'feedback_segments' AND constraint_name = 'feedback_segments_user_id_fkey'
    ) THEN
        ALTER TABLE feedback_segments DROP CONSTRAINT feedback_segments_user_id_fkey;
    END IF;
END $$;
ALTER TABLE feedback_segments
    ADD CONSTRAINT feedback_segments_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

-- ─── 3. article_versions: 加 ON DELETE CASCADE ─────────────
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE table_name = 'article_versions' AND constraint_name = 'article_versions_user_id_fkey'
    ) THEN
        ALTER TABLE article_versions DROP CONSTRAINT article_versions_user_id_fkey;
    END IF;
END $$;
ALTER TABLE article_versions
    ADD CONSTRAINT article_versions_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

-- ─── 4. editorial_tasks: 改 ON DELETE SET NULL ──────────────
-- owner_id (migration 036 changed to VARCHAR(64), only add FK if still UUID)
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE table_name = 'editorial_tasks' AND constraint_name = 'editorial_tasks_owner_id_fkey'
    ) THEN
        ALTER TABLE editorial_tasks DROP CONSTRAINT editorial_tasks_owner_id_fkey;
    END IF;
END $$;
-- Only add FK if column type is UUID (migration 036 changed to VARCHAR)
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'editorial_tasks' AND column_name = 'owner_id'
        AND data_type = 'uuid'
    ) THEN
        ALTER TABLE editorial_tasks
            ADD CONSTRAINT editorial_tasks_owner_id_fkey
            FOREIGN KEY (owner_id) REFERENCES users(id) ON DELETE SET NULL;
    END IF;
END $$;

-- created_by (migration 036 already changed to VARCHAR, skip if not UUID FK)
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE table_name = 'editorial_tasks' AND constraint_name = 'editorial_tasks_created_by_fkey'
    ) THEN
        ALTER TABLE editorial_tasks DROP CONSTRAINT editorial_tasks_created_by_fkey;
    END IF;
END $$;
-- Only add FK if column type is UUID (migration 036 changed some to VARCHAR)
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'editorial_tasks' AND column_name = 'created_by'
        AND data_type = 'uuid'
    ) THEN
        ALTER TABLE editorial_tasks
            ADD CONSTRAINT editorial_tasks_created_by_fkey
            FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE SET NULL;
    END IF;
END $$;

-- ─── 5. editorial_decisions: 改 ON DELETE SET NULL ─────────
-- (migration 036 already changed decided_by to VARCHAR, so FK may not exist)
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE table_name = 'editorial_decisions' AND constraint_name = 'editorial_decisions_decided_by_fkey'
    ) THEN
        ALTER TABLE editorial_decisions DROP CONSTRAINT editorial_decisions_decided_by_fkey;
        ALTER TABLE editorial_decisions
            ADD CONSTRAINT editorial_decisions_decided_by_fkey
            FOREIGN KEY (decided_by) REFERENCES users(id) ON DELETE SET NULL;
    END IF;
END $$;

-- ─── 6. user_subscriptions: 确保级联删除 ───────────────────
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE table_name = 'user_subscriptions' AND constraint_name = 'user_subscriptions_user_id_fkey'
    ) THEN
        ALTER TABLE user_subscriptions
            ADD CONSTRAINT user_subscriptions_user_id_fkey
            FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;
    END IF;
END $$;

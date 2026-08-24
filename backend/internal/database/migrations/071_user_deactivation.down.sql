-- 071 down: 回滚 FK 约束变更

-- agent_traces: 回退为无级联
ALTER TABLE agent_traces DROP CONSTRAINT IF EXISTS agent_traces_user_id_fkey;
ALTER TABLE agent_traces ADD CONSTRAINT agent_traces_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users(id);

-- feedback_segments: 回退为无级联
ALTER TABLE feedback_segments DROP CONSTRAINT IF EXISTS feedback_segments_user_id_fkey;
ALTER TABLE feedback_segments ADD CONSTRAINT feedback_segments_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users(id);

-- article_versions: 回退为无级联
ALTER TABLE article_versions DROP CONSTRAINT IF EXISTS article_versions_user_id_fkey;
ALTER TABLE article_versions ADD CONSTRAINT article_versions_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users(id);

-- editorial_tasks: 回退
ALTER TABLE editorial_tasks DROP CONSTRAINT IF EXISTS editorial_tasks_owner_id_fkey;
ALTER TABLE editorial_tasks ADD CONSTRAINT editorial_tasks_owner_id_fkey
    FOREIGN KEY (owner_id) REFERENCES users(id);
ALTER TABLE editorial_tasks DROP CONSTRAINT IF EXISTS editorial_tasks_created_by_fkey;

-- editorial_decisions: 回退 (列类型可能已是 VARCHAR，不重建 FK)
ALTER TABLE editorial_decisions DROP CONSTRAINT IF EXISTS editorial_decisions_decided_by_fkey;

-- user_subscriptions: 回退
ALTER TABLE user_subscriptions DROP CONSTRAINT IF EXISTS user_subscriptions_user_id_fkey;

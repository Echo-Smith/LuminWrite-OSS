-- 030_working_summaries.up.sql

-- 工作记忆摘要表 — 持久化每次执行的增量摘要
-- 跨请求继承工作记忆，使新请求能参考上一轮的执行上下文
CREATE TABLE IF NOT EXISTS working_summaries (
    conversation_id  VARCHAR(64) NOT NULL,
    trace_id         VARCHAR(64),
    summary          JSONB NOT NULL,
    last_updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (conversation_id)
);

-- 按 conversation_id 快速查找
CREATE INDEX IF NOT EXISTS idx_working_summaries_conversation
    ON working_summaries (conversation_id);

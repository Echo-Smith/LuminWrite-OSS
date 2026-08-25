-- 088: agent_traces 新增 plan_json 列，持久化编辑部 DAG 工作流计划
--
-- Planner 生成的 plan（agents + workflow spec + rationale）之前只缓存在
-- DAGExecutor 的内存 planCache 中，DAG 执行完成后被清除。
-- 历史会话无法恢复 DAG 视图。本迁移新增 plan_json JSONB 列持久化 plan。

ALTER TABLE agent_traces ADD COLUMN IF NOT EXISTS plan_json JSONB;

-- 索引：便于按 mode + plan_json 是否存在过滤编辑部会话
CREATE INDEX IF NOT EXISTS idx_traces_plan_json ON agent_traces (trace_id) WHERE plan_json IS NOT NULL;

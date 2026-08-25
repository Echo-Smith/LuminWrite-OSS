-- 088: 回滚 plan_json 列

DROP INDEX IF EXISTS idx_traces_plan_json;
ALTER TABLE agent_traces DROP COLUMN IF EXISTS plan_json;

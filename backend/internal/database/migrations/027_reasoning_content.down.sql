-- 027: Remove reasoning_content column from agent_traces

ALTER TABLE agent_traces DROP COLUMN IF EXISTS reasoning_content;

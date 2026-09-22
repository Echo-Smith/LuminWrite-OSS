-- 119 rollback: remove enhanced agent trace columns
DROP INDEX IF EXISTS idx_wabench_outputs_trace_ref;
DROP INDEX IF EXISTS idx_agent_traces_tool_calls;
DROP INDEX IF EXISTS idx_agent_traces_llm_calls;
DROP INDEX IF EXISTS idx_agent_traces_trace_id;

ALTER TABLE agent_traces
    DROP COLUMN IF EXISTS trace_schema,
    DROP COLUMN IF EXISTS estimated_cost,
    DROP COLUMN IF EXISTS pipeline_metadata,
    DROP COLUMN IF EXISTS tool_calls,
    DROP COLUMN IF EXISTS llm_calls;

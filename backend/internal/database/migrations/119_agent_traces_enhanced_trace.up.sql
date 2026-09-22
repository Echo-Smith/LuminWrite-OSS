-- 119: Enhanced agent trace for benchmark + WABench + trace merge (Phase 1 data-layer unification)
--
-- agent_traces is the natural junction of the three systems:
--   * user writing (live)      -> writes a trace row on completion
--   * benchmark_test.py        -> reads step timings / token usage from the same row
--   * WABench evaluation       -> wabench_outputs.trace_ref = agent_traces.trace_id
--
-- This migration adds the structured, per-call trace payloads that the writing
-- engine already accumulates in engine.ExecutionContext (LLMCalls / ToolCalls /
-- PipelineMeta) but previously persisted only as an opaque step_history array.
-- Everything is nullable/defaulted so existing rows and code paths keep working.

ALTER TABLE agent_traces
    ADD COLUMN IF NOT EXISTS llm_calls        JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS tool_calls       JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS pipeline_metadata JSONB,
    ADD COLUMN IF NOT EXISTS estimated_cost   DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS trace_schema     VARCHAR(32);

COMMENT ON COLUMN agent_traces.llm_calls        IS 'engine.LLMCallRecord[] - per LLM API call (model, tokens, latency, ttft, error)';
COMMENT ON COLUMN agent_traces.tool_calls       IS 'engine.ToolCallRecord[] - per tool invocation (tool_name, duration, result_count, error)';
COMMENT ON COLUMN agent_traces.pipeline_metadata IS 'engine.PipelineMetadata - step DAG, execution plan, skipped steps, agent mode';
COMMENT ON COLUMN agent_traces.estimated_cost    IS 'Token-derived cost estimate for this trace';
COMMENT ON COLUMN agent_traces.trace_schema      IS 'Schema tag of the enhanced trace payload, e.g. enhanced-trace.v1';

-- Efficient lookup of a WABench output's enhanced trace by trace_id (trace_ref),
-- and GIN indexes so run-level aggregation can scan the arrays without a seq scan.
CREATE INDEX IF NOT EXISTS idx_agent_traces_trace_id ON agent_traces (trace_id);
CREATE INDEX IF NOT EXISTS idx_agent_traces_llm_calls ON agent_traces USING GIN (llm_calls);
CREATE INDEX IF NOT EXISTS idx_agent_traces_tool_calls ON agent_traces USING GIN (tool_calls);

-- Allow WABench Eval Center to resolve a run's outputs -> traces in one join.
-- wabench_outputs.trace_ref already stores agent_traces.trace_id.
CREATE INDEX IF NOT EXISTS idx_wabench_outputs_trace_ref ON wabench_outputs (trace_ref) WHERE trace_ref IS NOT NULL;

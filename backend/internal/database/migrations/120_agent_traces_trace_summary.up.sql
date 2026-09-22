-- 120: Precomputed trace summary + derived metrics on agent_traces.
--
-- Motivation (from the llm-space comparison): the enhanced per-call arrays
-- (llm_calls / tool_calls, migration 119) are heavy to parse. Benchmarks, the
-- Eval Center list, and run-level aggregation only need scalars. This migration
-- persists a flat summary + derived metrics so those paths read cheap columns
-- instead of scanning large JSONB, while llm_calls/tool_calls stay as the
-- lazy, on-open detail payload.
--
-- Derived metrics mirror llm-space's usage model: cache read/write tokens as
-- first-class, TTFT separate from generation throughput
-- (output tokens / (duration - ttft), TTFT intentionally excluded).

ALTER TABLE agent_traces
    ADD COLUMN IF NOT EXISTS llm_call_count      INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS tool_call_count     INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS observation_count   INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS input_tokens        INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS output_tokens       INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS reasoning_tokens    INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cache_read_tokens   INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cache_write_tokens  INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS total_tokens        INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cost_input          DOUBLE PRECISION NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cost_output         DOUBLE PRECISION NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cost_cache_read     DOUBLE PRECISION NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cost_cache_write    DOUBLE PRECISION NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS first_token_ms      INTEGER,
    ADD COLUMN IF NOT EXISTS output_tokens_per_sec DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS model               VARCHAR(120),
    ADD COLUMN IF NOT EXISTS agent_mode          VARCHAR(64);

COMMENT ON COLUMN agent_traces.first_token_ms IS 'TTFT of the first streamed LLM call (ms); excluded from throughput window';
COMMENT ON COLUMN agent_traces.output_tokens_per_sec IS 'output_tokens / ((trace duration - first_token_ms)/1000), generation-only throughput';
COMMENT ON COLUMN agent_traces.cost_input IS 'Non-cached prompt token cost (USD), pricing mirrored from GetCostEstimate';

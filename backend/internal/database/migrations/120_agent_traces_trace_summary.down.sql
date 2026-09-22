-- 120 rollback
ALTER TABLE agent_traces
    DROP COLUMN IF EXISTS agent_mode,
    DROP COLUMN IF EXISTS model,
    DROP COLUMN IF EXISTS output_tokens_per_sec,
    DROP COLUMN IF EXISTS first_token_ms,
    DROP COLUMN IF EXISTS cost_cache_write,
    DROP COLUMN IF EXISTS cost_cache_read,
    DROP COLUMN IF EXISTS cost_output,
    DROP COLUMN IF EXISTS cost_input,
    DROP COLUMN IF EXISTS total_tokens,
    DROP COLUMN IF EXISTS cache_write_tokens,
    DROP COLUMN IF EXISTS cache_read_tokens,
    DROP COLUMN IF EXISTS reasoning_tokens,
    DROP COLUMN IF EXISTS output_tokens,
    DROP COLUMN IF EXISTS input_tokens,
    DROP COLUMN IF EXISTS observation_count,
    DROP COLUMN IF EXISTS tool_call_count,
    DROP COLUMN IF EXISTS llm_call_count;

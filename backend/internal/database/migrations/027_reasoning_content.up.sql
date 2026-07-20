-- 027: Add reasoning_content column to agent_traces
-- Stores the model's chain-of-thought (thinking mode) for a writing session.
-- Used to reconstruct the "Thinking" panel when viewing historical sessions.

ALTER TABLE agent_traces ADD COLUMN IF NOT EXISTS reasoning_content TEXT;

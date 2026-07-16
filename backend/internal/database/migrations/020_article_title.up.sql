-- 020: Add article_title column to agent_traces
-- Stores the structured title extracted from LLM output (JSON prefix or fallback).
-- Used by review checks, session list display, and evaluation systems.

ALTER TABLE agent_traces ADD COLUMN IF NOT EXISTS article_title VARCHAR(128);

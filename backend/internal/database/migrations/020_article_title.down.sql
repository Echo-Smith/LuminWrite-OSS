-- 020 down: Remove article_title column
DROP INDEX IF EXISTS idx_traces_article_title;
ALTER TABLE agent_traces DROP COLUMN IF EXISTS article_title;

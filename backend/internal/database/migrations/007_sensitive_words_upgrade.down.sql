-- 007_sensitive_words_upgrade.down.sql
ALTER TABLE sensitive_words DROP COLUMN IF EXISTS replacement;
ALTER TABLE sensitive_words DROP COLUMN IF EXISTS action;

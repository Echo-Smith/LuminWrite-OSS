-- 007_sensitive_words_upgrade.up.sql
-- Add missing columns to sensitive_words table (action, replacement)

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'sensitive_words' AND column_name = 'action'
    ) THEN
        ALTER TABLE sensitive_words ADD COLUMN action VARCHAR(16) NOT NULL DEFAULT 'warn';
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'sensitive_words' AND column_name = 'replacement'
    ) THEN
        ALTER TABLE sensitive_words ADD COLUMN replacement VARCHAR(128);
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_sensitive_words_category ON sensitive_words (category, is_active);
CREATE INDEX IF NOT EXISTS idx_sensitive_words_severity ON sensitive_words (severity, is_active);

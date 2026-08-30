-- Canonical artifact content storage for the governed runtime. Artifact rows
-- reference bodies through content_ref = 'db://canonical/<content_key>';
-- bodies are content-addressed and hash-verified on every load, so a restart
-- recovers artifacts without trusting external state.
CREATE TABLE IF NOT EXISTS writing_canonical_content (
    content_key   VARCHAR(512) PRIMARY KEY,
    media_type    VARCHAR(64)  NOT NULL,
    body          BYTEA        NOT NULL,
    content_hash  VARCHAR(80)  NOT NULL,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_writing_canonical_content_created
    ON writing_canonical_content (created_at);

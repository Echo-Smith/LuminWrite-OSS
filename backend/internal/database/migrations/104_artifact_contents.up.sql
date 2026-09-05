-- 104: governed artifact content store (V3.0 M0b-2c, docs/20 §20.2). The
-- governed runtime's canonical ContentGateway needs somewhere to persist
-- artifact bodies; writing_artifacts (090) carries only content_ref +
-- content_hash, never the bytes. This mirrors the proven writing_shadow_contents
-- pattern (096) but is content-addressed and canonical: keyed by content_hash,
-- immutable, deduplicated, no run scoping or expiry (governed artifacts are
-- durable lineage, not shadow scratch). The shadow lane keeps its own store.

CREATE TABLE writing_artifact_contents (
    content_hash VARCHAR(71) PRIMARY KEY,
    media_type   VARCHAR(160) NOT NULL,
    body         BYTEA NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_writing_artifact_content_hash CHECK (content_hash ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT chk_writing_artifact_content_body CHECK (octet_length(body) > 0)
);

-- Content-addressed rows are immutable: re-staging identical bytes is a no-op
-- (ON CONFLICT DO NOTHING at the store layer); nothing may rewrite a body.
CREATE TRIGGER trg_writing_artifact_content_immutable
BEFORE UPDATE ON writing_artifact_contents
FOR EACH ROW EXECUTE FUNCTION writing_reject_immutable_columns(
    'content_hash', 'media_type', 'body', 'created_at'
);

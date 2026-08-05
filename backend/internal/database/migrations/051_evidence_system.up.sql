-- 051: Evidence System
CREATE TABLE IF NOT EXISTS trace_evidence (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    trace_id VARCHAR(64) NOT NULL,
    evidence_type VARCHAR(32) NOT NULL,
    source_url TEXT NOT NULL DEFAULT '',
    source_domain VARCHAR(256) NOT NULL DEFAULT '',
    content_hash VARCHAR(64) NOT NULL DEFAULT '',
    snippet TEXT NOT NULL DEFAULT '',
    trust_level VARCHAR(16) NOT NULL DEFAULT 'unverified',
    confidence DECIMAL(3,2) NOT NULL DEFAULT 0.50,
    metadata JSONB NOT NULL DEFAULT '{}',
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_evidence_trace ON trace_evidence (trace_id);
CREATE INDEX IF NOT EXISTS idx_evidence_trust ON trace_evidence (trust_level);
CREATE INDEX IF NOT EXISTS idx_evidence_domain ON trace_evidence (source_domain);
CREATE INDEX IF NOT EXISTS idx_evidence_expires ON trace_evidence (expires_at) WHERE expires_at IS NOT NULL;

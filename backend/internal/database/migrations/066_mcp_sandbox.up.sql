-- 066: MCP Tool Security Sandbox
--
-- Per-tool security policies: network restrictions, resource limits,
-- rate limiting, and violation logging for MCP external tools.
--
-- Policies are matched by server_name + tool_name (or wildcard).

-- ─── Tool Security Policies ───────────────────────────
CREATE TABLE IF NOT EXISTS mcp_tool_policies (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    server_name     VARCHAR(128) NOT NULL,              -- MCP server name or "*" for all
    tool_name       VARCHAR(128) NOT NULL DEFAULT '*',  -- tool name or "*" for all tools on the server
    -- Policy mode
    mode            VARCHAR(16) NOT NULL DEFAULT 'allow', -- allow | deny | conditional
    -- Network restrictions (JSON array of allowed/blocked domains)
    allowed_domains JSONB NOT NULL DEFAULT '[]'::jsonb,   -- if non-empty, only these domains are allowed in args
    blocked_domains JSONB NOT NULL DEFAULT '[]'::jsonb,   -- these domains are always blocked
    -- Resource limits
    max_arg_length  INTEGER NOT NULL DEFAULT 10000,       -- max total args JSON length (chars)
    max_result_length INTEGER NOT NULL DEFAULT 2000,      -- max output length before truncation
    timeout_ms      INTEGER NOT NULL DEFAULT 30000,       -- execution timeout in ms
    -- Rate limiting
    rate_limit_per_min INTEGER NOT NULL DEFAULT 60,        -- max calls per minute (0 = unlimited)
    -- Metadata
    description     TEXT NOT NULL DEFAULT '',
    is_active      BOOLEAN NOT NULL DEFAULT TRUE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- Ensure one policy per (server_name, tool_name) pair
    CONSTRAINT uq_mcp_tool_policy UNIQUE (server_name, tool_name)
);

CREATE INDEX IF NOT EXISTS idx_mcp_policies_active ON mcp_tool_policies(is_active);
CREATE INDEX IF NOT EXISTS idx_mcp_policies_server ON mcp_tool_policies(server_name);

-- ─── Violation Log ─────────────────────────────────────
CREATE TABLE IF NOT EXISTS mcp_tool_violations (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    policy_id       UUID REFERENCES mcp_tool_policies(id) ON DELETE SET NULL,
    server_name     VARCHAR(128) NOT NULL,
    tool_name      VARCHAR(128) NOT NULL,
    violation_type VARCHAR(32) NOT NULL,                 -- blocked_domain | arg_too_large | timeout | rate_limit | denied
    detail         TEXT NOT NULL DEFAULT '',
    args_summary   TEXT NOT NULL DEFAULT '',              -- truncated args for audit
    trace_id       VARCHAR(64) NOT NULL DEFAULT '',
    user_id        VARCHAR(128) NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_mcp_violations_time ON mcp_tool_violations(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_mcp_violations_server ON mcp_tool_violations(server_name, tool_name);
CREATE INDEX IF NOT EXISTS idx_mcp_violations_type ON mcp_tool_violations(violation_type);

-- ─── Default policies for known MCP servers ────────────
-- Filesystem: allow with reasonable limits
INSERT INTO mcp_tool_policies (server_name, tool_name, mode, max_arg_length, max_result_length, timeout_ms, rate_limit_per_min, description)
VALUES ('filesystem', '*', 'allow', 5000, 4000, 10000, 60, 'Default filesystem policy — allow with limits')
ON CONFLICT (server_name, tool_name) DO NOTHING;

-- Catch-all default: allow with standard limits
INSERT INTO mcp_tool_policies (server_name, tool_name, mode, max_arg_length, max_result_length, timeout_ms, rate_limit_per_min, description)
VALUES ('*', '*', 'allow', 10000, 2000, 30000, 60, 'Default catch-all policy — allow with standard limits')
ON CONFLICT (server_name, tool_name) DO NOTHING;

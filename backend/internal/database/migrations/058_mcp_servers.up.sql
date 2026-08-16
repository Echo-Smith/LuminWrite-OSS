-- 058: MCP external server management table
-- Stores DB-backed MCP server configurations that can be managed via admin UI.
-- These complement (not replace) MCP_SERVERS env var configurations.

CREATE TABLE IF NOT EXISTS mcp_servers (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name         VARCHAR(128) NOT NULL UNIQUE,          -- unique server name (e.g. "filesystem", "github")
    transport    VARCHAR(16)  NOT NULL DEFAULT 'stdio', -- "stdio" | "sse"
    command      TEXT         NOT NULL DEFAULT '',       -- stdio: executable command
    args         JSONB        NOT NULL DEFAULT '[]'::jsonb,   -- stdio: command arguments array
    env          JSONB        NOT NULL DEFAULT '[]'::jsonb,   -- stdio: environment variables array
    url          TEXT         NOT NULL DEFAULT '',       -- sse: server URL
    is_active    BOOLEAN      NOT NULL DEFAULT TRUE,     -- if false, server is not connected at startup
    description  TEXT         NOT NULL DEFAULT '',
    last_status  VARCHAR(16)  NOT NULL DEFAULT 'unknown', -- connected | failed | unknown
    last_error   TEXT,
    last_connected_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_mcp_servers_active ON mcp_servers(is_active);

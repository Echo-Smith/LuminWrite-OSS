package mcp

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// stdio command allowlist (security review 2026-09-09): the stdio transport
// spawns a local process whose command/args can arrive over the admin API
// (DB-backed MCP server CRUD). Without a bound, a compromised admin session
// could start an arbitrary local binary. The allowlist — MCP_ALLOWED_COMMANDS,
// a comma-separated list of command basenames or absolute paths — is the only
// way stdio commands pass; with the variable unset, stdio transport is
// disabled entirely (fail closed). SSE transport is unaffected.
//
// exec.Command itself never invokes a shell (no injection through
// metacharacters); this check bounds WHICH binary may run.

// AllowedStdioCommands returns the configured allowlist, or nil when stdio
// transport is disabled (MCP_ALLOWED_COMMANDS unset/empty).
func AllowedStdioCommands() []string {
	raw := strings.TrimSpace(os.Getenv("MCP_ALLOWED_COMMANDS"))
	if raw == "" {
		return nil
	}
	entries := strings.Split(raw, ",")
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e != "" {
			out = append(out, e)
		}
	}
	return out
}

// ValidateStdioCommand enforces the allowlist for a stdio MCP command.
// Entries may be basenames ("npx", resolved from PATH at spawn time) or
// absolute paths ("/usr/local/bin/npx"); relative paths with separators are
// rejected (ambiguous + traversal-prone).
func ValidateStdioCommand(command string) error {
	allow := AllowedStdioCommands()
	if len(allow) == 0 {
		return fmt.Errorf("stdio transport disabled: MCP_ALLOWED_COMMANDS is not configured")
	}
	command = strings.TrimSpace(command)
	if command == "" {
		return fmt.Errorf("stdio transport requires 'command'")
	}
	if strings.ContainsRune(command, 0) {
		return fmt.Errorf("stdio command must not contain NUL bytes")
	}
	// Resolve to a comparable basename; keep absolute-prefix matching for
	// path-form entries.
	base := filepath.Base(command)
	isPath := strings.ContainsRune(command, filepath.Separator)
	if isPath && !filepath.IsAbs(command) {
		return fmt.Errorf("stdio command %q must be a bare name or an absolute path", command)
	}
	for _, entry := range allow {
		if !isPath && entry == base {
			return nil
		}
		if isPath && entry == command {
			return nil
		}
		// A basename-form allowlist entry also admits an absolute path whose
		// base matches, so operators can pin either way.
		if isPath && !strings.ContainsRune(entry, filepath.Separator) && entry == base {
			return nil
		}
	}
	return fmt.Errorf("stdio command %q is not in MCP_ALLOWED_COMMANDS", command)
}

// buildStdioCmd constructs the stdio child process command. Callers MUST
// gate on ValidateStdioCommand first (NewMCPClient does). The process is
// built as an explicit argv vector — no shell is involved, arguments are
// passed verbatim — and LookPath resolves bare names up front so an
// unresolvable command fails before any process work begins.
func buildStdioCmd(name string, args []string) (*exec.Cmd, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return nil, fmt.Errorf("stdio command %q not found: %w", name, err)
	}
	argv := make([]string, 0, len(args)+1)
	argv = append(argv, path)
	argv = append(argv, args...)
	return &exec.Cmd{Path: path, Args: argv}, nil
}

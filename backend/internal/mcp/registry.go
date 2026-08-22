package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
)

// ─── MCP Registry ────────────────────────────────────────
//
// Registry manages multiple MCP server connections and exposes
// all their tools as engine.AgentTool instances.
//
// Tool naming convention: "mcp__<server>__<tool>"
// Example: "mcp__filesystem__read_file", "mcp__github__create_issue"
//
// The registry is created at server startup from config, connects to
// all MCP servers, discovers their tools, and registers them in the
// engine.ToolRegistry. Tools are available to the Harness
// alongside built-in tools and step tools.

// Registry manages multiple MCP server connections.
type Registry struct {
	mu      sync.RWMutex
	clients map[string]*MCPClient // server name → client
}

// NewRegistry creates an empty MCP registry.
func NewRegistry() *Registry {
	return &Registry{clients: make(map[string]*MCPClient)}
}

// Connect connects to an MCP server and discovers its tools.
// If connection fails, logs a warning and continues (non-fatal).
func (r *Registry) Connect(ctx context.Context, cfg MCPClientConfig) error {
	client, err := NewMCPClient(ctx, cfg)
	if err != nil {
		slog.Warn("MCP server connection failed, skipping",
			"server", cfg.Name,
			"transport", cfg.Transport,
			"error", err,
		)
		return err
	}

	r.mu.Lock()
	r.clients[cfg.Name] = client
	r.mu.Unlock()

	slog.Info("MCP server registered",
		"server", cfg.Name,
		"tools", len(client.Tools()),
	)
	return nil
}

// RegisterTools registers all MCP tools as AgentTools in the engine.ToolRegistry.
// Each MCP tool becomes an AgentTool with name "mcp__<server>__<tool>".
func (r *Registry) RegisterTools(registry *engine.ToolRegistry) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for serverName, client := range r.clients {
		for _, mcpTool := range client.Tools() {
			toolName := fmt.Sprintf("mcp__%s__%s", serverName, mcpTool.Name)
			mcpTool := mcpTool // capture
			client := client   // capture

			registry.Register(&MCPAgentTool{
				name:        toolName,
				description: mcpTool.Description,
				schema:      mcpTool.InputSchema,
				client:      client,
				toolName:    mcpTool.Name,
			})
		}
	}
}

// Close shuts down all MCP server connections.
func (r *Registry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()

	for name, client := range r.clients {
		client.Close()
		delete(r.clients, name)
	}
}

// Disconnect shuts down a single MCP server connection by name.
// Returns an error if the server is not found.
func (r *Registry) Disconnect(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	client, ok := r.clients[name]
	if !ok {
		return fmt.Errorf("MCP server %q not found", name)
	}
	client.Close()
	delete(r.clients, name)
	slog.Info("MCP server disconnected", "server", name)
	return nil
}

// Reconnect disconnects (if connected) and reconnects to an MCP server.
// This is used when admin updates a server configuration via the UI.
func (r *Registry) Reconnect(ctx context.Context, cfg MCPClientConfig) error {
	// Disconnect existing connection if any
	r.Disconnect(cfg.Name)

	// Connect with new config
	return r.Connect(ctx, cfg)
}

// IsConnected returns true if a server with the given name is currently connected.
func (r *Registry) IsConnected(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.clients[name]
	return ok
}

// ServerNames returns the names of all connected MCP servers.
func (r *Registry) ServerNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.clients))
	for name := range r.clients {
		names = append(names, name)
	}
	return names
}

// IsServerPrefix returns true if the tool name starts with "mcp__".
func IsMCPTool(name string) bool {
	return strings.HasPrefix(name, "mcp__")
}

// ParseMCPToolName splits "mcp__server__tool" into (server, tool).
func ParseMCPToolName(name string) (server, tool string, ok bool) {
	if !IsMCPTool(name) {
		return "", "", false
	}
	parts := strings.SplitN(strings.TrimPrefix(name, "mcp__"), "__", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// ─── Sandbox Hook Interface ──────────────────────────────
//
// SandboxHook is implemented by the server layer to enforce
// database-driven security policies (domain restrictions, rate
// limits, configurable timeouts, etc.) on MCP tool calls.
// The mcp package defines the interface; the server package
// provides the concrete implementation to avoid circular imports.

// SandboxCheckResult is the outcome of a pre-execution sandbox check.
type SandboxCheckResult struct {
	Allowed       bool
	ViolationType string
	Detail        string
}

// SandboxHook is called before each MCP tool execution.
type SandboxHook interface {
	// Check evaluates whether a tool call should be allowed.
	Check(serverName, toolName string, args map[string]any, traceID, userID string) SandboxCheckResult
	// TruncateResult truncates output according to the policy.
	TruncateResult(serverName, toolName, output string) string
	// GetTimeout returns the timeout for a tool.
	GetTimeout(serverName, toolName string) time.Duration
}

// ─── MCPAgentTool: MCP Tool → AgentTool Adapter ──────────

// Default sandbox limits (used when no SandboxHook is configured).
const (
	defaultMCPToolTimeout     = 30 * time.Second
	defaultMCPToolMaxOutput   = 2000
	mcpToolMaxCallsPerSession = 10
)

// MCPAgentTool wraps an MCP tool as an engine.AgentTool.
// It enforces security sandbox constraints: timeout, output truncation,
// and per-session call limits to prevent runaway MCP tool usage.
type MCPAgentTool struct {
	name        string
	description string
	schema      map[string]any
	client      *MCPClient
	toolName    string

	// sandbox is the optional security hook (nil = use defaults)
	// Uses atomic.Pointer for concurrent read access during Execute.
	sandbox atomic.Pointer[SandboxHook]

	// callCount tracks total invocations (atomic for thread-safety).
	callCount atomic.Int64
}

// SetSandboxHook sets the security sandbox hook for this tool.
// This is called once during server initialization before any tool execution.
// Uses atomic pointer store for thread-safety.
func (t *MCPAgentTool) SetSandboxHook(hook SandboxHook) {
	t.sandbox.Store(&hook)
}

func (t *MCPAgentTool) Name() string        { return t.name }
func (t *MCPAgentTool) Description() string  { return t.description }
func (t *MCPAgentTool) Schema() map[string]any { return t.schema }

func (t *MCPAgentTool) Execute(ctx context.Context, args map[string]any, execCtx *engine.ExecutionContext, emitter engine.EventEmitter) (*engine.ToolResult, error) {
	// ── Sandbox: per-session call limit ──
	current := t.callCount.Add(1)
	if current > mcpToolMaxCallsPerSession {
		slog.Warn("MCP tool call limit exceeded, blocking execution",
			"server", t.client.Name(),
			"tool", t.toolName,
			"call_count", current,
			"max_calls", mcpToolMaxCallsPerSession,
			"trace_id", execCtx.TraceID,
		)
		return nil, fmt.Errorf("MCP tool %s 已达到单次会话调用上限 (%d 次)，请减少调用频率或使用内置工具替代", t.name, mcpToolMaxCallsPerSession)
	}

	// Extract user ID from execCtx (best-effort)
	userID := ""
	if execCtx != nil && execCtx.UserID != "" {
		userID = execCtx.UserID
	}

	// ── Sandbox: policy-driven pre-execution check ──
	if hookPtr := t.sandbox.Load(); hookPtr != nil {
		hook := *hookPtr
		result := hook.Check(t.client.Name(), t.toolName, args, execCtx.TraceID, userID)
		if !result.Allowed {
			slog.Warn("MCP tool blocked by sandbox policy",
				"server", t.client.Name(),
				"tool", t.toolName,
				"violation", result.ViolationType,
				"detail", result.Detail,
				"trace_id", execCtx.TraceID,
			)
			return nil, fmt.Errorf("MCP tool %s 被安全沙箱拦截: %s", t.name, result.Detail)
		}
	}

	slog.Info("MCP tool executing",
		"server", t.client.Name(),
		"tool", t.toolName,
		"args", args,
		"trace_id", execCtx.TraceID,
		"call_count", current,
	)

	// ── Sandbox: enforce timeout (policy-driven if available) ──
	timeout := defaultMCPToolTimeout
	if hookPtr := t.sandbox.Load(); hookPtr != nil {
		if pt := (*hookPtr).GetTimeout(t.client.Name(), t.toolName); pt > 0 {
			timeout = pt
		}
	}
	toolCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result, err := t.client.CallTool(toolCtx, t.toolName, args)
	if err != nil {
		// Check if it was a timeout
		if toolCtx.Err() == context.DeadlineExceeded {
			slog.Warn("MCP tool timed out",
				"server", t.client.Name(),
				"tool", t.toolName,
				"timeout", timeout,
				"trace_id", execCtx.TraceID,
			)
			return nil, fmt.Errorf("MCP tool %s 执行超时 (%v)，请稍后重试或检查外部服务状态", t.name, timeout)
		}
		return nil, fmt.Errorf("MCP tool %s failed: %w", t.name, err)
	}

	// ── Sandbox: output truncation (policy-driven if available) ──
	truncated := false
	if hookPtr := t.sandbox.Load(); hookPtr != nil {
		truncatedResult := (*hookPtr).TruncateResult(t.client.Name(), t.toolName, result)
		if len(truncatedResult) < len(result) {
			truncated = true
		}
		result = truncatedResult
	} else if len(result) > defaultMCPToolMaxOutput {
		result = result[:defaultMCPToolMaxOutput] + "...(截断)"
		truncated = true
	}

	slog.Info("MCP tool completed",
		"server", t.client.Name(),
		"tool", t.toolName,
		"result_length", len(result),
		"truncated", truncated,
		"call_count", current,
		"trace_id", execCtx.TraceID,
	)

	return &engine.ToolResult{
		Summary: result,
		Done:    false,
	}, nil
}

// ResetCallCount resets the per-session call counter.
// This should be called when a new writing session starts.
func (t *MCPAgentTool) ResetCallCount() {
	t.callCount.Store(0)
}

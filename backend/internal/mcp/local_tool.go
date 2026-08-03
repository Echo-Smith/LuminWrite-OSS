package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
)

// ─── In-Process MCP Server: Local Tool Interface ──────────
//
// LocalTool is the interface that in-process tools implement to be
// exposed via the MCP protocol.  It mirrors the MCP tool definition:
//   - Name: unique tool identifier
//   - Description: human-readable description
//   - InputSchema: JSON Schema for the tool's parameters
//   - Execute: runs the tool and returns a text result
//
// Any Go function can be wrapped as a LocalTool via LocalToolFunc,
// or an engine.AgentTool can be adapted via LocalToolFromAgentTool.

// LocalTool is a tool that can be served by the in-process MCP server.
type LocalTool interface {
	Name() string
	Description() string
	InputSchema() map[string]any
	Execute(ctx context.Context, args map[string]any) (string, error)
}

// LocalToolFunc wraps a Go function as a LocalTool.
type LocalToolFunc struct {
	name        string
	description string
	schema      map[string]any
	fn          func(ctx context.Context, args map[string]any) (string, error)
}

// NewLocalTool creates a LocalTool from a Go function.
func NewLocalTool(name, description string, schema map[string]any, fn func(ctx context.Context, args map[string]any) (string, error)) *LocalToolFunc {
	return &LocalToolFunc{
		name:        name,
		description: description,
		schema:      schema,
		fn:          fn,
	}
}

func (t *LocalToolFunc) Name() string           { return t.name }
func (t *LocalToolFunc) Description() string    { return t.description }
func (t *LocalToolFunc) InputSchema() map[string]any { return t.schema }
func (t *LocalToolFunc) Execute(ctx context.Context, args map[string]any) (string, error) {
	return t.fn(ctx, args)
}

// ─── LocalToolRegistry ────────────────────────────────────

// LocalToolRegistry holds tools registered for the in-process MCP server.
// It is thread-safe.
type LocalToolRegistry struct {
	mu    sync.RWMutex
	tools map[string]LocalTool
}

// NewLocalToolRegistry creates an empty registry.
func NewLocalToolRegistry() *LocalToolRegistry {
	return &LocalToolRegistry{tools: make(map[string]LocalTool)}
}

// Register adds or replaces a local tool.
func (r *LocalToolRegistry) Register(t LocalTool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
	slog.Debug("local MCP tool registered", "name", t.Name())
}

// Get returns a tool by name, or nil.
func (r *LocalToolRegistry) Get(name string) LocalTool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tools[name]
}

// All returns all registered tools.
func (r *LocalToolRegistry) All() []LocalTool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]LocalTool, 0, len(r.tools))
	for _, t := range r.tools {
		result = append(result, t)
	}
	return result
}

// Count returns the number of registered tools.
func (r *LocalToolRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools)
}

// ─── AgentTool Adapter ────────────────────────────────────

// AgentToolAdapter adapts an engine.AgentTool-like interface as a LocalTool.
// We use a minimal interface to avoid importing the engine package
// (which would create a circular dependency: engine → mcp → engine).
type AgentToolLike interface {
	Name() string
	Description() string
	Schema() map[string]any
}

// AgentToolExecutor executes an AgentTool and returns a text result.
type AgentToolExecutor func(ctx context.Context, name string, args map[string]any) (string, error)

// AgentToolAdapter wraps an AgentTool-like interface + executor as a LocalTool.
type AgentToolAdapter struct {
	tool    AgentToolLike
	exec    AgentToolExecutor
}

// NewAgentToolAdapter creates a LocalTool from an AgentTool.
// The exec function is responsible for actually executing the tool;
// the adapter only provides metadata + delegates to exec.
func NewAgentToolAdapter(tool AgentToolLike, exec AgentToolExecutor) *AgentToolAdapter {
	return &AgentToolAdapter{tool: tool, exec: exec}
}

func (a *AgentToolAdapter) Name() string           { return a.tool.Name() }
func (a *AgentToolAdapter) Description() string    { return a.tool.Description() }
func (a *AgentToolAdapter) InputSchema() map[string]any { return a.tool.Schema() }
func (a *AgentToolAdapter) Execute(ctx context.Context, args map[string]any) (string, error) {
	return a.exec(ctx, a.tool.Name(), args)
}

// ─── Helper: Build LocalToolRegistry from AgentTools ──────

// RegisterAgentTools registers multiple AgentTool-like tools into the registry.
// Each tool is wrapped with the provided executor.
func (r *LocalToolRegistry) RegisterAgentTools(tools []AgentToolLike, exec AgentToolExecutor) {
	for _, t := range tools {
		r.Register(NewAgentToolAdapter(t, exec))
	}
}

// ─── JSON-RPC helpers for MCP protocol ────────────────────

// toMCPToolDef converts a LocalTool to the MCP tool definition format.
func toMCPToolDef(t LocalTool) MCPToolDef {
	return MCPToolDef{
		Name:        t.Name(),
		Description: t.Description(),
		InputSchema: t.InputSchema(),
	}
}

// callLocalTool executes a local tool and returns the MCP-formatted result.
func callLocalTool(ctx context.Context, t LocalTool, args map[string]any) (string, error) {
	result, err := t.Execute(ctx, args)
	if err != nil {
		return "", fmt.Errorf("tool %s failed: %w", t.Name(), err)
	}
	// MCP tools/call returns content array; we return text directly
	// The MCPServer wraps this into the proper JSON-RPC response
	return result, nil
}

// marshalArgs safely marshals arguments from JSON-RPC params to map[string]any.
func marshalArgs(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		// Try as a generic value
		var val any
		if err2 := json.Unmarshal(raw, &val); err2 == nil {
			if m, ok := val.(map[string]any); ok {
				return m
			}
		}
		return nil
	}
	return args
}

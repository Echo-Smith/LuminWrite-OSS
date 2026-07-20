package engine

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

// ─── Unified Tool Interface ──────────────────────────────
//
// AgentTool is the unified interface for ALL executable capabilities:
//   - Macro tools: wrapped engine.Step (intent, search, write, review, etc.)
//   - Micro tools: built-in Go functions (search_web, get_topic_context)
//   - MCP tools: dynamically discovered from external MCP servers
//
// The UnifiedAgent calls LLM with all registered tools' OpenAI-compatible
// schema, and the LLM decides which tool to invoke next (ReAct pattern).
// This replaces the fixed []Step pipeline with LLM-driven orchestration.

// AgentTool is a single capability that can be invoked by the UnifiedAgent.
type AgentTool interface {
	// Name returns the unique tool identifier (e.g. "intent", "search_web", "mcp__fs__read_file").
	Name() string

	// Description returns a human-readable description for the LLM planner.
	Description() string

	// Schema returns the OpenAI-compatible function parameters schema.
	// This is sent to the LLM as part of the tools array.
	Schema() map[string]any

	// Execute runs the tool logic and returns a result summary.
	// The execCtx is shared across all tools — tools read from and write to it.
	// The summary is fed back to the LLM as the tool result (observation).
	Execute(ctx context.Context, args map[string]any, execCtx *ExecutionContext, emitter EventEmitter) (*ToolResult, error)
}

// ToolResult is the outcome of a tool execution.
type ToolResult struct {
	// Summary is the human-readable result fed back to the LLM.
	// Keep it concise (< 500 chars) to avoid context bloat.
	Summary string `json:"summary"`

	// Done indicates the agent should stop and emit the completed event.
	// Set to true when the final article is produced and reviewed.
	Done bool `json:"done"`
}

// ToolRegistry is the central registry for all agent tools.
// It is thread-safe and supports dynamic registration (e.g. MCP tools
// discovered at runtime).
type ToolRegistry struct {
	mu    sync.RWMutex
	tools map[string]AgentTool
}

// NewToolRegistry creates an empty registry.
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{tools: make(map[string]AgentTool)}
}

// Register adds or replaces a tool in the registry.
func (r *ToolRegistry) Register(t AgentTool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
	slog.Debug("tool registered", "name", t.Name(), "description", t.Description())
}

// Get returns a tool by name, or nil if not found.
func (r *ToolRegistry) Get(name string) AgentTool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tools[name]
}

// All returns all registered tools (for building the LLM tool array).
func (r *ToolRegistry) All() []AgentTool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]AgentTool, 0, len(r.tools))
	for _, t := range r.tools {
		result = append(result, t)
	}
	return result
}

// ToolDefs returns OpenAI-compatible tool definitions for all registered tools.
// This is the array sent to the LLM in the tools parameter.
func (r *ToolRegistry) ToolDefs() []map[string]any {
	r.mu.RLock()
	defer r.mu.RUnlock()
	defs := make([]map[string]any, 0, len(r.tools))
	for _, t := range r.tools {
		defs = append(defs, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name(),
				"description": t.Description(),
				"parameters":  t.Schema(),
			},
		})
	}
	return defs
}

// ExecuteTool looks up a tool by name and executes it.
func (r *ToolRegistry) ExecuteTool(ctx context.Context, name string, args map[string]any, execCtx *ExecutionContext, emitter EventEmitter) (*ToolResult, error) {
	tool := r.Get(name)
	if tool == nil {
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
	return tool.Execute(ctx, args, execCtx, emitter)
}

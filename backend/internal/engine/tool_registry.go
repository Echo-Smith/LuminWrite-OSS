package engine

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
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

// ─── ToolDescriptor: declarative tool metadata ─────────────
//
// ToolDescriptor carries dependency and repeatability metadata for a tool.
// This replaces the hardcoded nonRepeatableTools and toolDependencies maps
// in unified_agent.go, making tool relationships declarative and visible
// to the API layer for visualization.
//
// Inspired by dsh's plugin registration pattern where plugins declare
// their capabilities and dependencies via seam definitions.

// ToolDescriptor describes a tool's relational metadata.
type ToolDescriptor struct {
	// Name is the tool identifier (matches AgentTool.Name()).
	Name string `json:"name"`

	// Description is the human-readable summary.
	Description string `json:"description"`

	// DependsOn lists tools that must have executed before this tool.
	// The UnifiedAgent uses this to hide tools whose dependencies haven't run.
	DependsOn []string `json:"depends_on,omitempty"`

	// Repeatable indicates whether the tool can be invoked more than once.
	// false = the tool is removed from the LLM's tool list after first execution.
	Repeatable bool `json:"repeatable"`

	// Terminal indicates whether the tool can end the agent loop.
	// Terminal tools set Done=true in the ToolResult when conditions are met.
	Terminal bool `json:"terminal"`

	// Category groups tools for the dependency graph visualization.
	// Common values: "planning", "retrieval", "writing", "review", "memory".
	Category string `json:"category,omitempty"`
}

// ToolRegistry is the central registry for all agent tools.
// It is thread-safe and supports dynamic registration (e.g. MCP tools
// discovered at runtime).
type ToolRegistry struct {
	mu           sync.RWMutex
	tools        map[string]AgentTool
	descriptors  map[string]ToolDescriptor
	plugins      *pluginManager
}

// NewToolRegistry creates an empty registry.
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools:       make(map[string]AgentTool),
		descriptors: make(map[string]ToolDescriptor),
		plugins:     newPluginManager(),
	}
}

// Register adds or replaces a tool in the registry without descriptor metadata.
// Tools registered this way default to Repeatable=true, no dependencies.
func (r *ToolRegistry) Register(t AgentTool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := t.Name()
	r.tools[name] = t
	// Default descriptor: repeatable, no deps, non-terminal
	if _, exists := r.descriptors[name]; !exists {
		r.descriptors[name] = ToolDescriptor{
			Name:        name,
			Description: t.Description(),
			Repeatable:  true,
		}
	}
	slog.Debug("tool registered", "name", name, "description", t.Description())
}

// RegisterWithDescriptor adds a tool with explicit dependency metadata.
// This is the preferred registration method for tools with dependencies
// or that should only execute once per session.
func (r *ToolRegistry) RegisterWithDescriptor(t AgentTool, desc ToolDescriptor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := t.Name()
	r.tools[name] = t
	// Ensure Name and Description are filled from the tool if empty
	if desc.Name == "" {
		desc.Name = name
	}
	if desc.Description == "" {
		desc.Description = t.Description()
	}
	r.descriptors[name] = desc
	slog.Debug("tool registered with descriptor",
		"name", name,
		"depends_on", desc.DependsOn,
		"repeatable", desc.Repeatable,
		"terminal", desc.Terminal,
		"category", desc.Category,
	)
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

// GetDescriptor returns the descriptor for a tool, or a default if not found.
func (r *ToolRegistry) GetDescriptor(name string) (ToolDescriptor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	desc, ok := r.descriptors[name]
	return desc, ok
}

// IsRepeatable returns false if the tool should only execute once.
func (r *ToolRegistry) IsRepeatable(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if desc, ok := r.descriptors[name]; ok {
		return desc.Repeatable
	}
	return true // default: repeatable
}

// DependsOn returns the list of tools that must execute before the given tool.
func (r *ToolRegistry) DependsOn(name string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if desc, ok := r.descriptors[name]; ok {
		return desc.DependsOn
	}
	return nil
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

// ─── Tool Graph: dependency visualization ────────────────

// ToolGraphNode represents a single tool in the dependency graph.
type ToolGraphNode struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Category    string   `json:"category,omitempty"`
	DependsOn   []string `json:"depends_on,omitempty"`
	Repeatable  bool     `json:"repeatable"`
	Terminal    bool     `json:"terminal"`
}

// ToolGraphEdge represents a dependency relationship.
type ToolGraphEdge struct {
	From string `json:"from"` // tool that depends
	To   string `json:"to"`   // tool that is depended on
}

// ToolGraph is the complete dependency graph for visualization.
type ToolGraph struct {
	Nodes []ToolGraphNode `json:"nodes"`
	Edges []ToolGraphEdge `json:"edges"`
}

// ToolGraph returns the dependency graph of all registered tools.
// The frontend uses this to render a visual tool dependency diagram.
func (r *ToolRegistry) ToolGraph() ToolGraph {
	r.mu.RLock()
	defer r.mu.RUnlock()

	graph := ToolGraph{
		Nodes: make([]ToolGraphNode, 0, len(r.descriptors)),
	}

	// Collect nodes
	for name, desc := range r.descriptors {
		node := ToolGraphNode{
			Name:        name,
			Description: desc.Description,
			Category:    desc.Category,
			DependsOn:   desc.DependsOn,
			Repeatable:  desc.Repeatable,
			Terminal:    desc.Terminal,
		}
		// For tools without explicit descriptor (e.g. MCP tools),
		// pull description from the tool itself
		if node.Description == "" {
			if t, ok := r.tools[name]; ok {
				node.Description = t.Description()
			}
		}
		graph.Nodes = append(graph.Nodes, node)
	}

	// Sort nodes by name for stable output
	sort.Slice(graph.Nodes, func(i, j int) bool {
		return graph.Nodes[i].Name < graph.Nodes[j].Name
	})

	// Build edges from dependency declarations
	seen := make(map[string]bool) // deduplicate edges
	for name, desc := range r.descriptors {
		for _, dep := range desc.DependsOn {
			edgeKey := name + "->" + dep
			if !seen[edgeKey] {
				graph.Edges = append(graph.Edges, ToolGraphEdge{
					From: name,
					To:   dep,
				})
				seen[edgeKey] = true
			}
		}
	}

	// Sort edges for stable output
	sort.Slice(graph.Edges, func(i, j int) bool {
		if graph.Edges[i].From != graph.Edges[j].From {
			return graph.Edges[i].From < graph.Edges[j].From
		}
		return graph.Edges[i].To < graph.Edges[j].To
	})

	return graph
}

package server

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/config"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/mcp"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/memory"
)

// ─── In-Process MCP Server Initialization ─────────────────
//
// initMCPServer creates the in-process MCP server and registers
// built-in tools that expose the application's capabilities
// (search, knowledge base, memory, style profiles, LLM, etc.) to
// external MCP clients via the JSON-RPC 2.0 protocol.
//
// The MCP server can serve over:
//   - HTTP: for remote MCP clients (started in Start())
//   - stdio: for local MCP clients like Claude Desktop (blocking)

func (s *Server) initMCPServer(cfg *config.Config) {
	registry := mcp.NewLocalToolRegistry()

	// Register built-in meta tools (ping, server_info)
	mcpServer := mcp.NewMCPServer("luminbuddy-writing-agent", "2.0", registry)
	mcpServer.RegisterBuiltinTools()

	// ── Register application tools ──

	// 1. Web search tool
	if s.search != nil && s.search.HasSources() {
		registry.Register(mcp.NewLocalTool(
			"search_web",
			"Search the web across multiple sources (Tavily, Zhihu, Tencent News, Weibo, Bing, etc.). Returns titles, snippets, and URLs.",
			map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Search query",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Max results (default 5, max 20)",
						"default":     5,
					},
				},
				"required": []string{"query"},
			},
			func(ctx context.Context, args map[string]any) (string, error) {
				query, _ := args["query"].(string)
				if query == "" {
					return "Error: query is required", nil
				}
				limit := 5
				if l, ok := args["limit"].(float64); ok && l > 0 {
					limit = int(l)
					if limit > 20 {
						limit = 20
					}
				}
				results := s.search.Search(ctx, query, limit)
				if len(results) == 0 {
					return "No results found", nil
				}
				var sb strings.Builder
				for i, r := range results {
					fmt.Fprintf(&sb, "%d. %s\n   %s\n   URL: %s\n", i+1, r.Title, r.Snippet, r.URL)
				}
				return sb.String(), nil
			},
		))
	}

	// 2. Style profile tools
	if s.profiles != nil {
		registry.Register(mcp.NewLocalTool(
			"list_styles",
			"List all available writing style profiles (e.g. yinyue, shenlun, xiaohongshu).",
			map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
			func(ctx context.Context, args map[string]any) (string, error) {
				styles := s.profiles.List()
				var sb strings.Builder
				for _, st := range styles {
					fmt.Fprintf(&sb, "- %s (v%d): %s [%d-%d words]\n", st.Name, st.Version, st.Description, st.WordRange[0], st.WordRange[1])
				}
				return sb.String(), nil
			},
		))

		registry.Register(mcp.NewLocalTool(
			"get_style",
			"Get detailed configuration for a specific writing style profile.",
			map[string]any{
				"type": "object",
				"properties": map[string]any{
					"slug": map[string]any{
						"type":        "string",
						"description": "Style slug (e.g. yinyue, shenlun, xiaohongshu)",
					},
				},
				"required": []string{"slug"},
			},
			func(ctx context.Context, args map[string]any) (string, error) {
				slug, _ := args["slug"].(string)
				if slug == "" {
					return "Error: slug is required", nil
				}
				p, ok := s.profiles.Get(slug)
				if !ok {
					return "Error: style not found", nil
				}
				var sb strings.Builder
				fmt.Fprintf(&sb, "Name: %s (v%d)\n", p.Name, p.Version)
				fmt.Fprintf(&sb, "Description: %s\n", p.Description)
				fmt.Fprintf(&sb, "Word Range: %d-%d\n", p.WordRange.Min, p.WordRange.Max)
				fmt.Fprintf(&sb, "Structure: %s\n", p.Structure.Type)
				if p.SystemPrompt != "" {
					fmt.Fprintf(&sb, "System Prompt: %s\n", p.SystemPrompt)
				}
				return sb.String(), nil
			},
		))
	}

	// 3. Memory tools (if available)
	if s.memorySvc != nil && s.memorySvc.IsAvailable() {
		registry.Register(mcp.NewLocalTool(
			"list_user_memories",
			"List writing preference memories for a user. Returns categorized preferences like word count, style, tone, etc.",
			map[string]any{
				"type": "object",
				"properties": map[string]any{
					"user_id": map[string]any{
						"type":        "string",
						"description": "User UUID",
					},
					"tier": map[string]any{
						"type":        "string",
						"description": "Memory tier filter: hard, pattern, feedback (optional)",
					},
				},
				"required": []string{"user_id"},
			},
			func(ctx context.Context, args map[string]any) (string, error) {
				userID, _ := args["user_id"].(string)
				if userID == "" {
					return "Error: user_id is required", nil
				}
				opts := memory.ListOptions{Limit: 50}
				if tierStr, ok := args["tier"].(string); ok && tierStr != "" {
					tier := memory.Tier(tierStr)
					opts.Tier = &tier
				}
				memories, err := s.memorySvc.List(ctx, userID, opts)
				if err != nil {
					return "Error: " + err.Error(), nil
				}
				if len(memories) == 0 {
					return "No memories found", nil
				}
				var sb strings.Builder
				for _, m := range memories {
					fmt.Fprintf(&sb, "- [%s/%s] %s = %s (confidence: %.2f, occurrences: %d)\n",
						m.Tier, m.Category, m.Key, m.Value, m.Confidence, m.Occurrences)
				}
				return sb.String(), nil
			},
		))
	}

	// 4. LLM completion tool (if available)
	if s.llm != nil {
		registry.Register(mcp.NewLocalTool(
			"llm_complete",
			"Generate text using the configured LLM (DeepSeek). Useful for drafting, summarizing, or transforming text.",
			map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt": map[string]any{
						"type":        "string",
						"description": "The prompt to send to the LLM",
					},
					"system": map[string]any{
						"type":        "string",
						"description": "System prompt (optional)",
					},
				},
				"required": []string{"prompt"},
			},
			func(ctx context.Context, args map[string]any) (string, error) {
				prompt, _ := args["prompt"].(string)
				if prompt == "" {
					return "Error: prompt is required", nil
				}
				systemPrompt, _ := args["system"].(string)
				if systemPrompt == "" {
					systemPrompt = "You are a helpful assistant."
				}

				ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
				defer cancel()

				resp, _, err := s.llm.Chat(ctx, []tools.LLMMessage{
					{Role: "system", Content: systemPrompt},
					{Role: "user", Content: prompt},
				})
				if err != nil {
					return "Error: " + err.Error(), nil
				}
				return resp, nil
			},
		))
	}

	// 5. Knowledge base search tool (if available)
	if s.kbRepo != nil {
		registry.Register(mcp.NewLocalTool(
			"kb_search",
			"Search the internal knowledge base for relevant documents and passages using semantic search.",
			map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Search query",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Max results (default 5)",
						"default":     5,
					},
				},
				"required": []string{"query"},
			},
			func(ctx context.Context, args map[string]any) (string, error) {
				query, _ := args["query"].(string)
				if query == "" {
					return "Error: query is required", nil
				}
				limit := 5
				if l, ok := args["limit"].(float64); ok && l > 0 {
					limit = int(l)
				}
				results, err := s.kbRepo.SemanticSearch(ctx, query, limit, "")
				if err != nil {
					return "Error: " + err.Error(), nil
				}
				if len(results) == 0 {
					return "No results found", nil
				}
				var sb strings.Builder
				for i, r := range results {
					fmt.Fprintf(&sb, "%d. %s\n   %s\n", i+1, r.Title, r.Content)
				}
				return sb.String(), nil
			},
		))
	}

	s.mcpServer = mcpServer

	slog.Info("in-process MCP server initialized",
		"tools", registry.Count(),
		"http_addr", cfg.MCPServer.HTTPAddr,
		"stdio", cfg.MCPServer.Stdio,
	)
}

// startMCPServerHTTP starts the MCP server's HTTP listener (non-blocking).
// Called from Start().
func (s *Server) startMCPServerHTTP() {
	if s.mcpServer == nil {
		return
	}
	go s.mcpServer.StartHTTPServer(s.cfg.MCPServer.HTTPAddr)
}

// StartMCPServerStdio starts the MCP server in stdio mode (blocking).
// This is called from main.go when MCP_SERVER_STDIO=true, allowing the
// binary to be used as a subprocess by Claude Desktop and other MCP clients.
func (s *Server) StartMCPServerStdio(ctx context.Context) {
	if s.mcpServer == nil {
		slog.Error("MCP server not initialized, cannot start stdio mode")
		return
	}
	slog.Info("starting MCP server in stdio mode (blocking)")
	if err := s.mcpServer.ServeStdio(ctx); err != nil {
		slog.Error("MCP stdio server error", "error", err)
	}
}

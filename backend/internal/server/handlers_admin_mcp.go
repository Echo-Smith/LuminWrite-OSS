package server

import (
	"encoding/json"
	"net/http"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/mcp"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── Admin: MCP Server Management ─────────────────────────
//
// These endpoints provide visibility into the in-process MCP server
// and the external MCP server connections.

// handleAdminMCPStatus returns the status of the MCP system:
//   - In-process MCP server (enabled, tool count, transport)
//   - External MCP server connections (name, tool count, status)
func (s *Server) handleAdminMCPStatus(w http.ResponseWriter, r *http.Request) {
	status := map[string]any{
		"in_process": map[string]any{
			"enabled":   s.mcpServer != nil,
			"config":    s.cfg.MCPServer,
		},
		"external_servers": []any{},
	}

	// External MCP servers
	if s.mcpRegistry != nil {
		servers := s.mcpRegistry.ServerNames()
		extServers := make([]any, 0, len(servers))
		for _, name := range servers {
			extServers = append(extServers, map[string]any{
				"name":   name,
				"status": "connected",
			})
		}
		status["external_servers"] = extServers
		status["external_count"] = len(servers)
	}

	// In-process tool count
	if s.mcpServer != nil {
		registry := s.mcpServer.Registry()
		status["in_process"].(map[string]any)["tool_count"] = registry.Count()
	}

	response.OK(w, status)
}

// handleAdminMCPTools lists all registered MCP tools (in-process + external).
func (s *Server) handleAdminMCPTools(w http.ResponseWriter, r *http.Request) {
	tools := make([]any, 0)

	// In-process tools
	if s.mcpServer != nil {
		registry := s.mcpServer.Registry()
		for _, t := range registry.All() {
			tools = append(tools, map[string]any{
				"name":        t.Name(),
				"description": t.Description(),
				"source":      "in-process",
				"schema":      t.InputSchema(),
			})
		}
	}

	// External MCP tools (from the engine tool registry, prefixed with "mcp__")
	if s.toolRegistry != nil {
		for _, t := range s.toolRegistry.All() {
			if mcp.IsMCPTool(t.Name()) {
				tools = append(tools, map[string]any{
					"name":        t.Name(),
					"description": t.Description(),
					"source":      "external",
					"schema":      t.Schema(),
				})
			}
		}
	}

	response.OK(w, map[string]any{
		"tools": tools,
		"total": len(tools),
	})
}

// handleAdminMCPExport exports all MCP tool definitions as a JSON array
// in the MCP tools/list response format. This can be used to register
// the tools with external MCP clients.
func (s *Server) handleAdminMCPExport(w http.ResponseWriter, r *http.Request) {
	toolDefs := make([]mcp.MCPToolDef, 0)

	// In-process tools
	if s.mcpServer != nil {
		registry := s.mcpServer.Registry()
		for _, t := range registry.All() {
			toolDefs = append(toolDefs, mcp.MCPToolDef{
				Name:        t.Name(),
				Description: t.Description(),
				InputSchema: t.InputSchema(),
			})
		}
	}

	// External MCP tools
	if s.toolRegistry != nil {
		for _, t := range s.toolRegistry.All() {
			if mcp.IsMCPTool(t.Name()) {
				toolDefs = append(toolDefs, mcp.MCPToolDef{
					Name:        t.Name(),
					Description: t.Description(),
					InputSchema: t.Schema(),
				})
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=mcp-tools.json")
	json.NewEncoder(w).Encode(map[string]any{
		"tools":       toolDefs,
		"exported_at": "now",
		"total":       len(toolDefs),
	})
}

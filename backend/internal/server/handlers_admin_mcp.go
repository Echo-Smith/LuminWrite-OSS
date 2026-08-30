package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
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
			"enabled": s.mcpServer != nil,
			"config":  s.cfg.MCPServer,
		},
		"external_servers": []any{},
	}

	// External MCP servers
	if s.mcpRegistry != nil {
		servers := s.mcpRegistry.Statuses()
		status["external_servers"] = servers
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

// ─── Admin: MCP Server CRUD ─────────────────────────────

// handleAdminListMCPServers returns all DB-backed MCP server configurations.
func (s *Server) handleAdminListMCPServers(w http.ResponseWriter, r *http.Request) {
	if s.adminRepo == nil {
		response.OK(w, map[string]any{"servers": []any{}, "total": 0})
		return
	}
	servers, err := s.adminRepo.ListMCPServers(r.Context())
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list mcp servers")
		return
	}
	// Enrich with live connection status
	for _, srv := range servers {
		if s.mcpRegistry != nil && s.mcpRegistry.IsConnected(srv.Name) {
			srv.LastStatus = "connected"
		} else if srv.LastStatus == "connected" {
			srv.LastStatus = "disconnected"
		}
	}
	response.OK(w, map[string]any{"servers": servers, "total": len(servers)})
}

// handleAdminCreateMCPServer creates a new MCP server configuration.
func (s *Server) handleAdminCreateMCPServer(w http.ResponseWriter, r *http.Request) {
	var req database.MCPServerConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	if req.Name == "" || req.Transport == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "name and transport are required")
		return
	}
	if req.Transport == "stdio" && req.Command == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "stdio transport requires 'command'")
		return
	}
	if req.Transport == "sse" && req.URL == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "sse transport requires 'url'")
		return
	}
	if s.adminRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}
	created, err := s.adminRepo.CreateMCPServer(r.Context(), &req)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to create mcp server")
		return
	}
	// Auto-connect if active
	if created.IsActive && s.mcpRegistry != nil {
		s.connectMCPServer(r.Context(), created)
	}
	response.Created(w, created)
}

// handleAdminUpdateMCPServer updates an MCP server configuration.
func (s *Server) handleAdminUpdateMCPServer(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req database.MCPServerConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	if s.adminRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}
	updated, err := s.adminRepo.UpdateMCPServer(r.Context(), id, &req)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to update mcp server")
		return
	}
	// Reconnect with new config if active
	if updated.IsActive && s.mcpRegistry != nil {
		s.connectMCPServer(r.Context(), updated)
	} else if s.mcpRegistry != nil {
		// If deactivated, disconnect
		_ = s.mcpRegistry.Disconnect(updated.Name)
		s.updateMCPReadiness(time.Now())
		s.adminRepo.UpdateMCPServerStatus(r.Context(), id, "disconnected", "")
	}
	response.OK(w, updated)
}

// handleAdminDeleteMCPServer deletes an MCP server configuration.
func (s *Server) handleAdminDeleteMCPServer(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if s.adminRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}
	// Get server name before deletion for disconnect
	servers, _ := s.adminRepo.ListMCPServers(r.Context())
	var serverName string
	for _, srv := range servers {
		if srv.ID == id {
			serverName = srv.Name
			break
		}
	}
	if err := s.adminRepo.DeleteMCPServer(r.Context(), id); err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to delete mcp server")
		return
	}
	// Disconnect if currently connected
	if serverName != "" && s.mcpRegistry != nil {
		_ = s.mcpRegistry.Disconnect(serverName)
		s.mcpRegistry.Forget(serverName)
		s.updateMCPReadiness(time.Now())
	}
	response.OK(w, map[string]any{"message": "mcp server deleted"})
}

// handleAdminReconnectMCPServer triggers a manual reconnect for a server.
func (s *Server) handleAdminReconnectMCPServer(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if s.adminRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}
	servers, err := s.adminRepo.ListMCPServers(r.Context())
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list mcp servers")
		return
	}
	var target *database.MCPServerConfig
	for _, srv := range servers {
		if srv.ID == id {
			target = srv
			break
		}
	}
	if target == nil {
		response.Err(w, http.StatusNotFound, "not_found", "mcp server not found")
		return
	}
	if s.mcpRegistry == nil {
		response.Err(w, http.StatusServiceUnavailable, "mcp_unavailable", "mcp registry not initialized")
		return
	}
	s.connectMCPServer(r.Context(), target)
	// Re-fetch updated status
	if s.mcpRegistry.IsConnected(target.Name) {
		response.OK(w, map[string]any{"status": "connected", "message": "MCP server connected successfully"})
	} else {
		response.OK(w, map[string]any{"status": "failed", "message": "Failed to connect MCP server"})
	}
}

// connectMCPServer connects (or reconnects) an MCP server from a DB config.
// Updates the DB status after connection attempt.
func (s *Server) connectMCPServer(ctx context.Context, cfg *database.MCPServerConfig) {
	mcpCfg := mcp.MCPClientConfig{
		Name:      cfg.Name,
		Transport: cfg.Transport,
		Command:   cfg.Command,
		Args:      cfg.Args,
		Env:       cfg.Env,
		URL:       cfg.URL,
		Timeout:   30 * time.Second,
	}

	connectCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if err := s.mcpRegistry.Reconnect(connectCtx, mcpCfg); err != nil {
		slog.Warn("MCP server connect failed", "server", cfg.Name, "error", err)
		s.adminRepo.UpdateMCPServerStatus(ctx, cfg.ID, "failed", err.Error())
	} else {
		s.adminRepo.UpdateMCPServerStatus(ctx, cfg.ID, "connected", "")
		// Re-register tools in the engine tool registry
		if s.toolRegistry != nil {
			s.mcpRegistry.RegisterTools(s.toolRegistry)
		}
	}
	s.updateMCPReadiness(time.Now())
}

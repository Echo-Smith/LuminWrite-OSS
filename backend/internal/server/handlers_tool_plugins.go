package server

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── Tool Plugin Management (Admin) ──────────────────────
//
// These endpoints allow administrators to dynamically load and unload
// tool plugins at runtime, without restarting the server.
//
// A plugin is a named bundle of HTTP-based tools defined by a YAML/JSON
// configuration. Each tool in the plugin calls an external HTTP endpoint
// and returns the response to the LLM.
//
// Endpoints:
//   GET    /api/v2/admin/tool-plugins         — list all loaded plugins
//   POST   /api/v2/admin/tool-plugins         — load a new plugin (JSON body)
//   GET    /api/v2/admin/tool-plugins/{name}  — get plugin details
//   DELETE /api/v2/admin/tool-plugins/{name}  — unload a plugin
//
// All endpoints require admin authentication.

// handleAdminListToolPlugins returns all loaded tool plugins.
//
// GET /api/v2/admin/tool-plugins
func (s *Server) handleAdminListToolPlugins(w http.ResponseWriter, r *http.Request) {
	if s.toolRegistry == nil {
		response.OK(w, map[string]interface{}{
			"plugins": []interface{}{},
			"total":   0,
		})
		return
	}

	plugins := s.toolRegistry.ListPlugins()
	response.OK(w, map[string]interface{}{
		"plugins": plugins,
		"total":   len(plugins),
	})
}

// handleAdminCreateToolPlugin loads a new tool plugin from configuration.
//
// POST /api/v2/admin/tool-plugins
// Body: PluginConfig JSON (name, description, version, tools[])
//
// runtime-agility M3：加载成功后配置落盘 TOOL_PLUGINS_DIR（跨重启存活；
// SSRF 校验失败/重名工具在构建层即拒绝）。
func (s *Server) handleAdminCreateToolPlugin(w http.ResponseWriter, r *http.Request) {
	if s.toolPluginLoader == nil {
		response.Err(w, http.StatusServiceUnavailable, "registry_unavailable", "tool registry not initialized")
		return
	}

	var cfg engine.PluginConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid plugin configuration: "+err.Error())
		return
	}

	if cfg.Name == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "plugin name is required")
		return
	}

	if len(cfg.Tools) == 0 {
		response.Err(w, http.StatusBadRequest, "bad_request", "plugin must have at least one tool")
		return
	}

	// Build the plugin from config（SSRF 红线在此校验：endpoint 协议白名单
	// + 解析 IP 必须公开可路由）
	plugin, err := engine.BuildPluginFromConfig(cfg)
	if err != nil {
		slog.Error("failed to build tool plugin", "error", err, "name", cfg.Name)
		response.Err(w, http.StatusBadRequest, "invalid_plugin", "failed to build plugin: "+err.Error())
		return
	}

	// 双写注册：engine registry（管理权威）+ editorial registry（写作消费面）
	if err := s.toolPluginLoader.LoadPlugin(plugin); err != nil {
		slog.Error("failed to register tool plugin", "error", err, "name", cfg.Name)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to register plugin: "+err.Error())
		return
	}

	// 落盘：跨重启存活
	if path, err := s.toolPluginLoader.persist(cfg); err != nil {
		slog.Warn("tool plugin persisted failed（内存态生效，重启后丢失）", "error", err, "name", cfg.Name)
	} else {
		slog.Info("tool plugin persisted", "path", path, "name", cfg.Name)
	}

	info, _ := s.toolRegistry.GetPlugin(cfg.Name)

	slog.Info("tool plugin loaded via admin API",
		"name", cfg.Name,
		"tools", len(cfg.Tools),
	)

	response.Created(w, map[string]interface{}{
		"plugin":   info,
		"created":  true,
	})
}

// handleAdminGetToolPlugin returns details of a specific loaded plugin.
//
// GET /api/v2/admin/tool-plugins/{name}
func (s *Server) handleAdminGetToolPlugin(w http.ResponseWriter, r *http.Request) {
	if s.toolRegistry == nil {
		response.Err(w, http.StatusServiceUnavailable, "registry_unavailable", "tool registry not initialized")
		return
	}

	name := chi.URLParam(r, "name")
	if name == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "plugin name is required")
		return
	}

	info, ok := s.toolRegistry.GetPlugin(name)
	if !ok {
		response.Err(w, http.StatusNotFound, "not_found", "plugin not found")
		return
	}

	response.OK(w, info)
}

// handleAdminDeleteToolPlugin unloads a tool plugin and removes all its tools.
//
// DELETE /api/v2/admin/tool-plugins/{name}
//
// runtime-agility M3：双侧反注册（engine + editorial）并删除落盘配置。
func (s *Server) handleAdminDeleteToolPlugin(w http.ResponseWriter, r *http.Request) {
	if s.toolPluginLoader == nil {
		response.Err(w, http.StatusServiceUnavailable, "registry_unavailable", "tool registry not initialized")
		return
	}

	name := chi.URLParam(r, "name")
	if name == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "plugin name is required")
		return
	}

	if _, ok := s.toolRegistry.GetPlugin(name); !ok {
		response.Err(w, http.StatusNotFound, "not_found", "plugin not found")
		return
	}

	s.toolPluginLoader.UnloadPlugin(name)

	if err := s.toolPluginLoader.removePersisted(name); err != nil {
		slog.Warn("failed to remove persisted plugin config", "error", err, "name", name)
	}

	slog.Info("tool plugin unloaded via admin API", "name", name)

	response.OK(w, map[string]interface{}{
		"deleted": true,
		"name":    name,
	})
}

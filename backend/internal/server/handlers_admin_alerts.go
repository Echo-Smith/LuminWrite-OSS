package server

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── Admin Alerts (ops patrol) ──────────────────────────
//
// Read + acknowledge/resolve endpoints for alerts raised by the ops
// patrol worker. Any admin may view; ack/resolve are audit-logged.

// handleAdminListAlerts lists alerts with pagination and status filter.
//
// GET /api/v2/admin/alerts?status=pending|acked|resolved|all&page=1&page_size=50
func (s *Server) handleAdminListAlerts(w http.ResponseWriter, r *http.Request) {
	if s.adminAlertRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "数据库不可用")
		return
	}

	status := r.URL.Query().Get("status")
	page := parseIntDefault(r.URL.Query().Get("page"), 1)
	pageSize := parseIntDefault(r.URL.Query().Get("page_size"), 50)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 50
	}

	alerts, total, err := s.adminAlertRepo.ListAlerts(r.Context(), status, pageSize, (page-1)*pageSize)
	if err != nil {
		slog.Error("list admin alerts failed", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "查询告警失败")
		return
	}
	pending, acked, err := s.adminAlertRepo.CountsByStatus(r.Context())
	if err != nil {
		slog.Error("count admin alerts failed", "error", err)
		pending, acked = 0, 0
	}

	response.OK(w, map[string]interface{}{
		"alerts":        alerts,
		"total":         total,
		"page":          page,
		"page_size":     pageSize,
		"pending_count": pending,
		"acked_count":   acked,
	})
}

// handleAdminAckAlert marks a pending alert as acknowledged.
//
// POST /api/v2/admin/alerts/{id}/ack
func (s *Server) handleAdminAckAlert(w http.ResponseWriter, r *http.Request) {
	if s.adminAlertRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "数据库不可用")
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "缺少告警 ID")
		return
	}

	actor := "system"
	if user := userFromContext(r.Context()); user != nil {
		actor = user.Sub
	}
	if err := s.adminAlertRepo.AckAlert(r.Context(), id, actor); err != nil {
		slog.Warn("ack admin alert failed", "id", id, "error", err)
		response.Err(w, http.StatusConflict, "invalid_state", "告警不存在或已确认")
		return
	}
	s.writeAuditLog(r, "update", "admin_alert", id, "确认运维告警", nil)
	response.OK(w, map[string]interface{}{"id": id, "status": "acked"})
}

// handleAdminResolveAlert marks an alert as resolved.
//
// POST /api/v2/admin/alerts/{id}/resolve
func (s *Server) handleAdminResolveAlert(w http.ResponseWriter, r *http.Request) {
	if s.adminAlertRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "数据库不可用")
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "缺少告警 ID")
		return
	}

	if err := s.adminAlertRepo.ResolveAlert(r.Context(), id); err != nil {
		slog.Warn("resolve admin alert failed", "id", id, "error", err)
		response.Err(w, http.StatusConflict, "invalid_state", "告警不存在或已解决")
		return
	}
	s.writeAuditLog(r, "update", "admin_alert", id, "解决运维告警", nil)
	response.OK(w, map[string]interface{}{"id": id, "status": "resolved"})
}

// handleAdminGetPatrolConfig returns the patrol runtime toggle.
//
// GET /api/v2/admin/patrol/config
func (s *Server) handleAdminGetPatrolConfig(w http.ResponseWriter, r *http.Request) {
	if s.adminAlertRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "数据库不可用")
		return
	}
	cfg, err := s.adminAlertRepo.GetPatrolConfig(r.Context())
	if err != nil {
		slog.Error("get patrol config failed", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "读取巡检配置失败")
		return
	}
	response.OK(w, cfg)
}

// handleAdminUpdatePatrolConfig updates the patrol runtime toggle.
// Disabling stops future patrol rounds immediately; alerts API and
// historical alerts remain available.
//
// PUT /api/v2/admin/patrol/config  body: {"enabled": true|false}
func (s *Server) handleAdminUpdatePatrolConfig(w http.ResponseWriter, r *http.Request) {
	if s.adminAlertRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "数据库不可用")
		return
	}
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Enabled == nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "请求体需为 {\"enabled\": bool}")
		return
	}

	actor := "system"
	if user := userFromContext(r.Context()); user != nil {
		actor = user.Sub
	}
	if err := s.adminAlertRepo.SetPatrolEnabled(r.Context(), *req.Enabled, actor); err != nil {
		slog.Error("set patrol config failed", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "保存巡检配置失败")
		return
	}
	s.writeAuditLog(r, "update", "patrol_config", "singleton",
		map[bool]string{true: "开启自动巡检", false: "暂停自动巡检"}[*req.Enabled],
		map[string]interface{}{"enabled": *req.Enabled})
	response.OK(w, map[string]interface{}{"enabled": *req.Enabled})
}

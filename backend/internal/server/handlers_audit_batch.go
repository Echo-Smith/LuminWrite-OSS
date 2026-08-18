package server

import (
	"encoding/json"
	"strings"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)
// AuditEntry represents a single audit log entry.
type AuditEntry struct {
	ID         int64                  `json:"id"`
	ActorID    string                 `json:"actor_id"`
	ActorRole  string                 `json:"actor_role"`
	Action     string                 `json:"action"`
	Resource   string                 `json:"resource"`
	ResourceID string                 `json:"resource_id"`
	Detail     string                 `json:"detail"`
	Changes    map[string]interface{} `json:"changes"`
	IPAddress  string                 `json:"ip_address"`
	UserAgent  string                 `json:"user_agent"`
	CreatedAt  string                 `json:"created_at"`
}

func (s *Server) writeAuditLog(r *http.Request, action, resource, resourceID, detail string, changes map[string]interface{}) {
	if s.adminRepo == nil || s.adminRepo.DB() == nil { return }
	actorID := "system"
	actorRole := "system"
	if user := userFromContext(r.Context()); user != nil {
		actorID = user.Sub
		actorRole = user.Role
	}
	changesJSON, _ := json.Marshal(changes)
	_, err := s.adminRepo.DB().ExecContext(r.Context(), `INSERT INTO admin_audit_logs (actor_id, actor_role, action, resource, resource_id, detail, changes, ip_address, user_agent) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, actorID, actorRole, action, resource, resourceID, detail, changesJSON, r.RemoteAddr, r.UserAgent())
	if err != nil { slog.Warn("failed to write audit log", "action", action, "resource", resource, "error", err) }
}
func (s *Server) handleAdminListAuditLogs(w http.ResponseWriter, r *http.Request) {
	if s.adminRepo == nil || s.adminRepo.DB() == nil {
		response.OK(w, map[string]interface{}{"logs": []interface{}{}, "total": 0})
		return
	}
	resource := r.URL.Query().Get("resource")
	action := r.URL.Query().Get("action")
	actorID := r.URL.Query().Get("actor_id")
	page := parseIntDefault(r.URL.Query().Get("page"), 1)
	pageSize := parseIntDefault(r.URL.Query().Get("page_size"), 50)
	if pageSize > 200 { pageSize = 200 }
	offset := (page - 1) * pageSize
	conditions := []string{}
	args := []interface{}{}
	argIdx := 1
	if resource != "" { conditions = append(conditions, fmt.Sprintf("resource = $%d", argIdx)); args = append(args, resource); argIdx++ }
	if action != "" { conditions = append(conditions, fmt.Sprintf("action = $%d", argIdx)); args = append(args, action); argIdx++ }
	if actorID != "" { conditions = append(conditions, fmt.Sprintf("actor_id = $%d", argIdx)); args = append(args, actorID); argIdx++ }
	whereClause := ""
	if len(conditions) > 0 { whereClause = " WHERE " + strings.Join(conditions, " AND ") }
	var total int
	s.adminRepo.DB().QueryRowContext(r.Context(), "SELECT COUNT(*) FROM admin_audit_logs"+whereClause, args...).Scan(&total)
	query := `SELECT id, actor_id, actor_role, action, resource, resource_id, detail, changes, ip_address, user_agent, created_at FROM admin_audit_logs` + whereClause + fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", argIdx, argIdx+1)
	args = append(args, pageSize, offset)
	rows, err := s.adminRepo.DB().QueryContext(r.Context(), query, args...)
	if err != nil {
		slog.Warn("failed to list audit logs", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list audit logs")
		return
	}
	defer rows.Close()
	var logs []AuditEntry
	for rows.Next() {
		var entry AuditEntry
		var changesJSON []byte
		var resourceID, ipAddress, userAgent *string
		if err := rows.Scan(&entry.ID, &entry.ActorID, &entry.ActorRole, &entry.Action, &entry.Resource, &resourceID, &entry.Detail, &changesJSON, &ipAddress, &userAgent, &entry.CreatedAt); err != nil { continue }
		if resourceID != nil { entry.ResourceID = *resourceID }
		if ipAddress != nil { entry.IPAddress = *ipAddress }
		if userAgent != nil { entry.UserAgent = *userAgent }
		if len(changesJSON) > 0 { json.Unmarshal(changesJSON, &entry.Changes) }
		logs = append(logs, entry)
	}
	if logs == nil { logs = []AuditEntry{} }
	response.OK(w, map[string]interface{}{"logs": logs, "total": total, "page": page, "page_size": pageSize})
}
type BatchRequest struct {
	IDs    []string `json:"ids"`
	Action string  `json:"action"`
}

func (s *Server) handleAdminBatchModels(w http.ResponseWriter, r *http.Request) {
	var req BatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil { response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body"); return }
	if len(req.IDs) == 0 { response.Err(w, http.StatusBadRequest, "bad_request", "ids is required"); return }
	if s.adminRepo == nil { response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available"); return }
	affected := 0
	for _, id := range req.IDs {
		switch req.Action {
		case "delete":
			if err := s.adminRepo.DeleteModelConfig(r.Context(), id); err == nil { affected++ }
		case "activate", "deactivate":
			active := req.Action == "activate"
			if _, err := s.adminRepo.DB().ExecContext(r.Context(), "UPDATE model_configs SET is_active = $2, updated_at = NOW() WHERE id = $1", id, active); err == nil { affected++ }
		}
	}
	if s.llmSvc != nil { s.llmSvc.InvalidateCache() }
	s.writeAuditLog(r, "batch_"+req.Action, "model_config", "", fmt.Sprintf("Batch %s %d model configs", req.Action, affected), map[string]interface{}{"ids": req.IDs, "affected": affected})
	response.OK(w, map[string]interface{}{"action": req.Action, "affected": affected, "total": len(req.IDs)})
}
func (s *Server) handleAdminBatchAPIKeys(w http.ResponseWriter, r *http.Request) {
	var req BatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil { response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body"); return }
	if len(req.IDs) == 0 { response.Err(w, http.StatusBadRequest, "bad_request", "ids is required"); return }
	if s.adminRepo == nil { response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available"); return }
	affected := 0
	for _, id := range req.IDs {
		switch req.Action {
		case "delete":
			if err := s.adminRepo.DeleteAPIKey(r.Context(), id); err == nil { affected++ }
		case "activate", "deactivate":
			active := req.Action == "activate"
			if _, err := s.adminRepo.DB().ExecContext(r.Context(), "UPDATE api_keys SET is_active = $2, updated_at = NOW() WHERE id = $1", id, active); err == nil { affected++ }
		}
	}
	s.writeAuditLog(r, "batch_"+req.Action, "api_key", "", fmt.Sprintf("Batch %s %d API keys", req.Action, affected), map[string]interface{}{"ids": req.IDs, "affected": affected})
	response.OK(w, map[string]interface{}{"action": req.Action, "affected": affected, "total": len(req.IDs)})
}

func (s *Server) handleAdminBatchCronJobs(w http.ResponseWriter, r *http.Request) {
	var req BatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil { response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body"); return }
	if len(req.IDs) == 0 { response.Err(w, http.StatusBadRequest, "bad_request", "ids is required"); return }
	if s.adminRepo == nil { response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available"); return }
	affected := 0
	for _, id := range req.IDs {
		switch req.Action {
		case "delete":
			if err := s.adminRepo.DeleteCronJob(r.Context(), id); err == nil { affected++ }
		case "activate", "deactivate":
			active := req.Action == "activate"
			if _, err := s.adminRepo.DB().ExecContext(r.Context(), "UPDATE cron_jobs SET is_active = $2, updated_at = NOW() WHERE id = $1", id, active); err == nil { affected++ }
		}
	}
	s.writeAuditLog(r, "batch_"+req.Action, "cron_job", "", fmt.Sprintf("Batch %s %d cron jobs", req.Action, affected), map[string]interface{}{"ids": req.IDs, "affected": affected})
	response.OK(w, map[string]interface{}{"action": req.Action, "affected": affected, "total": len(req.IDs)})
}

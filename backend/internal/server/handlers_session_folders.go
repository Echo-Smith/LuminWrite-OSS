package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── Session ownership + organization (folders, batch, feedback history) ──

// assertTraceOwner reports whether the trace exists and belongs to userID.
// Legacy anonymous traces (user_id NULL) are ownerless: GetTraceUserID errors
// on them, so they resolve to false and the handler answers 404 — the admin
// trace views remain the only reader for those rows.
func (s *Server) assertTraceOwner(ctx context.Context, traceID, userID string) bool {
	if s.traces == nil || traceID == "" || userID == "" {
		return false
	}
	owner, err := s.traces.GetTraceUserID(ctx, traceID)
	if err != nil || owner == "" {
		return false
	}
	return owner == userID
}

// handleListSessionFolders lists the caller's session folders.
// GET /api/v2/session-folders
func (s *Server) handleListSessionFolders(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	if s.traces == nil {
		response.OK(w, map[string]interface{}{"folders": []interface{}{}})
		return
	}
	folders, err := s.traces.ListSessionFolders(r.Context(), user.Sub)
	if err != nil {
		slog.Warn("failed to list session folders", "error", err, "user_id", user.Sub)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list folders")
		return
	}
	if folders == nil {
		folders = []map[string]interface{}{}
	}
	response.OK(w, map[string]interface{}{"folders": folders})
}

// handleCreateSessionFolder creates a folder for the caller.
// POST /api/v2/session-folders  Body: { "name": "..." }
func (s *Server) handleCreateSessionFolder(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	if s.traces == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	folder, err := s.traces.CreateSessionFolder(r.Context(), user.Sub, body.Name)
	if err != nil {
		if strings.Contains(err.Error(), "1-64 characters") || strings.Contains(err.Error(), "invalid user") {
			response.Err(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique") {
			response.Err(w, http.StatusConflict, "folder_exists", "a folder with this name already exists")
			return
		}
		slog.Warn("failed to create session folder", "error", err, "user_id", user.Sub)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to create folder")
		return
	}
	response.Created(w, folder)
}

// handleRenameSessionFolder renames one owned folder.
// PUT /api/v2/session-folders/{folderId}  Body: { "name": "..." }
func (s *Server) handleRenameSessionFolder(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	if s.traces == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}
	folderID := chi.URLParam(r, "folderId")
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	if err := s.traces.RenameSessionFolder(r.Context(), user.Sub, folderID, body.Name); err != nil {
		if strings.Contains(err.Error(), "not found") {
			response.Err(w, http.StatusNotFound, "not_found", "folder not found")
			return
		}
		if strings.Contains(err.Error(), "1-64 characters") {
			response.Err(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique") {
			response.Err(w, http.StatusConflict, "folder_exists", "a folder with this name already exists")
			return
		}
		slog.Warn("failed to rename session folder", "error", err, "folder_id", folderID)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to rename folder")
		return
	}
	response.OK(w, map[string]interface{}{"renamed": true})
}

// handleDeleteSessionFolder deletes one owned folder. Member sessions survive
// (folder_id SET NULL via the FK).
// DELETE /api/v2/session-folders/{folderId}
func (s *Server) handleDeleteSessionFolder(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	if s.traces == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}
	folderID := chi.URLParam(r, "folderId")
	if err := s.traces.DeleteSessionFolder(r.Context(), user.Sub, folderID); err != nil {
		if strings.Contains(err.Error(), "not found") {
			response.Err(w, http.StatusNotFound, "not_found", "folder not found")
			return
		}
		slog.Warn("failed to delete session folder", "error", err, "folder_id", folderID)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to delete folder")
		return
	}
	response.OK(w, map[string]interface{}{"deleted": true})
}

// handleBatchSessions applies one organization action to a set of the caller's
// traces. Contract matches the sidebar store's existing calls.
// POST /api/v2/sessions/batch
// Body: { "action": "delete|archive|unarchive|move", "trace_ids": [...], "folder_id": "..." }
func (s *Server) handleBatchSessions(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	if s.traces == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}
	var body struct {
		Action   string   `json:"action"`
		TraceIDs []string `json:"trace_ids"`
		FolderID string   `json:"folder_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	affected, err := s.traces.BatchUpdateTraces(r.Context(), user.Sub, body.TraceIDs, body.Action, body.FolderID)
	if err != nil {
		if strings.Contains(err.Error(), "invalid request") || strings.Contains(err.Error(), "unsupported action") || strings.Contains(err.Error(), "too many") {
			response.Err(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		slog.Warn("failed to batch update sessions", "error", err, "user_id", user.Sub, "action", body.Action)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to apply batch action")
		return
	}
	response.OK(w, map[string]interface{}{"affected": affected})
}

// handleGetMyFeedback lists the caller's own feedback segments (read-only —
// feedback is one-shot by design).
// GET /api/v2/feedback/mine?limit=100
func (s *Server) handleGetMyFeedback(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	if s.traces == nil {
		response.OK(w, map[string]interface{}{"feedback": []interface{}{}, "total": 0})
		return
	}
	limit := parseIntDefault(r.URL.Query().Get("limit"), 100)
	rows, err := s.traces.GetMyFeedback(r.Context(), user.Sub, limit)
	if err != nil {
		slog.Warn("failed to list my feedback", "error", err, "user_id", user.Sub)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list feedback")
		return
	}
	if rows == nil {
		rows = []*database.MyFeedbackRow{}
	}
	response.OK(w, map[string]interface{}{"feedback": rows, "total": len(rows)})
}

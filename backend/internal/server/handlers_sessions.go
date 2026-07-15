package server

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
	"golang.org/x/crypto/bcrypt"
)

// ─── User Sessions (JWT-protected) ────────────────────────

// handleListUserSessions lists the authenticated user's writing traces.
//
// GET /api/v2/sessions?page=1&page_size=20
// Header: Authorization: Bearer <jwt>
func (s *Server) handleListUserSessions(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	if s.traces == nil {
		response.OK(w, map[string]interface{}{"sessions": []interface{}{}, "total": 0})
		return
	}

	page := parseIntDefault(r.URL.Query().Get("page"), 1)
	pageSize := parseIntDefault(r.URL.Query().Get("page_size"), 50)

	sessions, total, err := s.traces.ListTraces(r.Context(), user.Sub, page, pageSize)
	if err != nil {
		slog.Warn("failed to list user sessions", "error", err, "user_id", user.Sub)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list sessions")
		return
	}

	if sessions == nil {
		sessions = []map[string]interface{}{}
	}

	response.OK(w, map[string]interface{}{
		"sessions": sessions,
		"total":    total,
	})
}

// handleGetUserSession retrieves full detail of a specific trace for the authenticated user.
//
// GET /api/v2/sessions/{traceId}
// Header: Authorization: Bearer <jwt>
func (s *Server) handleGetUserSession(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	traceID := chi.URLParam(r, "traceId")
	if traceID == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "trace_id is required")
		return
	}

	if s.traces == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	detail, err := s.traces.GetTrace(r.Context(), traceID)
	if err != nil {
		slog.Warn("failed to get user session", "error", err, "trace_id", traceID)
		response.Err(w, http.StatusNotFound, "not_found", "session not found")
		return
	}

	response.OK(w, detail)
}

// handleDeleteUserSession soft-deletes a trace for the authenticated user.
// The trace remains visible in admin but disappears from the user's sidebar.
//
// DELETE /api/v2/sessions/{traceId}
// Header: Authorization: Bearer <jwt>
func (s *Server) handleDeleteUserSession(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	traceID := chi.URLParam(r, "traceId")
	if traceID == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "trace_id is required")
		return
	}

	if s.traces == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	err := s.traces.SoftDeleteTrace(r.Context(), traceID, user.Sub)
	if err != nil {
		slog.Warn("failed to delete user session", "error", err, "trace_id", traceID)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to delete session")
		return
	}

	response.OK(w, map[string]interface{}{"deleted": true})
}

// handleChangePassword allows an authenticated user to change their password.
//
// POST /api/v2/auth/change-password
// Header: Authorization: Bearer <jwt>
// Body: { "old_password": "...", "new_password": "..." }
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	var body struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	if len(body.NewPassword) < 6 {
		response.Err(w, http.StatusBadRequest, "weak_password", "password must be at least 6 characters")
		return
	}

	if s.adminRepo == nil || s.adminRepo.DB() == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	// Verify old password
	var storedHash *string
	err := s.adminRepo.DB().QueryRowContext(r.Context(),
		`SELECT password_hash FROM users WHERE id = $1`,
		user.Sub,
	).Scan(&storedHash)
	if err != nil || storedHash == nil || *storedHash == "" {
		response.Err(w, http.StatusBadRequest, "no_password", "no password set for this account")
		return
	}

	if bcrypt.CompareHashAndPassword([]byte(*storedHash), []byte(body.OldPassword)) != nil {
		response.Err(w, http.StatusUnauthorized, "wrong_password", "old password is incorrect")
		return
	}

	// Hash new password
	newHash, err := bcrypt.GenerateFromPassword([]byte(body.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		slog.Error("failed to hash password", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to update password")
		return
	}

	_, err = s.adminRepo.DB().ExecContext(r.Context(),
		`UPDATE users SET password_hash = $1 WHERE id = $2`,
		newHash, user.Sub,
	)
	if err != nil {
		slog.Error("failed to update password", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to update password")
		return
	}

	response.OK(w, map[string]interface{}{"changed": true})
}

package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/editorial"
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

// handleGetSessionArtifacts retrieves all writing process artifacts for a session.
//
// GET /api/v2/sessions/{traceId}/artifacts
// Header: Authorization: Bearer <jwt>
//
// Returns the full chain of intermediate products (search results, research
// brief, outline, draft, review report, etc.) recorded during the writing
// process, providing complete traceability.
func (s *Server) handleGetSessionArtifacts(w http.ResponseWriter, r *http.Request) {
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

	// Look up the editorial task ID linked to this trace
	taskID, err := s.traces.GetEditorialTaskID(r.Context(), traceID)
	if err != nil || taskID == "" {
		response.OK(w, map[string]interface{}{
			"artifacts": []interface{}{},
			"task_id":   "",
		})
		return
	}

	// Fetch all artifacts for the task
	if s.editorialSvc == nil {
		response.OK(w, map[string]interface{}{
			"artifacts": []interface{}{},
			"task_id":   taskID,
		})
		return
	}

	artifacts, err := s.editorialSvc.ListArtifacts(r.Context(), taskID)
	if err != nil {
		slog.Warn("failed to list session artifacts", "error", err, "trace_id", traceID, "task_id", taskID)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list artifacts")
		return
	}

	if artifacts == nil {
		artifacts = []editorial.Artifact{}
	}

	response.OK(w, map[string]interface{}{
		"artifacts": artifacts,
		"task_id":   taskID,
	})
}

// handleGetSessionEvents retrieves the append-only event log for a session.
//
// GET /api/v2/sessions/{traceId}/events?level=all|coarse|errors
// Header: Authorization: Bearer <jwt>
//
// Returns the complete sequence of discrete events recorded during
// the agent execution lifecycle. This enables:
//   - Session replay: reconstruct the UI from events in order
//   - Fork from step: identify the event boundary to re-run from
//   - Debug/audit: inspect the exact sequence of step transitions
//
// The events are returned in seq order, oldest first.
// Query parameter `event_type` can filter to specific event types
// (e.g. ?event_type=step.start,step.complete).
func (s *Server) handleGetSessionEvents(w http.ResponseWriter, r *http.Request) {
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

	if s.sessionEvents == nil {
		response.OK(w, map[string]interface{}{
			"events": []interface{}{},
			"total":  0,
		})
		return
	}

	// Parse optional event_type filter (comma-separated)
	var eventTypes []string
	if et := r.URL.Query().Get("event_type"); et != "" {
		for _, t := range strings.Split(et, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				eventTypes = append(eventTypes, t)
			}
		}
	}

	events, err := s.sessionEvents.GetEvents(r.Context(), traceID, eventTypes...)
	if err != nil {
		slog.Warn("failed to get session events", "error", err, "trace_id", traceID)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to retrieve events")
		return
	}

	if events == nil {
		events = []database.SessionEvent{}
	}

	response.OK(w, map[string]interface{}{
		"events": events,
		"total":  len(events),
		"trace_id": traceID,
	})
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

// handleUpdateProfile allows an authenticated user to update their profile (username).
//
// POST /api/v2/auth/update-profile
// Header: Authorization: Bearer <jwt>
// Body: { "username": "..." }
func (s *Server) handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	var body struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	if len(body.Username) < 2 || len(body.Username) > 64 {
		response.Err(w, http.StatusBadRequest, "bad_request", "username must be 2-64 characters")
		return
	}

	if s.adminRepo == nil || s.adminRepo.DB() == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	// Check username uniqueness (exclude current user)
	var exists bool
	err := s.adminRepo.DB().QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM users WHERE name = $1 AND id != $2)
	`, body.Username, user.Sub).Scan(&exists)
	if err != nil {
		slog.Error("failed to check username uniqueness", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to check username")
		return
	}
	if exists {
		response.Err(w, http.StatusConflict, "username_taken", "username already exists")
		return
	}

	// Update username in users table (both uid and name columns)
	_, err = s.adminRepo.DB().ExecContext(ctx, `
		UPDATE users SET uid = $1, name = $1, updated_at = NOW() WHERE id = $2
	`, body.Username, user.Sub)
	if err != nil {
		slog.Error("failed to update username", "error", err, "user_id", user.Sub)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to update profile")
		return
	}

	slog.Info("user profile updated", "user_id", user.Sub, "new_username", body.Username)

	response.OK(w, map[string]interface{}{
		"updated":  true,
		"username": body.Username,
	})
}

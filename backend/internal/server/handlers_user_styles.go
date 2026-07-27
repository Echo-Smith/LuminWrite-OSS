package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── Helpers ─────────────────────────────────────────────

// requireUserAndStore extracts the authenticated user ID and returns the UserStyleStore.
// Writes an error response and returns ok=false if the request is invalid.
func (s *Server) requireUserAndStore(w http.ResponseWriter, r *http.Request) (userID string, store *database.UserStyleStore, ok bool) {
	userID = s.getUserIDFromRequest(r)
	if userID == "anonymous" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "login required")
		return "", nil, false
	}
	if s.userStyleStore == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return "", nil, false
	}
	return userID, s.userStyleStore, true
}

// requireOwnedProfile loads a profile and verifies ownership.
// Writes an error response and returns nil if not found or not owned.
func (s *Server) requireOwnedProfile(w http.ResponseWriter, r *http.Request, userID string) (*database.UserStyleProfile, bool) {
	id := chi.URLParam(r, "id")
	p, err := s.userStyleStore.GetProfile(r.Context(), id)
	if err != nil {
		response.Err(w, http.StatusNotFound, "not_found", "style not found")
		return nil, false
	}
	if p.OwnerUserID != userID {
		response.Err(w, http.StatusForbidden, "forbidden", "you don't own this style")
		return nil, false
	}
	return p, true
}

// ─── User Custom Styles (my-styles) ──────────────────────

func (s *Server) handleListMyStyles(w http.ResponseWriter, r *http.Request) {
	userID, store, ok := s.requireUserAndStore(w, r)
	if !ok {
		return
	}

	styles, err := store.ListProfilesByOwner(r.Context(), userID)
	if err != nil {
		slog.Error("failed to list user styles", "error", err, "user_id", userID)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list styles")
		return
	}

	response.OK(w, map[string]any{"styles": styles})
}

func (s *Server) handleCreateMyStyle(w http.ResponseWriter, r *http.Request) {
	userID, store, ok := s.requireUserAndStore(w, r)
	if !ok {
		return
	}

	var req struct {
		Slug        string          `json:"slug"`
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Config      json.RawMessage `json:"config,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	if req.Slug == "" || req.Name == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "slug and name are required")
		return
	}

	p, err := store.CreateProfile(r.Context(), userID, req.Slug, req.Name, req.Description)
	if err != nil {
		response.Err(w, http.StatusConflict, "conflict", "slug already exists for this user")
		return
	}

	// If config is provided, save it as version 1
	if len(req.Config) > 0 {
		if _, err := store.SaveVersion(r.Context(), p.ID, req.Config, "initial version"); err != nil {
			slog.Error("failed to save initial version", "error", err, "profile_id", p.ID)
		}
	}

	response.Created(w, p)
}

func (s *Server) handleGetMyStyle(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := s.requireUserAndStore(w, r)
	if !ok {
		return
	}

	p, ok := s.requireOwnedProfile(w, r, userID)
	if !ok {
		return
	}

	result := map[string]any{
		"id":             p.ID,
		"slug":           p.Slug,
		"name":           p.Name,
		"description":    p.Description,
		"status":         p.Status,
		"current_version": p.CurrentVersion,
		"created_at":     p.CreatedAt,
		"updated_at":     p.UpdatedAt,
	}

	if p.CurrentVersion > 0 {
		if v, err := s.userStyleStore.GetLatestVersion(r.Context(), p.ID); err == nil {
			result["config"] = v.Config
		}
	}

	response.OK(w, result)
}

func (s *Server) handleUpdateMyStyle(w http.ResponseWriter, r *http.Request) {
	userID, store, ok := s.requireUserAndStore(w, r)
	if !ok {
		return
	}

	p, ok := s.requireOwnedProfile(w, r, userID)
	if !ok {
		return
	}

	var req struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Config      json.RawMessage `json:"config,omitempty"`
		Changelog   string          `json:"changelog,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	if req.Name != "" {
		p.Name = req.Name
	}
	if req.Description != "" {
		p.Description = req.Description
	}

	if err := store.UpdateProfile(r.Context(), p.ID, p.Name, p.Description); err != nil {
		slog.Error("failed to update user style", "error", err, "profile_id", p.ID)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to update style")
		return
	}

	if len(req.Config) > 0 {
		v, err := store.SaveVersion(r.Context(), p.ID, req.Config, req.Changelog)
		if err != nil {
			slog.Error("failed to save version", "error", err, "profile_id", p.ID)
			response.Err(w, http.StatusInternalServerError, "internal_error", "failed to save version")
			return
		}
		response.OK(w, map[string]any{
			"id":         p.ID,
			"version":    v.Version,
			"version_id": v.ID,
			"message":    "style updated with new version",
		})
		return
	}

	response.OK(w, map[string]any{"id": p.ID, "message": "style updated"})
}

func (s *Server) handleDeleteMyStyle(w http.ResponseWriter, r *http.Request) {
	userID, store, ok := s.requireUserAndStore(w, r)
	if !ok {
		return
	}

	p, ok := s.requireOwnedProfile(w, r, userID)
	if !ok {
		return
	}

	if err := store.DeleteProfile(r.Context(), p.ID); err != nil {
		slog.Error("failed to delete user style", "error", err, "profile_id", p.ID)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to delete style")
		return
	}

	response.OK(w, map[string]any{"message": "style deleted"})
}

func (s *Server) handleSubmitMyStyleForReview(w http.ResponseWriter, r *http.Request) {
	userID, store, ok := s.requireUserAndStore(w, r)
	if !ok {
		return
	}

	p, ok := s.requireOwnedProfile(w, r, userID)
	if !ok {
		return
	}

	if p.CurrentVersion == 0 {
		response.Err(w, http.StatusBadRequest, "no_version", "save at least one version before submitting")
		return
	}
	if p.Status == "pending_review" {
		response.Err(w, http.StatusConflict, "already_pending", "this style is already pending review")
		return
	}

	req, err := store.SubmitForReview(r.Context(), p.ID)
	if err != nil {
		slog.Error("failed to submit for review", "error", err, "profile_id", p.ID)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to submit for review")
		return
	}

	response.OK(w, map[string]any{
		"review_id": req.ID,
		"status":    req.Status,
		"message":   "style submitted for review",
	})
}

// ─── AI Style Builder ────────────────────────────────────

func (s *Server) handleCreateBuilderSession(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserIDFromRequest(r)
	if userID == "anonymous" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "login required")
		return
	}
	if s.styleBuilder == nil {
		response.Err(w, http.StatusServiceUnavailable, "llm_unavailable", "LLM not configured")
		return
	}

	session := s.styleBuilder.CreateSession(userID)
	response.Created(w, map[string]any{
		"session_id": session.ID,
		"messages":   session.Messages,
	})
}

func (s *Server) handleSendBuilderMessage(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserIDFromRequest(r)
	if userID == "anonymous" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "login required")
		return
	}
	if s.styleBuilder == nil {
		response.Err(w, http.StatusServiceUnavailable, "llm_unavailable", "LLM not configured")
		return
	}

	sessionID := chi.URLParam(r, "id")
	session, ok := s.styleBuilder.GetSession(sessionID)
	if !ok {
		response.Err(w, http.StatusNotFound, "not_found", "session not found")
		return
	}
	if session.UserID != userID {
		response.Err(w, http.StatusForbidden, "forbidden", "you don't own this session")
		return
	}

	var req struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	if req.Message == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "message is required")
		return
	}

	resp, err := s.styleBuilder.SendMessage(r.Context(), sessionID, req.Message)
	if err != nil {
		slog.Error("style builder message failed", "error", err, "session_id", sessionID)
		response.Err(w, http.StatusInternalServerError, "internal_error", "AI style builder failed")
		return
	}

	response.OK(w, resp)
}

func (s *Server) handleCommitBuilderSession(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserIDFromRequest(r)
	if userID == "anonymous" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "login required")
		return
	}
	if s.styleBuilder == nil || s.userStyleStore == nil {
		response.Err(w, http.StatusServiceUnavailable, "unavailable", "service not available")
		return
	}

	sessionID := chi.URLParam(r, "id")
	session, ok := s.styleBuilder.GetSession(sessionID)
	if !ok || !session.Ready || session.Profile == nil {
		response.Err(w, http.StatusBadRequest, "not_ready", "style is not ready yet, continue the conversation")
		return
	}
	if session.UserID != userID {
		response.Err(w, http.StatusForbidden, "forbidden", "you don't own this session")
		return
	}

	p := session.Profile

	created, err := s.userStyleStore.CreateProfile(r.Context(), userID, p.Slug, p.Name, p.Description)
	if err != nil {
		response.Err(w, http.StatusConflict, "conflict", "slug already exists for this user")
		return
	}

	configJSON, _ := json.Marshal(p)
	if _, err := s.userStyleStore.SaveVersion(r.Context(), created.ID, configJSON, "AI generated initial version"); err != nil {
		slog.Error("failed to save AI generated version", "error", err, "profile_id", created.ID)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to save style version")
		return
	}

	s.styleBuilder.DeleteSession(sessionID)

	response.OK(w, map[string]any{
		"profile_id": created.ID,
		"slug":       created.Slug,
		"name":       created.Name,
		"message":    "style created successfully",
	})
}

// ─── Admin: Pending Style Reviews ────────────────────────

func (s *Server) handleAdminListPendingStyles(w http.ResponseWriter, r *http.Request) {
	if s.userStyleStore == nil {
		response.OK(w, map[string]any{"reviews": []any{}})
		return
	}

	reviews, err := s.userStyleStore.ListPendingReviews(r.Context())
	if err != nil {
		slog.Error("failed to list pending reviews", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list pending reviews")
		return
	}

	response.OK(w, map[string]any{"reviews": reviews})
}

func (s *Server) handleAdminApproveStyle(w http.ResponseWriter, r *http.Request) {
	reviewID := chi.URLParam(r, "id")
	if s.userStyleStore == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	if err := s.userStyleStore.ApproveReview(r.Context(), reviewID, "admin"); err != nil {
		slog.Error("failed to approve review", "error", err, "review_id", reviewID)
		if errors.Is(err, database.ErrUserStyleNotFound) {
			response.Err(w, http.StatusNotFound, "not_found", "review request not found")
		} else {
			response.Err(w, http.StatusInternalServerError, "internal_error", "approval failed")
		}
		return
	}

	// Reload profiles from DB to pick up the new community style
	if s.profiles != nil {
		s.profiles.LoadFromDB()
		slog.Info("profiles reloaded after community style approval")
	}

	response.OK(w, map[string]any{
		"review_id": reviewID,
		"status":    "approved",
		"message":   "style approved and published to global catalog",
	})
}

func (s *Server) handleAdminRejectStyle(w http.ResponseWriter, r *http.Request) {
	reviewID := chi.URLParam(r, "id")
	if s.userStyleStore == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	var req struct {
		Note string `json:"note"`
	}
	// Body is optional for rejection; ignore decode errors
	_ = json.NewDecoder(r.Body).Decode(&req)

	if err := s.userStyleStore.RejectReview(r.Context(), reviewID, "admin", req.Note); err != nil {
		slog.Error("failed to reject review", "error", err, "review_id", reviewID)
		if errors.Is(err, database.ErrUserStyleNotFound) {
			response.Err(w, http.StatusNotFound, "not_found", "review request not found")
		} else {
			response.Err(w, http.StatusInternalServerError, "internal_error", "rejection failed")
		}
		return
	}

	response.OK(w, map[string]any{
		"review_id": reviewID,
		"status":    "rejected",
		"message":   "style rejected",
	})
}

// ─── Merged Styles List ──────────────────────────────────

// handleListStylesWithUserStyles returns global styles + user's private styles.
// Logged-in users see both; anonymous users see only global styles.
func (s *Server) handleListStylesWithUserStyles(w http.ResponseWriter, r *http.Request) {
	styles := s.profiles.List()

	// Merge user's private styles if authenticated
	userID := s.getUserIDFromRequest(r)
	if userID != "anonymous" && s.userStyleStore != nil {
		// Single query fetches all profiles + latest version config (no N+1)
		profiles, err := s.userStyleStore.ListProfilesWithLatestVersion(r.Context(), userID)
		if err == nil {
			for _, pwc := range profiles {
				if pwc.ConfigJSON == "" {
					continue
				}
				var p profile.StyleProfile
				if json.Unmarshal([]byte(pwc.ConfigJSON), &p) != nil {
					continue
				}
				styles = append(styles, profile.StyleOption{
					Slug:        "my_" + pwc.Slug,
					Name:        pwc.Name + " (我的风格)",
					Description: p.Description,
					Version:     pwc.CurrentVersion,
					WordRange:   [2]int{p.WordRange.Min, p.WordRange.Max},
					Tags:        append(p.Tags, "自定义"),
				})
			}
		}
	}

	response.OK(w, map[string]any{"styles": styles})
}

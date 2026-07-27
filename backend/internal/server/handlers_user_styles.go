package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── User Custom Styles (my-styles) ──────────────────────

// handleListMyStyles returns the current user's custom style profiles.
func (s *Server) handleListMyStyles(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserIDFromRequest(r)
	if userID == "anonymous" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "login required")
		return
	}

	if s.userStyleStore == nil {
		response.OK(w, map[string]interface{}{"styles": []interface{}{}})
		return
	}

	styles, err := s.userStyleStore.ListProfilesByOwner(r.Context(), userID)
	if err != nil {
		slog.Error("failed to list user styles", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list styles")
		return
	}

	response.OK(w, map[string]interface{}{"styles": styles})
}

// handleCreateMyStyle creates a new user style profile (draft).
func (s *Server) handleCreateMyStyle(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserIDFromRequest(r)
	if userID == "anonymous" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "login required")
		return
	}

	var req struct {
		Slug        string `json:"slug"`
		Name        string `json:"name"`
		Description string `json:"description"`
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

	if s.userStyleStore == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	p, err := s.userStyleStore.CreateProfile(r.Context(), userID, req.Slug, req.Name, req.Description)
	if err != nil {
		slog.Error("failed to create user style", "error", err)
		response.Err(w, http.StatusConflict, "conflict", "slug already exists for this user")
		return
	}

	// If config is provided, save it as version 1
	if len(req.Config) > 0 {
		_, err := s.userStyleStore.SaveVersion(r.Context(), p.ID, req.Config, "initial version")
		if err != nil {
			slog.Error("failed to save initial version", "error", err)
		}
	}

	response.Created(w, p)
}

// handleGetMyStyle returns a single user style profile with its latest version config.
func (s *Server) handleGetMyStyle(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserIDFromRequest(r)
	if userID == "anonymous" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "login required")
		return
	}

	id := chi.URLParam(r, "id")
	if s.userStyleStore == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	p, err := s.userStyleStore.GetProfile(r.Context(), id)
	if err != nil {
		response.Err(w, http.StatusNotFound, "not_found", "style not found")
		return
	}

	if p.OwnerUserID != userID {
		response.Err(w, http.StatusForbidden, "forbidden", "you don't own this style")
		return
	}

	// Include latest version config if available
	result := map[string]interface{}{
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
		v, err := s.userStyleStore.GetLatestVersion(r.Context(), id)
		if err == nil {
			result["config"] = v.Config
		}
	}

	response.OK(w, result)
}

// handleUpdateMyStyle updates the mutable fields and optionally saves a new version.
func (s *Server) handleUpdateMyStyle(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserIDFromRequest(r)
	if userID == "anonymous" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "login required")
		return
	}

	id := chi.URLParam(r, "id")
	if s.userStyleStore == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	p, err := s.userStyleStore.GetProfile(r.Context(), id)
	if err != nil {
		response.Err(w, http.StatusNotFound, "not_found", "style not found")
		return
	}

	if p.OwnerUserID != userID {
		response.Err(w, http.StatusForbidden, "forbidden", "you don't own this style")
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

	if err := s.userStyleStore.UpdateProfile(r.Context(), id, p.Name, p.Description); err != nil {
		slog.Error("failed to update user style", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to update style")
		return
	}

	// Save new version if config is provided
	if len(req.Config) > 0 {
		v, err := s.userStyleStore.SaveVersion(r.Context(), id, req.Config, req.Changelog)
		if err != nil {
			slog.Error("failed to save version", "error", err)
			response.Err(w, http.StatusInternalServerError, "internal_error", "failed to save version")
			return
		}
		response.OK(w, map[string]interface{}{
			"id":         id,
			"version":    v.Version,
			"version_id": v.ID,
			"message":    "style updated with new version",
		})
		return
	}

	response.OK(w, map[string]interface{}{
		"id":      id,
		"message": "style updated",
	})
}

// handleDeleteMyStyle deletes a user style profile.
func (s *Server) handleDeleteMyStyle(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserIDFromRequest(r)
	if userID == "anonymous" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "login required")
		return
	}

	id := chi.URLParam(r, "id")
	if s.userStyleStore == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	p, err := s.userStyleStore.GetProfile(r.Context(), id)
	if err != nil {
		response.Err(w, http.StatusNotFound, "not_found", "style not found")
		return
	}

	if p.OwnerUserID != userID {
		response.Err(w, http.StatusForbidden, "forbidden", "you don't own this style")
		return
	}

	if err := s.userStyleStore.DeleteProfile(r.Context(), id); err != nil {
		slog.Error("failed to delete user style", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to delete style")
		return
	}

	response.OK(w, map[string]interface{}{"message": "style deleted"})
}

// handleSubmitMyStyleForReview submits a user style for admin review.
func (s *Server) handleSubmitMyStyleForReview(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserIDFromRequest(r)
	if userID == "anonymous" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "login required")
		return
	}

	id := chi.URLParam(r, "id")
	if s.userStyleStore == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	p, err := s.userStyleStore.GetProfile(r.Context(), id)
	if err != nil {
		response.Err(w, http.StatusNotFound, "not_found", "style not found")
		return
	}

	if p.OwnerUserID != userID {
		response.Err(w, http.StatusForbidden, "forbidden", "you don't own this style")
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

	req, err := s.userStyleStore.SubmitForReview(r.Context(), id)
	if err != nil {
		slog.Error("failed to submit for review", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to submit for review")
		return
	}

	response.OK(w, map[string]interface{}{
		"review_id": req.ID,
		"status":    req.Status,
		"message":   "style submitted for review",
	})
}

// ─── AI Style Builder ────────────────────────────────────

// handleCreateBuilderSession starts a new AI style builder conversation.
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
	response.Created(w, map[string]interface{}{
		"session_id": session.ID,
		"messages":   session.Messages,
	})
}

// handleSendBuilderMessage sends a message in an AI style builder session.
func (s *Server) handleSendBuilderMessage(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserIDFromRequest(r)
	if userID == "anonymous" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "login required")
		return
	}

	sessionID := chi.URLParam(r, "id")
	if s.styleBuilder == nil {
		response.Err(w, http.StatusServiceUnavailable, "llm_unavailable", "LLM not configured")
		return
	}

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
		slog.Error("style builder message failed", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "AI style builder failed")
		return
	}

	response.OK(w, resp)
}

// handleCommitBuilderSession finalizes the AI-generated style and saves it as a user style profile.
func (s *Server) handleCommitBuilderSession(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserIDFromRequest(r)
	if userID == "anonymous" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "login required")
		return
	}

	sessionID := chi.URLParam(r, "id")
	if s.styleBuilder == nil || s.userStyleStore == nil {
		response.Err(w, http.StatusServiceUnavailable, "unavailable", "service not available")
		return
	}

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

	// Create user style profile
	created, err := s.userStyleStore.CreateProfile(r.Context(), userID, p.Slug, p.Name, p.Description)
	if err != nil {
		response.Err(w, http.StatusConflict, "conflict", "slug already exists for this user")
		return
	}

	// Save the config as version 1
	configJSON, _ := json.Marshal(p)
	_, err = s.userStyleStore.SaveVersion(r.Context(), created.ID, configJSON, "AI generated initial version")
	if err != nil {
		slog.Error("failed to save AI generated version", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to save style version")
		return
	}

	// Clean up session
	s.styleBuilder.DeleteSession(sessionID)

	response.OK(w, map[string]interface{}{
		"profile_id": created.ID,
		"slug":       created.Slug,
		"name":       created.Name,
		"message":    "style created successfully",
	})
}

// ─── Admin: Pending Style Reviews ────────────────────────

// handleAdminListPendingStyles returns all pending style review requests.
func (s *Server) handleAdminListPendingStyles(w http.ResponseWriter, r *http.Request) {
	if s.userStyleStore == nil {
		response.OK(w, map[string]interface{}{"reviews": []interface{}{}})
		return
	}

	reviews, err := s.userStyleStore.ListPendingReviews(r.Context())
	if err != nil {
		slog.Error("failed to list pending reviews", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list pending reviews")
		return
	}

	response.OK(w, map[string]interface{}{"reviews": reviews})
}

// handleAdminApproveStyle approves a pending style review.
func (s *Server) handleAdminApproveStyle(w http.ResponseWriter, r *http.Request) {
	reviewID := chi.URLParam(r, "id")
	if s.userStyleStore == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	reviewer := "admin"
	if err := s.userStyleStore.ApproveReview(r.Context(), reviewID, reviewer); err != nil {
		slog.Error("failed to approve review", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", fmt.Sprintf("approval failed: %s", err.Error()))
		return
	}

	// Reload profiles from DB to pick up the new community style
	if s.profiles != nil {
		s.profiles.LoadFromDB()
		slog.Info("profiles reloaded after community style approval")
	}

	response.OK(w, map[string]interface{}{
		"review_id": reviewID,
		"status":    "approved",
		"message":   "style approved and published to global catalog",
	})
}

// handleAdminRejectStyle rejects a pending style review.
func (s *Server) handleAdminRejectStyle(w http.ResponseWriter, r *http.Request) {
	reviewID := chi.URLParam(r, "id")
	if s.userStyleStore == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	var req struct {
		Note string `json:"note"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	reviewer := "admin"
	if err := s.userStyleStore.RejectReview(r.Context(), reviewID, reviewer, req.Note); err != nil {
		slog.Error("failed to reject review", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", fmt.Sprintf("rejection failed: %s", err.Error()))
		return
	}

	response.OK(w, map[string]interface{}{
		"review_id": reviewID,
		"status":    "rejected",
		"message":   "style rejected",
	})
}

// ─── Modified: handleListStyles ──────────────────────────

// handleListStylesWithUserStyles returns global styles + user's private styles.
// This replaces the original handleListStyles for the jwtOptionalMiddleware route.
func (s *Server) handleListStylesWithUserStyles(w http.ResponseWriter, r *http.Request) {
	// Get global styles
	styles := s.profiles.List()

	// Merge user's private styles if authenticated
	userID := s.getUserIDFromRequest(r)
	if userID != "anonymous" && s.userStyleStore != nil {
		userStyles, err := s.userStyleStore.ListProfilesByOwner(r.Context(), userID)
		if err == nil {
			for _, us := range userStyles {
				// Get latest version config
				if us.CurrentVersion > 0 {
					v, err := s.userStyleStore.GetLatestVersion(r.Context(), us.ID)
					if err == nil {
						var p profile.StyleProfile
						if json.Unmarshal([]byte(v.Config), &p) == nil {
							p.Slug = "my_" + us.Slug
							p.Name = us.Name + " (我的风格)"
							styles = append(styles, profile.StyleOption{
								Slug:        p.Slug,
								Name:        p.Name,
								Description: p.Description,
								Version:     us.CurrentVersion,
								WordRange:   [2]int{p.WordRange.Min, p.WordRange.Max},
								Tags:        append(p.Tags, "自定义"),
							})
						}
					}
				}
			}
		}
	}

	response.OK(w, map[string]interface{}{"styles": styles})
}

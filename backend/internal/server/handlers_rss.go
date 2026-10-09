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
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/services/rss"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── RSS Subscription Handlers ──────────────────────────
//
// User-scoped RSS subscription management (BYOK-style ownership: SQL
// user_id filtering, no RBAC permission). Feeds are validated at write time
// (scheme + host reachability); per-item ingestion is bounded.

// handleListRSSSubscriptions lists the caller's subscriptions.
// GET /api/v2/rss/subscriptions
func (s *Server) handleListRSSSubscriptions(w http.ResponseWriter, r *http.Request) {
	if s.adminRepo == nil {
		response.OK(w, map[string]interface{}{"subscriptions": []interface{}{}})
		return
	}
	user := userFromContext(r.Context())
	if user == nil {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	subs, err := s.adminRepo.ListRSSSubscriptions(r.Context(), user.Sub)
	if err != nil {
		slog.Warn("rss: list failed", "error", err, "user_id", user.Sub)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list subscriptions")
		return
	}
	if subs == nil {
		subs = []*database.RSSSubscription{}
	}
	response.OK(w, map[string]interface{}{"subscriptions": subs})
}

// handleCreateRSSSubscription validates and stores a new feed.
// POST /api/v2/rss/subscriptions
// Body: { "feed_url": "...", "target_folder_id": "..." (optional), "max_items_per_tick": 3 }
func (s *Server) handleCreateRSSSubscription(w http.ResponseWriter, r *http.Request) {
	if s.adminRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}
	user := userFromContext(r.Context())
	if user == nil {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	var req struct {
		FeedURL         string `json:"feed_url"`
		TargetFolderID  string `json:"target_folder_id"`
		MaxItemsPerTick int    `json:"max_items_per_tick"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	req.FeedURL = strings.TrimSpace(req.FeedURL)
	if req.FeedURL == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "feed_url is required")
		return
	}
	if err := rss.ValidateFeedURL(req.FeedURL); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.MaxItemsPerTick < 1 || req.MaxItemsPerTick > 20 {
		req.MaxItemsPerTick = 3
	}

	// Fetch once so the user gets immediate feedback (and we learn the
	// feed's title before the first scheduled tick).
	feed, _, err := rss.FetchFeed(r.Context(), req.FeedURL, rss.FetchOptions{})
	if err != nil {
		slog.Info("rss: create-time fetch failed", "url", req.FeedURL, "error", err)
		response.Err(w, http.StatusBadGateway, "feed_unreachable", "无法读取该订阅源："+err.Error())
		return
	}

	created, err := s.adminRepo.CreateRSSSubscription(r.Context(), &database.RSSSubscription{
		UserID:          user.Sub,
		FeedURL:         req.FeedURL,
		Title:           feed.Title,
		SiteURL:         feed.SiteURL,
		Description:     feed.Description,
		TargetFolderID:  req.TargetFolderID,
		MaxItemsPerTick: req.MaxItemsPerTick,
		IsActive:        true,
	})
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique") {
			response.Err(w, http.StatusConflict, "subscription_exists", "你已经订阅过这个源了")
			return
		}
		slog.Warn("rss: create failed", "error", err, "user_id", user.Sub)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to create subscription")
		return
	}

	// Import the first batch right away so a new subscription is useful
	// immediately instead of after the next cron tick.
	go func() {
		tickCtx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		if result := s.ingestRSSSubscription(tickCtx, created, rss.FetchFeed, created.MaxItemsPerTick); result.Error != "" {
			slog.Warn("rss: initial import failed", "sub", created.ID, "error", result.Error)
		} else {
			slog.Info("rss: initial import done", "sub", created.ID, "imported", result.Imported)
		}
	}()

	response.Created(w, created)
}

// handleUpdateRSSSubscription edits an owned subscription.
// PUT /api/v2/rss/subscriptions/{id}
func (s *Server) handleUpdateRSSSubscription(w http.ResponseWriter, r *http.Request) {
	if s.adminRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}
	user := userFromContext(r.Context())
	if user == nil {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")

	var req struct {
		Title           string `json:"title"`
		TargetFolderID  string `json:"target_folder_id"`
		MaxItemsPerTick int    `json:"max_items_per_tick"`
		IsActive        *bool  `json:"is_active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	current, err := s.adminRepo.GetRSSSubscription(r.Context(), user.Sub, id)
	if err != nil {
		response.Err(w, http.StatusNotFound, "not_found", "subscription not found")
		return
	}
	next := *current
	next.Title = req.Title
	next.TargetFolderID = req.TargetFolderID
	if req.MaxItemsPerTick >= 1 && req.MaxItemsPerTick <= 20 {
		next.MaxItemsPerTick = req.MaxItemsPerTick
	}
	if req.IsActive != nil {
		next.IsActive = *req.IsActive
	}
	updated, err := s.adminRepo.UpdateRSSSubscription(r.Context(), user.Sub, id, &next)
	if err != nil {
		response.Err(w, http.StatusNotFound, "not_found", "subscription not found")
		return
	}
	response.OK(w, updated)
}

// handleDeleteRSSSubscription removes an owned subscription (imported
// materials are kept).
// DELETE /api/v2/rss/subscriptions/{id}
func (s *Server) handleDeleteRSSSubscription(w http.ResponseWriter, r *http.Request) {
	if s.adminRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}
	user := userFromContext(r.Context())
	if user == nil {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	if err := s.adminRepo.DeleteRSSSubscription(r.Context(), user.Sub, id); err != nil {
		response.Err(w, http.StatusNotFound, "not_found", "subscription not found")
		return
	}
	response.OK(w, map[string]interface{}{"deleted": true})
}

// handleRefreshRSSSubscription triggers an immediate tick for one
// subscription (same bounded ingest path as the cron task).
// POST /api/v2/rss/subscriptions/{id}/refresh
func (s *Server) handleRefreshRSSSubscription(w http.ResponseWriter, r *http.Request) {
	if s.adminRepo == nil || s.kbMgr == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}
	user := userFromContext(r.Context())
	if user == nil {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	sub, err := s.adminRepo.GetRSSSubscription(r.Context(), user.Sub, id)
	if err != nil {
		response.Err(w, http.StatusNotFound, "not_found", "subscription not found")
		return
	}

	tickCtx, cancel := context.WithTimeout(r.Context(), 4*time.Minute)
	defer cancel()
	budget := sub.MaxItemsPerTick
	if budget < 1 || budget > 20 {
		budget = 3
	}
	result := s.ingestRSSSubscription(tickCtx, sub, rss.FetchFeed, budget)
	response.OK(w, result)
}

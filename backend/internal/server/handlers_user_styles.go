package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/services"
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

	// Use ListProfilesWithLatestVersion to include config JSON in a single query,
	// avoiding N+1 requests from the frontend.
	profiles, err := store.ListProfilesWithLatestVersion(r.Context(), userID)
	if err != nil {
		slog.Error("failed to list user styles", "error", err, "user_id", userID)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list styles")
		return
	}

	styles := make([]map[string]any, 0, len(profiles))
	for _, pwc := range profiles {
		item := map[string]any{
			"id":             pwc.ID,
			"slug":           pwc.Slug,
			"name":           pwc.Name,
			"description":    pwc.Description,
			"status":         pwc.Status,
			"current_version": pwc.CurrentVersion,
			"created_at":     pwc.CreatedAt,
			"updated_at":     pwc.UpdatedAt,
		}
		// Parse the config JSON string into a proper object so the frontend
		// receives a ready-to-use config without extra requests.
		if pwc.ConfigJSON != "" {
			var cfg any
			if json.Unmarshal([]byte(pwc.ConfigJSON), &cfg) == nil {
				item["config"] = cfg
			}
		}
		styles = append(styles, item)
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
			// v.Config is a JSON string; unmarshal it so the response contains
			// a proper JSON object instead of a doubly-encoded string.
			var cfg any
			if json.Unmarshal([]byte(v.Config), &cfg) == nil {
				result["config"] = cfg
			} else {
				result["config"] = v.Config // fallback: raw string
			}
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

func (s *Server) handleWithdrawMyStyleReview(w http.ResponseWriter, r *http.Request) {
	userID, store, ok := s.requireUserAndStore(w, r)
	if !ok {
		return
	}

	p, ok := s.requireOwnedProfile(w, r, userID)
	if !ok {
		return
	}

	if p.Status != "pending_review" {
		response.Err(w, http.StatusConflict, "not_pending", "only pending review can be withdrawn")
		return
	}

	if err := store.WithdrawReview(r.Context(), p.ID); err != nil {
		slog.Error("failed to withdraw review", "error", err, "profile_id", p.ID)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to withdraw review")
		return
	}

	response.OK(w, map[string]any{
		"id":      p.ID,
		"status":  "draft",
		"message": "review withdrawn, style reverted to draft",
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

	// Support both JSON and multipart (file upload) requests
	contentType := r.Header.Get("Content-Type")
	var uploadedFiles []services.UploadedFile

	if strings.HasPrefix(contentType, "multipart/form-data") {
		// Parse multipart form (50 MB max)
		if err := r.ParseMultipartForm(50 << 20); err != nil {
			response.Err(w, http.StatusBadRequest, "bad_request", "failed to parse form: "+err.Error())
			return
		}
		req.Message = r.FormValue("message")

		// Read uploaded files
		for _, fhs := range r.MultipartForm.File {
			for _, fh := range fhs {
				f, err := fh.Open()
				if err != nil {
					continue
				}
				content, err := io.ReadAll(f)
				f.Close()
				if err != nil {
					continue
				}
				// Only accept text-like files and ZIP archives
					ext := strings.ToLower(filepath.Ext(fh.Filename))
					if ext == ".zip" {
						// Unzip and extract text files
						extracted := extractTextFilesFromZip(content, fh.Filename)
						uploadedFiles = append(uploadedFiles, extracted...)
					} else if ext == ".md" || ext == ".txt" || ext == ".markdown" || ext == ".json" || ext == ".csv" || ext == ".yaml" || ext == ".yml" || ext == ".html" || ext == ".htm" {
						uploadedFiles = append(uploadedFiles, services.UploadedFile{
							Name:    fh.Filename,
							Content: string(content),
						})
					}
			}
		}
	} else {
		// JSON request (no files)
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
			return
		}
	}

	if req.Message == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "message is required")
		return
	}

	resp, err := s.styleBuilder.SendMessage(r.Context(), sessionID, req.Message, uploadedFiles)
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

// extractTextFilesFromZip reads a ZIP archive and extracts text-like files
// (md, txt, json, csv, yaml, html) as UploadedFile entries.
// Files in nested directories are flattened using "zipname/dir/file" naming.
// Non-text files and hidden files (starting with .) are skipped.
// Each file's content is capped at 500KB to prevent oversized context.
func extractTextFilesFromZip(zipData []byte, zipName string) []services.UploadedFile {
	var results []services.UploadedFile

	reader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		slog.Warn("failed to open zip archive", "zip", zipName, "error", err)
		return nil
	}

	// Supported text extensions
	textExts := map[string]bool{
		".md": true, ".markdown": true, ".txt": true,
		".json": true, ".csv": true,
		".yaml": true, ".yml": true,
		".html": true, ".htm": true,
	}

	const maxFileSize = 500 * 1024 // 500KB per file

	for _, f := range reader.File {
		// Skip directories
		if f.FileInfo().IsDir() {
			continue
		}

		// Skip hidden files (macOS ._ files, .DS_Store, etc.)
		base := filepath.Base(f.Name)
		if strings.HasPrefix(base, ".") {
			continue
		}

		// Check extension
		ext := strings.ToLower(filepath.Ext(f.Name))
		if !textExts[ext] {
			continue
		}

		// Open and read file content
		rc, err := f.Open()
		if err != nil {
			slog.Warn("failed to open file in zip", "file", f.Name, "error", err)
			continue
		}

		// Limit read size
		limited := io.LimitReader(rc, maxFileSize+1)
		content, err := io.ReadAll(limited)
		rc.Close()
		if err != nil {
			slog.Warn("failed to read file in zip", "file", f.Name, "error", err)
			continue
		}

		// Truncate if over limit
		contentStr := string(content)
		if len(content) > maxFileSize {
			contentStr = contentStr[:maxFileSize] + "\n\n[... 文件已截断，仅显示前 500KB ...]"
		}

		// Use "zipName/path/to/file" as the display name
		displayName := zipName + "/" + f.Name

		results = append(results, services.UploadedFile{
			Name:    displayName,
			Content: contentStr,
		})
	}

	slog.Info("zip extracted for style builder",
		"zip", zipName,
		"files_extracted", len(results),
	)

	return results
}

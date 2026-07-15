package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/routing"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── Admin: Dashboard Stats ──────────────────────────────

func (s *Server) handleAdminStats(w http.ResponseWriter, r *http.Request) {
	if s.adminRepo == nil {
		response.OK(w, map[string]interface{}{
			"today_writes":       0,
			"today_tokens":       0,
			"active_users":       0,
			"eval_avg_score":     0,
			"total_writes":       0,
			"total_tokens":       0,
			"style_distribution": []interface{}{},
			"recent_traces":      []interface{}{},
			"weekly_writes":      []interface{}{},
			"weekly_tokens":      []interface{}{},
		})
		return
	}

	stats, err := s.adminRepo.GetDashboardStats(r.Context())
	if err != nil {
		slog.Warn("failed to get dashboard stats", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to get stats")
		return
	}

	response.OK(w, stats)
}

// ─── Admin: Traces ───────────────────────────────────────

func (s *Server) handleAdminListTraces(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	styleSlug := r.URL.Query().Get("style")
	page := parseIntDefault(r.URL.Query().Get("page"), 1)
	pageSize := parseIntDefault(r.URL.Query().Get("page_size"), 20)

	if s.adminRepo == nil {
		response.OK(w, map[string]interface{}{"traces": []interface{}{}, "total": 0})
		return
	}

	traces, total, err := s.adminRepo.ListTraces(r.Context(), status, styleSlug, page, pageSize)
	if err != nil {
		slog.Warn("failed to list traces", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list traces")
		return
	}

	response.OK(w, map[string]interface{}{"traces": traces, "total": total})
}

func (s *Server) handleAdminGetTrace(w http.ResponseWriter, r *http.Request) {
	traceID := chi.URLParam(r, "traceId")

	if s.adminRepo == nil {
		// Try the regular trace repo
		if s.traces != nil {
			trace, err := s.traces.GetTrace(r.Context(), traceID)
			if err != nil {
				response.Err(w, http.StatusNotFound, "not_found", "trace not found")
				return
			}
			response.OK(w, trace)
			return
		}
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	trace, err := s.adminRepo.GetTraceDetail(r.Context(), traceID)
	if err != nil {
		response.Err(w, http.StatusNotFound, "not_found", "trace not found")
		return
	}

	response.OK(w, trace)
}

// ─── Admin: Style Profile CRUD ───────────────────────────

func (s *Server) handleAdminListStyles(w http.ResponseWriter, r *http.Request) {
	// Always include in-memory profiles (which includes drafts)
	profiles := s.profiles.ListAll()

	// Also try to get DB-backed profiles for additional info
	if s.adminRepo != nil {
		dbProfiles, err := s.adminRepo.ListProfiles(r.Context())
		if err == nil && len(dbProfiles) > 0 {
			// Merge DB statuses into in-memory profiles
			dbStatusMap := make(map[string]string)
			for _, dbp := range dbProfiles {
				dbStatusMap[dbp.Slug] = dbp.Status
			}
			for i := range profiles {
				if status, ok := dbStatusMap[profiles[i].Slug]; ok {
					profiles[i].Status = status
				}
			}
		}
	}

	response.OK(w, map[string]interface{}{"styles": profiles})
}

func (s *Server) handleAdminGetStyle(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	p, ok := s.profiles.GetDetail(slug)
	if !ok {
		response.Err(w, http.StatusNotFound, "not_found", "style not found")
		return
	}

	// Add status info
	result := map[string]interface{}{
		"slug":            p.Slug,
		"name":            p.Name,
		"description":     p.Description,
		"version":         p.Version,
		"status":          s.profiles.GetStatus(slug),
		"tags":            p.Tags,
		"word_range":      p.WordRange,
		"structure":       p.Structure,
		"rhetoric":        p.Rhetoric,
		"title_guidelines": p.TitleGuidelines,
		"system_prompt":   p.SystemPrompt,
		"writing_standard": p.WritingStandard,
		"fact_guard":      p.FactGuard,
		"output_format":   p.OutputFormat,
		"length_profiles": p.LengthProfiles,
	}

	response.OK(w, result)
}

func (s *Server) handleAdminCreateStyle(w http.ResponseWriter, r *http.Request) {
	var req profile.StyleProfile
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	if req.Slug == "" || req.Name == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "slug and name are required")
		return
	}

	// Set defaults
	if req.Version == 0 {
		req.Version = 1
	}
	if req.Tags == nil {
		req.Tags = []string{}
	}
	if req.LengthProfiles == nil {
		req.LengthProfiles = map[string]profile.WordRange{}
	}

	if err := s.profiles.CreateProfile(&req); err != nil {
		response.Err(w, http.StatusConflict, "conflict", err.Error())
		return
	}

	// Persist to DB if available
	if s.adminRepo != nil {
		configJSON, _ := json.Marshal(req)
		var configMap map[string]interface{}
		json.Unmarshal(configJSON, &configMap)
		rec := &database.StyleProfileRecord{
			Slug:        req.Slug,
			Name:        req.Name,
			Description: req.Description,
			Version:     req.Version,
			Status:      "draft",
			Config:      configMap,
			RolloutType: "full",
		}
		s.adminRepo.SaveProfile(r.Context(), rec)
	}

	response.Created(w, req)
}

func (s *Server) handleAdminUpdateStyle(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")

	var req profile.StyleProfile
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	// Ensure slug matches
	req.Slug = slug

	// Get existing to preserve version if not provided
	existing, ok := s.profiles.Get(slug)
	if !ok {
		response.Err(w, http.StatusNotFound, "not_found", "style not found")
		return
	}
	if req.Version == 0 {
		req.Version = existing.Version
	}

	s.profiles.UpdateProfile(slug, &req)

	// Persist to DB if available
	if s.adminRepo != nil {
		configJSON, _ := json.Marshal(req)
		var configMap map[string]interface{}
		json.Unmarshal(configJSON, &configMap)
		rec := &database.StyleProfileRecord{
			Slug:        slug,
			Name:        req.Name,
			Description: req.Description,
			Version:     req.Version,
			Status:      "draft",
			Config:      configMap,
			RolloutType: "full",
		}
		s.adminRepo.SaveProfile(r.Context(), rec)
	}

	response.OK(w, map[string]interface{}{
		"slug":    slug,
		"message": "profile saved as draft",
	})
}

func (s *Server) handleAdminPublishStyle(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")

	var req struct {
		Version   int    `json:"version"`
		Detail    string `json:"detail"`
		Changelog string `json:"changelog"`
	}
	// Allow empty body
	_ = json.NewDecoder(r.Body).Decode(&req)

	p, ok := s.profiles.Get(slug)
	if !ok {
		response.Err(w, http.StatusNotFound, "not_found", "style not found")
		return
	}

	// Validate profile before publishing
	if err := profile.ValidateProfile(p); err != nil {
		response.Err(w, http.StatusBadRequest, "validation_failed", fmt.Sprintf("profile validation failed: %s", err.Error()))
		return
	}

	if req.Version == 0 {
		req.Version = p.Version + 1
	}

	// Update version in memory
	p.Version = req.Version

	// Publish (persists to DB + triggers publish hook)
	if err := s.profiles.Publish(slug, req.Version, req.Detail); err != nil {
		slog.Warn("profile publish encountered error", "slug", slug, "error", err)
		response.Err(w, http.StatusInternalServerError, "publish_failed", fmt.Sprintf("publish failed: %s", err.Error()))
		return
	}

	response.OK(w, map[string]interface{}{
		"slug":    slug,
		"version": req.Version,
		"detail":  req.Detail,
		"message": "profile published, auto-evaluation triggered if eval sets exist",
	})
}

func (s *Server) handleAdminArchiveStyle(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")

	s.profiles.ArchiveProfile(slug)

	if s.adminRepo != nil {
		s.adminRepo.ArchiveProfile(r.Context(), slug)
	}

	response.OK(w, map[string]interface{}{
		"slug":    slug,
		"message": "profile archived",
	})
}

func (s *Server) handleAdminListVersions(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")

	if s.adminRepo == nil {
		// Return in-memory version info
		p, ok := s.profiles.Get(slug)
		if !ok {
			response.Err(w, http.StatusNotFound, "not_found", "style not found")
			return
		}
		response.OK(w, map[string]interface{}{
			"versions": []map[string]interface{}{
				{
					"version": p.Version,
					"status":  s.profiles.GetStatus(slug),
				},
			},
		})
		return
	}

	versions, err := s.adminRepo.ListProfileVersions(r.Context(), slug)
	if err != nil {
		slog.Warn("failed to list versions", "error", err)
	}

	if versions == nil {
		versions = []map[string]interface{}{}
	}

	response.OK(w, map[string]interface{}{"versions": versions})
}

// handleAdminRepublishVersion republishes an old version as a new version.
// POST /admin/styles/{slug}/versions/{version}/republish
// Body: { "changelog": "optional changelog text" }
func (s *Server) handleAdminRepublishVersion(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	versionStr := chi.URLParam(r, "version")

	oldVersion, err := strconv.Atoi(versionStr)
	if err != nil || oldVersion < 1 {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid version number")
		return
	}

	var req struct {
		Changelog string `json:"changelog"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	// Determine new version number
	p, ok := s.profiles.Get(slug)
	if !ok {
		response.Err(w, http.StatusNotFound, "not_found", "style not found")
		return
	}
	newVersion := p.Version + 1

	// Republish the old version as a new version
	if err := s.profiles.RepublishVersion(slug, oldVersion, newVersion, req.Changelog); err != nil {
		slog.Warn("failed to republish version", "slug", slug, "old_version", oldVersion, "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", fmt.Sprintf("failed to republish: %s", err.Error()))
		return
	}

	response.OK(w, map[string]interface{}{
		"slug":         slug,
		"old_version":  oldVersion,
		"new_version":  newVersion,
		"changelog":    req.Changelog,
		"message":      fmt.Sprintf("version %d republished as new version %d", oldVersion, newVersion),
	})
}

// handleAdminCompareVersions compares two versions of a profile and returns a field-level diff.
// GET /admin/styles/{slug}/versions/compare?v1=1&v2=2
func (s *Server) handleAdminCompareVersions(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	v1Str := r.URL.Query().Get("v1")
	v2Str := r.URL.Query().Get("v2")

	if v1Str == "" || v2Str == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "v1 and v2 query parameters are required")
		return
	}

	v1, err1 := strconv.Atoi(v1Str)
	v2, err2 := strconv.Atoi(v2Str)
	if err1 != nil || err2 != nil || v1 < 1 || v2 < 1 {
		response.Err(w, http.StatusBadRequest, "bad_request", "v1 and v2 must be positive integers")
		return
	}

	if s.adminRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	ver1, err := s.adminRepo.GetProfileVersion(r.Context(), slug, v1)
	if err != nil {
		slog.Warn("failed to get version", "slug", slug, "version", v1, "error", err)
		response.Err(w, http.StatusNotFound, "not_found", "version "+v1Str+" not found")
		return
	}

	ver2, err := s.adminRepo.GetProfileVersion(r.Context(), slug, v2)
	if err != nil {
		slog.Warn("failed to get version", "slug", slug, "version", v2, "error", err)
		response.Err(w, http.StatusNotFound, "not_found", "version "+v2Str+" not found")
		return
	}

	// Compute diff
	config1, _ := ver1["config"].(map[string]interface{})
	config2, _ := ver2["config"].(map[string]interface{})

	diff := computeConfigDiff(config1, config2)

	response.OK(w, map[string]interface{}{
		"slug":    slug,
		"v1":      ver1,
		"v2":      ver2,
		"diff":    diff,
		"summary": map[string]interface{}{
			"added":    len(diff["added"].([]string)),
			"removed":  len(diff["removed"].([]string)),
			"modified": len(diff["modified"].([]map[string]interface{})),
			"unchanged": len(diff["unchanged"].([]string)),
		},
	})
}

// computeConfigDiff computes a field-level diff between two profile config maps.
func computeConfigDiff(old, neu map[string]interface{}) map[string]interface{} {
	added := []string{}
	removed := []string{}
	modified := []map[string]interface{}{}
	unchanged := []string{}

	// Check for modified and removed keys
	for key, oldVal := range old {
		newVal, exists := neu[key]
		if !exists {
			removed = append(removed, key)
			continue
		}

		oldJSON, _ := json.Marshal(oldVal)
		newJSON, _ := json.Marshal(newVal)

		if string(oldJSON) == string(newJSON) {
			unchanged = append(unchanged, key)
		} else {
			modified = append(modified, map[string]interface{}{
				"field": key,
				"old":   oldVal,
				"new":   newVal,
			})
		}
	}

	// Check for added keys
	for key := range neu {
		if _, exists := old[key]; !exists {
			added = append(added, key)
		}
	}

	return map[string]interface{}{
		"added":     added,
		"removed":   removed,
		"modified":  modified,
		"unchanged": unchanged,
	}
}

// ─── Admin: Sensitive Words (Placeholder) ────────────────

func (s *Server) handleAdminListSensitiveWords(w http.ResponseWriter, r *http.Request) {
	category := r.URL.Query().Get("category")

	if s.adminRepo == nil {
		response.OK(w, map[string]interface{}{"words": []interface{}{}, "total": 0})
		return
	}

	words, total, err := s.adminRepo.ListSensitiveWords(r.Context(), category)
	if err != nil {
		slog.Warn("failed to list sensitive words", "error", err)
		response.OK(w, map[string]interface{}{"words": []interface{}{}, "total": 0})
		return
	}

	response.OK(w, map[string]interface{}{"words": words, "total": total})
}

func (s *Server) handleAdminAddSensitiveWord(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Word        string  `json:"word"`
		Category    string  `json:"category"`
		Severity    string  `json:"severity"`
		Action      string  `json:"action"`
		Replacement *string `json:"replacement"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	if req.Word == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "word is required")
		return
	}

	// Set defaults
	if req.Category == "" {
		req.Category = "clickbait"
	}
	if req.Severity == "" {
		req.Severity = "medium"
	}
	if req.Action == "" {
		req.Action = "warn"
	}

	if s.adminRepo == nil {
		response.Created(w, map[string]interface{}{
			"id":       "placeholder",
			"word":     req.Word,
			"category": req.Category,
			"severity": req.Severity,
			"action":   req.Action,
			"message":  "database not available, stored in memory only",
		})
		return
	}

	word, err := s.adminRepo.AddSensitiveWord(r.Context(), req.Word, req.Category, req.Severity, req.Action, req.Replacement)
	if err != nil {
		slog.Warn("failed to add sensitive word", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to add sensitive word")
		return
	}

	response.Created(w, word)
}

func (s *Server) handleAdminDeleteSensitiveWord(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if s.adminRepo == nil {
		response.OK(w, map[string]interface{}{"message": "deleted (no DB)"})
		return
	}

	if err := s.adminRepo.DeleteSensitiveWord(r.Context(), id); err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to delete")
		return
	}

	response.OK(w, map[string]interface{}{"message": "deleted"})
}

func (s *Server) handleAdminSensitiveConfig(w http.ResponseWriter, r *http.Request) {
	// Placeholder for global sensitivity config
	if r.Method == http.MethodGet {
		response.OK(w, map[string]interface{}{
			"strictness": "standard",
			"options":    []string{"loose", "standard", "strict"},
		})
		return
	}

	// PUT
	var req struct {
		Strictness string `json:"strictness"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	validLevels := map[string]bool{"loose": true, "standard": true, "strict": true}
	if !validLevels[req.Strictness] {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid strictness level")
		return
	}

	response.OK(w, map[string]interface{}{
		"strictness": req.Strictness,
		"message":    "global sensitivity level updated (placeholder — full implementation TBD)",
	})
}

// ─── Admin: Rollout (Grayscale) ─────────────────────────

func (s *Server) handleAdminGetRollout(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")

	config := s.profiles.GetRolloutConfig(slug)
	response.OK(w, map[string]interface{}{
		"slug":             slug,
		"rollout_type":     config.Type,
		"whitelist_uids":   config.WhitelistUIDs,
		"rollout_percent":  config.RolloutPercent,
		"fallback_version": config.FallbackVersion,
	})
}

func (s *Server) handleAdminUpdateRollout(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")

	var req struct {
		RolloutType     string   `json:"rollout_type"`
		WhitelistUIDs   []string `json:"whitelist_uids"`
		RolloutPercent  int      `json:"rollout_percent"`
		FallbackVersion int      `json:"fallback_version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	validTypes := map[string]bool{"full": true, "whitelist": true, "percentage": true}
	if !validTypes[req.RolloutType] {
		response.Err(w, http.StatusBadRequest, "bad_request", "rollout_type must be full, whitelist, or percentage")
		return
	}

	if req.RolloutPercent < 0 || req.RolloutPercent > 100 {
		response.Err(w, http.StatusBadRequest, "bad_request", "rollout_percent must be 0-100")
		return
	}

	config := routing.RolloutConfig{
		Type:            req.RolloutType,
		WhitelistUIDs:   req.WhitelistUIDs,
		RolloutPercent:  req.RolloutPercent,
		FallbackVersion: req.FallbackVersion,
	}

	if err := s.profiles.UpdateRolloutConfig(slug, config); err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to update rollout config")
		return
	}

	response.OK(w, map[string]interface{}{
		"slug":             slug,
		"rollout_type":     config.Type,
		"whitelist_uids":   config.WhitelistUIDs,
		"rollout_percent":  config.RolloutPercent,
		"fallback_version": config.FallbackVersion,
		"message":          "rollout config updated",
	})
}

func (s *Server) handleAdminPreviewRollout(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")

	var req struct {
		UIDs []string `json:"uids"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	config := s.profiles.GetRolloutConfig(slug)
	preview := routing.PreviewRollout(req.UIDs, config)

	response.OK(w, preview)
}

// ─── Admin Auth Middleware ───────────────────────────────

func (s *Server) adminAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check for admin token in Authorization header or query param
		token := r.Header.Get("Authorization")
		token = strings.TrimPrefix(token, "Bearer ")

		if token == "" {
			token = r.URL.Query().Get("admin_token")
		}

		// In dev mode, allow without token if ADMIN_TOKEN is default
		if s.cfg.Admin.Token == "dev-admin-token" && token == "" {
			next.ServeHTTP(w, r)
			return
		}

		// 1. Try JWT validation first (for logged-in admin users)
		if token != "" {
			if payload, err := s.ValidateJWT(token); err == nil {
				if payload.Role == "admin" {
					ctx := withUser(r.Context(), payload)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
			}
		}

		// 2. Fallback: check against static admin token
		if token == s.cfg.Admin.Token && token != "" {
			next.ServeHTTP(w, r)
			return
		}

		response.Err(w, http.StatusUnauthorized, "unauthorized", "admin token required")
	})
}

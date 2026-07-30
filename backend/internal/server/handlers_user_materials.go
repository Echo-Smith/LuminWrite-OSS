package server

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/services"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── User Material Handlers ─────────────────────────────
// These endpoints provide personal material management backed by WeKnora.
// Each user gets their own WeKnora KB (auto-created on first use).
// Material metadata is stored locally; actual content is indexed in WeKnora.

// getUserID extracts user ID from JWT context.
func (s *Server) getUserID(r *http.Request) string {
	if payload := userFromContext(r.Context()); payload != nil {
		return payload.Sub
	}
	return ""
}

// handleUserMaterialList lists the authenticated user's materials.
func (s *Server) handleUserMaterialList(w http.ResponseWriter, r *http.Request) {
	if s.weknoraMgr == nil {
		response.Err(w, http.StatusServiceUnavailable, "weknora_not_configured", "WeKnora is not configured")
		return
	}

	userID := s.getUserID(r)
	if userID == "" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	page := parseIntDefault(r.URL.Query().Get("page"), 1)
	pageSize := parseIntDefault(r.URL.Query().Get("page_size"), 20)

	materials, total, err := s.weknoraMgr.ListMaterials(r.Context(), userID, page, pageSize)
	if err != nil {
		slog.Warn("list user materials failed", "error", err, "user_id", userID)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list materials")
		return
	}

	response.OK(w, map[string]any{
		"materials": materials,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// handleUserMaterialCreate creates a new material from text/markdown.
func (s *Server) handleUserMaterialCreate(w http.ResponseWriter, r *http.Request) {
	if s.weknoraMgr == nil {
		response.Err(w, http.StatusServiceUnavailable, "weknora_not_configured", "WeKnora is not configured")
		return
	}

	userID := s.getUserID(r)
	if userID == "" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	var req struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	if req.Title == "" || req.Content == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "title and content are required")
		return
	}

	// Upload to WeKnora
	docID, err := s.weknoraMgr.AddKnowledgeToUserKB(r.Context(), userID, req.Title, req.Content)
	if err != nil {
		slog.Warn("add material to weknora failed", "error", err, "user_id", userID)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to add material")
		return
	}

	// Get the KB ID for local storage
	kbID, _ := s.weknoraMgr.GetOrCreateUserKB(r.Context(), userID)

	// Save metadata locally
	mat := &services.UserMaterial{
		ID:             uuid.NewString(),
		UserID:         userID,
		Title:          req.Title,
		ContentPreview: truncateStr(req.Content, 500),
		SourceType:     "text",
		WeKnoraDocID:   docID,
		WeKnoraKBID:    kbID,
		Status:         "active",
	}
	if err := s.weknoraMgr.SaveMaterial(r.Context(), mat); err != nil {
		slog.Warn("save material metadata failed", "error", err, "user_id", userID)
	}

	response.Created(w, map[string]any{
		"id":            mat.ID,
		"weknora_doc_id": docID,
		"title":         req.Title,
	})
}

// handleUserMaterialUpload uploads a file as a material.
func (s *Server) handleUserMaterialUpload(w http.ResponseWriter, r *http.Request) {
	if s.weknoraMgr == nil {
		response.Err(w, http.StatusServiceUnavailable, "weknora_not_configured", "WeKnora is not configured")
		return
	}

	userID := s.getUserID(r)
	if userID == "" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	if err := r.ParseMultipartForm(50 << 20); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "failed to parse multipart form: "+err.Error())
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "file is required")
		return
	}
	defer file.Close()

	title := r.FormValue("title")
	if title == "" {
		title = header.Filename
	}

	// Upload to WeKnora
	docID, err := s.weknoraMgr.UploadFileToUserKB(r.Context(), userID, header.Filename, file, title)
	if err != nil {
		slog.Warn("upload material to weknora failed", "error", err, "user_id", userID)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to upload material")
		return
	}

	// Reset file reader for preview (already consumed by WeKnora upload)
	// We'll use the filename as preview since we can't re-read
	kbID, _ := s.weknoraMgr.GetOrCreateUserKB(r.Context(), userID)

	mat := &services.UserMaterial{
		ID:             uuid.NewString(),
		UserID:         userID,
		Title:          title,
		ContentPreview: "上传文件: " + header.Filename,
		SourceType:     "file",
		FileName:       header.Filename,
		FileSize:       header.Size,
		WeKnoraDocID:   docID,
		WeKnoraKBID:    kbID,
		Status:         "active",
	}
	if err := s.weknoraMgr.SaveMaterial(r.Context(), mat); err != nil {
		slog.Warn("save material metadata failed", "error", err, "user_id", userID)
	}

	response.Created(w, map[string]any{
		"id":            mat.ID,
		"weknora_doc_id": docID,
		"filename":      header.Filename,
		"title":         title,
	})
}

// handleUserMaterialDelete deletes a material (from WeKnora + locally).
func (s *Server) handleUserMaterialDelete(w http.ResponseWriter, r *http.Request) {
	if s.weknoraMgr == nil {
		response.Err(w, http.StatusServiceUnavailable, "weknora_not_configured", "WeKnora is not configured")
		return
	}

	userID := s.getUserID(r)
	if userID == "" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	materialID := chi.URLParam(r, "id")
	if materialID == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "material ID is required")
		return
	}

	if err := s.weknoraMgr.DeleteMaterial(r.Context(), userID, materialID); err != nil {
		slog.Warn("delete material failed", "error", err, "material_id", materialID)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to delete material")
		return
	}

	response.OK(w, map[string]any{"message": "material deleted", "id": materialID})
}

// handleUserMaterialSearch searches the user's WeKnora KB using hybrid search.
func (s *Server) handleUserMaterialSearch(w http.ResponseWriter, r *http.Request) {
	if s.weknoraMgr == nil {
		response.Err(w, http.StatusServiceUnavailable, "weknora_not_configured", "WeKnora is not configured")
		return
	}

	userID := s.getUserID(r)
	if userID == "" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	var req struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		req.Query = r.URL.Query().Get("q")
		req.Limit = parseIntDefault(r.URL.Query().Get("limit"), 10)
	}
	if req.Query == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "query is required")
		return
	}
	if req.Limit <= 0 {
		req.Limit = 10
	}

	results, err := s.weknoraMgr.SearchInUserKB(r.Context(), userID, req.Query, req.Limit)
	if err != nil {
		slog.Warn("user material search failed", "error", err, "user_id", userID)
		response.Err(w, http.StatusInternalServerError, "internal_error", "search failed")
		return
	}

	// Convert to SearchResult format
	response.OK(w, map[string]any{
		"results": results,
		"query":   req.Query,
		"source":  "weknora",
	})
}

// ─── Topic-Material Association Handlers ────────────────

// handleTopicMaterialList lists materials associated with a topic.
func (s *Server) handleTopicMaterialList(w http.ResponseWriter, r *http.Request) {
	if s.weknoraMgr == nil {
		response.Err(w, http.StatusServiceUnavailable, "weknora_not_configured", "WeKnora is not configured")
		return
	}

	userID := s.getUserID(r)
	if userID == "" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	topicID := chi.URLParam(r, "topicId")
	if topicID == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "topic ID is required")
		return
	}

	associations, err := s.weknoraMgr.ListTopicMaterials(r.Context(), topicID, userID)
	if err != nil {
		slog.Warn("list topic materials failed", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list topic materials")
		return
	}

	// Enrich with material details
	type AssocWithMaterial struct {
		services.TopicMaterial
		Material *services.UserMaterial `json:"material"`
	}

	enriched := make([]AssocWithMaterial, 0, len(associations))
	for _, assoc := range associations {
		mat, _ := s.weknoraMgr.GetMaterial(r.Context(), userID, assoc.MaterialID)
		enriched = append(enriched, AssocWithMaterial{
			TopicMaterial: assoc,
			Material:      mat,
		})
	}

	response.OK(w, map[string]any{
		"associations": enriched,
		"total":        len(enriched),
	})
}

// handleTopicMaterialAssociate manually associates a material with a topic.
func (s *Server) handleTopicMaterialAssociate(w http.ResponseWriter, r *http.Request) {
	if s.weknoraMgr == nil {
		response.Err(w, http.StatusServiceUnavailable, "weknora_not_configured", "WeKnora is not configured")
		return
	}

	userID := s.getUserID(r)
	if userID == "" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	topicID := chi.URLParam(r, "topicId")
	materialID := chi.URLParam(r, "materialId")

	if topicID == "" || materialID == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "topic ID and material ID are required")
		return
	}

	if err := s.weknoraMgr.AssociateMaterialWithTopic(r.Context(), topicID, materialID, userID, "manual", 0); err != nil {
		slog.Warn("associate material with topic failed", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to associate material")
		return
	}

	response.OK(w, map[string]any{"message": "material associated", "topic_id": topicID, "material_id": materialID})
}

// handleTopicMaterialRemove removes a material association from a topic.
func (s *Server) handleTopicMaterialRemove(w http.ResponseWriter, r *http.Request) {
	if s.weknoraMgr == nil {
		response.Err(w, http.StatusServiceUnavailable, "weknora_not_configured", "WeKnora is not configured")
		return
	}

	userID := s.getUserID(r)
	if userID == "" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	topicID := chi.URLParam(r, "topicId")
	materialID := chi.URLParam(r, "materialId")

	if err := s.weknoraMgr.RemoveTopicMaterial(r.Context(), topicID, materialID, userID); err != nil {
		slog.Warn("remove topic material failed", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to remove association")
		return
	}

	response.OK(w, map[string]any{"message": "association removed", "topic_id": topicID, "material_id": materialID})
}

// handleTopicMaterialAuto auto-associates materials with a topic using WeKnora hybrid search.
func (s *Server) handleTopicMaterialAuto(w http.ResponseWriter, r *http.Request) {
	if s.weknoraMgr == nil {
		response.Err(w, http.StatusServiceUnavailable, "weknora_not_configured", "WeKnora is not configured")
		return
	}

	userID := s.getUserID(r)
	if userID == "" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	topicID := chi.URLParam(r, "topicId")

	var req struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		req.Query = ""
		req.Limit = 5
	}
	if req.Limit <= 0 {
		req.Limit = 5
	}

	// If no query provided, use the topic title as search query
	if req.Query == "" {
		// Fetch topic from DB
		var title string
		if s.dbAvail && s.adminRepo != nil {
			s.adminRepo.DB().DB.QueryRowContext(r.Context(),
				`SELECT title FROM topics WHERE id = $1`, topicID,
			).Scan(&title)
		}
		req.Query = title
		if req.Query == "" {
			response.Err(w, http.StatusBadRequest, "bad_request", "query is required (provide a query or ensure topic exists)")
			return
		}
	}

	// Search in user's WeKnora KB
	results, err := s.weknoraMgr.SearchInUserKB(r.Context(), userID, req.Query, req.Limit)
	if err != nil {
		slog.Warn("auto-associate search failed", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "search failed")
		return
	}

	// Create associations for each result
	associated := 0
	for _, result := range results {
		if result.ID == "" {
			continue
		}
		// Try to find the local material by WeKnora doc ID
		mat, _ := s.weknoraMgr.GetMaterialByDocID(r.Context(), userID, result.ID)
		if mat == nil {
			// Create a lightweight material record from search result
			mat = &services.UserMaterial{
				ID:             uuid.NewString(),
				UserID:         userID,
				Title:          result.Title,
				ContentPreview: truncateStr(result.Content, 500),
				SourceType:     "auto",
				WeKnoraDocID:   result.ID,
				Status:         "active",
			}
			s.weknoraMgr.SaveMaterial(r.Context(), mat)
		}

		if err := s.weknoraMgr.AssociateMaterialWithTopic(r.Context(), topicID, mat.ID, userID, "auto", result.Score); err == nil {
			associated++
		}
	}

	response.OK(w, map[string]any{
		"message":    "auto-association completed",
		"topic_id":   topicID,
		"query":      req.Query,
		"associated": associated,
		"results":    results,
	})
}

// ─── WeKnora Config Status ──────────────────────────────

// handleWeKnoraStatus returns WeKnora configuration status (for admin panel).
func (s *Server) handleWeKnoraStatus(w http.ResponseWriter, r *http.Request) {
	status := map[string]any{
		"enabled":       s.cfg.WeKnora.Enabled,
		"base_url":      s.cfg.WeKnora.BaseURL,
		"ui_url":        s.cfg.WeKnora.UIURL,
		"admin_email":   s.cfg.WeKnora.AdminEmail,
		"kb_id":         s.cfg.WeKnora.KBID,
		"scheme_b":      s.weknoraMgr != nil && s.weknoraMgr.IsConfigured(),
	}
	response.OK(w, status)
}

// ─── Helpers ─────────────────────────────────────────────

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}



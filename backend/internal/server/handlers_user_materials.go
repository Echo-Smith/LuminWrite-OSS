package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/services"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── User Material Handlers ─────────────────────────────
// These endpoints provide personal material management backed by the
// local knowledge base (replaces WeKnora Scheme B).
// Each user's materials are isolated by user_id in the knowledge_base table.
// Material metadata is stored in user_materials; actual content is in knowledge_base.

// getUserID extracts user ID from JWT context.
func (s *Server) getUserID(r *http.Request) string {
	if payload := userFromContext(r.Context()); payload != nil {
		return payload.Sub
	}
	return ""
}

// handleUserMaterialList lists the authenticated user's materials.
func (s *Server) handleUserMaterialList(w http.ResponseWriter, r *http.Request) {
	if s.kbMgr == nil {
		response.Err(w, http.StatusServiceUnavailable, "kb_not_configured", "Knowledge base is not configured")
		return
	}

	userID := s.getUserID(r)
	if userID == "" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	page := parseIntDefault(r.URL.Query().Get("page"), 1)
	pageSize := parseIntDefault(r.URL.Query().Get("page_size"), 20)

	materials, total, err := s.kbMgr.ListMaterials(r.Context(), userID, page, pageSize)
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
	if s.kbMgr == nil {
		response.Err(w, http.StatusServiceUnavailable, "kb_not_configured", "Knowledge base is not configured")
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

	// Add document directly to local knowledge base
	doc, err := s.kbMgr.AddDocument(r.Context(), userID, req.Title, req.Content, "text", map[string]interface{}{
		"source": "text",
	})
	if err != nil {
		slog.Warn("add document to local KB failed", "error", err, "user_id", userID)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to add material")
		return
	}

	// Chunk the content and store chunks
	chunkConfig := services.DefaultChunkConfig()
	chunks := services.ChunkText(req.Content, chunkConfig)
	for _, chunk := range chunks {
		_, chunkErr := s.kbMgr.AddChunk(r.Context(), doc.ID, userID, chunk.Index, chunk.Title, chunk.Content, map[string]interface{}{
			"start_pos": chunk.StartPos,
			"end_pos":   chunk.EndPos,
		})
		if chunkErr != nil {
			slog.Warn("failed to add chunk", "index", chunk.Index, "error", chunkErr)
		}
	}
	if err := s.kbMgr.UpdateChunkCount(r.Context(), doc.ID, len(chunks)); err != nil {
		slog.Warn("failed to update chunk count", "error", err)
	}

	// Save metadata locally
	mat := &services.UserMaterial{
		ID:             uuid.NewString(),
		UserID:         userID,
		Title:          req.Title,
		ContentPreview: truncateStr(req.Content, 500),
		SourceType:     "text",
		DocID:          doc.ID,
		ChunkCount:     len(chunks),
		Status:         "active",
	}
	if err := s.kbMgr.SaveMaterial(r.Context(), mat); err != nil {
		slog.Warn("save material metadata failed", "error", err, "user_id", userID)
	}

	response.Created(w, map[string]any{
		"id":     mat.ID,
		"doc_id": doc.ID,
		"title":  req.Title,
	})
}

// handleUserMaterialUpload uploads a file as a material.
func (s *Server) handleUserMaterialUpload(w http.ResponseWriter, r *http.Request) {
	if s.kbMgr == nil {
		response.Err(w, http.StatusServiceUnavailable, "kb_not_configured", "Knowledge base is not configured")
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

	// Parse file and import to knowledge base
	// For simple text formats, read directly; for complex formats, use docreader sidecar
	ext := filepath.Ext(header.Filename)
	var docID string
	var chunkCount int

	if isDirectReadFormat(ext) {
		// Read directly for text formats
		content, err := readFileContent(file)
		if err != nil {
			response.Err(w, http.StatusInternalServerError, "internal_error", "failed to read file")
			return
		}

		doc, err := s.kbMgr.AddDocument(r.Context(), userID, title, content, "file", map[string]interface{}{
			"source":     "file",
			"file_name":  header.Filename,
			"file_size":  header.Size,
			"file_format": ext,
		})
		if err != nil {
			slog.Warn("add document failed", "error", err, "user_id", userID)
			response.Err(w, http.StatusInternalServerError, "internal_error", "failed to add material")
			return
		}
		docID = doc.ID

		// Chunk and store
		chunkConfig := services.DefaultChunkConfig()
		chunks := services.ChunkText(content, chunkConfig)
		for _, chunk := range chunks {
			s.kbMgr.AddChunk(r.Context(), docID, userID, chunk.Index, chunk.Title, chunk.Content, nil)
		}
		chunkCount = len(chunks)
	} else {
		// For complex formats (PDF, Word, etc.), use docreader gRPC sidecar
		chunkConfig := services.DefaultChunkConfig()
		parser := services.NewFileParser(s.kbMgr, chunkConfig, s.cfg.Kb.DocreaderAddr)
		docID, err := parser.ParseAndImport(r.Context(), userID, header.Filename, file, title)
		if err != nil {
			slog.Warn("docreader file parsing failed", "error", err, "filename", header.Filename)
			response.Err(w, http.StatusInternalServerError, "internal_error", "文件解析失败: "+err.Error())
			return
		}
		mat := &services.UserMaterial{
			ID:             uuid.NewString(),
			UserID:         userID,
			Title:          title,
			ContentPreview: "上传文件: " + header.Filename,
			SourceType:     "file",
			FileName:       header.Filename,
			FileSize:       header.Size,
			DocID:          docID,
			ChunkCount:     0, // will be updated by parser
			Status:         "active",
		}
		if err := s.kbMgr.SaveMaterial(r.Context(), mat); err != nil {
			slog.Warn("save material metadata failed", "error", err, "user_id", userID)
		}

		response.Created(w, map[string]any{
			"id":       mat.ID,
			"doc_id":   docID,
			"filename": header.Filename,
			"title":    title,
		})
		return
	}

	if err := s.kbMgr.UpdateChunkCount(r.Context(), docID, chunkCount); err != nil {
		slog.Warn("failed to update chunk count", "error", err)
	}

	mat := &services.UserMaterial{
		ID:             uuid.NewString(),
		UserID:         userID,
		Title:          title,
		ContentPreview: "上传文件: " + header.Filename,
		SourceType:     "file",
		FileName:       header.Filename,
		FileSize:       header.Size,
		DocID:          docID,
		ChunkCount:     chunkCount,
		Status:         "active",
	}
	if err := s.kbMgr.SaveMaterial(r.Context(), mat); err != nil {
		slog.Warn("save material metadata failed", "error", err, "user_id", userID)
	}

	response.Created(w, map[string]any{
		"id":        mat.ID,
		"doc_id":    docID,
		"filename":  header.Filename,
		"title":     title,
	})
}

// handleUserMaterialDelete deletes a material (from local KB + metadata).
func (s *Server) handleUserMaterialDelete(w http.ResponseWriter, r *http.Request) {
	if s.kbMgr == nil {
		response.Err(w, http.StatusServiceUnavailable, "kb_not_configured", "Knowledge base is not configured")
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

	if err := s.kbMgr.DeleteMaterial(r.Context(), userID, materialID); err != nil {
		slog.Warn("delete material failed", "error", err, "material_id", materialID)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to delete material")
		return
	}

	response.OK(w, map[string]any{"message": "material deleted", "id": materialID})
}

// handleUserMaterialSearch searches the user's knowledge base using hybrid search.
func (s *Server) handleUserMaterialSearch(w http.ResponseWriter, r *http.Request) {
	if s.kbMgr == nil {
		response.Err(w, http.StatusServiceUnavailable, "kb_not_configured", "Knowledge base is not configured")
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

	results, err := s.kbMgr.HybridSearch(r.Context(), userID, req.Query, req.Limit)
	if err != nil {
		slog.Warn("user material search failed", "error", err, "user_id", userID)
		response.Err(w, http.StatusInternalServerError, "internal_error", "search failed")
		return
	}

	response.OK(w, map[string]any{
		"results": results,
		"query":   req.Query,
		"source":  "local_kb",
	})
}

// ─── Topic-Material Association Handlers ────────────────

// handleTopicMaterialList lists materials associated with a topic.
func (s *Server) handleTopicMaterialList(w http.ResponseWriter, r *http.Request) {
	if s.kbMgr == nil {
		response.Err(w, http.StatusServiceUnavailable, "kb_not_configured", "Knowledge base is not configured")
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

	associations, err := s.kbMgr.ListTopicMaterials(r.Context(), topicID, userID)
	if err != nil {
		slog.Warn("list topic materials failed", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list topic materials")
		return
	}

	// Enrich with material details
	type AssocWithMaterial struct {
		*services.TopicMaterial
		Material *services.UserMaterial `json:"material"`
	}

	enriched := make([]AssocWithMaterial, 0, len(associations))
	for _, assoc := range associations {
		mat, _ := s.kbMgr.GetMaterial(r.Context(), userID, assoc.MaterialID)
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
	if s.kbMgr == nil {
		response.Err(w, http.StatusServiceUnavailable, "kb_not_configured", "Knowledge base is not configured")
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

	if err := s.kbMgr.AssociateMaterialWithTopic(r.Context(), topicID, materialID, userID, "manual", 0); err != nil {
		slog.Warn("associate material with topic failed", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to associate material")
		return
	}

	response.OK(w, map[string]any{"message": "material associated", "topic_id": topicID, "material_id": materialID})
}

// handleTopicMaterialRemove removes a material association from a topic.
func (s *Server) handleTopicMaterialRemove(w http.ResponseWriter, r *http.Request) {
	if s.kbMgr == nil {
		response.Err(w, http.StatusServiceUnavailable, "kb_not_configured", "Knowledge base is not configured")
		return
	}

	userID := s.getUserID(r)
	if userID == "" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	topicID := chi.URLParam(r, "topicId")
	materialID := chi.URLParam(r, "materialId")

	if err := s.kbMgr.RemoveTopicMaterial(r.Context(), topicID, materialID, userID); err != nil {
		slog.Warn("remove topic material failed", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to remove association")
		return
	}

	response.OK(w, map[string]any{"message": "association removed", "topic_id": topicID, "material_id": materialID})
}

// handleTopicMaterialAuto auto-associates materials with a topic using local hybrid search.
func (s *Server) handleTopicMaterialAuto(w http.ResponseWriter, r *http.Request) {
	if s.kbMgr == nil {
		response.Err(w, http.StatusServiceUnavailable, "kb_not_configured", "Knowledge base is not configured")
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

	// Search in user's local knowledge base
	results, err := s.kbMgr.HybridSearch(r.Context(), userID, req.Query, req.Limit)
	if err != nil {
		slog.Warn("auto-associate search failed", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "search failed")
		return
	}

	// Create associations for each result
	associated := 0
	for _, result := range results {
		if result.DocID == "" {
			continue
		}
		// Try to find the local material by doc ID
		mat, _ := s.kbMgr.GetMaterial(r.Context(), userID, result.DocID)
		if mat == nil {
			// Create a lightweight material record from search result
			mat = &services.UserMaterial{
				ID:             uuid.NewString(),
				UserID:         userID,
				Title:          result.Title,
				ContentPreview: truncateStr(result.Content, 500),
				SourceType:     "auto",
				DocID:          result.DocID,
				Status:         "active",
			}
			s.kbMgr.SaveMaterial(r.Context(), mat)
		}

		if err := s.kbMgr.AssociateMaterialWithTopic(r.Context(), topicID, mat.ID, userID, "auto", result.Score); err == nil {
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

// ─── Knowledge Base Status ──────────────────────────────

// ─── Helpers ─────────────────────────────────────────────

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// isDirectReadFormat returns true for file formats that can be read as plain text.
func isDirectReadFormat(ext string) bool {
	switch ext {
	case ".txt", ".md", ".markdown", ".csv", ".json", ".html", ".htm", ".xml", ".yaml", ".yml", ".log":
		return true
	}
	return false
}

// readFileContent reads the content of a text file.
func readFileContent(file interface{ Read([]byte) (int, error) }) (string, error) {
	buf := make([]byte, 0, 512)
	tmp := make([]byte, 4096)
	for {
		n, err := file.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return "", err
		}
		if len(buf) > 10*1024*1024 { // 10MB limit
			break
		}
	}
	return string(buf), nil
}

// Ensure os import is used (for future file handling)
var _ = os.Open

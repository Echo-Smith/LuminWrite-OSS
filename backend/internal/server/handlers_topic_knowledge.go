package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/services"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── Topic → Knowledge Base ──────────────────────────────
//
// Saves a topic into the caller's knowledge base as a material
// (source_type="topic"), making it retrievable by the writing pipeline's
// hybrid search. Two modes:
//   - text: the caller supplies the body (e.g. the topic description or a
//     curated summary) — chunked and embedded inline;
//   - url: the topic's original link is imported through the shared
//     URLImporter pipeline (fetch → extract → chunk → embed), the same
//     path RSS full-text fallback uses.
//
// The document write goes through KbManager.AddDocument, which now lands
// in the seeded 'default' KB so it is visible to the KB views (migration
// 125 backfilled the rows written before that fix).

// handleSaveTopicToKnowledge files a topic as a knowledge-base material.
//
// POST /api/v2/topics/{id}/knowledge
// Body: {"mode": "text" | "url", "content": "...", "folder_id": "..."}
func (s *Server) handleSaveTopicToKnowledge(w http.ResponseWriter, r *http.Request) {
	if s.kbMgr == nil {
		response.Err(w, http.StatusServiceUnavailable, "kb_not_configured", "Knowledge base is not configured")
		return
	}

	user := userFromContext(r.Context())
	if user == nil || user.Sub == "" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	userID := user.Sub

	topicID := chi.URLParam(r, "id")
	if topicID == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "topic id is required")
		return
	}

	var req struct {
		Mode     string `json:"mode"` // text | url (default text)
		Content  string `json:"content"`
		FolderID string `json:"folder_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	mode := strings.TrimSpace(req.Mode)
	if mode == "" {
		mode = "text"
	}
	if mode != "text" && mode != "url" {
		response.Err(w, http.StatusBadRequest, "bad_request", "mode must be text or url")
		return
	}

	topic, err := s.traces.GetTopicByID(r.Context(), topicID)
	if err != nil {
		response.Err(w, http.StatusNotFound, "not_found", "topic not found")
		return
	}
	topicTitle, _ := topic["title"].(string)
	if topicTitle == "" {
		topicTitle = "未命名选题"
	}

	if mode == "url" {
		s.saveTopicToKnowledgeViaURL(w, r, userID, topicID, topicTitle, topic, req.FolderID)
		return
	}

	content := strings.TrimSpace(req.Content)
	if content == "" {
		// Fall back to the topic's own description so the common case
		// ("file this topic for later reference") needs no body.
		if desc, ok := topic["description"].(string); ok {
			content = strings.TrimSpace(desc)
		}
	}
	if content == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "content is required (the topic has no description to fall back on)")
		return
	}

	doc, err := s.kbMgr.AddDocument(r.Context(), userID, topicTitle, content, "topic", map[string]interface{}{
		"source":   "topic",
		"topic_id": topicID,
	})
	if err != nil {
		slog.Warn("save topic to KB: add document failed", "error", err, "user_id", userID, "topic_id", topicID)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to save topic")
		return
	}

	chunkConfig := services.DefaultChunkConfig()
	chunks := services.ChunkText(content, chunkConfig)
	for _, chunk := range chunks {
		if _, chunkErr := s.kbMgr.AddChunk(r.Context(), doc.ID, userID, chunk.Index, chunk.Title, chunk.Content, map[string]interface{}{
			"start_pos": chunk.StartPos,
			"end_pos":   chunk.EndPos,
		}); chunkErr != nil {
			slog.Warn("save topic to KB: chunk failed", "index", chunk.Index, "error", chunkErr)
		}
	}
	if err := s.kbMgr.UpdateChunkCount(r.Context(), doc.ID, len(chunks)); err != nil {
		slog.Warn("save topic to KB: chunk count update failed", "error", err)
	}

	s.saveTopicMaterial(w, r.Context(), userID, topicID, topicTitle, content, doc.ID, len(chunks), req.FolderID, "")
}

// saveTopicToKnowledgeViaURL imports the topic's original link through the
// shared URLImporter pipeline and files the result as a topic material.
func (s *Server) saveTopicToKnowledgeViaURL(w http.ResponseWriter, r *http.Request, userID, topicID, topicTitle string, topic map[string]interface{}, folderID string) {
	topicURL, _ := topic["url"].(string)
	if strings.TrimSpace(topicURL) == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "this topic has no original link to import")
		return
	}

	importer := services.NewURLImporter(s.kbMgr, services.DefaultChunkConfig())
	docID, err := importer.ImportURLToKB(r.Context(), userID, "", strings.TrimSpace(topicURL), topicTitle)
	if err != nil {
		slog.Warn("save topic to KB: url import failed", "error", err, "user_id", userID, "topic_id", topicID)
		response.Err(w, http.StatusBadGateway, "upstream_error", "failed to import the topic link")
		return
	}

	doc, err := s.kbMgr.GetDocument(r.Context(), userID, docID)
	if err != nil {
		slog.Warn("save topic to KB: imported document lookup failed", "error", err, "doc_id", docID)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to read imported document")
		return
	}

	preview, chunkCount := "", 0
	if doc != nil {
		preview = doc.ContentPreview
		chunkCount = doc.ChunkCount
	}
	s.saveTopicMaterial(w, r.Context(), userID, topicID, topicTitle, preview, docID, chunkCount, folderID, strings.TrimSpace(topicURL))
}

// saveTopicMaterial persists the user_materials metadata row and answers.
func (s *Server) saveTopicMaterial(w http.ResponseWriter, ctx context.Context, userID, topicID, topicTitle, preview, docID string, chunkCount int, folderID, sourceURL string) {
	mat := &services.UserMaterial{
		ID:             uuid.NewString(),
		UserID:         userID,
		Title:          topicTitle,
		ContentPreview: truncateStr(preview, 500),
		SourceType:     "topic",
		SourceURL:      sourceURL,
		DocID:          docID,
		ChunkCount:     chunkCount,
		FolderID:       folderID,
		Status:         "active",
	}
	if err := s.kbMgr.SaveMaterial(ctx, mat); err != nil {
		slog.Warn("save topic to KB: material metadata failed", "error", err, "user_id", userID, "topic_id", topicID)
	}

	response.Created(w, map[string]interface{}{
		"id":     mat.ID,
		"doc_id": docID,
		"title":  topicTitle,
	})
}

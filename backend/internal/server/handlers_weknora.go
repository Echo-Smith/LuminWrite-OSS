package server

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── WeKnora Knowledge Base Handlers ─────────────────────
// These endpoints proxy to the WeKnora REST API for knowledge base operations
// that go beyond simple text entries: URL import, file upload, hybrid search, etc.

// weknoraMiddleware checks if WeKnora is configured and injects the client into the request context.
// If WeKnora is not configured, it returns a 503 error.
func (s *Server) weknoraMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wk := s.getWeKnoraClient()
		if wk == nil {
			response.Err(w, http.StatusServiceUnavailable, "weknora_not_configured",
				"WeKnora knowledge base is not configured")
			return
		}
		next(w, r)
	}
}

// getWeKnoraClient returns the WeKnora client from the search client, or nil if not configured.
func (s *Server) getWeKnoraClient() *tools.WeKnoraClient {
	if s.search == nil {
		return nil
	}
	wk := s.search.WeKnoraClient()
	if wk == nil || !wk.IsConfigured() {
		return nil
	}
	return wk
}

// handleWeKnoraSearch performs a hybrid search (BM25 + Dense + GraphRAG) on the WeKnora KB.
func (s *Server) handleWeKnoraSearch(w http.ResponseWriter, r *http.Request) {
	wk := s.getWeKnoraClient()

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

	results, err := wk.Search(r.Context(), req.Query, req.Limit)
	if err != nil {
		slog.Warn("weknora search failed", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "WeKnora search failed")
		return
	}

	response.OK(w, map[string]any{"results": results, "query": req.Query, "source": "weknora"})
}

// handleWeKnoraAddKnowledge creates a new knowledge entry in WeKnora from text/markdown.
func (s *Server) handleWeKnoraAddKnowledge(w http.ResponseWriter, r *http.Request) {
	wk := s.getWeKnoraClient()

	var req struct {
		Title    string         `json:"title"`
		Content  string         `json:"content"`
		Metadata map[string]any `json:"metadata"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	if req.Title == "" || req.Content == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "title and content are required")
		return
	}

	id, err := wk.CreateKnowledge(r.Context(), req.Title, req.Content, req.Metadata)
	if err != nil {
		slog.Warn("weknora add knowledge failed", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to add knowledge to WeKnora")
		return
	}

	response.Created(w, map[string]any{"id": id})
}

// handleWeKnoraAddFromURL imports a web page into WeKnora by URL.
func (s *Server) handleWeKnoraAddFromURL(w http.ResponseWriter, r *http.Request) {
	wk := s.getWeKnoraClient()

	var req struct {
		URL   string `json:"url"`
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	if req.URL == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "url is required")
		return
	}

	id, err := wk.CreateKnowledgeFromURL(r.Context(), req.URL, req.Title)
	if err != nil {
		slog.Warn("weknora add from URL failed", "error", err, "url", req.URL)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to import URL to WeKnora")
		return
	}

	response.Created(w, map[string]any{"id": id})
}

// handleWeKnoraUploadFile handles file upload to WeKnora (PDF, Word, images, etc.).
func (s *Server) handleWeKnoraUploadFile(w http.ResponseWriter, r *http.Request) {
	wk := s.getWeKnoraClient()

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
	id, err := wk.UploadFile(r.Context(), header.Filename, file, title)
	if err != nil {
		slog.Warn("weknora upload file failed", "error", err, "filename", header.Filename)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to upload file to WeKnora")
		return
	}

	response.Created(w, map[string]any{"id": id, "filename": header.Filename})
}

// handleWeKnoraListKnowledge lists knowledge entries in the WeKnora KB.
func (s *Server) handleWeKnoraListKnowledge(w http.ResponseWriter, r *http.Request) {
	wk := s.getWeKnoraClient()

	page := parseIntDefault(r.URL.Query().Get("page"), 1)
	pageSize := parseIntDefault(r.URL.Query().Get("page_size"), 20)

	entries, total, err := wk.ListKnowledge(r.Context(), page, pageSize)
	if err != nil {
		slog.Warn("weknora list knowledge failed", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list WeKnora knowledge")
		return
	}

	response.OK(w, map[string]any{"entries": entries, "total": total, "source": "weknora"})
}

// handleWeKnoraDeleteKnowledge deletes a knowledge entry from WeKnora.
func (s *Server) handleWeKnoraDeleteKnowledge(w http.ResponseWriter, r *http.Request) {
	wk := s.getWeKnoraClient()

	knowledgeID := chi.URLParam(r, "id")
	if knowledgeID == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "knowledge ID is required")
		return
	}

	if err := wk.DeleteKnowledge(r.Context(), knowledgeID); err != nil {
		slog.Warn("weknora delete knowledge failed", "error", err, "id", knowledgeID)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to delete knowledge from WeKnora")
		return
	}

	response.OK(w, map[string]any{"message": "knowledge entry deleted", "id": knowledgeID})
}

// handleWeKnoraListKBs lists all knowledge bases in the WeKnora instance.
func (s *Server) handleWeKnoraListKBs(w http.ResponseWriter, r *http.Request) {
	wk := s.getWeKnoraClient()

	kbs, err := wk.ListKnowledgeBases(r.Context())
	if err != nil {
		slog.Warn("weknora list KBs failed", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list WeKnora knowledge bases")
		return
	}

	response.OK(w, map[string]any{"knowledge_bases": kbs})
}

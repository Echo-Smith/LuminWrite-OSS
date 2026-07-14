package server

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/writing-agent-v2/pkg/response"
)

// ─── Knowledge Base ──────────────────────────────────────

func (s *Server) handleKBList(w http.ResponseWriter, r *http.Request) {
	source := r.URL.Query().Get("source")
	page := parseIntDefault(r.URL.Query().Get("page"), 1)
	pageSize := parseIntDefault(r.URL.Query().Get("page_size"), 20)

	if s.kbRepo == nil {
		response.OK(w, map[string]interface{}{"entries": []interface{}{}, "total": 0})
		return
	}

	entries, total, err := s.kbRepo.List(r.Context(), source, page, pageSize)
	if err != nil {
		slog.Warn("failed to list KB entries", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list knowledge base")
		return
	}

	response.OK(w, map[string]interface{}{"entries": entries, "total": total})
}

func (s *Server) handleKBAdd(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Source   string                 `json:"source"`
		SourceID string                 `json:"source_id"`
		Title    string                 `json:"title"`
		Content  string                 `json:"content"`
		Metadata map[string]interface{} `json:"metadata"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	if req.Title == "" || req.Content == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "title and content are required")
		return
	}

	if req.Source == "" {
		req.Source = "manual"
	}

	if s.kbRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	entry, err := s.kbRepo.AddEntry(r.Context(), req.Source, req.SourceID, req.Title, req.Content, req.Metadata)
	if err != nil {
		slog.Warn("failed to add KB entry", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to add knowledge base entry")
		return
	}

	response.Created(w, entry)
}

func (s *Server) handleKBDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if s.kbRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	if err := s.kbRepo.Delete(r.Context(), id); err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to delete entry")
		return
	}

	response.OK(w, map[string]interface{}{"message": "entry deleted"})
}

func (s *Server) handleKBSemanticSearch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Query  string `json:"query"`
		Source string `json:"source"`
		Limit  int    `json:"limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Also allow query params
		req.Query = r.URL.Query().Get("q")
		req.Source = r.URL.Query().Get("source")
		req.Limit = parseIntDefault(r.URL.Query().Get("limit"), 10)
	}

	if req.Query == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "query is required")
		return
	}

	if s.kbRepo == nil {
		response.OK(w, map[string]interface{}{"results": []interface{}{}})
		return
	}

	results, err := s.kbRepo.SemanticSearch(r.Context(), req.Query, req.Limit, req.Source)
	if err != nil {
		slog.Warn("semantic search failed", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "search failed")
		return
	}

	response.OK(w, map[string]interface{}{"results": results, "query": req.Query})
}

func (s *Server) handleKBGenerateEmbeddings(w http.ResponseWriter, r *http.Request) {
	if s.kbRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	count, err := s.kbRepo.GenerateMissingEmbeddings(r.Context(), 50)
	if err != nil {
		slog.Warn("failed to generate missing embeddings", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to generate embeddings")
		return
	}

	response.OK(w, map[string]interface{}{
		"generated": count,
		"message":   "embeddings generated for entries missing vectors",
	})
}

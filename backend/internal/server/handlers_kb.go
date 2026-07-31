package server

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/services"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── Knowledge Base Handlers ───────────────────────────
// All operations run on local PostgreSQL (paradedb + pgvector).
// API paths: /api/v2/kb/* (primary) and /api/v2/weknora/* (compat alias).

// kbAvailable returns true if the local knowledge base is configured.
func (s *Server) kbAvailable() bool {
	return s.kbMgr != nil && s.kbMgr.IsConfigured()
}

// handleKBStatus returns knowledge base configuration status (for admin panel).
func (s *Server) handleKBStatus(w http.ResponseWriter, r *http.Request) {
	status := map[string]any{
		"enabled":       s.kbMgr != nil,
		"base_url":      "local (internal)",
		"ui_url":        "",
		"kb_id":         "",
		"scheme_b":      false,
		"local_kb":      s.kbMgr != nil,
	}
	response.OK(w, status)
}

// handleKBListKBs returns all knowledge bases (real multi-KB list).
func (s *Server) handleKBListKBs(w http.ResponseWriter, r *http.Request) {
	if !s.kbAvailable() {
		response.Err(w, http.StatusServiceUnavailable, "kb_not_configured", "knowledge base is not configured")
		return
	}

	kbs, err := s.kbMgr.ListKBs(r.Context(), "")
	if err != nil {
		slog.Warn("KB list failed", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list KBs")
		return
	}

	response.OK(w, map[string]any{"knowledge_bases": kbs})
}

// ─── KB CRUD ────────────────────────────────────────────

// handleKBCreate creates a new knowledge base.
func (s *Server) handleKBCreate(w http.ResponseWriter, r *http.Request) {
	if !s.kbAvailable() {
		response.Err(w, http.StatusServiceUnavailable, "kb_not_configured", "knowledge base is not configured")
		return
	}

	var req struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	if req.Name == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "name is required")
		return
	}

	kb, err := s.kbMgr.CreateKB(r.Context(), req.ID, req.Name, req.Description, "")
	if err != nil {
		slog.Warn("KB create failed", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to create KB")
		return
	}

	response.Created(w, kb)
}

// handleKBUpdate updates a knowledge base.
func (s *Server) handleKBUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.kbAvailable() {
		response.Err(w, http.StatusServiceUnavailable, "kb_not_configured", "knowledge base is not configured")
		return
	}

	kbID := chi.URLParam(r, "id")
	if kbID == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "KB ID is required")
		return
	}

	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	kb, err := s.kbMgr.UpdateKB(r.Context(), kbID, req.Name, req.Description)
	if err != nil {
		slog.Warn("KB update failed", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to update KB")
		return
	}

	response.OK(w, kb)
}

// handleKBDeleteKB deletes a knowledge base (not the default one).
func (s *Server) handleKBDeleteKB(w http.ResponseWriter, r *http.Request) {
	if !s.kbAvailable() {
		response.Err(w, http.StatusServiceUnavailable, "kb_not_configured", "knowledge base is not configured")
		return
	}

	kbID := chi.URLParam(r, "id")
	if kbID == "" || kbID == "default" {
		response.Err(w, http.StatusBadRequest, "bad_request", "cannot delete the default knowledge base")
		return
	}

	if err := s.kbMgr.DeleteKB(r.Context(), kbID); err != nil {
		slog.Warn("KB delete failed", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to delete KB")
		return
	}

	response.OK(w, map[string]any{"message": "KB deleted", "id": kbID})
}

// ─── Knowledge operations (KB-scoped) ───────────────────

// handleKBListKnowledge lists knowledge entries in a specific KB.
func (s *Server) handleKBListKnowledge(w http.ResponseWriter, r *http.Request) {
	if !s.kbAvailable() {
		response.Err(w, http.StatusServiceUnavailable, "kb_not_configured", "knowledge base is not configured")
		return
	}

	kbID := r.URL.Query().Get("kb_id")
	if kbID == "" {
		kbID = "default"
	}
	page := parseIntDefault(r.URL.Query().Get("page"), 1)
	pageSize := parseIntDefault(r.URL.Query().Get("page_size"), 20)

	docs, total, err := s.kbMgr.ListDocumentsInKB(r.Context(), "", kbID, page, pageSize)
	if err != nil {
		slog.Warn("KB list knowledge failed", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list knowledge")
		return
	}

	response.OK(w, map[string]any{"entries": docs, "total": total, "kb_id": kbID, "source": "local_kb"})
}

// handleKBAddKnowledge creates a new knowledge entry from text/markdown.
func (s *Server) handleKBAddKnowledge(w http.ResponseWriter, r *http.Request) {
	if !s.kbAvailable() {
		response.Err(w, http.StatusServiceUnavailable, "kb_not_configured", "knowledge base is not configured")
		return
	}

	var req struct {
		Title    string         `json:"title"`
		Content  string         `json:"content"`
		Metadata map[string]any `json:"metadata"`
		KBID     string         `json:"kb_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	if req.Title == "" || req.Content == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "title and content are required")
		return
	}

	doc, err := s.kbMgr.AddDocumentToKB(r.Context(), "", req.KBID, req.Title, req.Content, "text", req.Metadata)
	if err != nil {
		slog.Warn("KB add knowledge failed", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to add knowledge")
		return
	}

	// Chunk and store
	chunkConfig := services.DefaultChunkConfig()
	chunks := services.ChunkText(req.Content, chunkConfig)
	for _, chunk := range chunks {
		s.kbMgr.AddChunk(r.Context(), doc.ID, "", chunk.Index, chunk.Title, chunk.Content, nil)
	}
	s.kbMgr.UpdateChunkCount(r.Context(), doc.ID, len(chunks))

	response.Created(w, map[string]any{"id": doc.ID})
}

// handleKBAddFromURL imports a web page into the local KB by URL.
func (s *Server) handleKBAddFromURL(w http.ResponseWriter, r *http.Request) {
	if !s.kbAvailable() {
		response.Err(w, http.StatusServiceUnavailable, "kb_not_configured", "knowledge base is not configured")
		return
	}

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

	importer := services.NewURLImporter(s.kbMgr, services.DefaultChunkConfig())
	docID, err := importer.ImportURL(r.Context(), "", req.URL, req.Title)
	if err != nil {
		slog.Warn("KB add from URL failed", "error", err, "url", req.URL)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to import URL")
		return
	}

	response.Created(w, map[string]any{"id": docID})
}

// handleKBUploadFile handles file upload to the local KB.
func (s *Server) handleKBUploadFile(w http.ResponseWriter, r *http.Request) {
	if !s.kbAvailable() {
		response.Err(w, http.StatusServiceUnavailable, "kb_not_configured", "knowledge base is not configured")
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

	chunkConfig := services.DefaultChunkConfig()
	parser := services.NewFileParser(s.kbMgr, chunkConfig, s.cfg.Kb.DocreaderAddr)
	docID, err := parser.ParseAndImport(r.Context(), "", header.Filename, file, title)
	if err != nil {
		slog.Warn("KB upload file failed", "error", err, "filename", header.Filename)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to upload file")
		return
	}

	response.Created(w, map[string]any{"id": docID, "filename": header.Filename})
}

// handleKBDeleteKnowledge deletes a knowledge entry from the local KB.
func (s *Server) handleKBDeleteKnowledge(w http.ResponseWriter, r *http.Request) {
	if !s.kbAvailable() {
		response.Err(w, http.StatusServiceUnavailable, "kb_not_configured", "knowledge base is not configured")
		return
	}

	knowledgeID := chi.URLParam(r, "id")
	if knowledgeID == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "knowledge ID is required")
		return
	}

	if err := s.kbMgr.DeleteDocument(r.Context(), "", knowledgeID); err != nil {
		slog.Warn("KB delete knowledge failed", "error", err, "id", knowledgeID)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to delete knowledge")
		return
	}

	response.OK(w, map[string]any{"message": "knowledge entry deleted", "id": knowledgeID})
}

// handleKBSearch performs a hybrid search (BM25 + Dense + RRF) on the local KB.
// Supports optional mode parameter: "hybrid" (default), "bm25", "dense".
// Supports optional bm25_weight / dense_weight overrides.
func (s *Server) handleKBSearch(w http.ResponseWriter, r *http.Request) {
	if !s.kbAvailable() {
		response.Err(w, http.StatusServiceUnavailable, "kb_not_configured", "knowledge base is not configured")
		return
	}

	var req struct {
		Query      string  `json:"query"`
		Limit      int     `json:"limit"`
		Mode       string  `json:"mode"`
		BM25Weight float64 `json:"bm25_weight"`
		DenseWeight float64 `json:"dense_weight"`
		KBID       string  `json:"kb_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		req.Query = r.URL.Query().Get("q")
		req.Limit = parseIntDefault(r.URL.Query().Get("limit"), 10)
		req.Mode = r.URL.Query().Get("mode")
	}

	if req.Query == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "query is required")
		return
	}

	mode := services.SearchMode(req.Mode)
	if mode == "" {
		mode = services.SearchModeHybrid
	}

	results, err := s.kbMgr.HybridSearchInKB(r.Context(), "", req.KBID, req.Query, req.Limit, mode, req.BM25Weight, req.DenseWeight)
	if err != nil {
		slog.Warn("KB search failed", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "search failed")
		return
	}

	response.OK(w, map[string]any{
		"results":       results,
		"query":         req.Query,
		"mode":          string(mode),
		"bm25_weight":   req.BM25Weight,
		"dense_weight":  req.DenseWeight,
		"source":        "local_kb",
	})
}

// ─── Stats & Document Detail ────────────────────────────

// handleKBStats returns aggregate statistics about the knowledge base.
func (s *Server) handleKBStats(w http.ResponseWriter, r *http.Request) {
	if !s.kbAvailable() {
		response.Err(w, http.StatusServiceUnavailable, "kb_not_configured", "knowledge base is not configured")
		return
	}

	kbID := r.URL.Query().Get("kb_id")
	if kbID == "" {
		kbID = "default"
	}

	stats, err := s.kbMgr.GetStatsForKB(r.Context(), kbID)
	if err != nil {
		slog.Warn("KB stats failed", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to get stats")
		return
	}

	response.OK(w, stats)
}

// handleKBGetDocumentChunks returns all chunks for a document.
func (s *Server) handleKBGetDocumentChunks(w http.ResponseWriter, r *http.Request) {
	if !s.kbAvailable() {
		response.Err(w, http.StatusServiceUnavailable, "kb_not_configured", "knowledge base is not configured")
		return
	}

	docID := chi.URLParam(r, "id")
	if docID == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "document ID is required")
		return
	}

	chunks, err := s.kbMgr.ListChunks(r.Context(), docID)
	if err != nil {
		slog.Warn("KB list chunks failed", "error", err, "doc_id", docID)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list chunks")
		return
	}

	response.OK(w, map[string]any{"chunks": chunks, "doc_id": docID})
}

// handleKBGetDocumentEntities returns entities and relations for a document.
func (s *Server) handleKBGetDocumentEntities(w http.ResponseWriter, r *http.Request) {
	if !s.kbAvailable() {
		response.Err(w, http.StatusServiceUnavailable, "kb_not_configured", "knowledge base is not configured")
		return
	}

	docID := chi.URLParam(r, "id")
	if docID == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "document ID is required")
		return
	}

	// Entities and relations require GraphRAG — query directly from DB
	entities, err := s.kbMgr.GetDocEntities(r.Context(), docID)
	if err != nil {
		entities = nil
	}

	relations, err := s.kbMgr.GetDocRelations(r.Context(), docID)
	if err != nil {
		relations = nil
	}

	response.OK(w, map[string]any{
		"entities":  entities,
		"relations": relations,
		"doc_id":    docID,
	})
}

// handleKBGetGraph returns the global entity graph for visualization.
func (s *Server) handleKBGetGraph(w http.ResponseWriter, r *http.Request) {
	if !s.kbAvailable() {
		response.Err(w, http.StatusServiceUnavailable, "kb_not_configured", "knowledge base is not configured")
		return
	}

	limit := parseIntDefault(r.URL.Query().Get("limit"), 50)

	entities, relations, err := s.kbMgr.GetGlobalGraph(r.Context(), limit)
	if err != nil {
		slog.Warn("KB graph query failed", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to get graph")
		return
	}

	response.OK(w, map[string]any{
		"nodes": entities,
		"edges": relations,
	})
}

// ─── Legacy KB handlers (knowledge_base table — pre-WeKnora) ───

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

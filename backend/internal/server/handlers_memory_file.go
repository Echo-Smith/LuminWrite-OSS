package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── Memory File Handlers (Markdown Memory Layer) ─────────
//
// These endpoints provide CRUD operations for the Markdown-based
// memory file layer.  Users can:
//   - GET  /memories/file          → view their memory as Markdown
//   - POST /memories/file/export   → export DB memories to file
//   - POST /memories/file/import   → import file back to DB
//   - GET  /memories/global        → view global memory (CLAUDE.md style)
//   - PUT  /memories/global        → update global memory

// handleGetMemoryFile returns the user's memory file as Markdown.
func (s *Server) handleGetMemoryFile(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil || user.Sub == "" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "user not found")
		return
	}
	userID := user.Sub

	if s.memorySvc == nil || !s.memorySvc.IsAvailable() {
		response.OK(w, map[string]any{
			"content":  "",
			"message":  "memory service not available",
		})
		return
	}

	content, err := s.memorySvc.GetMemoryFileMarkdown(userID)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to get memory file")
		return
	}

	// If no file exists, auto-export from DB
	if content == "" {
		if err := s.memorySvc.ExportMemoryFile(r.Context(), userID); err == nil {
			content, _ = s.memorySvc.GetMemoryFileMarkdown(userID)
		}
	}

	response.OK(w, map[string]any{
		"content": content,
		"user_id": userID,
	})
}

// handleExportMemoryFile exports the user's DB memories to a Markdown file.
func (s *Server) handleExportMemoryFile(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil || user.Sub == "" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "user not found")
		return
	}
	userID := user.Sub

	if s.memorySvc == nil || !s.memorySvc.IsAvailable() {
		response.Err(w, http.StatusServiceUnavailable, "unavailable", "memory service not available")
		return
	}

	if err := s.memorySvc.ExportMemoryFile(r.Context(), userID); err != nil {
		response.Err(w, http.StatusInternalServerError, "export_failed", "failed to export memory file")
		return
	}

	// Return the exported content
	content, _ := s.memorySvc.GetMemoryFileMarkdown(userID)

	response.OK(w, map[string]any{
		"message": "memory file exported",
		"content": content,
	})
}

// handleImportMemoryFile imports a Markdown memory file and syncs to DB.
func (s *Server) handleImportMemoryFile(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil || user.Sub == "" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "user not found")
		return
	}
	userID := user.Sub

	if s.memorySvc == nil || !s.memorySvc.IsAvailable() {
		response.Err(w, http.StatusServiceUnavailable, "unavailable", "memory service not available")
		return
	}

	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	if strings.TrimSpace(req.Content) == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "content is required")
		return
	}

	synced, err := s.memorySvc.ImportMemoryFile(r.Context(), userID, req.Content)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "import_failed", "failed to import memory file")
		return
	}

	response.OK(w, map[string]any{
		"message":     "memory file imported and synced",
		"synced_count": synced,
	})
}

// handleGetGlobalMemory returns the global memory file content.
func (s *Server) handleGetGlobalMemory(w http.ResponseWriter, r *http.Request) {
	if s.memorySvc == nil || !s.memorySvc.IsAvailable() {
		response.OK(w, map[string]any{
			"content":  "",
			"entries":  []any{},
			"message":  "memory service not available",
		})
		return
	}

	md, entries, err := s.memorySvc.GetGlobalMemoryFile(r.Context())
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to get global memory")
		return
	}

	response.OK(w, map[string]any{
		"content": md,
		"entries": entries,
	})
}

// handleUpdateGlobalMemory updates the global memory file.
// This is an admin-only operation.
func (s *Server) handleUpdateGlobalMemory(w http.ResponseWriter, r *http.Request) {
	// Only admins can update the global memory
	user := userFromContext(r.Context())
	if user == nil || user.Role != "admin" {
		response.Err(w, http.StatusForbidden, "forbidden", "admin access required")
		return
	}

	if s.memorySvc == nil || !s.memorySvc.IsAvailable() {
		response.Err(w, http.StatusServiceUnavailable, "unavailable", "memory service not available")
		return
	}

	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	if err := s.memorySvc.SaveGlobalMemoryFile(req.Content); err != nil {
		response.Err(w, http.StatusInternalServerError, "save_failed", "failed to save global memory")
		return
	}

	response.OK(w, map[string]any{
		"message": "global memory file updated",
	})
}

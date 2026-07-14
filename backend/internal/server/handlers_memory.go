package server

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/memory"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── Memory Handlers ─────────────────────────────────────

// handleListMemories 列出当前用户的记忆
// GET /api/v2/memories?tier=hard&status=active&limit=50
func (s *Server) handleListMemories(w http.ResponseWriter, r *http.Request) {
	if s.memorySvc == nil || !s.memorySvc.IsAvailable() {
		response.Err(w, http.StatusServiceUnavailable, "memory_unavailable", "memory service not available")
		return
	}

	user := userFromContext(r.Context())
	if user == nil {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	opts := memory.ListOptions{Limit: 50}

	if tier := r.URL.Query().Get("tier"); tier != "" {
		t := memory.Tier(tier)
		opts.Tier = &t
	}
	if status := r.URL.Query().Get("status"); status != "" {
		st := memory.MemoryStatus(status)
		opts.Status = &st
	}
	if limit := r.URL.Query().Get("limit"); limit != "" {
		if n, err := strconv.Atoi(limit); err == nil && n > 0 && n <= 200 {
			opts.Limit = n
		}
	}

	memories, err := s.memorySvc.List(r.Context(), user.Sub, opts)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list memories")
		return
	}

	response.OK(w, map[string]interface{}{
		"memories": memories,
		"count":    len(memories),
	})
}

// handleCreateMemory 创建 Tier 1 硬偏好
// POST /api/v2/memories
// Body: {"category": "...", "key": "...", "value": "..."}
func (s *Server) handleCreateMemory(w http.ResponseWriter, r *http.Request) {
	if s.memorySvc == nil || !s.memorySvc.IsAvailable() {
		response.Err(w, http.StatusServiceUnavailable, "memory_unavailable", "memory service not available")
		return
	}

	user := userFromContext(r.Context())
	if user == nil {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	var req struct {
		Category string `json:"category"`
		Key      string `json:"key"`
		Value    string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	if req.Category == "" || req.Key == "" || req.Value == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "category, key, and value are required")
		return
	}

	mem, err := s.memorySvc.Create(r.Context(), user.Sub, req.Category, req.Key, req.Value)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to create memory")
		return
	}

	response.Created(w, mem)
}

// handleDeleteMemory 删除记忆
// DELETE /api/v2/memories/{id}
func (s *Server) handleDeleteMemory(w http.ResponseWriter, r *http.Request) {
	if s.memorySvc == nil || !s.memorySvc.IsAvailable() {
		response.Err(w, http.StatusServiceUnavailable, "memory_unavailable", "memory service not available")
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "memory id required")
		return
	}

	if err := s.memorySvc.Delete(r.Context(), id); err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to delete memory")
		return
	}

	response.OK(w, map[string]interface{}{"deleted": true, "id": id})
}

// handleDismissMemory 关闭记忆（本次会话不注入）
// POST /api/v2/memories/{id}/dismiss
// Body: {"session_id": "..."}
func (s *Server) handleDismissMemory(w http.ResponseWriter, r *http.Request) {
	if s.memorySvc == nil || !s.memorySvc.IsAvailable() {
		response.Err(w, http.StatusServiceUnavailable, "memory_unavailable", "memory service not available")
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "memory id required")
		return
	}

	var req struct {
		SessionID string `json:"session_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.SessionID == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "session_id is required")
		return
	}

	if err := s.memorySvc.Dismiss(r.Context(), id, req.SessionID); err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to dismiss memory")
		return
	}

	response.OK(w, map[string]interface{}{"dismissed": true, "id": id})
}

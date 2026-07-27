package editorial

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// Handlers 编辑部 HTTP 处理器
type Handlers struct {
	svc *Service
}

// NewHandlers 创建处理器
func NewHandlers(svc *Service) *Handlers {
	return &Handlers{svc: svc}
}

// RegisterRoutes 注册路由到 chi router
func (h *Handlers) RegisterRoutes(r chi.Router) {
	r.Route("/editorial", func(r chi.Router) {
		// 任务管理
		r.Post("/tasks", h.handleCreateTask)
		r.Get("/tasks", h.handleListTasks)
		r.Get("/tasks/{id}", h.handleGetTask)
		r.Post("/tasks/{id}/advance", h.handleAdvanceTask)

		// Artifact 管理
		r.Get("/tasks/{id}/artifacts", h.handleListArtifacts)
		r.Post("/tasks/{id}/artifacts", h.handleSubmitArtifact)
		r.Get("/artifacts/{id}", h.handleGetArtifact)
		r.Patch("/artifacts/{id}/review", h.handleReviewArtifact)

		// Decision 管理
		r.Get("/tasks/{id}/decisions", h.handleListDecisions)
		r.Post("/tasks/{id}/decisions", h.handleCreateDecision)

		// 统计
		r.Get("/stats", h.handleGetStats)
	})
}

// userIDFromContext 安全地从 context 中获取 userID
func userIDFromContext(ctx context.Context) string {
	if v := ctx.Value(userIDCtxKey); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

type ctxKey string

const userIDCtxKey ctxKey = "userID"

// ─── 任务处理 ─────────────────────────────────────────────

func (h *Handlers) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var input CreateTaskInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		response.JSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if input.Title == "" {
		response.JSON(w, http.StatusBadRequest, map[string]string{"error": "title is required"})
		return
	}

	userID := userIDFromContext(r.Context())
	task, err := h.svc.CreateTask(r.Context(), input, userID)
	if err != nil {
		response.JSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	response.JSON(w, http.StatusCreated, task)
}

func (h *Handlers) handleListTasks(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	tasks, err := h.svc.ListTasks(r.Context(), status, limit, offset)
	if err != nil {
		response.JSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	response.JSON(w, http.StatusOK, tasks)
}

func (h *Handlers) handleGetTask(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	task, err := h.svc.GetTask(r.Context(), taskID)
	if err != nil {
		if err == ErrTaskNotFound {
			response.JSON(w, http.StatusNotFound, map[string]string{"error": "task not found"})
			return
		}
		response.JSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	response.JSON(w, http.StatusOK, task)
}

func (h *Handlers) handleAdvanceTask(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	var input AdvanceTaskInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		response.JSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if err := h.svc.AdvanceTask(r.Context(), taskID, input); err != nil {
		if err == ErrTaskNotFound {
			response.JSON(w, http.StatusNotFound, map[string]string{"error": "task not found"})
			return
		}
		if err == ErrInvalidTransition {
			response.JSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		response.JSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	response.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ─── Artifact 处理 ───────────────────────────────────────

func (h *Handlers) handleSubmitArtifact(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	var input SubmitArtifactInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		response.JSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	art, err := h.svc.SubmitArtifact(r.Context(), taskID, input)
	if err != nil {
		response.JSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	response.JSON(w, http.StatusCreated, art)
}

func (h *Handlers) handleGetArtifact(w http.ResponseWriter, r *http.Request) {
	artID := chi.URLParam(r, "id")
	art, err := h.svc.GetArtifact(r.Context(), artID)
	if err != nil {
		if err == ErrArtifactNotFound {
			response.JSON(w, http.StatusNotFound, map[string]string{"error": "artifact not found"})
			return
		}
		response.JSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	response.JSON(w, http.StatusOK, art)
}

func (h *Handlers) handleListArtifacts(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	artifacts, err := h.svc.ListArtifacts(r.Context(), taskID)
	if err != nil {
		response.JSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	response.JSON(w, http.StatusOK, artifacts)
}

func (h *Handlers) handleReviewArtifact(w http.ResponseWriter, r *http.Request) {
	artID := chi.URLParam(r, "id")
	var input ReviewArtifactInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		response.JSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	userID := userIDFromContext(r.Context())
	input.ReviewerID = userID

	art, err := h.svc.ReviewArtifact(r.Context(), artID, input)
	if err != nil {
		if err == ErrArtifactNotFound {
			response.JSON(w, http.StatusNotFound, map[string]string{"error": "artifact not found"})
			return
		}
		response.JSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	response.JSON(w, http.StatusOK, art)
}

// ─── Decision 处理 ───────────────────────────────────────

func (h *Handlers) handleCreateDecision(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	var input CreateDecisionInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		response.JSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	userID := userIDFromContext(r.Context())
	if input.DecidedBy == "" {
		input.DecidedBy = userID
	}
	if input.DecidedByType == "" {
		input.DecidedByType = DecidedByHuman
	}

	d, err := h.svc.CreateDecision(r.Context(), taskID, input)
	if err != nil {
		response.JSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	response.JSON(w, http.StatusCreated, d)
}

func (h *Handlers) handleListDecisions(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	decisions, err := h.svc.ListDecisions(r.Context(), taskID)
	if err != nil {
		response.JSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	response.JSON(w, http.StatusOK, decisions)
}

// ─── 统计 ─────────────────────────────────────────────────

func (h *Handlers) handleGetStats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.svc.GetStats(r.Context())
	if err != nil {
		response.JSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	response.JSON(w, http.StatusOK, stats)
}

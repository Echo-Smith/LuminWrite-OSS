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
		r.Get("/decisions/pending", h.handleListPendingDecisions)
		r.Patch("/decisions/{id}/resolve", h.handleResolveDecision)

		// 组织记忆
		r.Get("/sources", h.handleListSources)
		r.Get("/sources/{domain}", h.handleGetSource)
		r.Get("/columns", h.handleListColumns)
		r.Put("/columns/{tag}", h.handleUpsertColumn)
		r.Get("/knowledge", h.handleListKnowledge)
		r.Post("/knowledge", h.handleCreateKnowledge)

		// Agent 信誉
		r.Get("/agent-reputation", h.handleListAgentReputation)

		// Artifact 版本
		r.Get("/artifacts/{id}/versions", h.handleListArtifactVersions)

		// 对照实验
		r.Post("/experiments", h.handleCreateExperiment)
		r.Get("/experiments", h.handleListExperiments)
		r.Get("/experiments/{id}", h.handleGetExperiment)
		r.Post("/experiments/{id}/run", h.handleRunExperiment)

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
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	if input.Title == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "title is required")
		return
	}

	userID := userIDFromContext(r.Context())
	task, err := h.svc.CreateTask(r.Context(), input, userID)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	response.Created(w, task)
}

func (h *Handlers) handleListTasks(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	tasks, err := h.svc.ListTasks(r.Context(), status, limit, offset)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	response.OK(w, map[string]interface{}{"tasks": tasks})
}

func (h *Handlers) handleGetTask(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	task, err := h.svc.GetTask(r.Context(), taskID)
	if err != nil {
		if err == ErrTaskNotFound {
			response.Err(w, http.StatusNotFound, "not_found", "task not found")
			return
		}
		response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	response.OK(w, task)
}

func (h *Handlers) handleAdvanceTask(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	var input AdvanceTaskInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	// 从 context 注入 decidedBy
	if input.DecidedBy == "" {
		input.DecidedBy = userIDFromContext(r.Context())
	}

	if err := h.svc.AdvanceTask(r.Context(), taskID, input); err != nil {
		if err == ErrTaskNotFound {
			response.Err(w, http.StatusNotFound, "not_found", "task not found")
			return
		}
		if err == ErrInvalidTransition {
			response.Err(w, http.StatusConflict, "invalid_transition", err.Error())
			return
		}
		response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	response.OK(w, map[string]string{"status": "ok"})
}

// ─── Artifact 处理 ───────────────────────────────────────

func (h *Handlers) handleSubmitArtifact(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	var input SubmitArtifactInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	art, err := h.svc.SubmitArtifact(r.Context(), taskID, input)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	response.Created(w, art)
}

func (h *Handlers) handleGetArtifact(w http.ResponseWriter, r *http.Request) {
	artID := chi.URLParam(r, "id")
	art, err := h.svc.GetArtifact(r.Context(), artID)
	if err != nil {
		if err == ErrArtifactNotFound {
			response.Err(w, http.StatusNotFound, "not_found", "artifact not found")
			return
		}
		response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	response.OK(w, art)
}

func (h *Handlers) handleListArtifacts(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	artifacts, err := h.svc.ListArtifacts(r.Context(), taskID)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	response.OK(w, map[string]interface{}{"artifacts": artifacts})
}

func (h *Handlers) handleReviewArtifact(w http.ResponseWriter, r *http.Request) {
	artID := chi.URLParam(r, "id")
	var input ReviewArtifactInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	userID := userIDFromContext(r.Context())
	input.ReviewerID = userID

	art, err := h.svc.ReviewArtifact(r.Context(), artID, input)
	if err != nil {
		if err == ErrArtifactNotFound {
			response.Err(w, http.StatusNotFound, "not_found", "artifact not found")
			return
		}
		response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	response.OK(w, art)
}

// ─── Decision 处理 ───────────────────────────────────────

func (h *Handlers) handleCreateDecision(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	var input CreateDecisionInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
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
		response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	response.Created(w, d)
}

func (h *Handlers) handleListDecisions(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	decisions, err := h.svc.ListDecisions(r.Context(), taskID)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	response.OK(w, map[string]interface{}{"decisions": decisions})
}

// ─── 全局待处理决策 ─────────────────────────────────────

func (h *Handlers) handleListPendingDecisions(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	results, err := h.svc.ListPendingDecisions(r.Context(), limit)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	response.OK(w, map[string]interface{}{"pending": results})
}

func (h *Handlers) handleResolveDecision(w http.ResponseWriter, r *http.Request) {
	decisionID := chi.URLParam(r, "id")
	var input ResolveDecisionInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	if input.Status != DecisionStatusApproved && input.Status != DecisionStatusRejected {
		response.Err(w, http.StatusBadRequest, "bad_request", "status must be approved or rejected")
		return
	}

	userOK := userIDFromContext(r.Context())
	d, err := h.svc.ResolveDecision(r.Context(), decisionID, input, userOK)
	if err != nil {
		if err == ErrDecisionNotFound {
			response.Err(w, http.StatusNotFound, "not_found", "decision not found")
			return
		}
		response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	response.OK(w, d)
}

// ─── 统计 ─────────────────────────────────────────────────

func (h *Handlers) handleGetStats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.svc.GetStats(r.Context())
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	response.OK(w, stats)
}

// ─── 组织记忆 ─────────────────────────────────────────────

func (h *Handlers) handleListSources(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	sources, err := h.svc.ListSourceCredibility(r.Context(), limit)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	response.OK(w, map[string]interface{}{"sources": sources})
}

func (h *Handlers) handleGetSource(w http.ResponseWriter, r *http.Request) {
	domain := chi.URLParam(r, "domain")
	sc, err := h.svc.GetSourceCredibility(r.Context(), domain)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if sc == nil {
		response.OK(w, map[string]interface{}{"source": nil})
		return
	}
	response.OK(w, map[string]interface{}{"source": sc})
}

func (h *Handlers) handleListColumns(w http.ResponseWriter, r *http.Request) {
	cols, err := h.svc.ListColumnPreferences(r.Context())
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	response.OK(w, map[string]interface{}{"columns": cols})
}

func (h *Handlers) handleUpsertColumn(w http.ResponseWriter, r *http.Request) {
	tag := chi.URLParam(r, "tag")
	var cp ColumnPreference
	if err := json.NewDecoder(r.Body).Decode(&cp); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	cp.ColumnTag = tag
	result, err := h.svc.UpsertColumnPreference(r.Context(), cp)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	response.OK(w, result)
}

func (h *Handlers) handleListKnowledge(w http.ResponseWriter, r *http.Request) {
	category := r.URL.Query().Get("category")
	columnTag := r.URL.Query().Get("column")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	knowledge, err := h.svc.ListKnowledge(r.Context(), category, columnTag, limit)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	response.OK(w, map[string]interface{}{"knowledge": knowledge})
}

func (h *Handlers) handleCreateKnowledge(w http.ResponseWriter, r *http.Request) {
	var k EditorialKnowledge
	if err := json.NewDecoder(r.Body).Decode(&k); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	result, err := h.svc.CreateKnowledge(r.Context(), k)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	response.Created(w, result)
}

// ─── Agent 信誉 ───────────────────────────────────────────

func (h *Handlers) handleListAgentReputation(w http.ResponseWriter, r *http.Request) {
	reps, err := h.svc.ListAgentReputation(r.Context())
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	response.OK(w, map[string]interface{}{"agents": reps})
}

// ─── Artifact 版本 ────────────────────────────────────────

func (h *Handlers) handleListArtifactVersions(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	art, err := h.svc.GetArtifact(r.Context(), id)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if art == nil {
		response.Err(w, http.StatusNotFound, "not_found", "artifact not found")
		return
	}
	versions, err := h.svc.ListArtifacts(r.Context(), art.TaskID)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	// 过滤同类型版本
	var sameType []Artifact
	for _, a := range versions {
		if a.Type == art.Type {
			sameType = append(sameType, a)
		}
	}
	response.OK(w, map[string]interface{}{"versions": sameType})
}

// ─── 对照实验 ─────────────────────────────────────────────

func (h *Handlers) handleCreateExperiment(w http.ResponseWriter, r *http.Request) {
	var input CreateExperimentInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	if input.Title == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "title is required")
		return
	}

	userID := userIDFromContext(r.Context())
	exp, err := h.svc.CreateExperiment(r.Context(), input, userID)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	response.Created(w, exp)
}

func (h *Handlers) handleListExperiments(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	exps, err := h.svc.ListExperiments(r.Context(), limit)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	response.OK(w, map[string]interface{}{"experiments": exps})
}

func (h *Handlers) handleGetExperiment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	exp, err := h.svc.GetExperiment(r.Context(), id)
	if err != nil {
		response.Err(w, http.StatusNotFound, "not_found", "experiment not found")
		return
	}
	response.OK(w, exp)
}

func (h *Handlers) handleRunExperiment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.svc.RunExperiment(r.Context(), id); err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	response.OK(w, map[string]string{"status": "running"})
}

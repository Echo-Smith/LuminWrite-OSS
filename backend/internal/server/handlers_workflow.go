package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/editorial"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/auth"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── Workflow 命令门面（REST）────────────────────────────
//
// Editorial Transport Migration：编辑部 DAG 工作流的命令通道从 WebSocket
// 消息（workflow.start / workflow.execute / workflow.cancel）迁移为 REST：
//
//	POST   /api/v2/workflows            规划（Planner 生成 DAG + 持久化 Task）
//	POST   /api/v2/workflows/{id}/execute  启动 DAG 执行
//	POST   /api/v2/workflows/{id}/cancel   取消执行
//	GET    /api/v2/workflows/{id}          当前任务状态
//
// 进度事件经 SSEHub 以 workflow:*/node:* 事件定向推送给 task owner
// （见 editorial_adapter.go）。
//
// 注意：刻意没有 pause/resume 端点。DAGExecutor 尚无可恢复的
// checkpoint/resume 语义（旧 WS 处理器中的 TODO 从未实现），
// 返回 "not_yet_implemented" 的假端点不是诚实的契约 —— 在真实语义
// 落地之前，API 的正确形态是不存在这些端点。

// workflowCreateRequest 映射旧 WS WorkflowStartPayload（命令部分）。
type workflowCreateRequest struct {
	UserInput   string   `json:"user_input"`
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description,omitempty"`
	StyleSlug   string   `json:"style_slug,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	KBEnabled   *bool    `json:"kb_enabled,omitempty"`
}

// handleWorkflowCreate 处理 POST /api/v2/workflows
// 调用 Planner 生成 DAG 工作流，持久化 Task 并缓存 plan。
// 返回与旧 WS workflow.created 相同结构的视图。
func (s *Server) handleWorkflowCreate(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	userID := user.Sub

	var req workflowCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	if req.UserInput == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "user_input is required")
		return
	}
	if s.planner == nil || s.dagExecutor == nil || s.editorialSvc == nil {
		response.Err(w, http.StatusServiceUnavailable, "editorial_unavailable", "planner, dag executor, or editorial service not initialized")
		return
	}

	// Planner 依赖 LLM，给足超时（与旧 WS 路径一致）
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	// 先用临时 ID 供 Planner 使用，DB Task ID 在 CreateTask 后获得
	tempID := editorial.GenerateNodeID()

	planResult, err := s.planner.Plan(ctx, editorial.PlanInput{
		UserInput:   req.UserInput,
		Title:       req.Title,
		Description: req.Description,
		StyleSlug:   req.StyleSlug,
		Tags:        req.Tags,
		KBEnabled:   req.KBEnabled,
	}, tempID, userID)
	if err != nil {
		slog.Error("workflow: planner failed", "error", err, "user_input", req.UserInput)
		response.Err(w, http.StatusInternalServerError, "plan_failed", err.Error())
		return
	}

	// 构建 Task 标题和描述
	taskTitle := req.Title
	if taskTitle == "" {
		taskTitle = req.UserInput
		if len([]rune(taskTitle)) > 50 {
			taskTitle = string([]rune(taskTitle)[:50]) + "..."
		}
	}
	taskDescription := req.Description
	if taskDescription == "" {
		taskDescription = req.UserInput
		if len([]rune(taskDescription)) > 200 {
			taskDescription = string([]rune(taskDescription)[:200]) + "..."
		}
	}

	// 将 Task 持久化到 DB（CreateTask 同时自动创建选题卡 Artifact 并批准）
	dbTask, err := s.editorialSvc.CreateTask(ctx, editorial.CreateTaskInput{
		Title:       taskTitle,
		Description: taskDescription,
		StyleSlug:   planResult.StyleSlug,
		Tags:        planResult.Tags,
		TokenBudget: 200000,
	}, userID)
	if err != nil {
		slog.Error("workflow: failed to persist task to DB", "error", err)
		response.Err(w, http.StatusInternalServerError, "task_create_failed", "failed to create task: "+err.Error())
		return
	}

	// 用 DB 返回的真正 Task ID 替换临时 ID
	taskID := dbTask.ID

	// 缓存 plan 结果到 DAGExecutor，同时注册生成的 Agent
	s.dagExecutor.CachePlan(taskID, planResult)

	// 持久化 plan 到 agent_traces.plan_json，使历史会话能恢复 DAG 视图
	if err := s.editorialSvc.Store().SavePlan(ctx, taskID, planResult); err != nil {
		slog.Warn("workflow: failed to persist plan to DB", "error", err, "task_id", taskID)
		// 非致命错误，继续执行
	} else {
		slog.Info("workflow: plan persisted to DB", "task_id", taskID)
	}

	slog.Info("workflow: plan created and task persisted",
		"task_id", taskID, "db_task_id", dbTask.ID,
		"agents", len(planResult.Agents), "nodes", len(planResult.Workflow.Nodes))

	response.Created(w, map[string]interface{}{
		"task_id":   taskID,
		"agents":    planResult.Agents,
		"workflow":  planResult.Workflow,
		"rationale": planResult.Rationale,
	})
}

// handleWorkflowExecute 处理 POST /api/v2/workflows/{id}/execute
// 从缓存/DB 恢复 plan，加载 Task，异步启动 DAG 执行。
// 执行进度经 SSE（workflow:*/node:*）推送给 task owner。
func (s *Server) handleWorkflowExecute(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	userID := user.Sub
	taskID := chi.URLParam(r, "id")

	if s.dagExecutor == nil || s.editorialSvc == nil {
		response.Err(w, http.StatusServiceUnavailable, "editorial_unavailable", "dag executor or editorial service not initialized")
		return
	}

	// 所有权校验（owner 或 admin 可执行）
	if _, err := s.editorialSvc.GetTaskForUser(r.Context(), taskID, userID, auth.IsAdminFromContext(r.Context())); err != nil {
		respondTaskAuthError(w, err)
		return
	}

	// 从缓存获取 plan；缓存 miss（如后端重启/换实例）时从 DB plan_json 恢复，
	// 否则两段式启动的第二次确认会报 "plan not found"，DAG 永远无法续跑
	plan, ok := s.dagExecutor.GetPlan(taskID)
	if !ok {
		dbPlan, err := s.editorialSvc.Store().GetPlan(r.Context(), taskID)
		if err != nil || dbPlan == nil {
			response.Err(w, http.StatusNotFound, "plan_not_found", "plan not found for task_id: "+taskID)
			return
		}
		s.dagExecutor.CachePlan(taskID, dbPlan)
		plan = dbPlan
		slog.Info("workflow: plan restored from DB", "task_id", taskID)
	}

	// 从 DB 加载 Task（不再内存构造，确保 task.ID 与 DB 一致）
	task, err := s.editorialSvc.GetTask(r.Context(), taskID)
	if err != nil {
		slog.Error("workflow: failed to load task from DB", "error", err, "task_id", taskID)
		response.Err(w, http.StatusInternalServerError, "task_load_failed", "failed to load task: "+err.Error())
		return
	}

	// 确保 OwnerID 正确（以防 DB 中为空）
	if task.OwnerID == "" {
		task.OwnerID = userID
	}

	// 异步执行 DAG
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		// 执行失败时 DAGExecutor.finalize 会发出 workflow.failed 事件
		// （经 SSE 定向给 owner），这里只记录日志。
		if err := s.dagExecutor.Execute(ctx, &plan.Workflow, task); err != nil {
			slog.Error("workflow: DAG execution failed", "error", err, "task_id", taskID)
			return
		}

		// ── DAG 执行成功：提取最终文章并创建 trace 记录 ──
		// 使编辑部模式产生的文章也出现在用户的写作历史列表中
		s.recordEditorialArticle(ctx, task, plan, userID)
	}()

	slog.Info("workflow: execution started", "task_id", taskID, "nodes", len(plan.Workflow.Nodes))
	response.OK(w, map[string]interface{}{
		"task_id":    taskID,
		"status":     "running",
		"node_count": len(plan.Workflow.Nodes),
	})
}

// handleWorkflowCancel 处理 POST /api/v2/workflows/{id}/cancel
// 调用 DAGExecutor.Cancel 取消正在执行的 DAG 工作流。
func (s *Server) handleWorkflowCancel(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	taskID := chi.URLParam(r, "id")

	if s.dagExecutor == nil {
		response.Err(w, http.StatusServiceUnavailable, "editorial_unavailable", "dag executor not initialized")
		return
	}

	// 所有权校验（owner 或 admin 可取消）
	if s.editorialSvc != nil {
		if _, err := s.editorialSvc.GetTaskForUser(r.Context(), taskID, user.Sub, auth.IsAdminFromContext(r.Context())); err != nil {
			respondTaskAuthError(w, err)
			return
		}
	}

	cancelled := s.dagExecutor.Cancel(taskID)
	if !cancelled {
		// 执行器内存中没有该任务（如后端重启）：兜底把 DB 状态收敛为失败，
		// 否则看板会永远停留在"写作中"
		slog.Warn("workflow.cancel: task not found in executor, falling back to DB cleanup", "task_id", taskID)
		if s.editorialSvc != nil {
			bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := s.editorialSvc.Store().MarkTaskFailed(bgCtx, taskID, "工作流已被用户取消"); err != nil {
				slog.Warn("workflow.cancel: DB cleanup failed", "task_id", taskID, "error", err)
			}
		}
	}

	slog.Info("workflow: cancelled", "task_id", taskID, "in_executor", cancelled)
	response.OK(w, map[string]interface{}{
		"task_id": taskID,
		"status":  "cancelled",
	})
}

// handleWorkflowGet 处理 GET /api/v2/workflows/{id}
// 返回当前任务状态（editorial.Service.GetTaskForUser，含 owner 隔离）。
func (s *Server) handleWorkflowGet(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	taskID := chi.URLParam(r, "id")

	if s.editorialSvc == nil {
		response.Err(w, http.StatusServiceUnavailable, "editorial_unavailable", "editorial service not initialized")
		return
	}

	task, err := s.editorialSvc.GetTaskForUser(r.Context(), taskID, user.Sub, auth.IsAdminFromContext(r.Context()))
	if err != nil {
		respondTaskAuthError(w, err)
		return
	}
	response.OK(w, task)
}

// respondTaskAuthError 将 editorial 服务的授权错误映射为 HTTP 状态码。
func respondTaskAuthError(w http.ResponseWriter, err error) {
	switch err {
	case editorial.ErrTaskNotFound:
		response.Err(w, http.StatusNotFound, "not_found", "task not found")
	case editorial.ErrForbidden:
		response.Err(w, http.StatusForbidden, "forbidden", "access denied")
	default:
		response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
	}
}

// recordEditorialArticle 在 DAG 执行完成后，从 editorial store 获取最终 draft artifact，
// 更新 agent_traces 记录（task.ID 即 trace_id，CreateTask 时已创建行），
// 使编辑部模式产生的文章出现在用户的写作历史列表中。
//
// 旧实现此处还向 WS Hub 全局广播 agent.completed；传输迁移后前端通过
// workflow:completed SSE 事件感知 DAG 终态，再经 REST 拉取文章
// （/editorial/tasks/{id}/artifacts），无需额外广播。
func (s *Server) recordEditorialArticle(ctx context.Context, task *editorial.Task, plan *editorial.PlanResult, userID string) {
	if s.editorialSvc == nil {
		return
	}

	edStore := s.editorialSvc.Store()

	// 优先获取 revised_draft，其次 draft
	draft, err := edStore.GetLatestApprovedArtifact(ctx, task.ID, editorial.ArtifactRevisedDraft)
	if err != nil || draft == nil {
		draft, err = edStore.GetLatestApprovedArtifact(ctx, task.ID, editorial.ArtifactDraft)
	}
	if err != nil || draft == nil {
		slog.Warn("editorial: no draft artifact found after DAG completion",
			"task_id", task.ID, "error", err)
		return
	}

	// 解析 draft JSON 获取文章内容
	var draftData struct {
		Title     string `json:"title"`
		Content   string `json:"content"`
		WordCount int    `json:"word_count"`
	}
	if err := json.Unmarshal([]byte(draft.Content), &draftData); err != nil {
		slog.Warn("editorial: failed to parse draft content", "error", err, "task_id", task.ID)
		return
	}

	article := draftData.Content
	articleTitle := draftData.Title
	if articleTitle == "" {
		articleTitle = task.Title
	}

	// 获取 total token usage
	totalTokens := 0
	if s.dagExecutor != nil {
		totalTokens = int(s.dagExecutor.GetTokenBudget().GetTotalUsed())
	}

	// task.ID 就是 trace_id（两表合一后 CreateTask 直接在 agent_traces 中创建行）
	traceID := task.ID

	// 更新 trace 为完成状态（含文章、标题等）
	if s.traces != nil {
		s.traces.CompleteTrace(ctx, &engine.ExecutionContext{
			TraceID:      traceID,
			Status:       engine.StatusCompleted,
			Article:      article,
			ArticleTitle: articleTitle,
			TotalTokens:  totalTokens,
			StepHistory:  []engine.StepRecord{},
			StartedAt:    task.CreatedAt,
		})
	}

	slog.Info("editorial: article recorded as trace",
		"trace_id", traceID, "task_id", task.ID,
		"article_title", articleTitle, "word_count", draftData.WordCount,
		"total_tokens", totalTokens, "user_id", userID)
}

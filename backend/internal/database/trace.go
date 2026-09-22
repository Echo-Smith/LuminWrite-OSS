package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/memory"
)

// isLikelyUUID checks if a string looks like a UUID (xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx).
// Used to avoid PostgreSQL errors when a non-UUID user_id is passed.
func isLikelyUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	parts := strings.Split(s, "-")
	return len(parts) == 5
}

// TraceRepo handles persistence of agent execution traces.
type TraceRepo struct {
	db *DB
}

// NewTraceRepo creates a new TraceRepo.
func NewTraceRepo(db *DB) *TraceRepo {
	return &TraceRepo{db: db}
}

// ─── Writing-lifecycle writes removed ─────────────────────
//
// CreateTrace / UpdateTraceStep / PauseTrace / UpdateTaskName /
// FailTrace / LinkEditorialTask were deleted with the legacy WebSocket
// writing path and the WABench RunCore migration (agent_traces is now a
// read-only history projection: the governed runtime owns run state in
// writingstore, and user-surface maintenance goes through the methods
// below — UpdateTraceTitle, CancelTrace, SoftDeleteTrace, …).

// intentString extracts the classified intent ("chat" | "writing" | …) from
// the execution context for token_usage persistence. Empty when unknown.
func intentString(execCtx *engine.ExecutionContext) string {
	if execCtx == nil || execCtx.TaskIntent == nil {
		return ""
	}
	return execCtx.TaskIntent.TaskMode
}

// marshalTraceArray serializes a slice to a JSON array for a NOT NULL JSONB
// column, coercing empty / nil to "[]" so the column constraint holds.
func marshalTraceArray[T any](items []T) []byte {
	if len(items) == 0 {
		return []byte("[]")
	}
	b, err := json.Marshal(items)
	if err != nil || string(b) == "null" {
		return []byte("[]")
	}
	return b
}

// Pricing mirrors engine.ExecutionContext.GetCostEstimate (DeepSeek), with
// cache-read and cache-write split out so the breakdown matches llm-space's
// usage model (llm-space treats cache read/write as first-class).
const (
	priceInputPer1M      = 0.27
	priceOutputPer1M     = 1.10
	priceCacheReadPer1M  = 0.014
	priceCacheWritePer1M = 0.35
)

// traceSummaryRow is the precomputed scalar summary persisted on agent_traces
// (migration 120). Pointer fields are nullable columns (unset when the metric
// is not derivable). Keeping it flat lets list/aggregate/benchmark paths read
// cheap columns instead of scanning the large llm_calls/tool_calls JSONB.
type traceSummaryRow struct {
	llmCallCount     int
	toolCallCount    int
	observationCount int
	inputTokens      int
	outputTokens     int
	reasoningTokens  int
	cacheReadTokens  int
	cacheWriteTokens int
	totalTokens      int

	costInput      float64
	costOutput     float64
	costCacheRead  float64
	costCacheWrite float64

	firstTokenMs       *int64
	outputTokensPerSec *float64
	model              *string
	agentMode          *string
}

func computeTraceSummary(execCtx *engine.ExecutionContext, durationMs int64) traceSummaryRow {
	var s traceSummaryRow
	for _, c := range execCtx.LLMCalls {
		s.llmCallCount++
		s.inputTokens += c.PromptTokens
		s.outputTokens += c.CompletionTokens
		s.reasoningTokens += c.ReasoningTokens
		s.cacheReadTokens += c.CacheHitTokens
		s.cacheWriteTokens += c.CacheWriteTokens
		s.totalTokens += c.TotalTokens

		nonCached := c.PromptTokens - c.CacheHitTokens - c.CacheWriteTokens
		if nonCached < 0 {
			nonCached = 0
		}
		s.costInput += float64(nonCached) / 1_000_000 * priceInputPer1M
		s.costOutput += float64(c.CompletionTokens) / 1_000_000 * priceOutputPer1M
		s.costCacheRead += float64(c.CacheHitTokens) / 1_000_000 * priceCacheReadPer1M
		s.costCacheWrite += float64(c.CacheWriteTokens) / 1_000_000 * priceCacheWritePer1M

		if s.firstTokenMs == nil && c.TTFT != nil {
			v := c.TTFT.Milliseconds()
			s.firstTokenMs = &v
		}
		if s.model == nil && c.Model != "" {
			m := c.Model
			s.model = &m
		}
	}
	s.toolCallCount = len(execCtx.ToolCalls)
	s.observationCount = s.llmCallCount + s.toolCallCount

	if execCtx.PipelineMeta != nil && execCtx.PipelineMeta.AgentMode != "" {
		am := execCtx.PipelineMeta.AgentMode
		s.agentMode = &am
	}

	// Generation-only throughput, TTFT excluded from the window (llm-space).
	if s.firstTokenMs != nil && s.outputTokens > 0 {
		genMs := durationMs - *s.firstTokenMs
		if genMs > 0 {
			tps := float64(s.outputTokens) / (float64(genMs) / 1000.0)
			s.outputTokensPerSec = &tps
		}
	}
	return s
}

// CompleteTrace finalizes the trace with article, review, and token usage.
func (r *TraceRepo) CompleteTrace(ctx context.Context, execCtx *engine.ExecutionContext) error {
	if r.db == nil {
		return nil
	}

	// ── Archive old article version before overwriting ──
	// If the trace already has an article (e.g. harness multi-round writing,
	// or pipeline re-generation), save it to article_versions for rollback.
	var oldArticle *string
	var oldTitle *string
	var traceUserID *string
	_ = r.db.QueryRowContext(ctx, `
		SELECT article, article_title, user_id::text
		FROM agent_traces WHERE trace_id = $1
	`, execCtx.TraceID).Scan(&oldArticle, &oldTitle, &traceUserID)

	if oldArticle != nil && *oldArticle != "" && *oldArticle != execCtx.Article {
		var userIDArg interface{}
		if traceUserID != nil && isLikelyUUID(*traceUserID) {
			userIDArg = *traceUserID
		}
		note := "AI 生成前自动保存"
		_, _ = r.db.ExecContext(ctx, `
			INSERT INTO article_versions (trace_id, user_id, article, article_title, version_note)
			VALUES ($1, $2, $3, $4, $5)
		`, execCtx.TraceID, userIDArg, *oldArticle, oldTitle, note)
	}

	var reviewJSON []byte
	if execCtx.ReviewResult != nil {
		reviewJSON, _ = json.Marshal(execCtx.ReviewResult)
	}
	tokenJSON, _ := json.Marshal(map[string]any{
		"total_tokens": execCtx.TotalTokens,
		"intent":       intentString(execCtx),
	})
	stepHistoryJSON, _ := json.Marshal(execCtx.StepHistory)
	durationMs := time.Since(execCtx.StartedAt).Milliseconds()

	// Enhanced per-call trace (migration 119) + precomputed scalar summary and
	// derived metrics (migration 120). The arrays are the lazy, on-open detail
	// payload; the flat summary is what the Eval Center list, benchmark and
	// run-level aggregation read, so they never have to parse large JSONB.
	llmCallsJSON := marshalTraceArray(execCtx.LLMCalls)
	toolCallsJSON := marshalTraceArray(execCtx.ToolCalls)
	// pipeline_metadata is nullable: pass SQL NULL (nil interface) when absent so
	// the driver sends NULL rather than an empty BYTEA that fails JSONB typing.
	var pipelineMetaJSON interface{}
	if execCtx.PipelineMeta != nil {
		if b, err := json.Marshal(execCtx.PipelineMeta); err == nil {
			pipelineMetaJSON = string(b)
		}
	}
	var traceSchema *string
	if len(execCtx.LLMCalls) > 0 || len(execCtx.ToolCalls) > 0 || execCtx.PipelineMeta != nil {
		ts := "enhanced-trace.v1"
		traceSchema = &ts
	}
	summary := computeTraceSummary(execCtx, durationMs)
	costTotal := summary.costInput + summary.costOutput + summary.costCacheRead + summary.costCacheWrite

	_, err := r.db.ExecContext(ctx, `
		UPDATE agent_traces
		SET status = $1, current_step = $2, step_history = $3,
		    article = $4, article_title = $5, review_result = $6, token_usage = $7,
		    duration_ms = $8, reasoning_content = $9,
		    llm_calls = $10, tool_calls = $11, pipeline_metadata = $12,
		    estimated_cost = $13, trace_schema = $14,
		    llm_call_count = $15, tool_call_count = $16, observation_count = $17,
		    input_tokens = $18, output_tokens = $19, reasoning_tokens = $20,
		    cache_read_tokens = $21, cache_write_tokens = $22, total_tokens = $23,
		    cost_input = $24, cost_output = $25, cost_cache_read = $26, cost_cache_write = $27,
		    first_token_ms = $28, output_tokens_per_sec = $29,
		    model = $30, agent_mode = $31,
		    completed_at = NOW()
		WHERE trace_id = $32
	`,
		string(execCtx.Status),
		string(execCtx.CurrentStep),
		stepHistoryJSON,
		execCtx.Article,
		execCtx.ArticleTitle,
		reviewJSON,
		tokenJSON,
		durationMs,
		execCtx.ReasoningContent,
		llmCallsJSON,
		toolCallsJSON,
		pipelineMetaJSON,
		costTotal,
		traceSchema,
		summary.llmCallCount,
		summary.toolCallCount,
		summary.observationCount,
		summary.inputTokens,
		summary.outputTokens,
		summary.reasoningTokens,
		summary.cacheReadTokens,
		summary.cacheWriteTokens,
		summary.totalTokens,
		summary.costInput,
		summary.costOutput,
		summary.costCacheRead,
		summary.costCacheWrite,
		summary.firstTokenMs,
		summary.outputTokensPerSec,
		summary.model,
		summary.agentMode,
		execCtx.TraceID,
	)
	if err != nil {
		slog.Warn("failed to complete trace", "error", err, "trace_id", execCtx.TraceID)
	}
	return err
}

// UpdateTraceTitle sets the user-editable custom_title for a trace.
// Ownership is enforced by the caller (userID empty = guest, no filter).
// 与 task_name/article_title 不同，custom_title 只代表用户显式命名，
// 显示优先级最高，不会被 LLM 自动提取覆盖。
func (r *TraceRepo) UpdateTraceTitle(ctx context.Context, traceID, userID, title string) error {
	if r.db == nil {
		return fmt.Errorf("database not available")
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Errorf("标题不能为空")
	}
	if len([]rune(title)) > 128 {
		title = string([]rune(title)[:128])
	}
	var (
		res interface{ RowsAffected() (int64, error) }
		err error
	)
	if userID == "" {
		res, err = r.db.ExecContext(ctx, `
			UPDATE agent_traces SET custom_title = $2, updated_at = NOW()
			WHERE trace_id = $1
		`, traceID, title)
	} else {
		res, err = r.db.ExecContext(ctx, `
			UPDATE agent_traces SET custom_title = $2, updated_at = NOW()
			WHERE trace_id = $1 AND user_id = $3::uuid
		`, traceID, title, userID)
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("trace not found or not owned by user")
	}
	return nil
}

// GetEditorialTaskID retrieves the editorial task ID associated with a trace.
// After the two-table merge (087), task.ID is the trace_id, so just return it.
func (r *TraceRepo) GetEditorialTaskID(ctx context.Context, traceID string) (string, error) {
	return traceID, nil
}

// CancelTrace marks a trace as cancelled in the database.
// Called when the user cancels a run whose session is no longer in server
// memory (e.g. after a backend restart) — without this, the DB row would
// stay "running" forever and the session list would keep showing 写作中.
func (r *TraceRepo) CancelTrace(ctx context.Context, traceID string) error {
	if r.db == nil {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE agent_traces
		SET status = 'cancelled', completed_at = COALESCE(completed_at, NOW()), updated_at = NOW()
		WHERE trace_id = $1 AND status IN ('running', 'paused')
	`, traceID)
	return err
}

// RecoverStaleRunningTraces marks long-stale running/paused traces as failed.
// Runs at server startup: if the process died (crash/restart) mid-run, no one
// will ever write a terminal status, and the frontend would keep showing
// "正在写作" for those sessions after a refresh.
// Traces whose updated_at is within staleAfter are left untouched, so
// multiple server instances don't kill each other's live runs.
func (r *TraceRepo) RecoverStaleRunningTraces(ctx context.Context, staleAfter time.Duration) (int64, error) {
	if r.db == nil {
		return 0, nil
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE agent_traces
		SET status = 'failed',
		    error = '服务重启时写作仍在进行，已自动标记为失败',
		    completed_at = COALESCE(completed_at, NOW()),
		    updated_at = NOW()
		WHERE status IN ('running', 'paused')
		  AND completed_at IS NULL
		  AND updated_at < NOW() - ($1::text)::interval
	`, fmt.Sprintf("%d seconds", int(staleAfter.Seconds())))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// GetTrace retrieves a trace by ID.
func (r *TraceRepo) GetTrace(ctx context.Context, traceID string) (map[string]interface{}, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	var (
		status      string
		currentStep string
		userInput   string
		styleSlug   *string
		mode        string
		article     *string
		articleTitle *string
		stepHistory []byte
		reviewJSON  []byte
		tokenJSON   []byte
		durationMs  *int64
		errorMsg    *string
		createdAt   time.Time
		completedAt *time.Time
		reasoningContent *string
	)

	var taskName *string
	var customTitle *string
	var llmCallsRaw, toolCallsRaw, pipelineMetaRaw []byte
	var traceSummaryRaw []byte
	var estimatedCost *float64
	var traceSchema *string
	err := r.db.QueryRowContext(ctx, `
		SELECT status, current_step, user_input, style_slug, mode,
		       article, article_title, step_history, review_result, token_usage,
		       duration_ms, error, created_at, completed_at, reasoning_content,
		       task_name, custom_title,
		       llm_calls, tool_calls, pipeline_metadata, estimated_cost, trace_schema,
		       jsonb_build_object(
		         'llm_call_count', llm_call_count, 'tool_call_count', tool_call_count,
		         'observation_count', observation_count,
		         'input_tokens', input_tokens, 'output_tokens', output_tokens,
		         'reasoning_tokens', reasoning_tokens,
		         'cache_read_tokens', cache_read_tokens, 'cache_write_tokens', cache_write_tokens,
		         'total_tokens', total_tokens,
		         'cost_input', cost_input, 'cost_output', cost_output,
		         'cost_cache_read', cost_cache_read, 'cost_cache_write', cost_cache_write,
		         'first_token_ms', first_token_ms, 'output_tokens_per_sec', output_tokens_per_sec,
		         'model', model, 'agent_mode', agent_mode
		       )
		FROM agent_traces
		WHERE trace_id = $1
	`, traceID).Scan(
		&status, &currentStep, &userInput, &styleSlug, &mode,
		&article, &articleTitle, &stepHistory, &reviewJSON, &tokenJSON,
		&durationMs, &errorMsg, &createdAt, &completedAt, &reasoningContent,
		&taskName, &customTitle,
		&llmCallsRaw, &toolCallsRaw, &pipelineMetaRaw, &estimatedCost, &traceSchema,
		&traceSummaryRaw,
	)
	if err != nil {
		return nil, err
	}

	result := map[string]interface{}{
		"trace_id":      traceID,
		"status":        status,
		"current_step":  currentStep,
		"user_input":    userInput,
		"mode":          mode,
		"created_at":    createdAt,
	}
	if len(traceSummaryRaw) > 0 {
		var summary interface{}
		if json.Unmarshal(traceSummaryRaw, &summary) == nil && summary != nil {
			result["summary"] = summary
		}
	}

	if styleSlug != nil {
		result["style_slug"] = *styleSlug
	}
	if article != nil {
		result["article"] = *article
	}
	if articleTitle != nil && *articleTitle != "" {
		result["article_title"] = *articleTitle
	}
	if completedAt != nil {
		result["completed_at"] = *completedAt
	}
	if durationMs != nil {
		result["duration_ms"] = *durationMs
	}
	if errorMsg != nil {
		result["error"] = *errorMsg
	}
	if len(llmCallsRaw) > 0 {
		var llmCalls interface{}
		if json.Unmarshal(llmCallsRaw, &llmCalls) == nil && llmCalls != nil {
			result["llm_calls"] = llmCalls
		}
	}
	if len(toolCallsRaw) > 0 {
		var toolCalls interface{}
		if json.Unmarshal(toolCallsRaw, &toolCalls) == nil && toolCalls != nil {
			result["tool_calls"] = toolCalls
		}
	}
	if len(pipelineMetaRaw) > 0 {
		var pipelineMeta interface{}
		if json.Unmarshal(pipelineMetaRaw, &pipelineMeta) == nil && pipelineMeta != nil {
			result["pipeline_metadata"] = pipelineMeta
		}
	}
	if estimatedCost != nil {
		result["estimated_cost"] = *estimatedCost
	}
	if traceSchema != nil {
		result["trace_schema"] = *traceSchema
	}
	if len(stepHistory) > 0 {
		var history interface{}
		json.Unmarshal(stepHistory, &history)
		result["step_history"] = history
	}
	if len(reviewJSON) > 0 {
		var review interface{}
		json.Unmarshal(reviewJSON, &review)
		result["review"] = review
	}
	if len(tokenJSON) > 0 {
		var tokens interface{}
		json.Unmarshal(tokenJSON, &tokens)
		result["token_usage"] = tokens
	}
	if reasoningContent != nil && *reasoningContent != "" {
		result["reasoning_content"] = *reasoningContent
	}
	if taskName != nil && *taskName != "" {
		result["task_name"] = *taskName
	}
	if customTitle != nil && *customTitle != "" {
		result["custom_title"] = *customTitle
	}

	// Check if user feedback has been submitted for this trace
	var feedbackCount int
	r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM feedback_segments WHERE trace_id = $1`, traceID).Scan(&feedbackCount)
	result["has_feedback"] = feedbackCount > 0

	return result, nil
}

// ListTraces lists recent traces with pagination.
// If userID is non-empty, results are filtered to that user.
// archived: nil=不过滤（默认行为，向后兼容）；true=仅归档；false=仅未归档。
func (r *TraceRepo) ListTraces(ctx context.Context, userID string, page, pageSize int, archived *bool) ([]map[string]interface{}, int, error) {
	if r.db == nil {
		return []map[string]interface{}{}, 0, nil
	}

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	// archived 过滤条件（默认排除已归档，保持旧行为）
	archivedCond := "AND archived_at IS NULL"
	if archived != nil {
		if *archived {
			archivedCond = "AND archived_at IS NOT NULL"
		} else {
			archivedCond = "AND archived_at IS NULL"
		}
	}

	var (
		rows   *sql.Rows
		err    error
		countQ string
		countArgs []interface{}
	)

	if userID != "" && userID != "anonymous" && isLikelyUUID(userID) {
		rows, err = r.db.QueryContext(ctx, `
			SELECT trace_id, status, COALESCE(current_step, ''), COALESCE(user_input, ''), style_slug, mode,
			       created_at, completed_at, duration_ms, article_title, task_name,
			       COALESCE(folder_id::text, ''), archived_at, updated_at, custom_title
			FROM agent_traces
			WHERE user_id = $1 AND user_deleted = FALSE `+archivedCond+`
			ORDER BY COALESCE(updated_at, created_at) DESC
			LIMIT $2 OFFSET $3
		`, userID, pageSize, offset)
		countQ = `SELECT COUNT(*) FROM agent_traces WHERE user_id = $1 AND user_deleted = FALSE ` + archivedCond
		countArgs = []interface{}{userID}
	} else {
		rows, err = r.db.QueryContext(ctx, `
			SELECT trace_id, status, COALESCE(current_step, ''), COALESCE(user_input, ''), style_slug, mode,
			       created_at, completed_at, duration_ms, article_title, task_name,
			       COALESCE(folder_id::text, ''), archived_at, updated_at, custom_title
			FROM agent_traces
			WHERE user_deleted = FALSE `+archivedCond+`
			ORDER BY COALESCE(updated_at, created_at) DESC
			LIMIT $1 OFFSET $2
		`, pageSize, offset)
		countQ = `SELECT COUNT(*) FROM agent_traces WHERE user_deleted = FALSE ` + archivedCond
		countArgs = []interface{}{}
	}
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var traces []map[string]interface{}
	for rows.Next() {
		var (
			traceID       string
			status        string
			currentStep   string
			userInput     string
			styleSlug     *string
			mode          string
			createdAt     time.Time
			completedAt   *time.Time
			durationMs    *int64
			articleTitle  *string
			taskName      *string
			folderID      string
			archivedAt    *time.Time
			updatedAt     *time.Time
			customTitle   *string
		)

		if err := rows.Scan(&traceID, &status, &currentStep, &userInput, &styleSlug, &mode,
			&createdAt, &completedAt, &durationMs, &articleTitle, &taskName,
			&folderID, &archivedAt, &updatedAt, &customTitle); err != nil {
			continue
		}

		trace := map[string]interface{}{
			"trace_id":     traceID,
			"status":       status,
			"current_step": currentStep,
			"user_input":   userInput,
			"mode":         mode,
			"created_at":   createdAt,
			"folder_id":    folderID,
		}
		if styleSlug != nil {
			trace["style_slug"] = *styleSlug
		}
		if completedAt != nil {
			trace["completed_at"] = *completedAt
		}
		if durationMs != nil {
			trace["duration_ms"] = *durationMs
		}
		if articleTitle != nil && *articleTitle != "" {
			trace["article_title"] = *articleTitle
		}
		if taskName != nil && *taskName != "" {
			trace["task_name"] = *taskName
		}
		if customTitle != nil && *customTitle != "" {
			trace["custom_title"] = *customTitle
		}
		if archivedAt != nil {
			trace["archived_at"] = *archivedAt
		}
		if updatedAt != nil {
			trace["updated_at"] = *updatedAt
		}

		traces = append(traces, trace)
	}

	// Get total count
	var total int
	r.db.QueryRowContext(ctx, countQ, countArgs...).Scan(&total)

	return traces, total, nil
}

// SoftDeleteTrace marks a trace as deleted by the user (admin still sees it).
// userID 为空（游客）时不限定 user_id —— 不能用 `OR $2 = ''` 的写法：
// Postgres 会把 $2 按 uuid 解析，空串直接报 invalid input syntax，
// 导致删除永远失败、刷新后"复活"。
func (r *TraceRepo) SoftDeleteTrace(ctx context.Context, traceID, userID string) error {
	if r.db == nil {
		return nil
	}
	var (
		res interface{ RowsAffected() (int64, error) }
		err error
	)
	if userID == "" {
		res, err = r.db.ExecContext(ctx, `
			UPDATE agent_traces SET user_deleted = TRUE, updated_at = NOW()
			WHERE trace_id = $1
		`, traceID)
	} else {
		res, err = r.db.ExecContext(ctx, `
			UPDATE agent_traces SET user_deleted = TRUE, updated_at = NOW()
			WHERE trace_id = $1 AND user_id = $2::uuid
		`, traceID, userID)
	}
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("trace not found or not owned by user")
	}
	return nil
}

// HasFeedback checks if feedback has already been submitted for a trace.
func (r *TraceRepo) HasFeedback(ctx context.Context, traceID string) (bool, error) {
	if r.db == nil {
		return false, nil
	}
	var count int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM feedback_segments WHERE trace_id = $1`, traceID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// SaveFeedback saves feedback segments for a trace.
func (r *TraceRepo) SaveFeedback(ctx context.Context, traceID string, segments []map[string]interface{}) error {
	if r.db == nil {
		return nil
	}

	for _, seg := range segments {
		segmentType, _ := seg["segment_type"].(string)
		segmentIndex, _ := seg["segment_index"].(float64)
		segmentText, _ := seg["segment_text"].(string)
		rating, _ := seg["rating"].(float64)
		feedbackType, _ := seg["feedback_type"].(string)
		comment, _ := seg["comment"].(string)

		_, err := r.db.ExecContext(ctx, `
			INSERT INTO feedback_segments (trace_id, segment_type, segment_index, segment_text, rating, feedback_type, comment, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
		`,
			traceID, segmentType, int(segmentIndex), segmentText, int(rating), feedbackType, comment,
		)
		if err != nil {
			slog.Warn("failed to save feedback segment", "error", err)
		}
	}

	return nil
}

// GetFeedbackByTrace retrieves feedback segments for a trace as FeedbackInfo.
func (r *TraceRepo) GetFeedbackByTrace(ctx context.Context, traceID string) ([]memory.FeedbackInfo, error) {
	if r.db == nil {
		return nil, nil
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT segment_type, rating, comment
		FROM feedback_segments
		WHERE trace_id = $1
		ORDER BY created_at ASC
	`, traceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var feedback []memory.FeedbackInfo
	for rows.Next() {
		var fb memory.FeedbackInfo
		if err := rows.Scan(&fb.SegmentType, &fb.Rating, &fb.Comment); err != nil {
			continue
		}
		feedback = append(feedback, fb)
	}
	return feedback, nil
}

// GetTraceUserID retrieves the user_id for a given trace.
func (r *TraceRepo) GetTraceUserID(ctx context.Context, traceID string) (string, error) {
	if r.db == nil {
		return "", nil
	}
	var userID sql.NullString
	err := r.db.QueryRowContext(ctx, `
		SELECT user_id::text FROM agent_traces WHERE trace_id = $1
	`, traceID).Scan(&userID)
	if err != nil {
		return "", err
	}
	if !userID.Valid || userID.String == "" {
		return "", fmt.Errorf("user_id not found for trace %s", traceID)
	}
	return userID.String, nil
}

// IsTraceAdopted checks if a trace has been adopted by workbuddy.
func (r *TraceRepo) IsTraceAdopted(ctx context.Context, traceID string) (bool, error) {
	if r.db == nil {
		return false, nil
	}
	var count int
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM workbuddy_adoptions WHERE trace_id = $1 AND status = 'adopted'
	`, traceID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// CreateTopic saves a user-submitted topic.
func (r *TraceRepo) CreateTopic(ctx context.Context, title, description, sourceUID string) (string, error) {
	if r.db == nil {
		return "", fmt.Errorf("database not available")
	}

	var id string
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO topics (title, description, source, source_uid, created_at)
		VALUES ($1, $2, 'user', $3, NOW())
		RETURNING id::text
	`, title, description, sourceUID).Scan(&id)
	if err != nil {
		return "", err
	}
	return id, nil
}

// ListTopics lists recent topics.
func (r *TraceRepo) ListTopics(ctx context.Context, source string, page, pageSize int) ([]map[string]interface{}, int, error) {
	if r.db == nil {
		return []map[string]interface{}{}, 0, nil
	}

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	query := `
		SELECT id::text, title, description, source, platform, hot_rank, fetched_at, created_at,
		       raw_data->>'url' AS url
		FROM topics
		WHERE status = 'active'
	`
	args := []interface{}{}
	argIdx := 1

	if source == "hot" {
		// "hot" means all non-user topics (tencent, weibo, baidu, zhihu, etc.)
		query += fmt.Sprintf(" AND source != 'user'")
	} else if source != "" {
		query += fmt.Sprintf(" AND source = $%d", argIdx)
		args = append(args, source)
		argIdx++
	}

	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", argIdx, argIdx+1)
	args = append(args, pageSize, offset)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var topics []map[string]interface{}
	for rows.Next() {
		var (
			id          string
			title       string
			description *string
			topicSource string
			platform    *string
			hotRank     *int
			fetchedAt   *time.Time
			createdAt   time.Time
			url         *string
		)

		if err := rows.Scan(&id, &title, &description, &topicSource, &platform, &hotRank, &fetchedAt, &createdAt, &url); err != nil {
			continue
		}

		topic := map[string]interface{}{
			"id":         id,
			"title":      title,
			"source":     topicSource,
			"created_at": createdAt,
		}
		if description != nil {
			topic["description"] = *description
		}
		if platform != nil {
			topic["platform"] = *platform
		}
		if hotRank != nil {
			topic["hot_rank"] = *hotRank
		}
		if fetchedAt != nil {
			topic["fetched_at"] = *fetchedAt
		}
		if url != nil && *url != "" {
			topic["url"] = *url
		}

		topics = append(topics, topic)
	}

	// Get total count
	total := 0
	countQuery := "SELECT COUNT(*) FROM topics WHERE status = 'active'"
	if source == "hot" {
		countQuery += " AND source != 'user'"
		r.db.QueryRowContext(ctx, countQuery).Scan(&total)
	} else if source != "" {
		countQuery += " AND source = $1"
		r.db.QueryRowContext(ctx, countQuery, source).Scan(&total)
	} else {
		r.db.QueryRowContext(ctx, countQuery).Scan(&total)
	}

	return topics, total, nil
}

// DeleteTopic deletes a topic by ID (soft delete: sets status to 'deleted').
// Only user-created topics can be hard-deleted; hot topics are soft-deleted.
func (r *TraceRepo) DeleteTopic(ctx context.Context, id string) error {
	if r.db == nil {
		return fmt.Errorf("database not available")
	}
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM topics WHERE id = $1 AND source = 'user'
	`, id)
	if err != nil {
		return err
	}
	// Also remove from favorites
	_, _ = r.db.ExecContext(ctx, `DELETE FROM topic_favorites WHERE topic_id = $1`, id)
	return nil
}

// UpdateTopic updates the title and description of a user-created topic.
// Only source='user' topics can be edited.
func (r *TraceRepo) UpdateTopic(ctx context.Context, id, title, description string) error {
	if r.db == nil {
		return fmt.Errorf("database not available")
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE topics
		SET title = $2, description = $3
		WHERE id = $1 AND source = 'user'
	`, id, title, description)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("topic not found or not editable")
	}
	return nil
}

// UpsertHotTopics batch-inserts hot topics fetched from external sources.
// It upserts by (title, platform) — if a topic with the same title+platform exists,
// it updates hot_rank, description, raw_data and fetched_at.
// Returns the number of rows inserted/updated.
func (r *TraceRepo) UpsertHotTopics(ctx context.Context, topics []map[string]interface{}) (int, error) {
	if r.db == nil {
		return 0, fmt.Errorf("database not available")
	}
	if len(topics) == 0 {
		return 0, nil
	}

	count := 0
	for _, t := range topics {
		title, _ := t["title"].(string)
		if title == "" {
			continue
		}
		description, _ := t["description"].(string)
		source, _ := t["source"].(string)
		if source == "" {
			source = "hotlist"
		}
		platform, _ := t["platform"].(string)
		if platform == "" {
			platform = "unknown"
		}
		hotRank := 0
		if r, ok := t["hot_rank"].(int); ok {
			hotRank = r
		} else if r, ok := t["hot_rank"].(float64); ok {
			hotRank = int(r)
		}

		rawData, _ := json.Marshal(t)

		_, err := r.db.ExecContext(ctx, `
			INSERT INTO topics (title, description, source, platform, hot_rank, raw_data, fetched_at, status, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, NOW(), 'active', NOW())
			ON CONFLICT (title, platform) DO UPDATE SET
				hot_rank = EXCLUDED.hot_rank,
				description = EXCLUDED.description,
				raw_data = EXCLUDED.raw_data,
				fetched_at = NOW(),
				status = 'active'
		`, title, description, source, platform, hotRank, rawData)
		if err != nil {
			slog.Warn("failed to upsert hot topic", "title", title, "error", err)
			continue
		}
		count++
	}

	// Deactivate old hot topics not refreshed in the last 3 hours
	_, _ = r.db.ExecContext(ctx, `
		UPDATE topics
		SET status = 'inactive'
		WHERE source IN ('tencent', 'weibo', 'hotlist')
		  AND fetched_at < NOW() - INTERVAL '3 hours'
		  AND status = 'active'
	`)

	slog.Info("hot topics upserted", "count", count, "total_fetched", len(topics))
	return count, nil
}

// ─── Article Version Management ──────────────────────────

// SaveArticleVersion archives the current article from agent_traces into
// article_versions before updating. This preserves history for rollback.
func (r *TraceRepo) SaveArticleVersion(ctx context.Context, traceID, userID, article, articleTitle, versionNote string) error {
	if r.db == nil {
		return nil
	}

	var userIDArg interface{}
	if userID != "" && userID != "anonymous" && isLikelyUUID(userID) {
		userIDArg = userID
	}

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO article_versions (trace_id, user_id, article, article_title, version_note)
		VALUES ($1, $2, $3, $4, $5)
	`, traceID, userIDArg, article, articleTitle, versionNote)
	if err != nil {
		slog.Warn("failed to save article version", "error", err, "trace_id", traceID)
	}
	return err
}

// UpdateTraceArticle updates the article content in agent_traces (latest version).
// Before updating, it archives the current article into article_versions.
// Only the trace owner (matching userID) can update the article.
func (r *TraceRepo) UpdateTraceArticle(ctx context.Context, traceID, userID, newArticle, newTitle, versionNote string) error {
	if r.db == nil {
		return nil
	}

	// 1. Archive current article before overwriting
	var (
		oldArticle  *string
		oldTitle    *string
		traceUserID *string
	)
	err := r.db.QueryRowContext(ctx, `
		SELECT article, article_title, user_id::text
		FROM agent_traces WHERE trace_id = $1
	`, traceID).Scan(&oldArticle, &oldTitle, &traceUserID)
	if err != nil {
		return fmt.Errorf("trace not found: %w", err)
	}

	// 2. Verify ownership (userID must match trace owner)
	// Admin access is handled at the handler layer before calling this method.
	if traceUserID != nil && *traceUserID != "" && *traceUserID != userID {
		return fmt.Errorf("unauthorized: user does not own this trace")
	}

	// 3. Archive old version if there was one
	if oldArticle != nil && *oldArticle != "" && *oldArticle != newArticle {
		var userIDArg interface{}
		if traceUserID != nil && isLikelyUUID(*traceUserID) {
			userIDArg = *traceUserID
		}
		note := versionNote
		if note == "" {
			note = "用户编辑前自动保存"
		}
		_, _ = r.db.ExecContext(ctx, `
			INSERT INTO article_versions (trace_id, user_id, article, article_title, version_note)
			VALUES ($1, $2, $3, $4, $5)
		`, traceID, userIDArg, *oldArticle, oldTitle, note)
	}

	// 4. Update to new version
	titleArg := newTitle
	if titleArg == "" && oldTitle != nil {
		titleArg = *oldTitle
	}
	_, err = r.db.ExecContext(ctx, `
		UPDATE agent_traces
		SET article = $1, article_title = $2, completed_at = COALESCE(completed_at, NOW())
		WHERE trace_id = $3
	`, newArticle, titleArg, traceID)
	if err != nil {
		slog.Warn("failed to update trace article", "error", err, "trace_id", traceID)
	}
	return err
}

// ListArticleVersions retrieves all historical versions of an article.
func (r *TraceRepo) ListArticleVersions(ctx context.Context, traceID string) ([]map[string]interface{}, error) {
	if r.db == nil {
		return []map[string]interface{}{}, nil
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT id::text, article_title, version_note, created_at
		FROM article_versions
		WHERE trace_id = $1
		ORDER BY created_at DESC
		LIMIT 20
	`, traceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var versions []map[string]interface{}
	for rows.Next() {
		var (
			id          string
			title       *string
			note        *string
			createdAt   time.Time
		)
		if err := rows.Scan(&id, &title, &note, &createdAt); err != nil {
			continue
		}
		item := map[string]interface{}{
			"version_id": id,
			"created_at": createdAt,
		}
		if title != nil {
			item["article_title"] = *title
		}
		if note != nil {
			item["version_note"] = *note
		}
		versions = append(versions, item)
	}
	return versions, nil
}

// GetArticleVersion retrieves the full article text for a specific version.
func (r *TraceRepo) GetArticleVersion(ctx context.Context, versionID string) (map[string]interface{}, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	var (
		article     string
		title       *string
		note        *string
		traceID     string
		createdAt   time.Time
	)
	err := r.db.QueryRowContext(ctx, `
		SELECT article, article_title, version_note, trace_id, created_at
		FROM article_versions
		WHERE id = $1
	`, versionID).Scan(&article, &title, &note, &traceID, &createdAt)
	if err != nil {
		return nil, err
	}

	result := map[string]interface{}{
		"version_id": versionID,
		"trace_id":   traceID,
		"article":    article,
		"created_at": createdAt,
	}
	if title != nil {
		result["article_title"] = *title
	}
	if note != nil {
		result["version_note"] = *note
	}
	return result, nil
}

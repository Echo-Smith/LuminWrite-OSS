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

// CreateTrace inserts a new trace record.
func (r *TraceRepo) CreateTrace(ctx context.Context, execCtx *engine.ExecutionContext) error {
	if r.db == nil {
		return nil
	}

	// Only set user_id for authenticated users with valid UUID (not "anonymous", etc.)
	var userIDArg interface{}
	if execCtx.UserID != "" && execCtx.UserID != "anonymous" && isLikelyUUID(execCtx.UserID) {
		userIDArg = execCtx.UserID
	}

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO agent_traces (trace_id, user_id, user_input, style_slug, mode, status, current_step, step_history, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())
		ON CONFLICT (trace_id) DO NOTHING
	`,
		execCtx.TraceID,
		userIDArg,
		execCtx.UserInput,
		execCtx.StyleSlug,
		execCtx.Mode,
		string(execCtx.Status),
		string(execCtx.CurrentStep),
		"[]",
	)
	if err != nil {
		slog.Warn("failed to create trace", "error", err, "trace_id", execCtx.TraceID)
	}
	return err
}

// UpdateTraceStep updates the trace with the current step and step history.
func (r *TraceRepo) UpdateTraceStep(ctx context.Context, execCtx *engine.ExecutionContext) error {
	if r.db == nil {
		return nil
	}

	stepHistoryJSON, err := json.Marshal(execCtx.StepHistory)
	if err != nil {
		return err
	}

	_, err = r.db.ExecContext(ctx, `
		UPDATE agent_traces
		SET status = $1, current_step = $2, step_history = $3
		WHERE trace_id = $4
	`,
		string(execCtx.Status),
		string(execCtx.CurrentStep),
		stepHistoryJSON,
		execCtx.TraceID,
	)
	return err
}

// PauseTrace persists the paused state to the database so the session
// can be resumed after a client disconnect. Unlike CompleteTrace, it
// does not set completed_at — the trace remains "in progress".
func (r *TraceRepo) PauseTrace(ctx context.Context, execCtx *engine.ExecutionContext) error {
	if r.db == nil {
		return nil
	}

	stepHistoryJSON, err := json.Marshal(execCtx.StepHistory)
	if err != nil {
		return err
	}

	tokenJSON, _ := json.Marshal(map[string]int{
		"total_tokens": execCtx.TotalTokens,
	})

	_, err = r.db.ExecContext(ctx, `
		UPDATE agent_traces
		SET status = $1, current_step = $2, step_history = $3,
		    article = $4, article_title = $5, token_usage = $6,
		    reasoning_content = $7
		WHERE trace_id = $8
	`,
		string(execCtx.Status),
		string(execCtx.CurrentStep),
		stepHistoryJSON,
		execCtx.Article,
		execCtx.ArticleTitle,
		tokenJSON,
		execCtx.ReasoningContent,
		execCtx.TraceID,
	)
	if err != nil {
		slog.Warn("failed to persist paused trace", "error", err, "trace_id", execCtx.TraceID)
	}
	return err
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
	tokenJSON, _ := json.Marshal(map[string]int{
		"total_tokens": execCtx.TotalTokens,
	})
	stepHistoryJSON, _ := json.Marshal(execCtx.StepHistory)
	durationMs := time.Since(execCtx.StartedAt).Milliseconds()

	_, err := r.db.ExecContext(ctx, `
		UPDATE agent_traces
		SET status = $1, current_step = $2, step_history = $3,
		    article = $4, article_title = $5, review_result = $6, token_usage = $7,
		    duration_ms = $8, reasoning_content = $9, completed_at = NOW()
		WHERE trace_id = $10
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
		execCtx.TraceID,
	)
	if err != nil {
		slog.Warn("failed to complete trace", "error", err, "trace_id", execCtx.TraceID)
	}
	return err
}

// UpdateTaskName sets the task_name column for a trace.
// task_name is a short title extracted from the user's input by an LLM,
// used as the display title in the session list (priority: article_title > task_name > user_input truncated).
func (r *TraceRepo) UpdateTaskName(ctx context.Context, traceID, taskName string) error {
	if r.db == nil {
		return nil
	}
	// Truncate to 128 chars (column width) to avoid DB error
	if len([]rune(taskName)) > 128 {
		taskName = string([]rune(taskName)[:128])
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE agent_traces SET task_name = $1 WHERE trace_id = $2
	`, taskName, traceID)
	if err != nil {
		slog.Warn("failed to update task_name", "error", err, "trace_id", traceID)
	}
	return err
}

// LinkEditorialTask is now a no-op — after the two-table merge (087),
// task.ID is the trace_id, so there's no separate link to maintain.
func (r *TraceRepo) LinkEditorialTask(ctx context.Context, traceID, taskID string) error {
	return nil
}

// GetEditorialTaskID retrieves the editorial task ID associated with a trace.
// After the two-table merge (087), task.ID is the trace_id, so just return it.
func (r *TraceRepo) GetEditorialTaskID(ctx context.Context, traceID string) (string, error) {
	return traceID, nil
}

// FailTrace marks a trace as failed with an error message.
func (r *TraceRepo) FailTrace(ctx context.Context, traceID, errMsg string) error {
	if r.db == nil {
		return nil
	}

	_, err := r.db.ExecContext(ctx, `
		UPDATE agent_traces
		SET status = 'failed', error = $1, completed_at = NOW()
		WHERE trace_id = $2
	`,
		errMsg,
		traceID,
	)
	return err
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
	err := r.db.QueryRowContext(ctx, `
		SELECT status, current_step, user_input, style_slug, mode,
		       article, article_title, step_history, review_result, token_usage,
		       duration_ms, error, created_at, completed_at, reasoning_content,
		       task_name
		FROM agent_traces
		WHERE trace_id = $1
	`, traceID).Scan(
		&status, &currentStep, &userInput, &styleSlug, &mode,
		&article, &articleTitle, &stepHistory, &reviewJSON, &tokenJSON,
		&durationMs, &errorMsg, &createdAt, &completedAt, &reasoningContent,
		&taskName,
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

	// Check if user feedback has been submitted for this trace
	var feedbackCount int
	r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM feedback_segments WHERE trace_id = $1`, traceID).Scan(&feedbackCount)
	result["has_feedback"] = feedbackCount > 0

	return result, nil
}

// ListTraces lists recent traces with pagination.
// If userID is non-empty, results are filtered to that user.
func (r *TraceRepo) ListTraces(ctx context.Context, userID string, page, pageSize int) ([]map[string]interface{}, int, error) {
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

	var (
		rows   *sql.Rows
		err    error
		countQ string
		countArgs []interface{}
	)

	if userID != "" && userID != "anonymous" && isLikelyUUID(userID) {
		rows, err = r.db.QueryContext(ctx, `
			SELECT trace_id, status, current_step, user_input, style_slug, mode, created_at, completed_at, duration_ms, article_title, task_name
			FROM agent_traces
			WHERE user_id = $1 AND user_deleted = FALSE
			ORDER BY created_at DESC
			LIMIT $2 OFFSET $3
		`, userID, pageSize, offset)
		countQ = `SELECT COUNT(*) FROM agent_traces WHERE user_id = $1 AND user_deleted = FALSE`
		countArgs = []interface{}{userID}
	} else {
		rows, err = r.db.QueryContext(ctx, `
			SELECT trace_id, status, current_step, user_input, style_slug, mode, created_at, completed_at, duration_ms, article_title, task_name
			FROM agent_traces
			WHERE user_deleted = FALSE
			ORDER BY created_at DESC
			LIMIT $1 OFFSET $2
		`, pageSize, offset)
		countQ = `SELECT COUNT(*) FROM agent_traces WHERE user_deleted = FALSE`
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
		)

		if err := rows.Scan(&traceID, &status, &currentStep, &userInput, &styleSlug, &mode, &createdAt, &completedAt, &durationMs, &articleTitle, &taskName); err != nil {
			continue
		}

		trace := map[string]interface{}{
			"trace_id":     traceID,
			"status":       status,
			"current_step": currentStep,
			"user_input":   userInput,
			"mode":         mode,
			"created_at":   createdAt,
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

		traces = append(traces, trace)
	}

	// Get total count
	var total int
	r.db.QueryRowContext(ctx, countQ, countArgs...).Scan(&total)

	return traces, total, nil
}

// SoftDeleteTrace marks a trace as deleted by the user (admin still sees it).
func (r *TraceRepo) SoftDeleteTrace(ctx context.Context, traceID, userID string) error {
	if r.db == nil {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE agent_traces SET user_deleted = TRUE
		WHERE trace_id = $1 AND (user_id = $2 OR $2 = '')
	`, traceID, userID)
	return err
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

package database

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
)

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

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO agent_traces (trace_id, user_input, style_slug, mode, status, current_step, step_history, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
		ON CONFLICT (trace_id) DO NOTHING
	`,
		execCtx.TraceID,
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

// CompleteTrace finalizes the trace with article, review, and token usage.
func (r *TraceRepo) CompleteTrace(ctx context.Context, execCtx *engine.ExecutionContext) error {
	if r.db == nil {
		return nil
	}

	reviewJSON, _ := json.Marshal(execCtx.ReviewResult)
	tokenJSON, _ := json.Marshal(map[string]int{
		"total_tokens": execCtx.TotalTokens,
	})
	stepHistoryJSON, _ := json.Marshal(execCtx.StepHistory)
	durationMs := time.Since(execCtx.StartedAt).Milliseconds()

	_, err := r.db.ExecContext(ctx, `
		UPDATE agent_traces
		SET status = $1, current_step = $2, step_history = $3,
		    article = $4, review_result = $5, token_usage = $6,
		    duration_ms = $7, completed_at = NOW()
		WHERE trace_id = $8
	`,
		string(execCtx.Status),
		string(execCtx.CurrentStep),
		stepHistoryJSON,
		execCtx.Article,
		reviewJSON,
		tokenJSON,
		durationMs,
		execCtx.TraceID,
	)
	if err != nil {
		slog.Warn("failed to complete trace", "error", err, "trace_id", execCtx.TraceID)
	}
	return err
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
		article     *string
		stepHistory []byte
		reviewJSON  []byte
		tokenJSON   []byte
		durationMs  *int64
		createdAt   time.Time
		completedAt *time.Time
	)

	err := r.db.QueryRowContext(ctx, `
		SELECT status, current_step, article, step_history, review_result, token_usage, duration_ms, created_at, completed_at
		FROM agent_traces
		WHERE trace_id = $1
	`, traceID).Scan(&status, &currentStep, &article, &stepHistory, &reviewJSON, &tokenJSON, &durationMs, &createdAt, &completedAt)
	if err != nil {
		return nil, err
	}

	result := map[string]interface{}{
		"trace_id":      traceID,
		"status":        status,
		"current_step":  currentStep,
		"created_at":    createdAt,
	}

	if article != nil {
		result["article"] = *article
	}
	if completedAt != nil {
		result["completed_at"] = *completedAt
	}
	if durationMs != nil {
		result["duration_ms"] = *durationMs
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

	return result, nil
}

// ListTraces lists recent traces with pagination.
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

	rows, err := r.db.QueryContext(ctx, `
		SELECT trace_id, status, current_step, user_input, style_slug, mode, created_at, completed_at, duration_ms
		FROM agent_traces
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2
	`, pageSize, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var traces []map[string]interface{}
	for rows.Next() {
		var (
			traceID     string
			status      string
			currentStep string
			userInput   string
			styleSlug   *string
			mode        string
			createdAt   time.Time
			completedAt *time.Time
			durationMs  *int64
		)

		if err := rows.Scan(&traceID, &status, &currentStep, &userInput, &styleSlug, &mode, &createdAt, &completedAt, &durationMs); err != nil {
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

		traces = append(traces, trace)
	}

	// Get total count
	var total int
	r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_traces`).Scan(&total)

	return traces, total, nil
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
		SELECT id::text, title, description, source, platform, hot_rank, fetched_at, created_at
		FROM topics
	`
	args := []interface{}{}
	argIdx := 1

	if source != "" {
		query += fmt.Sprintf(" WHERE source = $%d", argIdx)
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
		)

		if err := rows.Scan(&id, &title, &description, &topicSource, &platform, &hotRank, &fetchedAt, &createdAt); err != nil {
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

		topics = append(topics, topic)
	}

	// Get total count
	total := 0
	countQuery := "SELECT COUNT(*) FROM topics"
	if source != "" {
		countQuery += " WHERE source = $1"
		r.db.QueryRowContext(ctx, countQuery, source).Scan(&total)
	} else {
		r.db.QueryRowContext(ctx, countQuery).Scan(&total)
	}

	return topics, total, nil
}

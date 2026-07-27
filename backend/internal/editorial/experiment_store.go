package editorial

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// ─── 对照实验类型 ─────────────────────────────────────────

// Experiment 对照实验
type Experiment struct {
	ID               string          `json:"id"`
	Title            string          `json:"title"`
	Description      string          `json:"description"`
	StyleSlug        string          `json:"style_slug"`
	Status           string          `json:"status"` // pending | running | completed | failed
	PipelineResult   json.RawMessage `json:"pipeline_result"`
	UnifiedResult    json.RawMessage `json:"unified_result"`
	EditorialResult  json.RawMessage `json:"editorial_result"`
	Summary          json.RawMessage `json:"summary"`
	CreatedBy        string          `json:"created_by,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

// ExperimentMetrics 单次执行的指标
type ExperimentMetrics struct {
	Mode         string  `json:"mode"`          // pipeline | unified | editorial
	TokenCost    int     `json:"token_cost"`
	DurationMs   int64   `json:"duration_ms"`
	WordCount    int     `json:"word_count"`
	SourceCount  int     `json:"source_count"`
	ReviewPassed bool    `json:"review_passed"`
	IssueCount   int     `json:"issue_count"`
	QualityScore float64 `json:"quality_score"` // 0.0-1.0
	ArticleTitle string  `json:"article_title,omitempty"`
	ArticleExcerpt string `json:"article_excerpt,omitempty"` // 前200字
	Error        string  `json:"error,omitempty"`
}

// CreateExperimentInput 创建实验的输入
type CreateExperimentInput struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	StyleSlug   string `json:"style_slug"`
}

// ─── 实验 Store ───────────────────────────────────────────

// CreateExperiment 创建实验
func (s *Store) CreateExperiment(ctx context.Context, input CreateExperimentInput, userID string) (*Experiment, error) {
	styleSlug := input.StyleSlug
	if styleSlug == "" {
		styleSlug = "yinyue"
	}
	var exp Experiment
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO editorial_experiments (title, description, style_slug, status, created_by)
		VALUES ($1, $2, $3, 'pending', NULLIF($4, ''))
		RETURNING id::text, title, description, style_slug, status,
			pipeline_result, unified_result, editorial_result, summary,
			COALESCE(created_by, ''), created_at, updated_at
	`,
		input.Title, input.Description, styleSlug, userID,
	).Scan(
		&exp.ID, &exp.Title, &exp.Description, &exp.StyleSlug, &exp.Status,
		&exp.PipelineResult, &exp.UnifiedResult, &exp.EditorialResult, &exp.Summary,
		&exp.CreatedBy, &exp.CreatedAt, &exp.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create experiment: %w", err)
	}
	return &exp, nil
}

// GetExperiment 获取实验
func (s *Store) GetExperiment(ctx context.Context, id string) (*Experiment, error) {
	var exp Experiment
	err := s.db.QueryRowContext(ctx, `
		SELECT id::text, title, description, style_slug, status,
			pipeline_result, unified_result, editorial_result, summary,
			COALESCE(created_by, ''), created_at, updated_at
		FROM editorial_experiments WHERE id = $1
	`, id).Scan(
		&exp.ID, &exp.Title, &exp.Description, &exp.StyleSlug, &exp.Status,
		&exp.PipelineResult, &exp.UnifiedResult, &exp.EditorialResult, &exp.Summary,
		&exp.CreatedBy, &exp.CreatedAt, &exp.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("experiment not found")
	}
	if err != nil {
		return nil, fmt.Errorf("get experiment: %w", err)
	}
	return &exp, nil
}

// ListExperiments 列出实验
func (s *Store) ListExperiments(ctx context.Context, limit int) ([]Experiment, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id::text, title, description, style_slug, status,
			pipeline_result, unified_result, editorial_result, summary,
			COALESCE(created_by, ''), created_at, updated_at
		FROM editorial_experiments
		ORDER BY created_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list experiments: %w", err)
	}
	defer rows.Close()

	var results []Experiment
	for rows.Next() {
		var exp Experiment
		if err := rows.Scan(
			&exp.ID, &exp.Title, &exp.Description, &exp.StyleSlug, &exp.Status,
			&exp.PipelineResult, &exp.UnifiedResult, &exp.EditorialResult, &exp.Summary,
			&exp.CreatedBy, &exp.CreatedAt, &exp.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan experiment: %w", err)
		}
		results = append(results, exp)
	}
	return results, nil
}

// UpdateExperimentResult 更新实验某组结果
func (s *Store) UpdateExperimentResult(ctx context.Context, id string, mode string, result json.RawMessage) error {
	var col string
	switch mode {
	case "pipeline":
		col = "pipeline_result"
	case "unified":
		col = "unified_result"
	case "editorial":
		col = "editorial_result"
	default:
		return fmt.Errorf("invalid mode: %s", mode)
	}

	query := fmt.Sprintf(`
		UPDATE editorial_experiments SET %s = $2, updated_at = NOW() WHERE id = $1
	`, col)
	_, err := s.db.ExecContext(ctx, query, id, result)
	if err != nil {
		return fmt.Errorf("update experiment result: %w", err)
	}
	return nil
}

// UpdateExperimentStatus 更新实验状态
func (s *Store) UpdateExperimentStatus(ctx context.Context, id string, status string, summary json.RawMessage) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE editorial_experiments SET status = $2, summary = COALESCE($3, summary), updated_at = NOW() WHERE id = $1
	`, id, status, summary)
	if err != nil {
		return fmt.Errorf("update experiment status: %w", err)
	}
	return nil
}
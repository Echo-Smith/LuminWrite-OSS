package editorial

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/lib/pq"
)

// Store 编辑部 PostgreSQL 存储实现
type Store struct {
	db *sql.DB
}

// NewStore 创建编辑部存储
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// ─── Task CRUD ───────────────────────────────────────────

// CreateTask 创建任务
func (s *Store) CreateTask(ctx context.Context, input CreateTaskInput, userID string) (*Task, error) {
	styleSlug := input.StyleSlug
	if styleSlug == "" {
		styleSlug = "yinyue"
	}
	budget := input.TokenBudget
	if budget == 0 {
		budget = 300000
	}

	var task Task
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO editorial_tasks (title, description, owner_id, status, accept_criteria, token_budget, priority, tags, style_slug, created_by)
		VALUES ($1, $2, $3, 'draft', $4, $5, $6, $7, $8, $3)
		RETURNING id, title, description, owner_id, assignee_type, deadline, status, accept_criteria,
			allowed_tools, token_budget, token_used, priority, tags, style_slug, conversation_id,
			created_by, created_at, updated_at
	`,
		input.Title, input.Description, userID, input.AcceptCriteria,
		budget, input.Priority, pq.Array(input.Tags), styleSlug,
	).Scan(
		&task.ID, &task.Title, &task.Description, &task.OwnerID, &task.AssigneeType,
		&task.Deadline, &task.Status, &task.AcceptCriteria,
		pq.Array(&task.AllowedTools), &task.TokenBudget, &task.TokenUsed,
		&task.Priority, pq.Array(&task.Tags), &task.StyleSlug, &task.ConversationID,
		&task.CreatedBy, &task.CreatedAt, &task.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create task: %w", err)
	}
	return &task, nil
}

// GetTask 获取任务
func (s *Store) GetTask(ctx context.Context, id string) (*Task, error) {
	var task Task
	err := s.db.QueryRowContext(ctx, `
		SELECT id, title, description, owner_id, assignee_type, deadline, status, accept_criteria,
			allowed_tools, token_budget, token_used, priority, tags, style_slug, conversation_id,
			created_by, created_at, updated_at
		FROM editorial_tasks WHERE id = $1
	`, id).Scan(
		&task.ID, &task.Title, &task.Description, &task.OwnerID, &task.AssigneeType,
		&task.Deadline, &task.Status, &task.AcceptCriteria,
		pq.Array(&task.AllowedTools), &task.TokenBudget, &task.TokenUsed,
		&task.Priority, pq.Array(&task.Tags), &task.StyleSlug, &task.ConversationID,
		&task.CreatedBy, &task.CreatedAt, &task.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, ErrTaskNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get task: %w", err)
	}
	return &task, nil
}

// ListTasks 列出任务（支持状态过滤）
func (s *Store) ListTasks(ctx context.Context, status string, limit, offset int) ([]Task, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	query := `
		SELECT id, title, description, owner_id, assignee_type, deadline, status, accept_criteria,
			allowed_tools, token_budget, token_used, priority, tags, style_slug, conversation_id,
			created_by, created_at, updated_at
		FROM editorial_tasks
	`
	args := []interface{}{}
	if status != "" {
		query += " WHERE status = $1"
		args = append(args, status)
	}
	query += " ORDER BY created_at DESC LIMIT $" + fmt.Sprintf("%d", len(args)+1) + " OFFSET $" + fmt.Sprintf("%d", len(args)+2)
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		var task Task
		if err := rows.Scan(
			&task.ID, &task.Title, &task.Description, &task.OwnerID, &task.AssigneeType,
			&task.Deadline, &task.Status, &task.AcceptCriteria,
			pq.Array(&task.AllowedTools), &task.TokenBudget, &task.TokenUsed,
			&task.Priority, pq.Array(&task.Tags), &task.StyleSlug, &task.ConversationID,
			&task.CreatedBy, &task.CreatedAt, &task.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}

// UpdateTaskStatus 更新任务状态
func (s *Store) UpdateTaskStatus(ctx context.Context, id string, status TaskStatus, assignee AssigneeType) error {
	now := time.Now()
	_, err := s.db.ExecContext(ctx, `
		UPDATE editorial_tasks SET status = $2, assignee_type = $3, updated_at = $4 WHERE id = $1
	`, id, status, assignee, now)
	if err != nil {
		return fmt.Errorf("update task status: %w", err)
	}
	return nil
}

// AddTokenUsage 增加任务 Token 用量
func (s *Store) AddTokenUsage(ctx context.Context, taskID string, tokens int) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE editorial_tasks SET token_used = token_used + $2, updated_at = NOW() WHERE id = $1
	`, taskID, tokens)
	return err
}

// ─── Artifact CRUD ───────────────────────────────────────

// CreateArtifact 创建交付物
func (s *Store) CreateArtifact(ctx context.Context, input SubmitArtifactInput, taskID string) (*Artifact, error) {
	// 获取当前最大版本号
	var maxVersion int
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(version), 0) FROM editorial_artifacts WHERE task_id = $1 AND type = $2
	`, taskID, input.Type).Scan(&maxVersion)
	if err != nil {
		return nil, fmt.Errorf("get max version: %w", err)
	}
	nextVersion := maxVersion + 1

	// 如果有前一版本，标记为 superseded
	if input.ParentID != "" {
		s.db.ExecContext(ctx, `
			UPDATE editorial_artifacts SET status = 'superseded', updated_at = NOW() WHERE id = $1
		`, input.ParentID)
	} else if maxVersion > 0 {
		// 自动找到前一版本并标记
		s.db.ExecContext(ctx, `
			UPDATE editorial_artifacts SET status = 'superseded', updated_at = NOW()
			WHERE task_id = $1 AND type = $2 AND version = $3
		`, taskID, input.Type, maxVersion)
	}

	var art Artifact
	parentID := sql.NullString{}
	if input.ParentID != "" {
		parentID.Valid = true
		parentID.String = input.ParentID
	}

	err = s.db.QueryRowContext(ctx, `
		INSERT INTO editorial_artifacts (task_id, type, version, content, status, produced_by, parent_id, token_cost)
		VALUES ($1, $2, $3, $4, 'submitted', $5, $6, $7)
		RETURNING id, task_id, type, version, content, status, produced_by, reviewed_by, review_note, parent_id, token_cost, created_at, updated_at
	`,
		taskID, input.Type, nextVersion, input.Content, input.ProducedBy, parentID, input.TokenCost,
	).Scan(
		&art.ID, &art.TaskID, &art.Type, &art.Version, &art.Content, &art.Status,
		&art.ProducedBy, &art.ReviewedBy, &art.ReviewNote, &art.ParentID, &art.TokenCost,
		&art.CreatedAt, &art.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create artifact: %w", err)
	}

	// 更新任务 Token 用量
	if input.TokenCost > 0 {
		s.AddTokenUsage(ctx, taskID, input.TokenCost)
	}

	return &art, nil
}

// GetArtifact 获取交付物
func (s *Store) GetArtifact(ctx context.Context, id string) (*Artifact, error) {
	var art Artifact
	err := s.db.QueryRowContext(ctx, `
		SELECT id, task_id, type, version, content::text, status, produced_by,
			COALESCE(reviewed_by::text, ''), review_note, COALESCE(parent_id::text, ''),
			token_cost, created_at, updated_at
		FROM editorial_artifacts WHERE id = $1
	`, id).Scan(
		&art.ID, &art.TaskID, &art.Type, &art.Version, &art.Content, &art.Status,
		&art.ProducedBy, &art.ReviewedBy, &art.ReviewNote, &art.ParentID,
		&art.TokenCost, &art.CreatedAt, &art.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, ErrArtifactNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get artifact: %w", err)
	}
	return &art, nil
}

// ListArtifacts 列出任务的所有交付物
func (s *Store) ListArtifacts(ctx context.Context, taskID string) ([]Artifact, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, task_id, type, version, content::text, status, produced_by,
			COALESCE(reviewed_by::text, ''), review_note, COALESCE(parent_id::text, ''),
			token_cost, created_at, updated_at
		FROM editorial_artifacts WHERE task_id = $1 ORDER BY created_at ASC
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list artifacts: %w", err)
	}
	defer rows.Close()

	var artifacts []Artifact
	for rows.Next() {
		var art Artifact
		if err := rows.Scan(
			&art.ID, &art.TaskID, &art.Type, &art.Version, &art.Content, &art.Status,
			&art.ProducedBy, &art.ReviewedBy, &art.ReviewNote, &art.ParentID,
			&art.TokenCost, &art.CreatedAt, &art.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan artifact: %w", err)
		}
		artifacts = append(artifacts, art)
	}
	return artifacts, nil
}

// GetLatestApprovedArtifact 获取指定类型的最新已批准交付物
func (s *Store) GetLatestApprovedArtifact(ctx context.Context, taskID string, artType ArtifactType) (*Artifact, error) {
	var art Artifact
	err := s.db.QueryRowContext(ctx, `
		SELECT id, task_id, type, version, content::text, status, produced_by,
			COALESCE(reviewed_by::text, ''), review_note, COALESCE(parent_id::text, ''),
			token_cost, created_at, updated_at
		FROM editorial_artifacts
		WHERE task_id = $1 AND type = $2 AND status = 'approved'
		ORDER BY version DESC LIMIT 1
	`, taskID, artType).Scan(
		&art.ID, &art.TaskID, &art.Type, &art.Version, &art.Content, &art.Status,
		&art.ProducedBy, &art.ReviewedBy, &art.ReviewNote, &art.ParentID,
		&art.TokenCost, &art.CreatedAt, &art.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, ErrArtifactNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get latest approved artifact: %w", err)
	}
	return &art, nil
}

// ReviewArtifact 审批交付物
func (s *Store) ReviewArtifact(ctx context.Context, id string, input ReviewArtifactInput) (*Artifact, error) {
	var art Artifact
	err := s.db.QueryRowContext(ctx, `
		UPDATE editorial_artifacts
		SET status = $2, reviewed_by = $3, review_note = $4, updated_at = NOW()
		WHERE id = $1
		RETURNING id, task_id, type, version, content::text, status, produced_by,
			COALESCE(reviewed_by::text, ''), review_note, COALESCE(parent_id::text, ''),
			token_cost, created_at, updated_at
	`, id, input.Status, input.ReviewerID, input.ReviewNote).Scan(
		&art.ID, &art.TaskID, &art.Type, &art.Version, &art.Content, &art.Status,
		&art.ProducedBy, &art.ReviewedBy, &art.ReviewNote, &art.ParentID,
		&art.TokenCost, &art.CreatedAt, &art.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, ErrArtifactNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("review artifact: %w", err)
	}
	slog.Info("artifact reviewed",
		"artifact_id", id, "status", input.Status, "reviewer", input.ReviewerID)
	return &art, nil
}

// ─── Decision CRUD ───────────────────────────────────────

// CreateDecision 创建决策
func (s *Store) CreateDecision(ctx context.Context, input CreateDecisionInput, taskID string) (*Decision, error) {
	var d Decision
	var decidedAt sql.NullTime
	var decidedBy sql.NullString
	if input.DecidedBy != "" {
		decidedBy.Valid = true
		decidedBy.String = input.DecidedBy
	}

	err := s.db.QueryRowContext(ctx, `
		INSERT INTO editorial_decisions (task_id, type, decided_by, decided_by_type, status, rationale, evidence, artifact_id, decided_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, CASE WHEN $5 != 'pending' THEN NOW() ELSE NULL END)
		RETURNING id, task_id, type, COALESCE(decided_by::text, ''), decided_by_type, status, rationale, evidence,
			COALESCE(artifact_id::text, ''), created_at, decided_at
	`,
		taskID, input.Type, decidedBy, input.DecidedByType, input.Status,
		input.Rationale, input.Evidence, input.ArtifactID,
	).Scan(
		&d.ID, &d.TaskID, &d.Type, &d.DecidedBy, &d.DecidedByType, &d.Status,
		&d.Rationale, &d.Evidence, &d.ArtifactID, &d.CreatedAt, &decidedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create decision: %w", err)
	}
	if decidedAt.Valid {
		d.DecidedAt = &decidedAt.Time
	}
	return &d, nil
}

// ListDecisions 列出任务的所有决策
func (s *Store) ListDecisions(ctx context.Context, taskID string) ([]Decision, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, task_id, type, COALESCE(decided_by::text, ''), decided_by_type, status, rationale, evidence,
			COALESCE(artifact_id::text, ''), created_at, decided_at
		FROM editorial_decisions WHERE task_id = $1 ORDER BY created_at ASC
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list decisions: %w", err)
	}
	defer rows.Close()

	var decisions []Decision
	for rows.Next() {
		var d Decision
		var decidedAt sql.NullTime
		if err := rows.Scan(
			&d.ID, &d.TaskID, &d.Type, &d.DecidedBy, &d.DecidedByType, &d.Status,
			&d.Rationale, &d.Evidence, &d.ArtifactID, &d.CreatedAt, &decidedAt,
		); err != nil {
			return nil, fmt.Errorf("scan decision: %w", err)
		}
		if decidedAt.Valid {
			d.DecidedAt = &decidedAt.Time
		}
		decisions = append(decisions, d)
	}
	return decisions, nil
}

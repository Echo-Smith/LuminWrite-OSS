package editorial

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
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
			allowed_tools, token_budget, token_used, priority, tags, style_slug, COALESCE(conversation_id, ''),
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
		allowed_tools, token_budget, token_used, priority, tags, style_slug, COALESCE(conversation_id, ''),
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

// ListTasks 列出任务（支持状态过滤和用户隔离）
func (s *Store) ListTasks(ctx context.Context, status string, ownerID string, limit, offset int) ([]Task, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	query := `
		SELECT id, title, description, owner_id, assignee_type, deadline, status, accept_criteria,
			allowed_tools, token_budget, token_used, priority, tags, style_slug, COALESCE(conversation_id, ''),
			created_by, created_at, updated_at
		FROM editorial_tasks
	`
	args := []interface{}{}
	argIdx := 1
	whereParts := []string{}
	if status != "" {
		whereParts = append(whereParts, fmt.Sprintf("status = $%d", argIdx))
		args = append(args, status)
		argIdx++
	}
	if ownerID != "" {
		whereParts = append(whereParts, fmt.Sprintf("owner_id = $%d", argIdx))
		args = append(args, ownerID)
		argIdx++
	}
	if len(whereParts) > 0 {
		query += " WHERE " + strings.Join(whereParts, " AND ")
	}
	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", argIdx, argIdx+1)
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
		RETURNING id, task_id, type, version, content, status, produced_by,
			reviewed_by, review_note, COALESCE(parent_id::text, ''), token_cost, created_at, updated_at
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

// decisionScanColumns is the canonical column list for scanning Decision rows.
// Includes both new actor model columns and legacy columns for backward compat.
const decisionScanColumns = `id, task_id, type,
	COALESCE(actor_type, 'system'), COALESCE(actor_user_id::text, ''), COALESCE(actor_role, ''), COALESCE(actor_label, ''),
	COALESCE(approve_target_status, ''), COALESCE(reject_target_status, ''),
	status, rationale, evidence, COALESCE(artifact_id::text, ''),
	created_at, decided_at,
	COALESCE(decided_by, ''), COALESCE(decided_by_type, '')`

// scanDecision scans a Decision row using the canonical column list.
func scanDecision(scanner interface{ Scan(dest ...interface{}) error }, d *Decision) error {
	var decidedAt sql.NullTime
	var actorUserID, actorRole, actorLabel, actorType string
	var approveTarget, rejectTarget string
	err := scanner.Scan(
		&d.ID, &d.TaskID, &d.Type,
		&actorType, &actorUserID, &actorRole, &actorLabel,
		&approveTarget, &rejectTarget,
		&d.Status, &d.Rationale, &d.Evidence, &d.ArtifactID,
		&d.CreatedAt, &decidedAt,
		&d.DecidedBy, &d.DecidedByType,
	)
	if err != nil {
		return err
	}
	d.Actor = Actor{
		Type:   ActorType(actorType),
		UserID: actorUserID,
		Role:   actorRole,
		Label:  actorLabel,
	}
	d.ApproveTargetStatus = approveTarget
	d.RejectTargetStatus = rejectTarget
	if decidedAt.Valid {
		d.DecidedAt = &decidedAt.Time
	}
	return nil
}

// CreateDecision 创建决策
func (s *Store) CreateDecision(ctx context.Context, input CreateDecisionInput, taskID string) (*Decision, error) {
	// Resolve actor from input — prefer Actor field, fall back to legacy fields
	actor := input.Actor
	if actor.Type == "" {
		actor = actorFromLegacy(input.DecidedBy, input.DecidedByType)
	}
	if actor.Label == "" {
		actor.Label = actor.UserID
		if actor.Label == "" {
			actor.Label = actor.Role
		}
		if actor.Label == "" {
			actor.Label = string(actor.Type)
		}
	}

	// Handle nullable actor_user_id (UUID)
	var actorUserID interface{}
	if actor.UserID != "" {
		actorUserID = actor.UserID
	} else {
		actorUserID = nil
	}

	// Handle nullable artifact_id
	var artifactID interface{}
	if input.ArtifactID != "" {
		artifactID = input.ArtifactID
	} else {
		artifactID = nil
	}

	// Handle nullable target statuses
	var approveTarget, rejectTarget interface{}
	if input.ApproveTargetStatus != "" {
		approveTarget = string(input.ApproveTargetStatus)
	} else {
		approveTarget = nil
	}
	if input.RejectTargetStatus != "" {
		rejectTarget = string(input.RejectTargetStatus)
	} else {
		rejectTarget = nil
	}

	// Legacy decided_by for backward compat column
	var decidedBy sql.NullString
	if input.DecidedBy != "" {
		decidedBy.Valid = true
		decidedBy.String = input.DecidedBy
	} else if actor.UserID != "" {
		decidedBy.Valid = true
		decidedBy.String = actor.UserID
	} else if actor.Role != "" {
		decidedBy.Valid = true
		decidedBy.String = actor.Role
	}

	// Compute decided_at in Go to avoid using $9 twice in SQL (causes type inference issues)
	var decidedAt interface{}
	if input.Status != DecisionStatusPending {
		decidedAt = time.Now()
	} else {
		decidedAt = nil
	}

	var d Decision
	rawRow := s.db.QueryRowContext(ctx, `
		INSERT INTO editorial_decisions (
			task_id, type,
			actor_type, actor_user_id, actor_role, actor_label,
			approve_target_status, reject_target_status,
			status, rationale, evidence, artifact_id, decided_at,
			decided_by, decided_by_type
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		RETURNING ` + decisionScanColumns,
		taskID, input.Type,
		actor.Type, actorUserID, actor.Role, actor.Label,
		approveTarget, rejectTarget,
		input.Status, input.Rationale, input.Evidence, artifactID,
		decidedAt,
		decidedBy, input.DecidedByType,
	)
	if err := scanDecision(rawRow, &d); err != nil {
		return nil, fmt.Errorf("create decision: %w", err)
	}
	return &d, nil
}

// actorFromLegacy converts legacy decided_by/decided_by_type to Actor
func actorFromLegacy(decidedBy string, decidedByType DecidedByType) Actor {
	a := Actor{Label: decidedBy}
	switch decidedByType {
	case DecidedByHuman:
		a.Type = ActorHuman
		a.UserID = decidedBy
	case DecidedByResearchAgent, DecidedByWritingAgent, DecidedByReviewAgent:
		a.Type = ActorAgent
		a.Role = string(decidedByType)
		if a.Label == "" {
			a.Label = string(decidedByType)
		}
	default:
		a.Type = ActorSystem
		if a.Label == "" {
			a.Label = "system"
		}
	}
	return a
}

// ListDecisions 列出任务的所有决策
func (s *Store) ListDecisions(ctx context.Context, taskID string) ([]Decision, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+decisionScanColumns+`
		FROM editorial_decisions WHERE task_id = $1 ORDER BY created_at ASC
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list decisions: %w", err)
	}
	defer rows.Close()

	var decisions []Decision
	for rows.Next() {
		var d Decision
		if err := scanDecision(rows, &d); err != nil {
			return nil, fmt.Errorf("scan decision: %w", err)
		}
		decisions = append(decisions, d)
	}
	return decisions, nil
}

// ListPendingDecisions 列出所有待处理决策（跨任务，支持用户隔离）
func (s *Store) ListPendingDecisions(ctx context.Context, ownerID string, limit int) ([]DecisionWithTask, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	query := `
		SELECT d.` + decisionScanColumns + `,
		       t.title, t.status, t.assignee_type, t.owner_id, t.priority, t.token_used, t.token_budget
		FROM editorial_decisions d
		JOIN editorial_tasks t ON t.id = d.task_id
		WHERE d.status = 'pending'
	`
	args := []interface{}{}
	argIdx := 1
	if ownerID != "" {
		query += fmt.Sprintf(" AND t.owner_id = $%d", argIdx)
		args = append(args, ownerID)
		argIdx++
	}
	query += fmt.Sprintf(" ORDER BY t.priority DESC, d.created_at ASC LIMIT $%d", argIdx)
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list pending decisions: %w", err)
	}
	defer rows.Close()

	var results []DecisionWithTask
	for rows.Next() {
		var dwt DecisionWithTask
		if err := scanDecisionWithTask(rows, &dwt); err != nil {
			return nil, fmt.Errorf("scan pending decision: %w", err)
		}
		results = append(results, dwt)
	}
	return results, nil
}

// scanDecisionWithTask scans a Decision + Task info row.
func scanDecisionWithTask(scanner interface{ Scan(dest ...interface{}) error }, dwt *DecisionWithTask) error {
	var decidedAt sql.NullTime
	var actorUserID, actorRole, actorLabel, actorType string
	var approveTarget, rejectTarget string
	err := scanner.Scan(
		&dwt.Decision.ID, &dwt.Decision.TaskID, &dwt.Decision.Type,
		&actorType, &actorUserID, &actorRole, &actorLabel,
		&approveTarget, &rejectTarget,
		&dwt.Decision.Status, &dwt.Decision.Rationale, &dwt.Decision.Evidence, &dwt.Decision.ArtifactID,
		&dwt.Decision.CreatedAt, &decidedAt,
		&dwt.Decision.DecidedBy, &dwt.Decision.DecidedByType,
		&dwt.TaskTitle, &dwt.TaskStatus, &dwt.TaskAssignee, &dwt.TaskOwnerID, &dwt.TaskPriority,
		&dwt.TaskTokenUsed, &dwt.TaskTokenBudget,
	)
	if err != nil {
		return err
	}
	dwt.Decision.Actor = Actor{
		Type:   ActorType(actorType),
		UserID: actorUserID,
		Role:   actorRole,
		Label:  actorLabel,
	}
	dwt.Decision.ApproveTargetStatus = approveTarget
	dwt.Decision.RejectTargetStatus = rejectTarget
	if decidedAt.Valid {
		dwt.Decision.DecidedAt = &decidedAt.Time
	}
	return nil
}

// UpdateDecisionStatus 更新决策状态（用于人类处理 pending 决策）
func (s *Store) UpdateDecisionStatus(ctx context.Context, decisionID string, status DecisionStatus, rationale string, decidedBy string) (*Decision, error) {
	var d Decision
	rawRow := s.db.QueryRowContext(ctx, `
		UPDATE editorial_decisions
		SET status = $2, rationale = $3, decided_by = $4, decided_at = NOW()
		WHERE id = $1
		RETURNING ` + decisionScanColumns,
		decisionID, status, rationale, decidedBy,
	)
	if err := scanDecision(rawRow, &d); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrDecisionNotFound
		}
		return nil, fmt.Errorf("update decision status: %w", err)
	}
	return &d, nil
}

// GetDecision 获取单个决策
func (s *Store) GetDecision(ctx context.Context, decisionID string) (*Decision, error) {
	var d Decision
	rawRow := s.db.QueryRowContext(ctx, `
		SELECT `+decisionScanColumns+`
		FROM editorial_decisions WHERE id = $1
	`, decisionID)
	if err := scanDecision(rawRow, &d); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrDecisionNotFound
		}
		return nil, fmt.Errorf("get decision: %w", err)
	}
	return &d, nil
}

// ResolveDecisionTxParams holds parameters for the transactional decision resolution.
type ResolveDecisionTxParams struct {
	DecisionID string
	Status     DecisionStatus // approved | rejected
	Rationale  string
	DecidedBy  string // user ID of the human resolving the decision
}

// ResolveDecisionTx atomically resolves a decision and advances the task.
// Uses approve_target_status / reject_target_status stored on the Decision itself,
// eliminating the need for a global switch to guess the next state.
// Both the decision status update and the task status update happen in a single transaction.
// If either fails, the entire operation rolls back.
func (s *Store) ResolveDecisionTx(ctx context.Context, params ResolveDecisionTxParams) (*Decision, TaskStatus, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, "", fmt.Errorf("begin tx: %w", err)
	}
	// Always rollback — no-op if already committed
	defer tx.Rollback()

	// 1. Resolve the decision and read its target statuses
	var approveTarget, rejectTarget string
	var taskID string
	err = tx.QueryRowContext(ctx, `
		UPDATE editorial_decisions
		SET status = $2, rationale = $3, decided_by = $4, decided_at = NOW()
		WHERE id = $1 AND status = 'pending'
		RETURNING task_id,
			COALESCE(approve_target_status, ''),
			COALESCE(reject_target_status, '')
	`, params.DecisionID, params.Status, params.Rationale, params.DecidedBy).Scan(
		&taskID, &approveTarget, &rejectTarget,
	)
	if err == sql.ErrNoRows {
		return nil, "", ErrDecisionNotFound
	}
	if err != nil {
		return nil, "", fmt.Errorf("update decision in tx: %w", err)
	}

	// 2. Determine the target status from the Decision itself
	var targetStatusStr string
	if params.Status == DecisionStatusApproved {
		targetStatusStr = approveTarget
	} else {
		targetStatusStr = rejectTarget
	}

	// 3. If there's a target status, advance the task with SELECT FOR UPDATE
	var nextStatus TaskStatus
	if targetStatusStr != "" {
		nextStatus = TaskStatus(targetStatusStr)

		// Lock the task row for the duration of this transaction
		var currentStatus TaskStatus
		err = tx.QueryRowContext(ctx, `
			SELECT status FROM editorial_tasks WHERE id = $1 FOR UPDATE
		`, taskID).Scan(&currentStatus)
		if err != nil {
			return nil, "", fmt.Errorf("lock task in tx: %w", err)
		}

		// Validate the transition is legal
		// If the target is the same as current, no transition needed (e.g., reject select_angle stays at research)
		if currentStatus == nextStatus {
			// No status change needed, but still commit the decision update
		} else if !currentStatus.CanTransitionTo(nextStatus) {
			return nil, "", fmt.Errorf("%w: %s → %s", ErrInvalidTransition, currentStatus, nextStatus)
		} else {
			assignee := defaultAssignee(nextStatus)
			_, err = tx.ExecContext(ctx, `
			UPDATE editorial_tasks SET status = $2, assignee_type = $3, updated_at = NOW() WHERE id = $1
		`, taskID, nextStatus, assignee)
			if err != nil {
				return nil, "", fmt.Errorf("update task status in tx: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, "", fmt.Errorf("commit resolve decision tx: %w", err)
	}

	// Re-read the full decision for the return value
	dPtr, err := s.GetDecision(ctx, params.DecisionID)
	if err != nil {
		return nil, "", fmt.Errorf("re-read decision after resolve: %w", err)
	}
	return dPtr, nextStatus, nil
}

// ─── TransitionTask (P0-2: 单一事务化入口) ──────────────

// TransitionTask is the single entry point for all task status transitions.
// It performs the following in one transaction:
//   - SELECT ... FOR UPDATE to lock the task row
//   - Validate expected status (optimistic locking)
//   - Validate the transition is legal
//   - Update task status
//   - Create an Agent run lease if transitioning to an agent-executing state
//
// Agent execution is NOT started here — the caller should do that after commit.
func (s *Store) TransitionTask(ctx context.Context, cmd TransitionCommand) (*Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	// Always rollback — no-op if already committed
	defer tx.Rollback()

	// 1. Lock the task row and read current state
	var task Task
	err = tx.QueryRowContext(ctx, `
		SELECT id, title, description, owner_id, assignee_type, deadline, status, accept_criteria,
			allowed_tools, token_budget, token_used, priority, tags, style_slug, COALESCE(conversation_id, ''),
			created_by, created_at, updated_at
		FROM editorial_tasks WHERE id = $1 FOR UPDATE
	`, cmd.TaskID).Scan(
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
		return nil, fmt.Errorf("lock task: %w", err)
	}

	// 2. Validate expected status (optimistic locking)
	if cmd.ExpectedStatus != "" && task.Status != cmd.ExpectedStatus {
		return nil, fmt.Errorf("%w: expected %s, got %s", ErrStatusConflict, cmd.ExpectedStatus, task.Status)
	}

	// 3. Validate the transition is legal
	if !task.Status.CanTransitionTo(cmd.TargetStatus) {
		return nil, fmt.Errorf("%w: %s → %s", ErrInvalidTransition, task.Status, cmd.TargetStatus)
	}

	// 4. Update task status
	assignee := defaultAssignee(cmd.TargetStatus)
	_, err = tx.ExecContext(ctx, `
		UPDATE editorial_tasks SET status = $2, assignee_type = $3, updated_at = NOW() WHERE id = $1
	`, cmd.TaskID, cmd.TargetStatus, assignee)
	if err != nil {
		return nil, fmt.Errorf("update task status: %w", err)
	}

	// 5. If transitioning to an agent-executing state, create a lease
	if cmd.AutoStartAgent {
		agentRole := agentRoleForStatus(cmd.TargetStatus)
		if agentRole != "" {
			_, err = tx.ExecContext(ctx, `
				INSERT INTO editorial_agent_leases (task_id, agent_role, expired_at)
				VALUES ($1, $2, NOW() + INTERVAL '10 minutes')
			`, cmd.TaskID, agentRole)
			if err != nil {
				// If the unique index on active leases fails, it means another agent is already running
				return nil, fmt.Errorf("%w: task_id=%s role=%s", ErrLeaseConflict, cmd.TaskID, agentRole)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit transition: %w", err)
	}

	task.Status = cmd.TargetStatus
	task.AssigneeType = assignee
	return &task, nil
}

// ─── Agent Run Events (P0-4: Event/Decision/Transition 三层模型) ──

// RecordAgentRunEvent records an objective event about agent execution.
// Unlike Decisions, Events do not involve choice — they record what happened.
func (s *Store) RecordAgentRunEvent(ctx context.Context, evt AgentRunEvent) (*AgentRunEvent, error) {
	var e AgentRunEvent
	var artifactID interface{}
	if evt.ArtifactID != "" {
		artifactID = evt.ArtifactID
	} else {
		artifactID = nil
	}
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO editorial_agent_run_events (task_id, type, agent_role, status, artifact_id, error, metadata)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, task_id, type, agent_role, status, COALESCE(artifact_id::text, ''), COALESCE(error, ''), created_at
	`, evt.TaskID, evt.Type, evt.AgentRole, evt.Status, artifactID, evt.Error, evt.Metadata,
	).Scan(&e.ID, &e.TaskID, &e.Type, &e.AgentRole, &e.Status, &e.ArtifactID, &e.Error, &e.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("record agent run event: %w", err)
	}
	return &e, nil
}

// ListAgentRunEvents lists events for a task, ordered by creation time.
func (s *Store) ListAgentRunEvents(ctx context.Context, taskID string) ([]AgentRunEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, task_id, type, agent_role, status, COALESCE(artifact_id::text, ''), COALESCE(error, ''), created_at
		FROM editorial_agent_run_events
		WHERE task_id = $1
		ORDER BY created_at ASC
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list agent run events: %w", err)
	}
	defer rows.Close()

	var events []AgentRunEvent
	for rows.Next() {
		var e AgentRunEvent
		if err := rows.Scan(&e.ID, &e.TaskID, &e.Type, &e.AgentRole, &e.Status, &e.ArtifactID, &e.Error, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan agent run event: %w", err)
		}
		events = append(events, e)
	}
	return events, nil
}

// ─── Agent Run Leases (P0-2: 数据库级互斥) ──────────────

// AcquireLease attempts to acquire an agent run lease for the given task and role.
// Returns ErrLeaseConflict if an active lease already exists.
// The lease expires after the given duration.
func (s *Store) AcquireLease(ctx context.Context, taskID string, role AgentRole, ttl time.Duration) error {
	ttlStr := fmt.Sprintf("%d seconds", int(ttl.Seconds()))

	// First, try to reactivate an existing non-active lease (handles re-acquire after release)
	result, err := s.db.ExecContext(ctx, `
		UPDATE editorial_agent_leases
		SET status = 'active', expired_at = NOW() + $3::interval,
		    released_at = NULL, acquired_at = NOW()
		WHERE task_id = $1 AND agent_role = $2 AND status != 'active'
	`, taskID, string(role), ttlStr)
	if err == nil {
		if rows, _ := result.RowsAffected(); rows > 0 {
			return nil // Successfully reactivated
		}
	}

	// No existing non-active lease to reactivate — try to INSERT a new one.
	// This will fail if an active lease exists (partial unique index).
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO editorial_agent_leases (task_id, agent_role, expired_at)
		VALUES ($1, $2, NOW() + $3::interval)
	`, taskID, string(role), ttlStr)
	if err != nil {
		return ErrLeaseConflict
	}
	return nil
}

// ReleaseLease marks an agent run lease as completed.
func (s *Store) ReleaseLease(ctx context.Context, taskID string, role AgentRole, status string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE editorial_agent_leases
		SET status = $3, released_at = NOW()
		WHERE task_id = $1 AND agent_role = $2 AND status = 'active'
	`, taskID, string(role), status)
	return err
}

// HasActiveLease checks if there's an active (non-expired) lease for the given task and role.
func (s *Store) HasActiveLease(ctx context.Context, taskID string, role AgentRole) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM editorial_agent_leases
		WHERE task_id = $1 AND agent_role = $2 AND status = 'active' AND expired_at > NOW()
	`, taskID, string(role)).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// ─── Helpers ─────────────────────────────────────────────

// agentRoleForStatus returns the agent role that should execute for the given task status.
func agentRoleForStatus(status TaskStatus) string {
	switch status {
	case StatusResearch:
		return string(RoleResearch)
	case StatusWriting:
		return string(RoleWriting)
	case StatusReview:
		return string(RoleReview)
	default:
		return ""
	}
}

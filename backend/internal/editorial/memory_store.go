package editorial

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ─── 组织记忆类型 ─────────────────────────────────────────

// SourceCredibility 信源可信度记录
type SourceCredibility struct {
	ID               string    `json:"id"`
	SourceDomain     string    `json:"source_domain"`
	SourceName       string    `json:"source_name"`
	Category         string    `json:"category"`          // news | gov | academic | social | blog
	CredibilityScore float64   `json:"credibility_score"`  // 0.0-1.0
	TotalUses        int       `json:"total_uses"`
	VerifiedCount    int       `json:"verified_count"`
	RefutedCount     int       `json:"refuted_count"`
	LastTaskID       string    `json:"last_task_id,omitempty"`
	Notes            string    `json:"notes,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// ColumnPreference 栏目偏好
type ColumnPreference struct {
	ID                string   `json:"id"`
	ColumnTag         string   `json:"column_tag"`
	StyleSlug         string   `json:"style_slug"`
	PreferredLengthMin int     `json:"preferred_length_min"`
	PreferredLengthMax int     `json:"preferred_length_max"`
	Tone              string   `json:"tone,omitempty"`
	ForbiddenWords    []string `json:"forbidden_words,omitempty"`
	ReviewCriteria    string   `json:"review_criteria,omitempty"`
	AcceptanceRate    float64  `json:"acceptance_rate"`
	TotalTasks        int      `json:"total_tasks"`
	PublishedCount    int      `json:"published_count"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// ─── 知识生命周期状态 ─────────────────────────────────────

// Knowledge status constants
const (
	KnowledgeStatusCandidate  string = "candidate"  // 候选：Agent 提取，待人类确认
	KnowledgeStatusActive     string = "active"     // 活跃：已确认，正常注入
	KnowledgeStatusArchived   string = "archived"   // 归档：不再使用但不删除
	KnowledgeStatusSuperseded string = "superseded" // 已被新知识替代
)

// canTransitionTo validates knowledge status transitions.
func knowledgeCanTransitionTo(from, to string) bool {
	switch from {
	case KnowledgeStatusCandidate:
		return to == KnowledgeStatusActive || to == KnowledgeStatusArchived
	case KnowledgeStatusActive:
		return to == KnowledgeStatusArchived || to == KnowledgeStatusSuperseded
	case KnowledgeStatusArchived:
		return false // archived is terminal
	case KnowledgeStatusSuperseded:
		return false // superseded is terminal
	default:
		return false
	}
}

// EditorialKnowledge 编辑部知识沉淀
type EditorialKnowledge struct {
	ID                 string     `json:"id"`
	Category           string     `json:"category"` // rejection_reason | review_tip | style_guideline | fact_check_note
	ColumnTag          string     `json:"column_tag,omitempty"`
	Title              string     `json:"title"`
	Content            string     `json:"content"`
	ContentFingerprint string     `json:"content_fingerprint,omitempty"` // SHA-256 of normalized content (dedup key)
	Scope              string     `json:"scope"`                          // global | column | task
	Source             string     `json:"source"`                         // agent | human | system
	SourceTaskID       string     `json:"source_task_id,omitempty"`
	SourceArtifactID   string     `json:"source_artifact_id,omitempty"`
	Confidence         float64    `json:"confidence"`
	OccurrenceCount    int        `json:"occurrence_count"`
	Status             string     `json:"status"`
	SupersededBy       string     `json:"superseded_by,omitempty"`
	ArchivedReason     string     `json:"archived_reason,omitempty"`
	ArchivedAt         *time.Time `json:"archived_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// AgentReputation Agent 信誉记录
type AgentReputation struct {
	ID               string     `json:"id"`
	AgentRole        string     `json:"agent_role"`
	TotalExecutions  int        `json:"total_executions"`
	SuccessCount     int        `json:"success_count"`
	FailureCount     int        `json:"failure_count"`
	AvgTokenCost     int        `json:"avg_token_cost"`
	AvgQualityScore  float64    `json:"avg_quality_score"`
	AvgDurationMs    int        `json:"avg_duration_ms"`
	LastTaskID       string     `json:"last_task_id,omitempty"`
	LastExecutionAt  *time.Time `json:"last_execution_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// RecordSourceInput 记录像源使用情况的输入
type RecordSourceInput struct {
	SourceDomain string
	SourceName   string
	Category     string
	TaskID       string
	Verified     bool // true=验证为真, false=未验证或证伪
	Refuted      bool // true=被证伪
}

// RecordAgentOutcomeInput 记录 Agent 执行结果的输入
type RecordAgentOutcomeInput struct {
	AgentRole    string
	TaskID       string
	Success      bool
	TokenCost    int
	QualityScore float64 // 0.0-1.0
	DurationMs   int64
}

// ─── 组织记忆 Store ───────────────────────────────────────

// RecordSourceUsage 记录像源使用情况（更新或创建可信度记录）
func (s *Store) RecordSourceUsage(ctx context.Context, input RecordSourceInput) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO editorial_source_credibility (source_domain, source_name, category, credibility_score, total_uses, verified_count, refuted_count, last_task_id)
		VALUES ($1, $2, $3, $4, 1, $5, $6, NULLIF($7, '')::uuid)
		ON CONFLICT (source_domain) DO UPDATE SET
			total_uses = editorial_source_credibility.total_uses + 1,
			verified_count = editorial_source_credibility.verified_count + $5,
			refuted_count = editorial_source_credibility.refuted_count + $6,
			credibility_score = CASE
				WHEN editorial_source_credibility.total_uses + 1 > 0
				THEN (editorial_source_credibility.verified_count + $5)::FLOAT / (editorial_source_credibility.total_uses + 1)
				ELSE 0.5
			END,
			last_task_id = NULLIF($7, '')::uuid,
			updated_at = NOW()
	`,
		input.SourceDomain, input.SourceName, input.Category, 0.5,
		boolToInt(input.Verified), boolToInt(input.Refuted), input.TaskID,
	)
	if err != nil {
		return fmt.Errorf("record source usage: %w", err)
	}
	return nil
}

// GetSourceCredibility 获取信源可信度
func (s *Store) GetSourceCredibility(ctx context.Context, domain string) (*SourceCredibility, error) {
	var sc SourceCredibility
	var lastTaskID sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT id::text, source_domain, source_name, category, credibility_score,
		       total_uses, verified_count, refuted_count,
		       COALESCE(last_task_id::text, ''), notes, created_at, updated_at
		FROM editorial_source_credibility WHERE source_domain = $1
	`, domain).Scan(
		&sc.ID, &sc.SourceDomain, &sc.SourceName, &sc.Category, &sc.CredibilityScore,
		&sc.TotalUses, &sc.VerifiedCount, &sc.RefutedCount,
		&lastTaskID, &sc.Notes, &sc.CreatedAt, &sc.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get source credibility: %w", err)
	}
	sc.LastTaskID = lastTaskID.String
	return &sc, nil
}

// ListSourceCredibility 列出信源可信度（按评分排序）
func (s *Store) ListSourceCredibility(ctx context.Context, limit int) ([]SourceCredibility, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id::text, source_domain, source_name, category, credibility_score,
		       total_uses, verified_count, refuted_count,
		       COALESCE(last_task_id::text, ''), notes, created_at, updated_at
		FROM editorial_source_credibility
		WHERE total_uses > 0
		ORDER BY credibility_score DESC, total_uses DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list source credibility: %w", err)
	}
	defer rows.Close()

	var results []SourceCredibility
	for rows.Next() {
		var sc SourceCredibility
		var lastTaskID sql.NullString
		if err := rows.Scan(
			&sc.ID, &sc.SourceDomain, &sc.SourceName, &sc.Category, &sc.CredibilityScore,
			&sc.TotalUses, &sc.VerifiedCount, &sc.RefutedCount,
			&lastTaskID, &sc.Notes, &sc.CreatedAt, &sc.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan source credibility: %w", err)
		}
		sc.LastTaskID = lastTaskID.String
		results = append(results, sc)
	}
	return results, nil
}

// GetColumnPreference 获取栏目偏好
func (s *Store) GetColumnPreference(ctx context.Context, columnTag string) (*ColumnPreference, error) {
	var cp ColumnPreference
	var forbiddenWords []string
	err := s.db.QueryRowContext(ctx, `
		SELECT id::text, column_tag, style_slug, preferred_length_min, preferred_length_max,
		       tone, forbidden_words, review_criteria, acceptance_rate, total_tasks, published_count,
		       created_at, updated_at
		FROM editorial_column_preferences WHERE column_tag = $1
	`, columnTag).Scan(
		&cp.ID, &cp.ColumnTag, &cp.StyleSlug, &cp.PreferredLengthMin, &cp.PreferredLengthMax,
		&cp.Tone, &forbiddenWords, &cp.ReviewCriteria, &cp.AcceptanceRate, &cp.TotalTasks, &cp.PublishedCount,
		&cp.CreatedAt, &cp.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get column preference: %w", err)
	}
	cp.ForbiddenWords = forbiddenWords
	return &cp, nil
}

// UpsertColumnPreference 创建或更新栏目偏好
func (s *Store) UpsertColumnPreference(ctx context.Context, cp ColumnPreference) (*ColumnPreference, error) {
	var result ColumnPreference
	var forbiddenWords []string
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO editorial_column_preferences (column_tag, style_slug, preferred_length_min, preferred_length_max, tone, forbidden_words, review_criteria)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (column_tag) DO UPDATE SET
			style_slug = EXCLUDED.style_slug,
			preferred_length_min = EXCLUDED.preferred_length_min,
			preferred_length_max = EXCLUDED.preferred_length_max,
			tone = EXCLUDED.tone,
			forbidden_words = EXCLUDED.forbidden_words,
			review_criteria = EXCLUDED.review_criteria,
			updated_at = NOW()
		RETURNING id::text, column_tag, style_slug, preferred_length_min, preferred_length_max,
			tone, forbidden_words, review_criteria, acceptance_rate, total_tasks, published_count,
			created_at, updated_at
	`,
		cp.ColumnTag, cp.StyleSlug, cp.PreferredLengthMin, cp.PreferredLengthMax,
		cp.Tone, cp.ForbiddenWords, cp.ReviewCriteria,
	).Scan(
		&result.ID, &result.ColumnTag, &result.StyleSlug, &result.PreferredLengthMin, &result.PreferredLengthMax,
		&result.Tone, &forbiddenWords, &result.ReviewCriteria, &result.AcceptanceRate, &result.TotalTasks, &result.PublishedCount,
		&result.CreatedAt, &result.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("upsert column preference: %w", err)
	}
	result.ForbiddenWords = forbiddenWords
	return &result, nil
}

// ListColumnPreferences 列出所有栏目偏好
func (s *Store) ListColumnPreferences(ctx context.Context) ([]ColumnPreference, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id::text, column_tag, style_slug, preferred_length_min, preferred_length_max,
		       tone, forbidden_words, review_criteria, acceptance_rate, total_tasks, published_count,
		       created_at, updated_at
		FROM editorial_column_preferences ORDER BY column_tag ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list column preferences: %w", err)
	}
	defer rows.Close()

	var results []ColumnPreference
	for rows.Next() {
		var cp ColumnPreference
		var forbiddenWords []string
		if err := rows.Scan(
			&cp.ID, &cp.ColumnTag, &cp.StyleSlug, &cp.PreferredLengthMin, &cp.PreferredLengthMax,
			&cp.Tone, &forbiddenWords, &cp.ReviewCriteria, &cp.AcceptanceRate, &cp.TotalTasks, &cp.PublishedCount,
			&cp.CreatedAt, &cp.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan column preference: %w", err)
		}
		cp.ForbiddenWords = forbiddenWords
		results = append(results, cp)
	}
	return results, nil
}

// Knowledge scope constants
const (
	KnowledgeScopeGlobal string = "global" // applies to all columns/tasks
	KnowledgeScopeColumn string = "column" // applies to a specific column_tag
	KnowledgeScopeTask   string = "task"   // applies to a specific task
)

// Knowledge source constants
const (
	KnowledgeSourceAgent  string = "agent"  // derived from agent execution
	KnowledgeSourceHuman  string = "human"  // created by human editor
	KnowledgeSourceSystem string = "system" // auto-generated by system
)

// whitespaceRe matches sequences of whitespace characters for normalization
var whitespaceRe = regexp.MustCompile(`\s+`)

// ComputeContentFingerprint computes a SHA-256 fingerprint of the normalized content.
// Normalization: lowercase, trim, collapse all whitespace to single spaces.
// This replaces the old title + column_tag dedup strategy with a content-based fingerprint.
func ComputeContentFingerprint(title, content string) string {
	normalized := strings.ToLower(strings.TrimSpace(title)) + "|" + strings.ToLower(strings.TrimSpace(content))
	normalized = whitespaceRe.ReplaceAllString(normalized, " ")
	hash := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(hash[:])
}

// CreateKnowledge 创建编辑部知识 — 使用内容指纹去重
// 如果相同指纹的知识已存在，递增 occurrence_count 并更新 confidence（取较高值）
// Agent 来源的知识默认状态为 candidate，人类来源默认为 active
func (s *Store) CreateKnowledge(ctx context.Context, k EditorialKnowledge) (*EditorialKnowledge, error) {
	// Compute fingerprint if not provided
	if k.ContentFingerprint == "" {
		k.ContentFingerprint = ComputeContentFingerprint(k.Title, k.Content)
	}

	// Default scope
	if k.Scope == "" {
		if k.ColumnTag != "" {
			k.Scope = KnowledgeScopeColumn
		} else {
			k.Scope = KnowledgeScopeGlobal
		}
	}

	// Default source
	if k.Source == "" {
		k.Source = KnowledgeSourceAgent
	}

	// Default status: agent-sourced starts as candidate, human/system starts as active
	if k.Status == "" {
		if k.Source == KnowledgeSourceAgent {
			k.Status = KnowledgeStatusCandidate
		} else {
			k.Status = KnowledgeStatusActive
		}
	}

	var result EditorialKnowledge
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO editorial_knowledge (category, column_tag, title, content, content_fingerprint, scope, source, source_task_id, source_artifact_id, confidence, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, '')::uuid, NULLIF($9, '')::uuid, $10, $11)
		ON CONFLICT (content_fingerprint) WHERE content_fingerprint IS NOT NULL
		DO UPDATE SET
			occurrence_count = editorial_knowledge.occurrence_count + 1,
			confidence = GREATEST(editorial_knowledge.confidence, EXCLUDED.confidence),
			updated_at = NOW()
		RETURNING id::text, category, column_tag, title, content,
			COALESCE(content_fingerprint, ''), COALESCE(scope, 'global'), COALESCE(source, 'agent'),
			COALESCE(source_task_id::text, ''), COALESCE(source_artifact_id::text, ''),
			confidence, occurrence_count, status,
			COALESCE(superseded_by::text, ''), COALESCE(archived_reason, ''),
			archived_at, created_at, updated_at
	`,
		k.Category, k.ColumnTag, k.Title, k.Content, k.ContentFingerprint, k.Scope, k.Source,
		k.SourceTaskID, k.SourceArtifactID, k.Confidence, k.Status,
	).Scan(
		&result.ID, &result.Category, &result.ColumnTag, &result.Title, &result.Content,
		&result.ContentFingerprint, &result.Scope, &result.Source,
		&result.SourceTaskID, &result.SourceArtifactID,
		&result.Confidence, &result.OccurrenceCount, &result.Status,
		&result.SupersededBy, &result.ArchivedReason,
		&result.ArchivedAt, &result.CreatedAt, &result.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create knowledge: %w", err)
	}
	return &result, nil
}

// ListKnowledge 列出编辑部知识
// statusFilter: "" = active only (default), "all" = all statuses, specific status = filter by that status
func (s *Store) ListKnowledge(ctx context.Context, category, columnTag, statusFilter string, limit int) ([]EditorialKnowledge, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query := `
		SELECT id::text, category, column_tag, title, content,
		       COALESCE(content_fingerprint, ''), COALESCE(scope, 'global'), COALESCE(source, 'agent'),
		       COALESCE(source_task_id::text, ''), COALESCE(source_artifact_id::text, ''),
		       confidence, occurrence_count, status,
		       COALESCE(superseded_by::text, ''), COALESCE(archived_reason, ''),
		       archived_at, created_at, updated_at
		FROM editorial_knowledge
	`
	args := []interface{}{}
	argIdx := 1

	// Status filter
	if statusFilter == "" || statusFilter == "active" {
		query += fmt.Sprintf(" WHERE status = '%s'", KnowledgeStatusActive)
	} else if statusFilter != "all" {
		query += fmt.Sprintf(" WHERE status = $%d", argIdx)
		args = append(args, statusFilter)
		argIdx++
	}

	if category != "" {
		if argIdx == 1 {
			query += " WHERE"
		} else {
			query += " AND"
		}
		query += fmt.Sprintf(" category = $%d", argIdx)
		args = append(args, category)
		argIdx++
	}
	if columnTag != "" {
		if argIdx == 1 {
			query += " WHERE"
		} else {
			query += " AND"
		}
		query += fmt.Sprintf(" (column_tag = $%d OR column_tag = '' OR scope = 'global')", argIdx)
		args = append(args, columnTag)
		argIdx++
	}
	query += fmt.Sprintf(" ORDER BY occurrence_count DESC, created_at DESC LIMIT $%d", argIdx)
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list knowledge: %w", err)
	}
	defer rows.Close()

	var results []EditorialKnowledge
	for rows.Next() {
		var k EditorialKnowledge
		if err := rows.Scan(
			&k.ID, &k.Category, &k.ColumnTag, &k.Title, &k.Content,
			&k.ContentFingerprint, &k.Scope, &k.Source,
			&k.SourceTaskID, &k.SourceArtifactID,
			&k.Confidence, &k.OccurrenceCount, &k.Status,
			&k.SupersededBy, &k.ArchivedReason,
			&k.ArchivedAt, &k.CreatedAt, &k.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan knowledge: %w", err)
		}
		results = append(results, k)
	}
	return results, nil
}

// GetKnowledge 获取单条编辑部知识
func (s *Store) GetKnowledge(ctx context.Context, id string) (*EditorialKnowledge, error) {
	var k EditorialKnowledge
	err := s.db.QueryRowContext(ctx, `
		SELECT id::text, category, column_tag, title, content,
		       COALESCE(content_fingerprint, ''), COALESCE(scope, 'global'), COALESCE(source, 'agent'),
		       COALESCE(source_task_id::text, ''), COALESCE(source_artifact_id::text, ''),
		       confidence, occurrence_count, status,
		       COALESCE(superseded_by::text, ''), COALESCE(archived_reason, ''),
		       archived_at, created_at, updated_at
		FROM editorial_knowledge WHERE id = $1::uuid
	`, id).Scan(
		&k.ID, &k.Category, &k.ColumnTag, &k.Title, &k.Content,
		&k.ContentFingerprint, &k.Scope, &k.Source,
		&k.SourceTaskID, &k.SourceArtifactID,
		&k.Confidence, &k.OccurrenceCount, &k.Status,
		&k.SupersededBy, &k.ArchivedReason,
		&k.ArchivedAt, &k.CreatedAt, &k.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get knowledge: %w", err)
	}
	return &k, nil
}

// PromoteKnowledge 将候选知识提升为活跃（candidate → active）
// 同时提升置信度
func (s *Store) PromoteKnowledge(ctx context.Context, id string) (*EditorialKnowledge, error) {
	// First read current state
	current, err := s.GetKnowledge(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get knowledge for promote: %w", err)
	}
	if current == nil {
		return nil, fmt.Errorf("knowledge not found")
	}
	if !knowledgeCanTransitionTo(current.Status, KnowledgeStatusActive) {
		return nil, fmt.Errorf("cannot promote knowledge from %s to %s", current.Status, KnowledgeStatusActive)
	}

	// Promote: set status to active and bump confidence (cap at 1.0)
	newConfidence := current.Confidence + 0.15
	if newConfidence > 1.0 {
		newConfidence = 1.0
	}

	_, err = s.db.ExecContext(ctx, `
		UPDATE editorial_knowledge
		SET status = $2, confidence = $3, updated_at = NOW()
		WHERE id = $1::uuid
	`, id, KnowledgeStatusActive, newConfidence)
	if err != nil {
		return nil, fmt.Errorf("promote knowledge: %w", err)
	}

	return s.GetKnowledge(ctx, id)
}

// ArchiveKnowledge 将活跃知识归档（active → archived）
func (s *Store) ArchiveKnowledge(ctx context.Context, id, reason string) (*EditorialKnowledge, error) {
	current, err := s.GetKnowledge(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get knowledge for archive: %w", err)
	}
	if current == nil {
		return nil, fmt.Errorf("knowledge not found")
	}
	if !knowledgeCanTransitionTo(current.Status, KnowledgeStatusArchived) {
		return nil, fmt.Errorf("cannot archive knowledge from %s", current.Status)
	}

	_, err = s.db.ExecContext(ctx, `
		UPDATE editorial_knowledge
		SET status = $2, archived_reason = $3, archived_at = NOW(), updated_at = NOW()
		WHERE id = $1::uuid
	`, id, KnowledgeStatusArchived, reason)
	if err != nil {
		return nil, fmt.Errorf("archive knowledge: %w", err)
	}

	return s.GetKnowledge(ctx, id)
}

// SupersedeKnowledge 将旧知识标记为已被新知识替代（active → superseded）
func (s *Store) SupersedeKnowledge(ctx context.Context, oldID, newID string) error {
	current, err := s.GetKnowledge(ctx, oldID)
	if err != nil {
		return fmt.Errorf("get knowledge for supersede: %w", err)
	}
	if current == nil {
		return fmt.Errorf("knowledge not found")
	}
	if !knowledgeCanTransitionTo(current.Status, KnowledgeStatusSuperseded) {
		return fmt.Errorf("cannot supersede knowledge from %s", current.Status)
	}

	_, err = s.db.ExecContext(ctx, `
		UPDATE editorial_knowledge
		SET status = $2, superseded_by = NULLIF($3, '')::uuid, updated_at = NOW()
		WHERE id = $1::uuid
	`, oldID, KnowledgeStatusSuperseded, newID)
	if err != nil {
		return fmt.Errorf("supersede knowledge: %w", err)
	}
	return nil
}

// ─── Agent 信誉 Store ─────────────────────────────────────

// RecordAgentOutcome 记录 Agent 执行结果
func (s *Store) RecordAgentOutcome(ctx context.Context, input RecordAgentOutcomeInput) error {
	successInc := boolToInt(input.Success)
	failureInc := boolToInt(!input.Success)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO editorial_agent_reputation (agent_role, total_executions, success_count, failure_count, avg_token_cost, avg_quality_score, avg_duration_ms, last_task_id, last_execution_at)
		VALUES ($1, 1, $2, $3, $4, $5, $6, NULLIF($7, '')::uuid, NOW())
		ON CONFLICT (agent_role) DO UPDATE SET
			total_executions = editorial_agent_reputation.total_executions + 1,
			success_count = editorial_agent_reputation.success_count + $2,
			failure_count = editorial_agent_reputation.failure_count + $3,
			avg_token_cost = (editorial_agent_reputation.avg_token_cost * editorial_agent_reputation.total_executions + $4) / (editorial_agent_reputation.total_executions + 1),
			avg_quality_score = (editorial_agent_reputation.avg_quality_score * editorial_agent_reputation.total_executions + $5) / (editorial_agent_reputation.total_executions + 1),
			avg_duration_ms = (editorial_agent_reputation.avg_duration_ms * editorial_agent_reputation.total_executions + $6) / (editorial_agent_reputation.total_executions + 1),
			last_task_id = NULLIF($7, '')::uuid,
			last_execution_at = NOW(),
			updated_at = NOW()
	`,
		input.AgentRole, successInc, failureInc, input.TokenCost, input.QualityScore, input.DurationMs, input.TaskID,
	)
	if err != nil {
		return fmt.Errorf("record agent outcome: %w", err)
	}
	return nil
}

// GetAgentReputation 获取 Agent 信誉
func (s *Store) GetAgentReputation(ctx context.Context, role string) (*AgentReputation, error) {
	var ar AgentReputation
	var lastTaskID sql.NullString
	var lastExecAt sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT id::text, agent_role, total_executions, success_count, failure_count,
		       avg_token_cost, avg_quality_score, avg_duration_ms,
		       COALESCE(last_task_id::text, ''), last_execution_at, created_at, updated_at
		FROM editorial_agent_reputation WHERE agent_role = $1
	`, role).Scan(
		&ar.ID, &ar.AgentRole, &ar.TotalExecutions, &ar.SuccessCount, &ar.FailureCount,
		&ar.AvgTokenCost, &ar.AvgQualityScore, &ar.AvgDurationMs,
		&lastTaskID, &lastExecAt, &ar.CreatedAt, &ar.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get agent reputation: %w", err)
	}
	ar.LastTaskID = lastTaskID.String
	if lastExecAt.Valid {
		ar.LastExecutionAt = &lastExecAt.Time
	}
	return &ar, nil
}

// ListAgentReputation 列出所有 Agent 信誉
func (s *Store) ListAgentReputation(ctx context.Context) ([]AgentReputation, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id::text, agent_role, total_executions, success_count, failure_count,
		       avg_token_cost, avg_quality_score, avg_duration_ms,
		       COALESCE(last_task_id::text, ''), last_execution_at, created_at, updated_at
		FROM editorial_agent_reputation ORDER BY agent_role ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list agent reputation: %w", err)
	}
	defer rows.Close()

	var results []AgentReputation
	for rows.Next() {
		var ar AgentReputation
		var lastTaskID sql.NullString
		var lastExecAt sql.NullTime
		if err := rows.Scan(
			&ar.ID, &ar.AgentRole, &ar.TotalExecutions, &ar.SuccessCount, &ar.FailureCount,
			&ar.AvgTokenCost, &ar.AvgQualityScore, &ar.AvgDurationMs,
			&lastTaskID, &lastExecAt, &ar.CreatedAt, &ar.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan agent reputation: %w", err)
		}
		ar.LastTaskID = lastTaskID.String
		if lastExecAt.Valid {
			ar.LastExecutionAt = &lastExecAt.Time
		}
		results = append(results, ar)
	}
	return results, nil
}

// ─── 信源信誉适配器 ─────────────────────────────────────────

// CredibilityLookupAdapter adapts editorial.Store to engine.CredibilityLookup.
// This allows the search client to enrich search results with source credibility
// scores from the editorial_source_credibility table.
type CredibilityLookupAdapter struct {
	store *Store
}

// NewCredibilityLookupAdapter creates a new adapter wrapping the editorial Store.
func NewCredibilityLookupAdapter(store *Store) *CredibilityLookupAdapter {
	return &CredibilityLookupAdapter{store: store}
}

// GetCredibility returns the credibility score (0.0-1.0) for a given domain.
// Returns 0.5 (neutral) if the domain is not in the database.
func (a *CredibilityLookupAdapter) GetCredibility(ctx context.Context, domain string) (float64, error) {
	if domain == "" {
		return 0.5, nil
	}
	sc, err := a.store.GetSourceCredibility(ctx, domain)
	if err != nil {
		return 0.5, nil // don't fail search on credibility lookup error
	}
	if sc == nil {
		return 0.5, nil // unknown domain → neutral
	}
	return sc.CredibilityScore, nil
}

// ─── 辅助 ─────────────────────────────────────────────────

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

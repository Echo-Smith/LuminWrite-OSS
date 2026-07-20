package database

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// AdminRepo handles admin-specific database operations.
type AdminRepo struct {
	db     *DB
	encKey []byte // AES-256 key for API key encryption (optional)
}

// NewAdminRepo creates a new AdminRepo.
func NewAdminRepo(db *DB) *AdminRepo {
	return &AdminRepo{db: db}
}

// WithEncryptionKey sets the AES-256 encryption key for API key storage.
func (r *AdminRepo) WithEncryptionKey(key []byte) *AdminRepo {
	r.encKey = key
	return r
}

// DB returns the underlying database connection for direct queries.
func (r *AdminRepo) DB() *DB {
	return r.db
}

// ─── Dashboard Statistics ────────────────────────────────

// DashboardStats holds the overview statistics for the admin dashboard.
type DashboardStats struct {
	TodayWrites      int            `json:"today_writes"`
	TodayTokens      int            `json:"today_tokens"`
	ActiveUsers      int            `json:"active_users"`
	EvalAvgScore     float64        `json:"eval_avg_score"`
	TotalWrites      int            `json:"total_writes"`
	TotalTokens      int            `json:"total_tokens"`
	StyleDistribution []StyleUsage  `json:"style_distribution"`
	RecentTraces     []TraceSummary `json:"recent_traces"`
	WeeklyWrites     []DailyCount   `json:"weekly_writes"`
	WeeklyTokens     []DailyCount   `json:"weekly_tokens"`
}

// StyleUsage represents the usage count per style.
type StyleUsage struct {
	StyleSlug string `json:"style_slug"`
	Count     int    `json:"count"`
	Percent   float64 `json:"percent"`
}

// DailyCount represents a daily aggregated count.
type DailyCount struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
}

// TraceSummary is a compact trace representation for list views.
type TraceSummary struct {
	TraceID     string                 `json:"trace_id"`
	Status      string                 `json:"status"`
	CurrentStep string                 `json:"current_step"`
	UserInput   string                 `json:"user_input"`
	StyleSlug   string                 `json:"style_slug"`
	Mode        string                 `json:"mode"`
	Article     string                 `json:"article,omitempty"`
	ReviewScore *float64               `json:"review_score,omitempty"`
	TokenUsage  map[string]interface{} `json:"token_usage,omitempty"`
	DurationMs  *int64                 `json:"duration_ms,omitempty"`
	CreatedAt   time.Time              `json:"created_at"`
	CompletedAt *time.Time             `json:"completed_at,omitempty"`
}

// GetDashboardStats retrieves overview statistics.
func (r *AdminRepo) GetDashboardStats(ctx context.Context) (*DashboardStats, error) {
	if r.db == nil {
		return &DashboardStats{}, nil
	}

	stats := &DashboardStats{}

	// Today's writes
	r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM agent_traces WHERE created_at >= CURRENT_DATE
	`).Scan(&stats.TodayWrites)

	// Today's tokens
	r.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM((token_usage->>'total_tokens')::int), 0)
		FROM agent_traces WHERE created_at >= CURRENT_DATE
	`).Scan(&stats.TodayTokens)

	// Active users (distinct users in last 24h)
	r.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT user_id) FROM agent_traces
		WHERE created_at >= NOW() - INTERVAL '24 hours' AND user_id IS NOT NULL
	`).Scan(&stats.ActiveUsers)

	// Average evaluation score
	r.db.QueryRowContext(ctx, `
		SELECT COALESCE(AVG(overall_score), 0) FROM evaluation_runs WHERE status = 'completed'
	`).Scan(&stats.EvalAvgScore)

	// Total writes
	r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_traces`).Scan(&stats.TotalWrites)

	// Total tokens
	r.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM((token_usage->>'total_tokens')::int), 0) FROM agent_traces
	`).Scan(&stats.TotalTokens)

	// Style distribution
	rows, err := r.db.QueryContext(ctx, `
		SELECT COALESCE(style_slug, 'unknown') as slug, COUNT(*) as cnt
		FROM agent_traces
		WHERE status = 'completed'
		GROUP BY style_slug
		ORDER BY cnt DESC
	`)
	if err == nil {
		defer rows.Close()
		var totalStyleWrites int
		var styleUsages []StyleUsage
		for rows.Next() {
			var su StyleUsage
			if err := rows.Scan(&su.StyleSlug, &su.Count); err != nil {
				continue
			}
			totalStyleWrites += su.Count
			styleUsages = append(styleUsages, su)
		}
		for i := range styleUsages {
			if totalStyleWrites > 0 {
				styleUsages[i].Percent = float64(styleUsages[i].Count) / float64(totalStyleWrites) * 100
			}
		}
		stats.StyleDistribution = styleUsages
	}

	// Weekly writes (last 7 days)
	weeklyRows, err := r.db.QueryContext(ctx, `
		SELECT DATE(created_at) as d, COUNT(*) as cnt
		FROM agent_traces
		WHERE created_at >= CURRENT_DATE - INTERVAL '6 days'
		GROUP BY d ORDER BY d
	`)
	if err == nil {
		defer weeklyRows.Close()
		for weeklyRows.Next() {
			var dc DailyCount
			var d time.Time
			if err := weeklyRows.Scan(&d, &dc.Count); err != nil {
				continue
			}
			dc.Date = d.Format("2006-01-02")
			stats.WeeklyWrites = append(stats.WeeklyWrites, dc)
		}
	}

	// Weekly tokens (last 7 days)
	weeklyTokenRows, err := r.db.QueryContext(ctx, `
		SELECT DATE(created_at) as d, COALESCE(SUM((token_usage->>'total_tokens')::int), 0) as cnt
		FROM agent_traces
		WHERE created_at >= CURRENT_DATE - INTERVAL '6 days'
		GROUP BY d ORDER BY d
	`)
	if err == nil {
		defer weeklyTokenRows.Close()
		for weeklyTokenRows.Next() {
			var dc DailyCount
			var d time.Time
			if err := weeklyTokenRows.Scan(&d, &dc.Count); err != nil {
				continue
			}
			dc.Date = d.Format("2006-01-02")
			stats.WeeklyTokens = append(stats.WeeklyTokens, dc)
		}
	}

	// Recent traces (latest 10)
	recentTraces, _, _ := r.ListTraces(ctx, "", "", 1, 10)
	stats.RecentTraces = recentTraces

	return stats, nil
}

// ─── Trace Listing (Admin) ───────────────────────────────

// ListTraces lists traces with optional status and style filters.
func (r *AdminRepo) ListTraces(ctx context.Context, status, styleSlug string, page, pageSize int) ([]TraceSummary, int, error) {
	if r.db == nil {
		return []TraceSummary{}, 0, nil
	}

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	query := `
		SELECT trace_id, status, current_step, user_input, style_slug, mode,
		       article, review_result, token_usage, duration_ms, created_at, completed_at
		FROM agent_traces
	`
	args := []interface{}{}
	argIdx := 1
	conditions := []string{}

	if status != "" {
		conditions = append(conditions, fmt.Sprintf("status = $%d", argIdx))
		args = append(args, status)
		argIdx++
	}
	if styleSlug != "" {
		conditions = append(conditions, fmt.Sprintf("style_slug = $%d", argIdx))
		args = append(args, styleSlug)
		argIdx++
	}

	if len(conditions) > 0 {
		query += " WHERE " + joinStrings(conditions, " AND ")
	}

	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", argIdx, argIdx+1)
	args = append(args, pageSize, offset)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var traces []TraceSummary
	for rows.Next() {
		var t TraceSummary
		var (
			article     *string
			reviewJSON  []byte
			tokenJSON   []byte
			durationMs  *int64
			completedAt *time.Time
		)

		if err := rows.Scan(&t.TraceID, &t.Status, &t.CurrentStep, &t.UserInput, &t.StyleSlug, &t.Mode,
			&article, &reviewJSON, &tokenJSON, &durationMs, &t.CreatedAt, &completedAt); err != nil {
			continue
		}

		if article != nil {
			t.Article = *article
		}
		if durationMs != nil {
			t.DurationMs = durationMs
		}
		if completedAt != nil {
			t.CompletedAt = completedAt
		}

		// Extract review score
		if len(reviewJSON) > 0 {
			var review map[string]interface{}
			if json.Unmarshal(reviewJSON, &review) == nil {
				if scores, ok := review["scores"].(map[string]interface{}); ok {
					// Calculate average score
					total := 0.0
					count := 0
					for _, v := range scores {
						if f, ok := v.(float64); ok {
							total += f
							count++
						}
					}
					if count > 0 {
						avg := total / float64(count)
						t.ReviewScore = &avg
					}
				}
			}
		}

		// Parse token usage
		if len(tokenJSON) > 0 {
			var tokens map[string]interface{}
			if json.Unmarshal(tokenJSON, &tokens) == nil {
				t.TokenUsage = tokens
			}
		}

		traces = append(traces, t)
	}

	// Get total count
	var total int
	countQuery := "SELECT COUNT(*) FROM agent_traces"
	countArgs := []interface{}{}
	if status != "" {
		countQuery += " WHERE status = $1"
		countArgs = append(countArgs, status)
		if styleSlug != "" {
			countQuery += " AND style_slug = $2"
			countArgs = append(countArgs, styleSlug)
		}
	} else if styleSlug != "" {
		countQuery += " WHERE style_slug = $1"
		countArgs = append(countArgs, styleSlug)
	}
	r.db.QueryRowContext(ctx, countQuery, countArgs...).Scan(&total)

	return traces, total, nil
}

// GetTraceDetail retrieves full trace detail including article, review, and step history.
func (r *AdminRepo) GetTraceDetail(ctx context.Context, traceID string) (map[string]interface{}, error) {
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
		stepHistory []byte
		reviewJSON  []byte
		tokenJSON   []byte
		durationMs  *int64
		errorMsg    *string
		createdAt   time.Time
		completedAt *time.Time
		reasoningContent *string
	)

	err := r.db.QueryRowContext(ctx, `
		SELECT status, current_step, user_input, style_slug, mode,
		       article, step_history, review_result, token_usage,
		       duration_ms, error, created_at, completed_at, reasoning_content
		FROM agent_traces WHERE trace_id = $1
	`, traceID).Scan(
		&status, &currentStep, &userInput, &styleSlug, &mode,
		&article, &stepHistory, &reviewJSON, &tokenJSON,
		&durationMs, &errorMsg, &createdAt, &completedAt, &reasoningContent,
	)
	if err != nil {
		return nil, err
	}

	result := map[string]interface{}{
		"trace_id":     traceID,
		"status":       status,
		"current_step": currentStep,
		"user_input":   userInput,
		"mode":         mode,
		"created_at":   createdAt,
	}

	if styleSlug != nil {
		result["style_slug"] = *styleSlug
	}
	if article != nil {
		result["article"] = *article
	}
	if durationMs != nil {
		result["duration_ms"] = *durationMs
	}
	if completedAt != nil {
		result["completed_at"] = *completedAt
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

	return result, nil
}

// ─── Style Profile DB Operations ─────────────────────────

// StyleProfileRecord represents a style profile row in the database.
type StyleProfileRecord struct {
	ID             string                 `json:"id"`
	Slug           string                 `json:"slug"`
	Name           string                 `json:"name"`
	Description    string                 `json:"description"`
	Version        int                    `json:"version"`
	Status         string                 `json:"status"`
	Config         map[string]interface{} `json:"config"`
	RolloutType    string                 `json:"rollout_type"`
	WhitelistUIDs  []string               `json:"whitelist_uids"`
	RolloutPercent int                    `json:"rollout_percent"`
	PublishedAt    *time.Time             `json:"published_at,omitempty"`
	PublishedBy    *string                `json:"published_by,omitempty"`
	CreatedAt      time.Time              `json:"created_at"`
	UpdatedAt      time.Time              `json:"updated_at"`
}

// SaveProfile inserts or updates a style profile in the database.
func (r *AdminRepo) SaveProfile(ctx context.Context, rec *StyleProfileRecord) error {
	if r.db == nil {
		return nil
	}

	configJSON, _ := json.Marshal(rec.Config)

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO style_profiles (id, slug, name, description, version, status, config,
			rollout_type, whitelist_uids, rollout_percent, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW(), NOW())
		ON CONFLICT (slug) DO UPDATE SET
			name = EXCLUDED.name,
			description = EXCLUDED.description,
			config = EXCLUDED.config,
			rollout_type = EXCLUDED.rollout_type,
			whitelist_uids = EXCLUDED.whitelist_uids,
			rollout_percent = EXCLUDED.rollout_percent,
			updated_at = NOW()
	`,
		uuid.New().String(), rec.Slug, rec.Name, rec.Description, rec.Version, rec.Status,
		string(configJSON), rec.RolloutType, pq.Array(rec.WhitelistUIDs), rec.RolloutPercent,
	)
	return err
}

// ListProfiles lists all style profiles from the database (including drafts).
func (r *AdminRepo) ListProfiles(ctx context.Context) ([]*StyleProfileRecord, error) {
	if r.db == nil {
		return []*StyleProfileRecord{}, nil
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT id::text, slug, name, description, version, status, config,
		       rollout_type, whitelist_uids, rollout_percent,
		       published_at, published_by, created_at, updated_at
		FROM style_profiles
		ORDER BY updated_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var profiles []*StyleProfileRecord
	for rows.Next() {
		var p StyleProfileRecord
		var (
			configJSON    []byte
			whitelistUIDs []string
			publishedBy   *string
		)
		if err := rows.Scan(&p.ID, &p.Slug, &p.Name, &p.Description, &p.Version, &p.Status,
			&configJSON, &p.RolloutType, pq.Array(&whitelistUIDs), &p.RolloutPercent,
			&p.PublishedAt, &publishedBy, &p.CreatedAt, &p.UpdatedAt); err != nil {
			continue
		}
		if len(configJSON) > 0 {
			json.Unmarshal(configJSON, &p.Config)
		}
		p.WhitelistUIDs = whitelistUIDs
		p.PublishedBy = publishedBy
		profiles = append(profiles, &p)
	}

	return profiles, nil
}

// GetProfile retrieves a single style profile by slug.
func (r *AdminRepo) GetProfile(ctx context.Context, slug string) (*StyleProfileRecord, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	var p StyleProfileRecord
	var (
		configJSON    []byte
		whitelistUIDs []string
		publishedBy   *string
	)

	err := r.db.QueryRowContext(ctx, `
		SELECT id::text, slug, name, description, version, status, config,
		       rollout_type, whitelist_uids, rollout_percent,
		       published_at, published_by, created_at, updated_at
		FROM style_profiles WHERE slug = $1
	`, slug).Scan(
		&p.ID, &p.Slug, &p.Name, &p.Description, &p.Version, &p.Status,
		&configJSON, &p.RolloutType, pq.Array(&whitelistUIDs), &p.RolloutPercent,
		&p.PublishedAt, &publishedBy, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	if len(configJSON) > 0 {
		json.Unmarshal(configJSON, &p.Config)
	}
	p.WhitelistUIDs = whitelistUIDs
	p.PublishedBy = publishedBy

	return &p, nil
}

// PublishProfile marks a profile as published and creates a version record.
func (r *AdminRepo) PublishProfile(ctx context.Context, slug string, version int, configJSON string, changelog string, publishedBy string) error {
	if r.db == nil {
		return nil
	}

	// Archive previous published version
	_, err := r.db.ExecContext(ctx, `
		UPDATE style_profiles SET status = 'archived' WHERE slug = $1 AND status = 'published'
	`, slug)
	if err != nil {
		return err
	}

	// Publish new version
	_, err = r.db.ExecContext(ctx, `
		UPDATE style_profiles
		SET status = 'published', version = $2, published_at = NOW(), published_by = $3, updated_at = NOW()
		WHERE slug = $1
	`, slug, version, publishedBy)
	if err != nil {
		return err
	}

	// Create version record
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO profile_versions (id, profile_slug, version, config, changelog, status, published_at, created_at, created_by)
		VALUES ($1, $2, $3, $4, $5, 'published', NOW(), NOW(), $6)
		ON CONFLICT (profile_slug, version) DO UPDATE SET
			config = EXCLUDED.config,
			changelog = EXCLUDED.changelog,
			status = 'published',
			published_at = NOW()
	`,
		uuid.New().String(), slug, version, configJSON, changelog, publishedBy,
	)
	return err
}

// ArchiveProfile marks a profile as archived.
func (r *AdminRepo) ArchiveProfile(ctx context.Context, slug string) error {
	if r.db == nil {
		return nil
	}

	_, err := r.db.ExecContext(ctx, `
		UPDATE style_profiles SET status = 'archived', updated_at = NOW() WHERE slug = $1
	`, slug)
	return err
}

// ListProfileVersions retrieves version history for a profile.
func (r *AdminRepo) ListProfileVersions(ctx context.Context, slug string) ([]map[string]interface{}, error) {
	if r.db == nil {
		return []map[string]interface{}{}, nil
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT id::text, profile_slug, version, config, changelog, status,
		       published_at, created_at, created_by
		FROM profile_versions
		WHERE profile_slug = $1
		ORDER BY version DESC
	`, slug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var versions []map[string]interface{}
	for rows.Next() {
		var (
			id          string
			profileSlug string
			version     int
			configJSON  []byte
			changelog   *string
			status      string
			publishedAt *time.Time
			createdAt   time.Time
			createdBy   *string
		)
		if err := rows.Scan(&id, &profileSlug, &version, &configJSON, &changelog, &status,
			&publishedAt, &createdAt, &createdBy); err != nil {
			continue
		}

		v := map[string]interface{}{
			"id":           id,
			"profile_slug": profileSlug,
			"version":      version,
			"status":       status,
			"created_at":   createdAt,
		}
		if changelog != nil {
			v["changelog"] = *changelog
		}
		if publishedAt != nil {
			v["published_at"] = *publishedAt
		}
		if createdBy != nil {
			v["created_by"] = *createdBy
		}

		versions = append(versions, v)
	}

	return versions, nil
}

// GetProfileVersion retrieves a specific version's full config for a profile.
func (r *AdminRepo) GetProfileVersion(ctx context.Context, slug string, version int) (map[string]interface{}, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	var (
		id          string
		configJSON  []byte
		changelog   *string
		status      string
		publishedAt *time.Time
		createdAt   time.Time
		createdBy   *string
	)

	err := r.db.QueryRowContext(ctx, `
		SELECT id::text, config, changelog, status, published_at, created_at, created_by
		FROM profile_versions
		WHERE profile_slug = $1 AND version = $2
	`, slug, version).Scan(&id, &configJSON, &changelog, &status, &publishedAt, &createdAt, &createdBy)
	if err != nil {
		return nil, err
	}

	result := map[string]interface{}{
		"id":         id,
		"slug":       slug,
		"version":    version,
		"status":     status,
		"created_at": createdAt,
	}

	var config map[string]interface{}
	if err := json.Unmarshal(configJSON, &config); err == nil {
		result["config"] = config
	} else {
		result["config"] = map[string]interface{}{}
	}

	if changelog != nil {
		result["changelog"] = *changelog
	}
	if publishedAt != nil {
		result["published_at"] = *publishedAt
	}
	if createdBy != nil {
		result["created_by"] = *createdBy
	}

	return result, nil
}

// ─── Sensitive Words (Placeholder) ───────────────────────

// SensitiveWord represents a sensitive word entry.
type SensitiveWord struct {
	ID          string  `json:"id"`
	Word        string  `json:"word"`
	Category    string  `json:"category"`
	Severity    string  `json:"severity"`
	Action      string  `json:"action"`
	Replacement *string `json:"replacement,omitempty"`
	IsActive    bool    `json:"is_active"`
	CreatedAt   time.Time `json:"created_at"`
}

// ListSensitiveWords lists all sensitive words (placeholder — returns empty when table doesn't exist).
func (r *AdminRepo) ListSensitiveWords(ctx context.Context, category string) ([]*SensitiveWord, int, error) {
	if r.db == nil {
		return []*SensitiveWord{}, 0, nil
	}

	query := `SELECT id::text, word, category, severity, action, replacement, is_active, created_at FROM sensitive_words`
	args := []interface{}{}
	if category != "" {
		query += " WHERE category = $1"
		args = append(args, category)
	}
	query += " ORDER BY created_at DESC LIMIT 200"

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		// Table might not exist yet — return empty
		return []*SensitiveWord{}, 0, nil
	}
	defer rows.Close()

	var words []*SensitiveWord
	for rows.Next() {
		var w SensitiveWord
		if err := rows.Scan(&w.ID, &w.Word, &w.Category, &w.Severity, &w.Action,
			&w.Replacement, &w.IsActive, &w.CreatedAt); err != nil {
			continue
		}
		words = append(words, &w)
	}

	return words, len(words), nil
}

// AddSensitiveWord adds a sensitive word entry.
func (r *AdminRepo) AddSensitiveWord(ctx context.Context, word, category, severity, action string, replacement *string) (*SensitiveWord, error) {
	if r.db == nil {
		return &SensitiveWord{
			ID:        "placeholder",
			Word:      word,
			Category:  category,
			Severity:  severity,
			Action:    action,
			IsActive:  true,
			CreatedAt: time.Now(),
		}, nil
	}

	var w SensitiveWord
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO sensitive_words (id, word, category, severity, action, replacement, is_active, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, TRUE, NOW())
		RETURNING id::text, word, category, severity, action, replacement, is_active, created_at
	`, uuid.New().String(), word, category, severity, action, replacement).Scan(
		&w.ID, &w.Word, &w.Category, &w.Severity, &w.Action,
		&w.Replacement, &w.IsActive, &w.CreatedAt,
	)
	if err != nil {
		// Table might not exist — return placeholder
		return &SensitiveWord{
			ID:        "placeholder",
			Word:      word,
			Category:  category,
			Severity:  severity,
			Action:    action,
			IsActive:  true,
			CreatedAt: time.Now(),
		}, nil
	}
	return &w, nil
}

// DeleteSensitiveWord deletes a sensitive word.
func (r *AdminRepo) DeleteSensitiveWord(ctx context.Context, id string) error {
	if r.db == nil {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `DELETE FROM sensitive_words WHERE id = $1`, id)
	return err
}

// ─── Helpers ─────────────────────────────────────────────

func joinStrings(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	result := strs[0]
	for i := 1; i < len(strs); i++ {
		result += sep + strs[i]
	}
	return result
}

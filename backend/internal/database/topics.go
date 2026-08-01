package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// ─── Topic Detail ────────────────────────────────────────

// GetTopicByID retrieves a single topic by ID, including raw_data.
func (r *TraceRepo) GetTopicByID(ctx context.Context, id string) (map[string]interface{}, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	var (
		titleID     string
		title       string
		description *string
		source      string
		platform    *string
		hotRank     *int
		rawData     []byte
		fetchedAt   *time.Time
		createdAt   time.Time
		status      string
	)

	err := r.db.QueryRowContext(ctx, `
		SELECT id::text, title, description, source, platform, hot_rank, raw_data, fetched_at, created_at, status
		FROM topics WHERE id = $1
	`, id).Scan(&titleID, &title, &description, &source, &platform, &hotRank, &rawData, &fetchedAt, &createdAt, &status)
	if err != nil {
		return nil, err
	}

	topic := map[string]interface{}{
		"id":         titleID,
		"title":      title,
		"source":     source,
		"created_at": createdAt,
		"status":     status,
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
	if rawData != nil {
		var raw interface{}
		if err := json.Unmarshal(rawData, &raw); err == nil {
			topic["raw_data"] = raw
		}
	}

	return topic, nil
}

// ListRelatedTraces retrieves completed writing traces related to a topic title.
// If userID is non-empty, results are filtered to that user only.
// If topicTitle is empty, returns recent completed traces (for recommendations).
func (r *TraceRepo) ListRelatedTraces(ctx context.Context, topicTitle string, limit int) ([]map[string]interface{}, error) {
	if r.db == nil {
		return []map[string]interface{}{}, nil
	}
	if limit <= 0 || limit > 20 {
		limit = 10
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT id::text, user_id, style_slug, mode, status,
		       article_title, article, created_at, completed_at
		FROM agent_traces
		WHERE user_input ILIKE '%' || $1 || '%'
		  AND status = 'completed'
		ORDER BY created_at DESC
		LIMIT $2
	`, topicTitle, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var traces []map[string]interface{}
	for rows.Next() {
		var (
			id           string
			userID       string
			styleSlug    *string
			mode         *string
			status       string
			articleTitle *string
			article      *string
			createdAt    time.Time
			completedAt  *time.Time
		)
		if err := rows.Scan(&id, &userID, &styleSlug, &mode, &status, &articleTitle, &article, &createdAt, &completedAt); err != nil {
			continue
		}
		item := map[string]interface{}{
			"trace_id":   id,
			"user_id":    userID,
			"status":     status,
			"created_at": createdAt,
		}
		if styleSlug != nil {
			item["style_slug"] = *styleSlug
		}
		if mode != nil {
			item["mode"] = *mode
		}
		if articleTitle != nil {
			item["article_title"] = *articleTitle
		}
		if article != nil {
			text := *article
			if len([]rune(text)) > 200 {
				text = string([]rune(text)[:200]) + "..."
			}
			item["article_preview"] = text
		}
		if completedAt != nil {
			item["completed_at"] = *completedAt
		}
		traces = append(traces, item)
	}

	return traces, nil
}

// ListUserRecentTraces retrieves a user's recent completed writing traces.
func (r *TraceRepo) ListUserRecentTraces(ctx context.Context, userID string, limit int) ([]map[string]interface{}, error) {
	if r.db == nil || userID == "" {
		return []map[string]interface{}{}, nil
	}
	if limit <= 0 || limit > 20 {
		limit = 10
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT id::text, user_id, style_slug, mode, status,
		       article_title, article, created_at, completed_at
		FROM agent_traces
		WHERE user_id = $1 AND status = 'completed'
		ORDER BY created_at DESC
		LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var traces []map[string]interface{}
	for rows.Next() {
		var (
			id           string
			uid          string
			styleSlug    *string
			mode         *string
			status       string
			articleTitle *string
			article      *string
			createdAt    time.Time
			completedAt  *time.Time
		)
		if err := rows.Scan(&id, &uid, &styleSlug, &mode, &status, &articleTitle, &article, &createdAt, &completedAt); err != nil {
			continue
		}
		item := map[string]interface{}{
			"trace_id":   id,
			"user_id":    uid,
			"status":     status,
			"created_at": createdAt,
		}
		if styleSlug != nil {
			item["style_slug"] = *styleSlug
		}
		if mode != nil {
			item["mode"] = *mode
		}
		if articleTitle != nil {
			item["article_title"] = *articleTitle
		}
		if article != nil {
			text := *article
			if len([]rune(text)) > 200 {
				text = string([]rune(text)[:200]) + "..."
			}
			item["article_preview"] = text
		}
		if completedAt != nil {
			item["completed_at"] = *completedAt
		}
		traces = append(traces, item)
	}

	return traces, nil
}

// ─── Platform Aggregation ────────────────────────────────

// PlatformStat holds per-platform topic counts.
type PlatformStat struct {
	Platform string `json:"platform"`
	Count    int    `json:"count"`
}

// GetPlatformStats returns topic counts grouped by platform.
func (r *TraceRepo) GetPlatformStats(ctx context.Context) ([]PlatformStat, error) {
	if r.db == nil {
		return nil, nil
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT COALESCE(platform, '') AS platform, COUNT(*) AS cnt
		FROM topics
		WHERE status = 'active'
		GROUP BY platform
		ORDER BY cnt DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stats []PlatformStat
	for rows.Next() {
		var s PlatformStat
		if err := rows.Scan(&s.Platform, &s.Count); err != nil {
			continue
		}
		stats = append(stats, s)
	}
	return stats, nil
}

// ListTopicsByPlatform lists topics filtered by platform.
func (r *TraceRepo) ListTopicsByPlatform(ctx context.Context, platform string, page, pageSize int) ([]map[string]interface{}, int, error) {
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
		SELECT id::text, title, description, source, platform, hot_rank, fetched_at, created_at,
		       raw_data->>'url' AS url
		FROM topics
		WHERE status = 'active' AND COALESCE(platform, '') = $1
		ORDER BY hot_rank ASC NULLS LAST, created_at DESC
		LIMIT $2 OFFSET $3
	`, platform, pageSize, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	topics := scanTopicRows(rows)

	var total int
	r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM topics WHERE status = 'active' AND COALESCE(platform, '') = $1`, platform).Scan(&total)

	return topics, total, nil
}

// ─── Topic Trend ─────────────────────────────────────────

// TrendPoint is a single data point in a topic's hot-rank history.
type TrendPoint struct {
	Timestamp time.Time `json:"timestamp"`
	HotRank   *int      `json:"hot_rank"`
	Platform  string    `json:"platform"`
}

// GetTopicTrend returns the hot-rank history for a topic within the given time window.
func (r *TraceRepo) GetTopicTrend(ctx context.Context, topicID string, hours int) ([]TrendPoint, error) {
	if r.db == nil {
		return nil, nil
	}
	if hours <= 0 || hours > 168 {
		hours = 24
	}

	query := fmt.Sprintf(`
		SELECT recorded_at, hot_rank, COALESCE(platform, '')
		FROM topic_trends
		WHERE topic_id = $1 AND recorded_at >= NOW() - INTERVAL '%d hours'
		ORDER BY recorded_at ASC
	`, hours)

	rows, err := r.db.QueryContext(ctx, query, topicID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var points []TrendPoint
	for rows.Next() {
		var p TrendPoint
		if err := rows.Scan(&p.Timestamp, &p.HotRank, &p.Platform); err != nil {
			continue
		}
		points = append(points, p)
	}
	return points, nil
}

// RecordTopicTrends records the current hot_rank for all active hotlist topics.
// Called periodically by a cron job.
func (r *TraceRepo) RecordTopicTrends(ctx context.Context) error {
	if r.db == nil {
		return fmt.Errorf("database not available")
	}

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO topic_trends (topic_id, hot_rank, platform, recorded_at)
		SELECT id, hot_rank, platform, NOW()
		FROM topics
		WHERE status = 'active' AND hot_rank IS NOT NULL
	`)
	return err
}

// ─── Topic Favorites ─────────────────────────────────────

// FavoriteTopic adds a topic to a user's favorites.
func (r *TraceRepo) FavoriteTopic(ctx context.Context, userID, topicID string) error {
	if r.db == nil {
		return fmt.Errorf("database not available")
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO topic_favorites (user_id, topic_id)
		VALUES ($1, $2)
		ON CONFLICT (user_id, topic_id) DO NOTHING
	`, userID, topicID)
	return err
}

// UnfavoriteTopic removes a topic from a user's favorites.
func (r *TraceRepo) UnfavoriteTopic(ctx context.Context, userID, topicID string) error {
	if r.db == nil {
		return fmt.Errorf("database not available")
	}
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM topic_favorites WHERE user_id = $1 AND topic_id = $2
	`, userID, topicID)
	return err
}

// IsTopicFavorited checks if a user has favorited a topic.
func (r *TraceRepo) IsTopicFavorited(ctx context.Context, userID, topicID string) (bool, error) {
	if r.db == nil {
		return false, nil
	}
	var exists bool
	err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM topic_favorites WHERE user_id = $1 AND topic_id = $2)
	`, userID, topicID).Scan(&exists)
	return exists, err
}

// ListFavoriteTopicIDs returns all favorited topic IDs for a user.
func (r *TraceRepo) ListFavoriteTopicIDs(ctx context.Context, userID string) (map[string]bool, error) {
	if r.db == nil {
		return map[string]bool{}, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT topic_id::text FROM topic_favorites WHERE user_id = $1
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			result[id] = true
		}
	}
	return result, nil
}

// ListFavoriteTopics lists a user's favorited topics with full topic data.
func (r *TraceRepo) ListFavoriteTopics(ctx context.Context, userID string, page, pageSize int) ([]map[string]interface{}, int, error) {
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
		SELECT t.id::text, t.title, t.description, t.source, t.platform, t.hot_rank, t.fetched_at, t.created_at,
		       t.raw_data->>'url' AS url,
		       f.created_at AS favorited_at
		FROM topic_favorites f
		JOIN topics t ON t.id = f.topic_id
		WHERE f.user_id = $1 AND t.status = 'active'
		ORDER BY f.created_at DESC
		LIMIT $2 OFFSET $3
	`, userID, pageSize, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	topics := scanTopicRowsWithFavorite(rows)

	var total int
	r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM topic_favorites f JOIN topics t ON t.id = f.topic_id WHERE f.user_id = $1 AND t.status = 'active'`, userID).Scan(&total)

	return topics, total, nil
}

// ─── Helper: scan topic rows ─────────────────────────────

func scanTopicRows(rows *sql.Rows) []map[string]interface{} {
	var topics []map[string]interface{}
	for rows.Next() {
		var (
			id          string
			title       string
			description *string
			source      string
			platform    *string
			hotRank     *int
			fetchedAt   *time.Time
			createdAt   time.Time
			url         *string
		)
		if err := rows.Scan(&id, &title, &description, &source, &platform, &hotRank, &fetchedAt, &createdAt, &url); err != nil {
			continue
		}
		topic := map[string]interface{}{
			"id":         id,
			"title":      title,
			"source":     source,
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
	return topics
}

func scanTopicRowsWithFavorite(rows *sql.Rows) []map[string]interface{} {
	var topics []map[string]interface{}
	for rows.Next() {
		var (
			id           string
			title        string
			description  *string
			source       string
			platform     *string
			hotRank      *int
			fetchedAt    *time.Time
			createdAt    time.Time
			url          *string
			favoritedAt  time.Time
		)
		if err := rows.Scan(&id, &title, &description, &source, &platform, &hotRank, &fetchedAt, &createdAt, &url, &favoritedAt); err != nil {
			continue
		}
		topic := map[string]interface{}{
			"id":            id,
			"title":         title,
			"source":        source,
			"created_at":    createdAt,
			"favorited_at":  favoritedAt,
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
	return topics
}

// ─── AI Recommendation Cache ─────────────────────────────

// RecommendationTTL defines how long cached recommendations stay valid.
const RecommendationTTL = 1 * time.Hour

// GetCachedRecommendations retrieves cached AI recommendations for a user.
// Returns (nil, false, nil) if cache is missing or expired.
func (r *TraceRepo) GetCachedRecommendations(ctx context.Context, userID string) ([]map[string]interface{}, bool, error) {
	if r.db == nil {
		return nil, false, nil
	}

	var (
		recsJSON    []byte
		generatedAt time.Time
	)
	err := r.db.QueryRowContext(ctx, `
		SELECT recommendations, generated_at
		FROM topic_recommendations
		WHERE user_id = $1
	`, userID).Scan(&recsJSON, &generatedAt)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	// Check TTL
	if time.Since(generatedAt) > RecommendationTTL {
		slog.Debug("recommendation cache expired", "user_id", userID, "age", time.Since(generatedAt))
		return nil, false, nil
	}

	var recs []map[string]interface{}
	if err := json.Unmarshal(recsJSON, &recs); err != nil {
		return nil, false, err
	}

	slog.Debug("recommendation cache hit", "user_id", userID, "count", len(recs), "age", time.Since(generatedAt))
	return recs, true, nil
}

// SaveRecommendations caches AI recommendations for a user (upsert).
func (r *TraceRepo) SaveRecommendations(ctx context.Context, userID string, recs []map[string]interface{}) error {
	if r.db == nil {
		return nil
	}

	recsJSON, err := json.Marshal(recs)
	if err != nil {
		return fmt.Errorf("failed to marshal recommendations: %w", err)
	}

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO topic_recommendations (user_id, recommendations, generated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (user_id) DO UPDATE SET
			recommendations = EXCLUDED.recommendations,
			generated_at = NOW()
	`, userID, recsJSON)
	if err != nil {
		slog.Warn("failed to save recommendation cache", "error", err, "user_id", userID)
	}
	return err
}

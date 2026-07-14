package database

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// FeedbackRepo handles feedback aggregation queries.
type FeedbackRepo struct {
	db *DB
}

// NewFeedbackRepo creates a new FeedbackRepo.
func NewFeedbackRepo(db *DB) *FeedbackRepo {
	return &FeedbackRepo{db: db}
}

// FeedbackAggregation is the aggregated feedback for a style+version.
type FeedbackAggregation struct {
	StyleSlug            string                 `json:"style_slug"`
	ProfileVersion       int                    `json:"profile_version"`
	TotalFeedback        int                    `json:"total_feedback"`
	TotalAdopted         int                    `json:"total_adopted"`
	AvgRating            float64                `json:"avg_rating"`
	WeightedScore        float64                `json:"weighted_score"`
	DimensionScores      map[string]float64     `json:"dimension_scores"`
	SegmentBreakdown     map[string]interface{} `json:"segment_breakdown"`
	ImprovementSuggestions string               `json:"improvement_suggestions"`
	ReadyForIteration    bool                   `json:"ready_for_iteration"`
	PeriodStart          time.Time              `json:"period_start"`
	PeriodEnd            time.Time              `json:"period_end"`
}

// AggregateFeedback computes aggregated feedback stats for a given style+version.
// It joins feedback_segments with agent_traces (filtered by style_slug) and
// upserts the result into feedback_aggregation.
func (r *FeedbackRepo) AggregateFeedback(ctx context.Context, styleSlug string, profileVersion int) (*FeedbackAggregation, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	// Query feedback joined with traces
	rows, err := r.db.QueryContext(ctx, `
		SELECT
			fs.segment_type,
			fs.rating,
			fs.feedback_type,
			fs.comment,
			fs.user_reputation,
			fs.is_adopted
		FROM feedback_segments fs
		JOIN agent_traces at ON fs.trace_id = at.trace_id
		WHERE at.style_slug = $1
	`, styleSlug)
	if err != nil {
		return nil, fmt.Errorf("failed to query feedback: %w", err)
	}
	defer rows.Close()

	type segFeedback struct {
		segmentType  string
		rating       int
		feedbackType string
		comment      string
		reputation   float64
		adopted      bool
	}

	var all []segFeedback
	totalRating := 0
	totalWeighted := 0.0
	totalAdopted := 0
	byType := map[string][]segFeedback{}

	for rows.Next() {
		var s segFeedback
		if err := rows.Scan(&s.segmentType, &s.rating, &s.feedbackType, &s.comment, &s.reputation, &s.adopted); err != nil {
			continue
		}
		all = append(all, s)
		totalRating += s.rating
		totalWeighted += float64(s.rating) * s.reputation
		if s.adopted {
			totalAdopted++
		}
		byType[s.segmentType] = append(byType[s.segmentType], s)
	}

	total := len(all)
	if total == 0 {
		return &FeedbackAggregation{
			StyleSlug:      styleSlug,
			ProfileVersion: profileVersion,
			PeriodStart:    time.Now().AddDate(0, -1, 0),
			PeriodEnd:      time.Now(),
		}, nil
	}

	avgRating := float64(totalRating) / float64(total)
	weightedScore := totalWeighted / float64(total)

	// Dimension scores by segment type
	dimensionScores := make(map[string]float64)
	for segType, feedbacks := range byType {
		sum := 0
		for _, f := range feedbacks {
			sum += f.rating
		}
		dimensionScores[segType] = float64(sum) / float64(len(feedbacks))
	}

	// Segment breakdown
	segmentBreakdown := make(map[string]interface{})
	for segType, feedbacks := range byType {
		good, bad, suggestion := 0, 0, 0
		comments := []string{}
		for _, f := range feedbacks {
			switch f.feedbackType {
			case "good":
				good++
			case "bad":
				bad++
			case "suggestion":
				suggestion++
			}
			if f.comment != "" {
				comments = append(comments, f.comment)
			}
		}
		segmentBreakdown[segType] = map[string]interface{}{
			"count":      len(feedbacks),
			"good":       good,
			"bad":        bad,
			"suggestion": suggestion,
			"comments":   comments,
		}
	}

	iterationThreshold := 30
	ready := total >= iterationThreshold

	now := time.Now()
	periodStart := now.AddDate(0, -1, 0)

	dimJSON, _ := json.Marshal(dimensionScores)
	breakdownJSON, _ := json.Marshal(segmentBreakdown)

	// Upsert aggregation
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO feedback_aggregation
			(style_slug, profile_version, total_feedback, total_adopted, avg_rating,
			 weighted_score, dimension_scores, segment_breakdown, ready_for_iteration,
			 iteration_threshold, period_start, period_end, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NOW())
		ON CONFLICT (style_slug, profile_version, period_start)
		DO UPDATE SET
			total_feedback = EXCLUDED.total_feedback,
			total_adopted = EXCLUDED.total_adopted,
			avg_rating = EXCLUDED.avg_rating,
			weighted_score = EXCLUDED.weighted_score,
			dimension_scores = EXCLUDED.dimension_scores,
			segment_breakdown = EXCLUDED.segment_breakdown,
			ready_for_iteration = EXCLUDED.ready_for_iteration
	`,
		styleSlug, profileVersion, total, totalAdopted, avgRating,
		weightedScore, string(dimJSON), string(breakdownJSON), ready,
		iterationThreshold, periodStart, now,
	)
	if err != nil {
		slog.Warn("failed to upsert feedback aggregation", "error", err)
	}

	return &FeedbackAggregation{
		StyleSlug:         styleSlug,
		ProfileVersion:    profileVersion,
		TotalFeedback:     total,
		TotalAdopted:      totalAdopted,
		AvgRating:         avgRating,
		WeightedScore:     weightedScore,
		DimensionScores:   dimensionScores,
		SegmentBreakdown:  segmentBreakdown,
		ReadyForIteration: ready,
		PeriodStart:       periodStart,
		PeriodEnd:         now,
	}, nil
}

// SaveImprovementSuggestions saves LLM-generated improvement suggestions.
func (r *FeedbackRepo) SaveImprovementSuggestions(ctx context.Context, styleSlug string, profileVersion int, suggestions string) error {
	if r.db == nil {
		return nil
	}

	_, err := r.db.ExecContext(ctx, `
		UPDATE feedback_aggregation
		SET improvement_suggestions = $3
		WHERE style_slug = $1 AND profile_version = $2
	`, styleSlug, profileVersion, suggestions)
	return err
}

// GetAggregation retrieves the latest aggregation for a style+version.
func (r *FeedbackRepo) GetAggregation(ctx context.Context, styleSlug string, profileVersion int) (*FeedbackAggregation, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	var (
		agg                  FeedbackAggregation
		dimJSON              []byte
		breakdownJSON        []byte
		suggestions          *string
	)

	err := r.db.QueryRowContext(ctx, `
		SELECT style_slug, profile_version, total_feedback, total_adopted,
		       avg_rating, weighted_score, dimension_scores, segment_breakdown,
		       improvement_suggestions, ready_for_iteration, period_start, period_end
		FROM feedback_aggregation
		WHERE style_slug = $1 AND profile_version = $2
		ORDER BY created_at DESC
		LIMIT 1
	`, styleSlug, profileVersion).Scan(
		&agg.StyleSlug, &agg.ProfileVersion, &agg.TotalFeedback, &agg.TotalAdopted,
		&agg.AvgRating, &agg.WeightedScore, &dimJSON, &breakdownJSON,
		&suggestions, &agg.ReadyForIteration, &agg.PeriodStart, &agg.PeriodEnd,
	)
	if err != nil {
		return nil, err
	}

	if len(dimJSON) > 0 {
		json.Unmarshal(dimJSON, &agg.DimensionScores)
	}
	if len(breakdownJSON) > 0 {
		json.Unmarshal(breakdownJSON, &agg.SegmentBreakdown)
	}
	if suggestions != nil {
		agg.ImprovementSuggestions = *suggestions
	}

	return &agg, nil
}

// ListAggregations lists all aggregations.
func (r *FeedbackRepo) ListAggregations(ctx context.Context, page, pageSize int) ([]map[string]interface{}, int, error) {
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
		SELECT style_slug, profile_version, total_feedback, total_adopted,
		       avg_rating, weighted_score, ready_for_iteration, created_at
		FROM feedback_aggregation
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2
	`, pageSize, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var results []map[string]interface{}
	for rows.Next() {
		var (
			styleSlug      string
			profileVersion int
			totalFeedback  int
			totalAdopted   int
			avgRating      float64
			weightedScore  float64
			ready          bool
			createdAt      time.Time
		)
		if err := rows.Scan(&styleSlug, &profileVersion, &totalFeedback, &totalAdopted,
			&avgRating, &weightedScore, &ready, &createdAt); err != nil {
			continue
		}
		results = append(results, map[string]interface{}{
			"style_slug":          styleSlug,
			"profile_version":     profileVersion,
			"total_feedback":      totalFeedback,
			"total_adopted":       totalAdopted,
			"avg_rating":          avgRating,
			"weighted_score":      weightedScore,
			"ready_for_iteration": ready,
			"created_at":          createdAt,
		})
	}

	var total int
	r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM feedback_aggregation`).Scan(&total)

	return results, total, nil
}

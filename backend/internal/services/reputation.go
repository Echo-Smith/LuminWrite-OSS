package services

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/luminbuddy/writing-agent-v2/internal/database"
)

// ReputationService manages user reputation based on feedback history.
type ReputationService struct {
	feedbackRepo *database.FeedbackRepo
	db           *database.DB
}

// NewReputationService creates a new ReputationService.
func NewReputationService(feedbackRepo *database.FeedbackRepo, db *database.DB) *ReputationService {
	return &ReputationService{
		feedbackRepo: feedbackRepo,
		db:           db,
	}
}

// ReputationConfig defines the parameters for reputation calculation.
type ReputationConfig struct {
	BaseReputation     float64 `json:"base_reputation"`     // Starting reputation for new users
	MaxReputation      float64 `json:"max_reputation"`      // Maximum reputation cap
	MinReputation      float64 `json:"min_reputation"`      // Minimum reputation floor
	AdoptionBonus      float64 `json:"adoption_bonus"`      // Bonus per adopted feedback
	HighRatingBonus    float64 `json:"high_rating_bonus"`   // Bonus for 4-5 star feedback
	LowRatingPenalty   float64 `json:"low_rating_penalty"`  // Penalty for 1-2 star feedback
	DecayFactor        float64 `json:"decay_factor"`        // Daily decay factor (0-1)
	VolumeBonusThreshold int   `json:"volume_bonus_threshold"` // Feedback count threshold for volume bonus
	VolumeBonus        float64 `json:"volume_bonus"`        // Bonus for crossing volume threshold
}

// DefaultReputationConfig returns sensible default configuration.
func DefaultReputationConfig() ReputationConfig {
	return ReputationConfig{
		BaseReputation:     1.00,
		MaxReputation:      5.00,
		MinReputation:      0.10,
		AdoptionBonus:      0.10,
		HighRatingBonus:    0.05,
		LowRatingPenalty:   0.08,
		DecayFactor:        0.01, // 1% daily decay toward base
		VolumeBonusThreshold: 10,
		VolumeBonus:        0.20,
	}
}

// UserReputation holds the computed reputation for a user.
type UserReputation struct {
	UserID            string  `json:"user_id"`
	Reputation        float64 `json:"reputation"`
	TotalFeedback     int     `json:"total_feedback"`
	TotalAdopted      int     `json:"total_adopted"`
	AvgRating         float64 `json:"avg_rating"`
	AdoptionRate      float64 `json:"adoption_rate"`
	LastActiveAt      *time.Time `json:"last_active_at,omitempty"`
}

// CalculateReputation computes the reputation for a user based on their feedback history.
func (s *ReputationService) CalculateReputation(ctx context.Context, userID string) (*UserReputation, error) {
	if s.db == nil {
		return &UserReputation{
			UserID:     userID,
			Reputation: 1.00,
		}, nil
	}

	config := DefaultReputationConfig()

	// Query feedback stats for the user
	var (
		totalFeedback int
		totalAdopted  int
		totalRating   float64
		lastActive    *time.Time
	)

	err := s.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE is_adopted = TRUE),
			COALESCE(AVG(rating), 0),
			MAX(created_at)
		FROM feedback_segments
		WHERE user_id = $1
	`, userID).Scan(&totalFeedback, &totalAdopted, &totalRating, &lastActive)
	if err != nil {
		return nil, fmt.Errorf("failed to query user feedback stats: %w", err)
	}

	if totalFeedback == 0 {
		return &UserReputation{
			UserID:      userID,
			Reputation:  config.BaseReputation,
			LastActiveAt: lastActive,
		}, nil
	}

	avgRating := totalRating / float64(totalFeedback)
	adoptionRate := float64(totalAdopted) / float64(totalFeedback)

	// Start from base reputation
	reputation := config.BaseReputation

	// Adoption bonus
	reputation += float64(totalAdopted) * config.AdoptionBonus

	// Rating-based adjustments
	if avgRating >= 4.0 {
		reputation += config.HighRatingBonus * float64(totalFeedback)
	} else if avgRating <= 2.0 {
		reputation -= config.LowRatingPenalty * float64(totalFeedback)
	}

	// Volume bonus
	if totalFeedback >= config.VolumeBonusThreshold {
		reputation += config.VolumeBonus
	}

	// Adoption rate bonus (if >50% adoption rate and >5 feedbacks)
	if totalFeedback >= 5 && adoptionRate > 0.5 {
		reputation += 0.15
	}

	// Apply time decay (move toward base reputation)
	if lastActive != nil {
		daysSinceActive := time.Since(*lastActive).Hours() / 24
		if daysSinceActive > 7 {
			decayAmount := (reputation - config.BaseReputation) * config.DecayFactor * daysSinceActive
			reputation -= decayAmount
		}
	}

	// Clamp to min/max
	reputation = math.Max(config.MinReputation, math.Min(config.MaxReputation, reputation))

	// Round to 2 decimal places
	reputation = math.Round(reputation*100) / 100

	result := &UserReputation{
		UserID:        userID,
		Reputation:    reputation,
		TotalFeedback: totalFeedback,
		TotalAdopted:  totalAdopted,
		AvgRating:     math.Round(avgRating*100) / 100,
		AdoptionRate:  math.Round(adoptionRate*100) / 100,
		LastActiveAt:  lastActive,
	}

	return result, nil
}

// UpdateReputation recalculates and persists the user's reputation.
// It updates the user_reputation field in all their feedback_segments and
// records the change in user_reputation_history.
func (s *ReputationService) UpdateReputation(ctx context.Context, userID string) (*UserReputation, error) {
	if s.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	// Get current reputation
	var oldReputation float64
	s.db.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(user_reputation), 1.00) FROM feedback_segments WHERE user_id = $1
	`, userID).Scan(&oldReputation)

	// Calculate new reputation
	result, err := s.CalculateReputation(ctx, userID)
	if err != nil {
		return nil, err
	}

	delta := result.Reputation - oldReputation

	// Update all feedback_segments for this user
	_, err = s.db.ExecContext(ctx, `
		UPDATE feedback_segments
		SET user_reputation = $2, reputation_recalc_at = NOW()
		WHERE user_id = $1
	`, userID, result.Reputation)
	if err != nil {
		slog.Warn("failed to update user reputation in feedback_segments", "user_id", userID, "error", err)
	}

	// Record in history
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO user_reputation_history (user_id, old_reputation, new_reputation, delta, reason, created_at)
		VALUES ($1, $2, $3, $4, 'recalculation', NOW())
	`, userID, oldReputation, result.Reputation, delta)
	if err != nil {
		slog.Warn("failed to record reputation history", "user_id", userID, "error", err)
	}

	slog.Info("user reputation updated",
		"user_id", userID,
		"old", oldReputation,
		"new", result.Reputation,
		"delta", delta,
	)

	return result, nil
}

// AdoptFeedback marks a feedback segment as adopted (via Workbuddy or manual).
// It also triggers a reputation recalculation for the feedback author.
func (s *ReputationService) AdoptFeedback(ctx context.Context, feedbackID, traceID, userID, source, adoptedText string) error {
	if s.db == nil {
		return fmt.Errorf("database not available")
	}

	// Mark the feedback as adopted
	_, err := s.db.ExecContext(ctx, `
		UPDATE feedback_segments
		SET is_adopted = TRUE, adopted_at = NOW(), adopted_source = $2
		WHERE id = $1
	`, feedbackID, source)
	if err != nil {
		return fmt.Errorf("failed to mark feedback as adopted: %w", err)
	}

	// Record the adoption
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO workbuddy_adoptions (feedback_id, trace_id, user_id, source, status, adopted_text, adopted_at, adopted_by)
		VALUES ($1, $2, $3, $4, 'adopted', $5, NOW(), $4)
		ON CONFLICT (feedback_id) DO UPDATE SET
			status = 'adopted',
			adopted_text = EXCLUDED.adopted_text,
			adopted_at = NOW()
	`, feedbackID, traceID, userID, source, adoptedText)
	if err != nil {
		slog.Warn("failed to record workbuddy adoption", "error", err)
	}

	// Trigger reputation recalculation if we have a user ID
	if userID != "" {
		go func() {
			recalcCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if _, err := s.UpdateReputation(recalcCtx, userID); err != nil {
				slog.Warn("failed to recalculate reputation after adoption", "user_id", userID, "error", err)
			}
		}()
	}

	slog.Info("feedback adopted", "feedback_id", feedbackID, "source", source, "user_id", userID)
	return nil
}

// RejectFeedback marks a feedback as rejected (not adopted).
func (s *ReputationService) RejectFeedback(ctx context.Context, feedbackID, traceID, userID, source, reason string) error {
	if s.db == nil {
		return fmt.Errorf("database not available")
	}

	// Record the rejection
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO workbuddy_adoptions (feedback_id, trace_id, user_id, source, status, metadata, adopted_at, adopted_by)
		VALUES ($1, $2, $3, $4, 'rejected', $5, NOW(), $4)
		ON CONFLICT (feedback_id) DO UPDATE SET
			status = 'rejected',
			metadata = EXCLUDED.metadata,
			adopted_at = NOW()
	`, feedbackID, traceID, userID, source, fmt.Sprintf(`{"reason": "%s"}`, reason))
	if err != nil {
		return fmt.Errorf("failed to record rejection: %w", err)
	}

	slog.Info("feedback rejected", "feedback_id", feedbackID, "source", source, "reason", reason)
	return nil
}

// GetAdoptionHistory retrieves the adoption history for a trace.
func (s *ReputationService) GetAdoptionHistory(ctx context.Context, traceID string) ([]map[string]interface{}, error) {
	if s.db == nil {
		return []map[string]interface{}{}, nil
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT wa.id::text, wa.feedback_id::text, wa.trace_id, wa.user_id::text,
		       wa.source, wa.status, wa.adopted_text, wa.metadata,
		       wa.adopted_at, wa.adopted_by
		FROM workbuddy_adoptions wa
		WHERE wa.trace_id = $1
		ORDER BY wa.adopted_at DESC
	`, traceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []map[string]interface{}
	for rows.Next() {
		var (
			id           string
			feedbackID   string
			traceID      string
			userID       *string
			source       string
			status       string
			adoptedText  *string
			metadataJSON []byte
			adoptedAt    time.Time
			adoptedBy    string
		)
		if err := rows.Scan(&id, &feedbackID, &traceID, &userID, &source, &status,
			&adoptedText, &metadataJSON, &adoptedAt, &adoptedBy); err != nil {
			continue
		}

		entry := map[string]interface{}{
			"id":          id,
			"feedback_id": feedbackID,
			"trace_id":    traceID,
			"source":      source,
			"status":      status,
			"adopted_at":  adoptedAt,
			"adopted_by":  adoptedBy,
		}
		if userID != nil {
			entry["user_id"] = *userID
		}
		if adoptedText != nil {
			entry["adopted_text"] = *adoptedText
		}

		results = append(results, entry)
	}

	return results, nil
}

// GetReputationHistory retrieves the reputation change history for a user.
func (s *ReputationService) GetReputationHistory(ctx context.Context, userID string, limit int) ([]map[string]interface{}, error) {
	if s.db == nil {
		return []map[string]interface{}{}, nil
	}

	if limit <= 0 || limit > 100 {
		limit = 20
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id::text, trace_id, old_reputation, new_reputation, delta, reason, created_at
		FROM user_reputation_history
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []map[string]interface{}
	for rows.Next() {
		var (
			id            string
			traceID       *string
			oldRep        float64
			newRep        float64
			delta         float64
			reason        string
			createdAt     time.Time
		)
		if err := rows.Scan(&id, &traceID, &oldRep, &newRep, &delta, &reason, &createdAt); err != nil {
			continue
		}

		entry := map[string]interface{}{
			"id":             id,
			"old_reputation": oldRep,
			"new_reputation": newRep,
			"delta":          delta,
			"reason":         reason,
			"created_at":     createdAt,
		}
		if traceID != nil {
			entry["trace_id"] = *traceID
		}

		results = append(results, entry)
	}

	return results, nil
}

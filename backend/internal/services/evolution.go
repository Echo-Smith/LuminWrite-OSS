package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
)

// EvolutionService manages the self-evolution loop: eval → candidate → gate → rollout
type EvolutionService struct {
	evalRepo *database.EvaluationRepo
	profiles *profile.Loader
	db       *database.DB
}

func NewEvolutionService(evalRepo *database.EvaluationRepo, profiles *profile.Loader) *EvolutionService {
	return &EvolutionService{
		evalRepo: evalRepo,
		profiles: profiles,
	}
}

// SetDB sets the database handle for candidate persistence.
func (s *EvolutionService) SetDB(db *database.DB) {
	s.db = db
}

type ProfileCandidate struct {
	ID              string                 `json:"id"`
	StyleSlug       string                 `json:"style_slug"`
	ParentVersion   int                    `json:"parent_version"`
	Changes         map[string]interface{} `json:"changes"`
	EvalBaselineID  string                 `json:"eval_baseline_id"`
	EvalCandidateID string                 `json:"eval_candidate_id"`
	Status          string                 `json:"status"`
	CreatedAt       time.Time              `json:"created_at"`
}

type CanaryRollout struct {
	ID             string    `json:"id"`
	StyleSlug      string    `json:"style_slug"`
	Version        int       `json:"version"`
	CandidateID    string    `json:"candidate_id"`
	Percentage     float64   `json:"percentage"`
	Enabled        bool      `json:"enabled"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	EndedAt        *time.Time `json:"ended_at,omitempty"`
	RollbackReason string    `json:"rollback_reason,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

func (s *EvolutionService) CreateCandidateFromFeedback(ctx context.Context, slug string, agg *database.FeedbackAggregation, changes map[string]interface{}) (*ProfileCandidate, error) {
	if s.evalRepo == nil {
		return nil, fmt.Errorf("evaluation repository not available")
	}
	sets, _, err := s.evalRepo.ListSets(ctx, slug, 1, 1)
	if err != nil || len(sets) == 0 {
		return nil, fmt.Errorf("no evaluation set found for style: %s", slug)
	}
	candidate := &ProfileCandidate{
		StyleSlug:     slug,
		ParentVersion: s.getCurrentVersion(slug),
		Changes:       changes,
		Status:        "draft",
		CreatedAt:     time.Now(),
	}

	// Persist to database if available
	if s.db != nil {
		changesJSON, _ := json.Marshal(changes)
		var id string
		err := s.db.QueryRowContext(ctx, `
			INSERT INTO style_profile_candidates (style_slug, parent_version, changes, status)
			VALUES ($1, $2, $3, $4)
			RETURNING id::text
		`, slug, candidate.ParentVersion, changesJSON, "draft").Scan(&id)
		if err != nil {
			slog.Warn("failed to persist candidate to DB", "error", err)
		} else {
			candidate.ID = id
		}
	}

	slog.Info("profile candidate created", "slug", slug, "parent_version", candidate.ParentVersion, "id", candidate.ID)
	return candidate, nil
}

func (s *EvolutionService) RunEvalGate(ctx context.Context, candidate *ProfileCandidate, evalService *EvaluationService) (bool, error) {
	if evalService == nil || s.evalRepo == nil {
		return false, fmt.Errorf("evaluation service not available")
	}
	sets, _, err := s.evalRepo.ListSets(ctx, candidate.StyleSlug, 1, 1)
	if err != nil || len(sets) == 0 {
		return false, fmt.Errorf("no evaluation set found")
	}
	run, err := s.evalRepo.CreateRun(ctx, sets[0].ID, candidate.StyleSlug, candidate.ParentVersion+1, "candidate_eval", fmt.Sprintf("Evaluating candidate %s", candidate.ID))
	if err != nil {
		return false, err
	}
	if err := evalService.RunEvaluation(ctx, run.ID); err != nil {
		slog.Error("candidate evaluation failed", "candidate_id", candidate.ID, "error", err)
		return false, err
	}
	finalRun, err := s.evalRepo.GetRun(ctx, run.ID)
	if err != nil {
		return false, err
	}
	comparisons, _ := s.evalRepo.GetRegressionComparisons(ctx, candidate.StyleSlug, 1)
	passing := true
	if finalRun.OverallScore < 3.0 {
		passing = false
	}
	for _, cmp := range comparisons {
		if isPassing, ok := cmp["is_passing"].(bool); ok && !isPassing {
			passing = false
			break
		}
	}
	slog.Info("eval gate completed", "candidate_id", candidate.ID, "passing", passing, "score", finalRun.OverallScore)
	return passing, nil
}

func (s *EvolutionService) EnableCanaryRollout(ctx context.Context, slug string, version int, percentage float64) (*CanaryRollout, error) {
	now := time.Now()
	rollout := &CanaryRollout{
		StyleSlug:  slug,
		Version:    version,
		Percentage: percentage,
		Enabled:    true,
		StartedAt:  &now,
		CreatedAt:  time.Now(),
	}
	slog.Info("canary rollout enabled", "slug", slug, "version", version, "percentage", percentage)
	return rollout, nil
}

func (s *EvolutionService) getCurrentVersion(slug string) int {
	if p, ok := s.profiles.Get(slug); ok {
		return p.Version
	}
	return 0
}

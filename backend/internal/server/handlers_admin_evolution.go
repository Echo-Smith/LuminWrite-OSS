package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/services"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── Admin: Self-Evolution Candidate Management ──────────
//
// These endpoints manage the self-evolution loop for style profiles:
//   - List candidates (profile iterations from feedback)
//   - Approve/reject candidates (eval gate decisions)
//   - Enable canary rollout for approved candidates
//
// The candidate data is persisted in the style_profile_candidates
// and canary_rollouts tables (migration 055).

// handleAdminListEvolutionCandidates returns all profile candidates.
//
// GET /api/v2/admin/evolution/candidates
func (s *Server) handleAdminListEvolutionCandidates(w http.ResponseWriter, r *http.Request) {
	if s.evolutionSvc == nil {
		response.OK(w, map[string]any{
			"candidates": []any{},
			"total":      0,
		})
		return
	}

	candidates, err := s.listEvolutionCandidatesFromDB(r.Context())
	if err != nil {
		slog.Warn("failed to list evolution candidates", "error", err)
		response.OK(w, map[string]any{
			"candidates": []any{},
			"total":      0,
		})
		return
	}

	response.OK(w, map[string]any{
		"candidates": candidates,
		"total":      len(candidates),
	})
}

// handleAdminApproveEvolutionCandidate approves a candidate, triggering the eval gate.
//
// POST /api/v2/admin/evolution/candidates/{id}/approve
func (s *Server) handleAdminApproveEvolutionCandidate(w http.ResponseWriter, r *http.Request) {
	if s.evolutionSvc == nil {
		response.Err(w, http.StatusServiceUnavailable, "service_unavailable", "evolution service not available")
		return
	}

	candidateID := chi.URLParam(r, "id")
	if candidateID == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "candidate ID is required")
		return
	}

	// Update candidate status to "approved" in DB
	if err := s.updateEvolutionCandidateStatus(r.Context(), candidateID, "approved"); err != nil {
		slog.Warn("failed to approve candidate", "id", candidateID, "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to approve candidate")
		return
	}

	slog.Info("evolution candidate approved", "candidate_id", candidateID)

	response.OK(w, map[string]any{
		"id":     candidateID,
		"status": "approved",
	})
}

// handleAdminRejectEvolutionCandidate rejects a candidate.
//
// POST /api/v2/admin/evolution/candidates/{id}/reject
func (s *Server) handleAdminRejectEvolutionCandidate(w http.ResponseWriter, r *http.Request) {
	if s.evolutionSvc == nil {
		response.Err(w, http.StatusServiceUnavailable, "service_unavailable", "evolution service not available")
		return
	}

	candidateID := chi.URLParam(r, "id")
	if candidateID == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "candidate ID is required")
		return
	}

	var body struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	if err := s.updateEvolutionCandidateStatus(r.Context(), candidateID, "rejected"); err != nil {
		slog.Warn("failed to reject candidate", "id", candidateID, "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to reject candidate")
		return
	}

	slog.Info("evolution candidate rejected", "candidate_id", candidateID, "reason", body.Reason)

	response.OK(w, map[string]any{
		"id":     candidateID,
		"status": "rejected",
		"reason": body.Reason,
	})
}

// handleAdminEnableCanaryRollout enables canary rollout for an approved candidate.
//
// POST /api/v2/admin/evolution/candidates/{id}/canary
func (s *Server) handleAdminEnableCanaryRollout(w http.ResponseWriter, r *http.Request) {
	if s.evolutionSvc == nil {
		response.Err(w, http.StatusServiceUnavailable, "service_unavailable", "evolution service not available")
		return
	}

	candidateID := chi.URLParam(r, "id")
	if candidateID == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "candidate ID is required")
		return
	}

	var body struct {
		Percentage float64 `json:"percentage"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Percentage <= 0 || body.Percentage > 100 {
		body.Percentage = 10 // default 10%
	}

	// Get candidate from DB to get slug + version
	candidate, err := s.getEvolutionCandidate(r.Context(), candidateID)
	if err != nil {
		slog.Warn("failed to get candidate for canary", "id", candidateID, "error", err)
		response.Err(w, http.StatusNotFound, "not_found", "candidate not found")
		return
	}

	if candidate.Status != "approved" {
		response.Err(w, http.StatusBadRequest, "invalid_status", "candidate must be approved before canary rollout")
		return
	}

	// Enable canary rollout
	rollout, err := s.evolutionSvc.EnableCanaryRollout(r.Context(), candidate.StyleSlug, candidate.ParentVersion+1, body.Percentage)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "canary_failed", fmt.Sprintf("failed to enable canary: %v", err))
		return
	}

	// Persist rollout to DB
	if s.db != nil {
		_, err = s.db.ExecContext(r.Context(), `
			INSERT INTO canary_rollouts (style_slug, version, candidate_id, percentage, enabled, started_at)
			VALUES ($1, $2, $3, $4, TRUE, NOW())
		`, candidate.StyleSlug, candidate.ParentVersion+1, candidateID, body.Percentage/100)
		if err != nil {
			slog.Warn("failed to persist canary rollout", "error", err)
		}
	}

	slog.Info("canary rollout enabled", "candidate_id", candidateID, "percentage", body.Percentage)

	response.OK(w, map[string]any{
		"candidate_id": candidateID,
		"rollout":       rollout,
	})
}

// ─── DB Helpers ──────────────────────────────────────────

type evolutionCandidate struct {
	ID              string                 `json:"id"`
	StyleSlug       string                 `json:"style_slug"`
	ParentVersion   int                    `json:"parent_version"`
	Changes         map[string]interface{} `json:"changes"`
	Status          string                 `json:"status"`
	EvalBaselineID  string                 `json:"eval_baseline_id,omitempty"`
	EvalCandidateID string                 `json:"eval_candidate_id,omitempty"`
	CreatedAt       time.Time              `json:"created_at"`
}

func (s *Server) listEvolutionCandidatesFromDB(ctx context.Context) ([]evolutionCandidate, error) {
	if s.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id::text, style_slug, parent_version, changes,
		       status, COALESCE(eval_baseline_id::text, ''), COALESCE(eval_candidate_id::text, ''),
		       created_at
		FROM style_profile_candidates
		ORDER BY created_at DESC
		LIMIT 50
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var candidates []evolutionCandidate
	for rows.Next() {
		var c evolutionCandidate
		var changesJSON []byte
		if err := rows.Scan(&c.ID, &c.StyleSlug, &c.ParentVersion, &changesJSON,
			&c.Status, &c.EvalBaselineID, &c.EvalCandidateID, &c.CreatedAt); err != nil {
			continue
		}
		if len(changesJSON) > 0 {
			json.Unmarshal(changesJSON, &c.Changes)
		}
		candidates = append(candidates, c)
	}
	return candidates, nil
}

func (s *Server) getEvolutionCandidate(ctx context.Context, id string) (*evolutionCandidate, error) {
	if s.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	var c evolutionCandidate
	var changesJSON []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT id::text, style_slug, parent_version, changes,
		       status, COALESCE(eval_baseline_id::text, ''), COALESCE(eval_candidate_id::text, ''),
		       created_at
		FROM style_profile_candidates
		WHERE id = $1::uuid
	`, id).Scan(&c.ID, &c.StyleSlug, &c.ParentVersion, &changesJSON,
		&c.Status, &c.EvalBaselineID, &c.EvalCandidateID, &c.CreatedAt)
	if err != nil {
		return nil, err
	}
	if len(changesJSON) > 0 {
		json.Unmarshal(changesJSON, &c.Changes)
	}
	return &c, nil
}

func (s *Server) updateEvolutionCandidateStatus(ctx context.Context, id, status string) error {
	if s.db == nil {
		return fmt.Errorf("database not available")
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE style_profile_candidates SET status = $1 WHERE id = $2::uuid
	`, status, id)
	return err
}

// storeCandidateToDB persists a ProfileCandidate to the database.
// This is called by the EvolutionService when creating candidates from feedback.
func (s *Server) storeCandidateToDB(ctx context.Context, candidate *services.ProfileCandidate) error {
	if s.db == nil {
		return fmt.Errorf("database not available")
	}
	changesJSON, _ := json.Marshal(candidate.Changes)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO style_profile_candidates (style_slug, parent_version, changes, status)
		VALUES ($1, $2, $3, $4)
	`, candidate.StyleSlug, candidate.ParentVersion, changesJSON, candidate.Status)
	return err
}

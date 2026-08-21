package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/routing"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/services"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── Admin: Self-Evolution Candidate Management ──────────
//
// These endpoints manage the self-evolution loop for style profiles:
//   - List candidates (profile iterations from feedback)
//   - Approve/reject candidates (eval gate decisions)
//   - Enable canary rollout for approved candidates
//   - Rollback canary rollout
//   - Promote canary to full rollout
//   - Get canary metrics (routing counters)
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

	// Enrich each candidate with its canary rollout info if any
	type candidateWithRollout struct {
		evolutionCandidate
		Rollout *canaryRolloutInfo `json:"rollout,omitempty"`
	}

	result := make([]candidateWithRollout, 0, len(candidates))
	for _, c := range candidates {
		item := candidateWithRollout{evolutionCandidate: c}
		if c.Status == "rollout" {
			if rollout, _ := s.getActiveCanaryRollout(r.Context(), c.ID); rollout != nil {
				item.Rollout = rollout
			}
		}
		result = append(result, item)
	}

	response.OK(w, map[string]any{
		"candidates": result,
		"total":      len(result),
	})
}

// handleAdminApproveEvolutionCandidate approves a candidate and asynchronously triggers the eval gate.
// The candidate status transitions: draft → eval_running → approved/rejected (by gate result).
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

	// Get candidate from DB
	candidate, err := s.getEvolutionCandidate(r.Context(), candidateID)
	if err != nil {
		response.Err(w, http.StatusNotFound, "not_found", "candidate not found")
		return
	}

	if candidate.Status != "draft" && candidate.Status != "rejected" {
		response.Err(w, http.StatusBadRequest, "invalid_status",
			fmt.Sprintf("candidate must be in draft or rejected status (current: %s)", candidate.Status))
		return
	}

	// Update status to "eval_running"
	if err := s.updateEvolutionCandidateStatus(r.Context(), candidateID, "eval_running"); err != nil {
		slog.Warn("failed to update candidate to eval_running", "id", candidateID, "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to update candidate status")
		return
	}

	// Record gate event: approve triggered
	s.recordGateEventStatic(r.Context(), candidateID, "approve_triggered", r,
		"Admin approved candidate, eval gate started", map[string]any{
			"style_slug": candidate.StyleSlug,
		})

	// Record audit log
	s.writeAuditLog(r, "evolution_approve", "evolution_candidate", candidateID,
		fmt.Sprintf("Approved candidate for style %s v%d", candidate.StyleSlug, candidate.ParentVersion+1),
		map[string]any{"candidate_id": candidateID, "style_slug": candidate.StyleSlug})

	// Build a ProfileCandidate for the eval gate
	pc := &services.ProfileCandidate{
		ID:            candidateID,
		StyleSlug:     candidate.StyleSlug,
		ParentVersion: candidate.ParentVersion,
		Changes:       candidate.Changes,
		Status:        "eval_running",
		CreatedAt:     candidate.CreatedAt,
	}

	// Asynchronously run eval gate
	go s.runEvalGateAsync(pc)

	slog.Info("evolution candidate approved, eval gate started", "candidate_id", candidateID)

	response.OK(w, map[string]any{
		"id":      candidateID,
		"status":  "eval_running",
		"message": "eval gate started asynchronously; check status after completion",
	})
}

// runEvalGateAsync runs the eval gate asynchronously and updates the candidate status.
// It persists the eval result (score, run ID, pass/fail) to the candidate record.
func (s *Server) runEvalGateAsync(candidate *services.ProfileCandidate) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	passing, err := s.evolutionSvc.RunEvalGate(ctx, candidate, s.evalSvc)
	if err != nil {
		slog.Error("eval gate failed for candidate", "id", candidate.ID, "error", err)
		_ = s.updateEvolutionCandidateStatus(ctx, candidate.ID, "rejected")

		// Persist eval failure result
		if s.db != nil {
			_, _ = s.db.ExecContext(ctx, `
				UPDATE style_profile_candidates
				SET eval_passed = FALSE, eval_completed_at = NOW(),
				    eval_summary = $2, rejected_reason = $3
				WHERE id = $1::uuid
			`, candidate.ID, `{"error": "eval gate execution failed"}`, err.Error())
		}

		// Record gate event
		s.recordGateEventStatic(ctx, candidate.ID, "eval_failed", nil,
			fmt.Sprintf("Eval gate failed: %v", err), map[string]any{"error": err.Error()})
		return
	}

	// Get the eval run result for persistence
	var evalRunID string
	var evalScore float64
	sets, _, _ := s.evalRepo.ListSets(ctx, candidate.StyleSlug, 1, 1)
	if len(sets) > 0 {
		runs, _, _ := s.evalRepo.ListRuns(ctx, sets[0].ID, 1, 1)
		if len(runs) > 0 {
			evalRunID = runs[0].ID
			evalScore = runs[0].OverallScore
		}
	}

	// Build eval summary
	evalSummary, _ := json.Marshal(map[string]any{
		"score":      evalScore,
		"threshold":   3.0,
		"run_id":      evalRunID,
		"passing":     passing,
		"completed_at": time.Now().Format(time.RFC3339),
	})

	if passing {
		_ = s.updateEvolutionCandidateStatus(ctx, candidate.ID, "approved")

		// Persist eval success result
		if s.db != nil {
			actorID := "system"
			_, _ = s.db.ExecContext(ctx, `
				UPDATE style_profile_candidates
				SET eval_run_id = $2::uuid, eval_score = $3, eval_passed = TRUE,
				    eval_completed_at = NOW(), eval_summary = $4,
				    approved_by = $5, approved_at = NOW()
				WHERE id = $1::uuid
			`, candidate.ID, nullableUUID(evalRunID), evalScore, evalSummary, actorID)
		}

		slog.Info("eval gate passed, candidate approved", "id", candidate.ID, "score", evalScore)

		// Record gate event
		s.recordGateEventStatic(ctx, candidate.ID, "eval_passed", nil,
			fmt.Sprintf("Eval gate passed with score %.2f", evalScore),
			map[string]any{"score": evalScore, "run_id": evalRunID})
	} else {
		_ = s.updateEvolutionCandidateStatus(ctx, candidate.ID, "rejected")

		// Persist eval failure result
		if s.db != nil {
			_, _ = s.db.ExecContext(ctx, `
				UPDATE style_profile_candidates
				SET eval_run_id = $2::uuid, eval_score = $3, eval_passed = FALSE,
				    eval_completed_at = NOW(), eval_summary = $4,
				    rejected_reason = $5
				WHERE id = $1::uuid
			`, candidate.ID, nullableUUID(evalRunID), evalScore, evalSummary,
				"eval gate did not pass: score below threshold or regression detected")
		}

		slog.Info("eval gate failed, candidate rejected", "id", candidate.ID, "score", evalScore)

		// Record gate event
		s.recordGateEventStatic(ctx, candidate.ID, "eval_rejected", nil,
			fmt.Sprintf("Eval gate rejected with score %.2f", evalScore),
			map[string]any{"score": evalScore, "run_id": evalRunID})
	}
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
	if body.Reason == "" {
		body.Reason = "rejected by admin"
	}

	if err := s.updateEvolutionCandidateStatus(r.Context(), candidateID, "rejected"); err != nil {
		slog.Warn("failed to reject candidate", "id", candidateID, "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to reject candidate")
		return
	}

	// Persist rejected reason
	if s.db != nil {
		_, _ = s.db.ExecContext(r.Context(), `
			UPDATE style_profile_candidates SET rejected_reason = $2
			WHERE id = $1::uuid
		`, candidateID, body.Reason)
	}

	// Record gate event
	s.recordGateEventStatic(r.Context(), candidateID, "manual_reject", r,
		fmt.Sprintf("Admin rejected candidate: %s", body.Reason), map[string]any{"reason": body.Reason})

	// Record audit log
	s.writeAuditLog(r, "evolution_reject", "evolution_candidate", candidateID,
		fmt.Sprintf("Rejected candidate: %s", body.Reason),
		map[string]any{"candidate_id": candidateID, "reason": body.Reason})

	slog.Info("evolution candidate rejected", "candidate_id", candidateID, "reason", body.Reason)

	response.OK(w, map[string]any{
		"id":     candidateID,
		"status": "rejected",
		"reason": body.Reason,
	})
}

// handleAdminEnableCanaryRollout enables canary rollout for an approved candidate.
// Also updates the profile Loader's RolloutConfig so the routing engine picks it up.
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
		response.Err(w, http.StatusBadRequest, "invalid_status",
			fmt.Sprintf("candidate must be approved before canary rollout (current: %s)", candidate.Status))
		return
	}

	// Enable canary rollout via EvolutionService
	rollout, err := s.evolutionSvc.EnableCanaryRollout(r.Context(), candidate.StyleSlug, candidate.ParentVersion+1, body.Percentage)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "canary_failed", fmt.Sprintf("failed to enable canary: %v", err))
		return
	}

	// Persist rollout to DB
	if s.db != nil {
		// End any existing active rollout for this style
		_, _ = s.db.ExecContext(r.Context(), `
			UPDATE canary_rollouts SET enabled = FALSE, ended_at = NOW()
			WHERE style_slug = $1 AND enabled = TRUE
		`, candidate.StyleSlug)

		_, err = s.db.ExecContext(r.Context(), `
			INSERT INTO canary_rollouts (style_slug, version, candidate_id, percentage, enabled, started_at)
			VALUES ($1, $2, $3, $4, TRUE, NOW())
		`, candidate.StyleSlug, candidate.ParentVersion+1, candidateID, body.Percentage/100)
		if err != nil {
			slog.Warn("failed to persist canary rollout", "error", err)
		}
	}

	// Update the profile Loader's RolloutConfig so routing engine uses it
	if s.profiles != nil {
		config := routing.RolloutConfig{
			Type:           "percentage",
			RolloutPercent: int(body.Percentage),
			FallbackVersion: candidate.ParentVersion,
		}
		if err := s.profiles.UpdateRolloutConfig(candidate.StyleSlug, config); err != nil {
			slog.Warn("failed to update rollout config on profile loader", "slug", candidate.StyleSlug, "error", err)
		}
	}

	// Update candidate status to "rollout"
	_ = s.updateEvolutionCandidateStatus(r.Context(), candidateID, "rollout")

	// Record gate event
	s.recordGateEventStatic(r.Context(), candidateID, "canary_enabled", r,
		fmt.Sprintf("Canary rollout enabled at %.0f%%", body.Percentage),
		map[string]any{"percentage": body.Percentage, "slug": candidate.StyleSlug})

	// Record audit log
	s.writeAuditLog(r, "evolution_canary", "evolution_candidate", candidateID,
		fmt.Sprintf("Canary rollout enabled at %.0f%% for style %s", body.Percentage, candidate.StyleSlug),
		map[string]any{"candidate_id": candidateID, "percentage": body.Percentage, "style_slug": candidate.StyleSlug})

	slog.Info("canary rollout enabled", "candidate_id", candidateID, "percentage", body.Percentage, "slug", candidate.StyleSlug)

	response.OK(w, map[string]any{
		"candidate_id": candidateID,
		"rollout":      rollout,
		"percentage":   body.Percentage,
		"status":       "rollout",
	})
}

// handleAdminRollbackCanary rolls back a canary rollout, disabling it and reverting to fallback.
//
// POST /api/v2/admin/evolution/candidates/{id}/rollback
func (s *Server) handleAdminRollbackCanary(w http.ResponseWriter, r *http.Request) {
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
	if body.Reason == "" {
		body.Reason = "manual rollback by admin"
	}

	// Get candidate from DB
	candidate, err := s.getEvolutionCandidate(r.Context(), candidateID)
	if err != nil {
		response.Err(w, http.StatusNotFound, "not_found", "candidate not found")
		return
	}

	if candidate.Status != "rollout" {
		response.Err(w, http.StatusBadRequest, "invalid_status", "candidate is not in rollout phase")
		return
	}

	// Disable canary rollout in DB
	if s.db != nil {
		_, err = s.db.ExecContext(r.Context(), `
			UPDATE canary_rollouts
			SET enabled = FALSE, ended_at = NOW(), rollback_reason = $2
			WHERE candidate_id = $1::uuid AND enabled = TRUE
		`, candidateID, body.Reason)
		if err != nil {
			slog.Warn("failed to rollback canary in DB", "error", err)
		}
	}

	// Revert the profile Loader's RolloutConfig to full (old version)
	if s.profiles != nil {
		config := routing.RolloutConfig{
			Type:            "full",
			RolloutPercent:  100,
			FallbackVersion: candidate.ParentVersion,
		}
		if err := s.profiles.UpdateRolloutConfig(candidate.StyleSlug, config); err != nil {
			slog.Warn("failed to revert rollout config", "slug", candidate.StyleSlug, "error", err)
		}
	}

	// Update candidate status to "rejected"
	_ = s.updateEvolutionCandidateStatus(r.Context(), candidateID, "rejected")

	// Persist rejected reason
	if s.db != nil {
		_, _ = s.db.ExecContext(r.Context(), `
			UPDATE style_profile_candidates SET rejected_reason = $2
			WHERE id = $1::uuid
		`, candidateID, body.Reason)
	}

	// Record gate event
	s.recordGateEventStatic(r.Context(), candidateID, "manual_rollback", r,
		fmt.Sprintf("Manual rollback: %s", body.Reason),
		map[string]any{"reason": body.Reason, "slug": candidate.StyleSlug})

	// Record audit log
	s.writeAuditLog(r, "evolution_rollback", "evolution_candidate", candidateID,
		fmt.Sprintf("Canary rolled back for style %s: %s", candidate.StyleSlug, body.Reason),
		map[string]any{"candidate_id": candidateID, "reason": body.Reason, "style_slug": candidate.StyleSlug})

	slog.Info("canary rollout rolled back", "candidate_id", candidateID, "reason", body.Reason, "slug", candidate.StyleSlug)

	response.OK(w, map[string]any{
		"candidate_id": candidateID,
		"status":       "rejected",
		"reason":       body.Reason,
		"message":      "canary rollout rolled back, reverted to previous version",
	})
}

// handleAdminPromoteToFull promotes a canary rollout to full rollout (100%).
//
// POST /api/v2/admin/evolution/candidates/{id}/promote
func (s *Server) handleAdminPromoteToFull(w http.ResponseWriter, r *http.Request) {
	if s.evolutionSvc == nil {
		response.Err(w, http.StatusServiceUnavailable, "service_unavailable", "evolution service not available")
		return
	}

	candidateID := chi.URLParam(r, "id")
	if candidateID == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "candidate ID is required")
		return
	}

	// Get candidate from DB
	candidate, err := s.getEvolutionCandidate(r.Context(), candidateID)
	if err != nil {
		response.Err(w, http.StatusNotFound, "not_found", "candidate not found")
		return
	}

	if candidate.Status != "rollout" {
		response.Err(w, http.StatusBadRequest, "invalid_status", "candidate must be in rollout phase to promote")
		return
	}

	// Mark canary rollout as ended in DB
	if s.db != nil {
		_, err = s.db.ExecContext(r.Context(), `
			UPDATE canary_rollouts
			SET enabled = FALSE, ended_at = NOW()
			WHERE candidate_id = $1::uuid AND enabled = TRUE
		`, candidateID)
		if err != nil {
			slog.Warn("failed to end canary rollout in DB", "error", err)
		}
	}

	// Set the profile Loader's RolloutConfig to full (new version)
	if s.profiles != nil {
		config := routing.RolloutConfig{
			Type:            "full",
			RolloutPercent:  100,
			FallbackVersion: candidate.ParentVersion + 1,
		}
		if err := s.profiles.UpdateRolloutConfig(candidate.StyleSlug, config); err != nil {
			slog.Warn("failed to promote rollout config to full", "slug", candidate.StyleSlug, "error", err)
		}
	}

	// Update candidate status to "active" (fully promoted)
	_ = s.updateEvolutionCandidateStatus(r.Context(), candidateID, "active")

	// Record gate event
	s.recordGateEventStatic(r.Context(), candidateID, "promoted", r,
		fmt.Sprintf("Promoted to full rollout (v%d)", candidate.ParentVersion+1),
		map[string]any{"version": candidate.ParentVersion + 1, "slug": candidate.StyleSlug})

	// Record audit log
	s.writeAuditLog(r, "evolution_promote", "evolution_candidate", candidateID,
		fmt.Sprintf("Promoted to full rollout for style %s v%d", candidate.StyleSlug, candidate.ParentVersion+1),
		map[string]any{"candidate_id": candidateID, "version": candidate.ParentVersion + 1, "style_slug": candidate.StyleSlug})

	slog.Info("canary promoted to full rollout", "candidate_id", candidateID, "slug", candidate.StyleSlug, "version", candidate.ParentVersion+1)

	response.OK(w, map[string]any{
		"candidate_id": candidateID,
		"status":       "active",
		"version":      candidate.ParentVersion + 1,
		"message":      "promoted to full rollout (100%)",
	})
}

// handleAdminGetCanaryMetrics returns the routing metrics for the canary rollout.
//
// GET /api/v2/admin/evolution/candidates/{id}/metrics
func (s *Server) handleAdminGetCanaryMetrics(w http.ResponseWriter, r *http.Request) {
	if s.evolutionSvc == nil {
		response.Err(w, http.StatusServiceUnavailable, "service_unavailable", "evolution service not available")
		return
	}

	candidateID := chi.URLParam(r, "id")
	if candidateID == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "candidate ID is required")
		return
	}

	// Get candidate for slug + version info
	candidate, err := s.getEvolutionCandidate(r.Context(), candidateID)
	if err != nil {
		response.Err(w, http.StatusNotFound, "not_found", "candidate not found")
		return
	}

	// Get routing metrics — read directly from the global atomic counters
	// (do NOT copy the struct, as atomic.Int64 contains noCopy)
	// Get the rollout config for this style
	var config routing.RolloutConfig
	if s.profiles != nil {
		config = s.profiles.GetRolloutConfig(candidate.StyleSlug)
	}

	response.OK(w, map[string]any{
		"candidate_id":   candidateID,
		"style_slug":     candidate.StyleSlug,
		"rollout_config": config,
		"new_version":    candidate.ParentVersion + 1,
		"old_version":    candidate.ParentVersion,
		"metrics": map[string]int64{
			"total":       routing.RolloutMetrics.Requests.Load(),
			"new_version": routing.RolloutMetrics.NewVersion.Load(),
			"old_version": routing.RolloutMetrics.OldVersion.Load(),
			"whitelist":   routing.RolloutMetrics.Whitelist.Load(),
			"percentage":  routing.RolloutMetrics.Percentage.Load(),
			"errors":      routing.RolloutMetrics.Errors.Load(),
		},
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
	EvalRunID       string                 `json:"eval_run_id,omitempty"`
	EvalScore       *float64               `json:"eval_score,omitempty"`
	EvalPassed      *bool                  `json:"eval_passed,omitempty"`
	EvalCompletedAt *time.Time             `json:"eval_completed_at,omitempty"`
	RejectedReason  string                 `json:"rejected_reason,omitempty"`
	ApprovedBy      string                 `json:"approved_by,omitempty"`
	ApprovedAt      *time.Time             `json:"approved_at,omitempty"`
}

type canaryRolloutInfo struct {
	ID           string     `json:"id"`
	StyleSlug    string     `json:"style_slug"`
	Version      int        `json:"version"`
	CandidateID  string     `json:"candidate_id"`
	Percentage   float64    `json:"percentage"`
	Enabled      bool       `json:"enabled"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	EndedAt      *time.Time `json:"ended_at,omitempty"`
	RollbackReason string   `json:"rollback_reason,omitempty"`
}

func (s *Server) listEvolutionCandidatesFromDB(ctx context.Context) ([]evolutionCandidate, error) {
	if s.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id::text, style_slug, parent_version, changes,
		       status, COALESCE(eval_baseline_id::text, ''), COALESCE(eval_candidate_id::text, ''),
		       created_at,
		       COALESCE(eval_run_id::text, ''),
		       eval_score, eval_passed, eval_completed_at,
		       COALESCE(rejected_reason, ''), COALESCE(approved_by, ''), approved_at
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
			&c.Status, &c.EvalBaselineID, &c.EvalCandidateID, &c.CreatedAt,
			&c.EvalRunID, &c.EvalScore, &c.EvalPassed, &c.EvalCompletedAt,
			&c.RejectedReason, &c.ApprovedBy, &c.ApprovedAt); err != nil {
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
		       created_at,
		       COALESCE(eval_run_id::text, ''),
		       eval_score, eval_passed, eval_completed_at,
		       COALESCE(rejected_reason, ''), COALESCE(approved_by, ''), approved_at
		FROM style_profile_candidates
		WHERE id = $1::uuid
	`, id).Scan(&c.ID, &c.StyleSlug, &c.ParentVersion, &changesJSON,
		&c.Status, &c.EvalBaselineID, &c.EvalCandidateID, &c.CreatedAt,
		&c.EvalRunID, &c.EvalScore, &c.EvalPassed, &c.EvalCompletedAt,
		&c.RejectedReason, &c.ApprovedBy, &c.ApprovedAt)
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

func (s *Server) getActiveCanaryRollout(ctx context.Context, candidateID string) (*canaryRolloutInfo, error) {
	if s.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	var r canaryRolloutInfo
	err := s.db.QueryRowContext(ctx, `
		SELECT id::text, style_slug, version, candidate_id::text,
		       percentage, enabled, started_at, ended_at, COALESCE(rollback_reason, '')
		FROM canary_rollouts
		WHERE candidate_id = $1::uuid AND enabled = TRUE
		ORDER BY created_at DESC
		LIMIT 1
	`, candidateID).Scan(&r.ID, &r.StyleSlug, &r.Version, &r.CandidateID,
		&r.Percentage, &r.Enabled, &r.StartedAt, &r.EndedAt, &r.RollbackReason)
	if err != nil {
		return nil, err
	}
	// DB stores percentage as 0-1 decimal; convert to 0-100 for consistent API output
	if r.Percentage <= 1.0 {
		r.Percentage *= 100
	}
	return &r, nil
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

// recordGateEventStatic is a static method version of recordGateEvent that can be called
// from contexts without an HTTP request (e.g. async goroutines).
func (s *Server) recordGateEventStatic(ctx context.Context, candidateID, eventType string, r *http.Request,
	detail string, metadata map[string]any) {
	if s.db == nil {
		return
	}

	actorID := "system"
	actorType := "system"
	if r != nil {
		if user := userFromContext(r.Context()); user != nil {
			actorID = user.Sub
			actorType = "admin"
		}
	}

	metaJSON, _ := json.Marshal(metadata)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO evolution_gate_events (candidate_id, event_type, actor_id, actor_type, detail, metadata)
		VALUES ($1::uuid, $2, $3, $4, $5, $6)
	`, candidateID, eventType, actorID, actorType, detail, metaJSON)
	if err != nil {
		slog.Warn("failed to record gate event", "error", err)
	}
}

// nullableUUID returns a string suitable for UUID column insertion.
// Empty string is converted to nil to avoid invalid UUID errors.
func nullableUUID(s string) any {
	if s == "" {
		return nil
	}
	return s
}

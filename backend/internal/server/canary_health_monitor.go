package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/routing"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── Canary Health Monitor ──────────────────────────────
//
// Background goroutine that periodically checks active canary rollouts.
// When error rate exceeds the configured threshold, it automatically
// rolls back the canary and records a health snapshot.
//
// The monitor also records periodic health snapshots to the database
// for historical analysis and auditing.

// CanaryHealthMonitor runs in the background to monitor active canary rollouts.
type CanaryHealthMonitor struct {
	server   *Server
	interval time.Duration
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// NewCanaryHealthMonitor creates a new health monitor with the given check interval.
func NewCanaryHealthMonitor(server *Server, interval time.Duration) *CanaryHealthMonitor {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &CanaryHealthMonitor{
		server:   server,
		interval: interval,
		stopCh:   make(chan struct{}),
	}
}

// Start launches the health monitor goroutine.
func (m *CanaryHealthMonitor) Start() {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		ticker := time.NewTicker(m.interval)
		defer ticker.Stop()
		slog.Info("canary health monitor started", "interval", m.interval)
		for {
			select {
			case <-ticker.C:
				m.checkAllCanaries()
			case <-m.stopCh:
				slog.Info("canary health monitor stopped")
				return
			}
		}
	}()
}

// Stop gracefully stops the health monitor.
func (m *CanaryHealthMonitor) Stop() {
	close(m.stopCh)
	m.wg.Wait()
}

// checkAllCanaries queries the DB for all active canary rollouts and checks their health.
func (m *CanaryHealthMonitor) checkAllCanaries() {
	if m.server.db == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Get all active canary rollouts with their candidate info
	rows, err := m.server.db.QueryContext(ctx, `
		SELECT cr.candidate_id::text, cr.style_slug, cr.version, cr.percentage, cr.started_at
		FROM canary_rollouts cr
		WHERE cr.enabled = TRUE
	`)
	if err != nil {
		slog.Warn("canary monitor: failed to query active rollouts", "error", err)
		return
	}
	defer rows.Close()

	type activeRollout struct {
		CandidateID string
		StyleSlug   string
		Version     int
		Percentage  float64
		StartedAt   time.Time
	}

	var rollouts []activeRollout
	for rows.Next() {
		var r activeRollout
		if err := rows.Scan(&r.CandidateID, &r.StyleSlug, &r.Version, &r.Percentage, &r.StartedAt); err != nil {
			continue
		}
		rollouts = append(rollouts, r)
	}

	if len(rollouts) == 0 {
		return
	}

	for _, r := range rollouts {
		m.checkCanaryHealth(ctx, r.CandidateID, r.StyleSlug, r.Version, r.StartedAt)
	}
}

// checkCanaryHealth evaluates the health of a single canary rollout.
func (m *CanaryHealthMonitor) checkCanaryHealth(ctx context.Context, candidateID, styleSlug string, version int, startedAt time.Time) {
	// Get current routing metrics
	metrics := routing.RolloutMetrics
	totalRequests := metrics.Requests.Load()
	newVersionHits := metrics.NewVersion.Load()
	errorCount := metrics.Errors.Load()

	// Calculate error rate
	var errorRate float64
	if totalRequests > 0 {
		errorRate = float64(errorCount) / float64(totalRequests) * 100
	}

	// Calculate uptime (non-error requests / total)
	var uptimePct float64 = 100
	if totalRequests > 0 {
		uptimePct = float64(totalRequests-errorCount) / float64(totalRequests) * 100
	}

	// Get gate config for this style
	config := m.server.getGateConfig(ctx, styleSlug)
	if config == nil {
		// Use defaults
		config = &GateConfig{
			ErrorRateThreshold:  10.0,
			MinSampleSize:       50,
			AutoRollbackEnabled: true,
		}
	}

	// Record health snapshot
	m.recordHealthSnapshot(ctx, candidateID, styleSlug, int(totalRequests), int(newVersionHits),
		int(totalRequests-newVersionHits), int(errorCount), errorRate, uptimePct)

	// Check if we have enough samples and rollback is needed
	if totalRequests < int64(config.MinSampleSize) {
		slog.Debug("canary monitor: insufficient samples",
			"candidate_id", candidateID,
			"total", totalRequests,
			"min_required", config.MinSampleSize)
		return
	}

	// Check observation window
	elapsed := time.Since(startedAt)
	if elapsed < time.Duration(config.ObservationWindowMin)*time.Minute {
		slog.Debug("canary monitor: within observation window",
			"candidate_id", candidateID,
			"elapsed", elapsed,
			"window_min", config.ObservationWindowMin)
		return
	}

	// Auto-rollback check
	if config.AutoRollbackEnabled && errorRate > config.ErrorRateThreshold {
		slog.Warn("canary monitor: auto-rollback triggered",
			"candidate_id", candidateID,
			"style_slug", styleSlug,
			"error_rate", errorRate,
			"threshold", config.ErrorRateThreshold,
			"total_requests", totalRequests)

		reason := fmt.Sprintf("auto-rollback: error rate %.2f%% exceeds threshold %.2f%%",
			errorRate, config.ErrorRateThreshold)

		// Perform rollback
		m.autoRollback(ctx, candidateID, styleSlug, reason)

		// Record gate event
		m.recordGateEvent(ctx, candidateID, "auto_rollback", "system", "system",
			reason, map[string]any{
				"error_rate":     errorRate,
				"threshold":      config.ErrorRateThreshold,
				"total_requests": totalRequests,
				"error_count":    errorCount,
			})
		return
	}

	// Auto-promote check
	if config.AutoPromoteEnabled && uptimePct >= config.AutoPromoteMinUptime {
		promoteWindow := time.Duration(config.AutoPromoteWindowMin) * time.Minute
		if elapsed >= promoteWindow {
			slog.Info("canary monitor: auto-promote triggered",
				"candidate_id", candidateID,
				"style_slug", styleSlug,
				"uptime", uptimePct,
				"uptime_threshold", config.AutoPromoteMinUptime)

			m.autoPromote(ctx, candidateID, styleSlug, version)

			m.recordGateEvent(ctx, candidateID, "auto_promote", "system", "system",
				fmt.Sprintf("auto-promote: uptime %.2f%% meets threshold %.2f%%",
					uptimePct, config.AutoPromoteMinUptime),
				map[string]any{
					"uptime":         uptimePct,
					"threshold":      config.AutoPromoteMinUptime,
					"total_requests": totalRequests,
				})
		}
	}
}

// autoRollback performs an automatic rollback of a canary rollout.
func (m *CanaryHealthMonitor) autoRollback(ctx context.Context, candidateID, styleSlug, reason string) {
	// Disable canary in DB
	_, _ = m.server.db.ExecContext(ctx, `
		UPDATE canary_rollouts
		SET enabled = FALSE, ended_at = NOW(), rollback_reason = $2
		WHERE candidate_id = $1::uuid AND enabled = TRUE
	`, candidateID, reason)

	// Revert routing config
	if m.server.profiles != nil {
		// Get candidate's parent version for fallback
		var parentVersion int
		m.server.db.QueryRowContext(ctx, `
			SELECT parent_version FROM style_profile_candidates WHERE id = $1::uuid
		`, candidateID).Scan(&parentVersion)

		config := routing.RolloutConfig{
			Type:            "full",
			RolloutPercent:  100,
			FallbackVersion: parentVersion,
		}
		if err := m.server.profiles.UpdateRolloutConfig(styleSlug, config); err != nil {
			slog.Warn("canary monitor: failed to revert rollout config",
				"slug", styleSlug, "error", err)
		}
	}

	// Update candidate status
	_ = m.server.updateEvolutionCandidateStatus(ctx, candidateID, "rejected")

	// Update the rejected_reason
	_, _ = m.server.db.ExecContext(ctx, `
		UPDATE style_profile_candidates SET rejected_reason = $2
		WHERE id = $1::uuid
	`, candidateID, reason)

	slog.Info("canary monitor: auto-rollback completed",
		"candidate_id", candidateID, "style_slug", styleSlug, "reason", reason)
}

// autoPromote performs an automatic promotion of a canary to full rollout.
func (m *CanaryHealthMonitor) autoPromote(ctx context.Context, candidateID, styleSlug string, version int) {
	// End canary in DB
	_, _ = m.server.db.ExecContext(ctx, `
		UPDATE canary_rollouts
		SET enabled = FALSE, ended_at = NOW()
		WHERE candidate_id = $1::uuid AND enabled = TRUE
	`, candidateID)

	// Set routing config to full (new version)
	if m.server.profiles != nil {
		config := routing.RolloutConfig{
			Type:            "full",
			RolloutPercent:  100,
			FallbackVersion: version,
		}
		if err := m.server.profiles.UpdateRolloutConfig(styleSlug, config); err != nil {
			slog.Warn("canary monitor: failed to promote rollout config",
				"slug", styleSlug, "error", err)
		}
	}

	// Update candidate status
	_ = m.server.updateEvolutionCandidateStatus(ctx, candidateID, "active")

	slog.Info("canary monitor: auto-promote completed",
		"candidate_id", candidateID, "style_slug", styleSlug, "version", version)
}

// recordHealthSnapshot saves a point-in-time health snapshot to the database.
func (m *CanaryHealthMonitor) recordHealthSnapshot(ctx context.Context, candidateID, styleSlug string,
	total, newVersion, oldVersion, errors int, errorRate, uptime float64) {
	if m.server.db == nil {
		return
	}

	triggered := errorRate > 10.0 // default threshold
	var reason string
	if triggered {
		reason = fmt.Sprintf("error rate %.2f%% exceeds 10%%", errorRate)
	}

	_, err := m.server.db.ExecContext(ctx, `
		INSERT INTO canary_health_snapshots
			(candidate_id, style_slug, total_requests, new_version_hits, old_version_hits,
			 error_count, error_rate, uptime_pct, triggered_rollback, rollback_reason)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, candidateID, styleSlug, total, newVersion, oldVersion, errors, errorRate, uptime, triggered, reason)
	if err != nil {
		slog.Warn("canary monitor: failed to record health snapshot", "error", err)
	}
}

// recordGateEvent records a gate event to the audit trail.
func (m *CanaryHealthMonitor) recordGateEvent(ctx context.Context, candidateID, eventType, actorID, actorType, detail string, metadata map[string]any) {
	if m.server.db == nil {
		return
	}

	metaJSON, _ := json.Marshal(metadata)
	_, err := m.server.db.ExecContext(ctx, `
		INSERT INTO evolution_gate_events (candidate_id, event_type, actor_id, actor_type, detail, metadata)
		VALUES ($1::uuid, $2, $3, $4, $5, $6)
	`, candidateID, eventType, actorID, actorType, detail, metaJSON)
	if err != nil {
		slog.Warn("canary monitor: failed to record gate event", "error", err)
	}
}

// ─── GateConfig struct ──────────────────────────────────

// GateConfig holds configurable thresholds for evolution gate decisions.
type GateConfig struct {
	ID                    string  `json:"id"`
	StyleSlug             string  `json:"style_slug"`
	MinEvalScore          float64 `json:"min_eval_score"`
	MaxRegressionDrop     float64 `json:"max_regression_drop"`
	ErrorRateThreshold    float64 `json:"error_rate_threshold"`
	MinSampleSize         int     `json:"min_sample_size"`
	ObservationWindowMin  int     `json:"observation_window_min"`
	AutoRollbackEnabled   bool    `json:"auto_rollback_enabled"`
	AutoPromoteEnabled    bool    `json:"auto_promote_enabled"`
	AutoPromoteMinUptime  float64 `json:"auto_promote_min_uptime"`
	AutoPromoteWindowMin  int     `json:"auto_promote_window_min"`
	CreatedAt             string  `json:"created_at"`
	UpdatedAt             string  `json:"updated_at"`
}

// getGateConfig retrieves the gate config for a style slug from the database.
func (s *Server) getGateConfig(ctx context.Context, styleSlug string) *GateConfig {
	if s.db == nil {
		return nil
	}

	var g GateConfig
	err := s.db.QueryRowContext(ctx, `
		SELECT id::text, style_slug,
		       min_eval_score::float8, max_regression_drop::float8,
		       error_rate_threshold::float8, min_sample_size,
		       observation_window_min, auto_rollback_enabled,
		       auto_promote_enabled, auto_promote_min_uptime::float8,
		       auto_promote_window_min,
		       created_at::text, updated_at::text
		FROM evolution_gate_configs
		WHERE style_slug = $1
	`, styleSlug).Scan(&g.ID, &g.StyleSlug, &g.MinEvalScore, &g.MaxRegressionDrop,
		&g.ErrorRateThreshold, &g.MinSampleSize, &g.ObservationWindowMin,
		&g.AutoRollbackEnabled, &g.AutoPromoteEnabled, &g.AutoPromoteMinUptime,
		&g.AutoPromoteWindowMin, &g.CreatedAt, &g.UpdatedAt)
	if err != nil {
		return nil
	}
	return &g
}

// ─── Admin: Gate Config API ─────────────────────────────

// handleAdminGetGateConfig returns the gate configuration for a style.
//
// GET /api/v2/admin/evolution/gate-config/{slug}
func (s *Server) handleAdminGetGateConfig(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	slug := chi.URLParam(r, "slug")
	config := s.getGateConfig(r.Context(), slug)
	if config == nil {
		// Return default config
		response.OK(w, map[string]any{
			"style_slug":              slug,
			"min_eval_score":          3.0,
			"max_regression_drop":     0.3,
			"error_rate_threshold":    10.0,
			"min_sample_size":         50,
			"observation_window_min":  10,
			"auto_rollback_enabled":   true,
			"auto_promote_enabled":    false,
			"auto_promote_min_uptime": 99.0,
			"auto_promote_window_min": 30,
			"is_default":              true,
		})
		return
	}

	response.OK(w, config)
}

// handleAdminUpdateGateConfig creates or updates the gate configuration for a style.
//
// PUT /api/v2/admin/evolution/gate-config/{slug}
func (s *Server) handleAdminUpdateGateConfig(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	slug := chi.URLParam(r, "slug")
	if slug == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "style slug is required")
		return
	}

	var body struct {
		MinEvalScore          *float64 `json:"min_eval_score"`
		MaxRegressionDrop     *float64 `json:"max_regression_drop"`
		ErrorRateThreshold    *float64 `json:"error_rate_threshold"`
		MinSampleSize         *int     `json:"min_sample_size"`
		ObservationWindowMin  *int     `json:"observation_window_min"`
		AutoRollbackEnabled   *bool    `json:"auto_rollback_enabled"`
		AutoPromoteEnabled    *bool    `json:"auto_promote_enabled"`
		AutoPromoteMinUptime  *float64 `json:"auto_promote_min_uptime"`
		AutoPromoteWindowMin  *int     `json:"auto_promote_window_min"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	// Upsert config
	_, err := s.db.ExecContext(r.Context(), `
		INSERT INTO evolution_gate_configs
			(style_slug, min_eval_score, max_regression_drop, error_rate_threshold,
			 min_sample_size, observation_window_min, auto_rollback_enabled,
			 auto_promote_enabled, auto_promote_min_uptime, auto_promote_window_min)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (style_slug) DO UPDATE SET
			min_eval_score = EXCLUDED.min_eval_score,
			max_regression_drop = EXCLUDED.max_regression_drop,
			error_rate_threshold = EXCLUDED.error_rate_threshold,
			min_sample_size = EXCLUDED.min_sample_size,
			observation_window_min = EXCLUDED.observation_window_min,
			auto_rollback_enabled = EXCLUDED.auto_rollback_enabled,
			auto_promote_enabled = EXCLUDED.auto_promote_enabled,
			auto_promote_min_uptime = EXCLUDED.auto_promote_min_uptime,
			auto_promote_window_min = EXCLUDED.auto_promote_window_min,
			updated_at = NOW()
	`,
		slug,
		coalesceFloat(body.MinEvalScore, 3.0),
		coalesceFloat(body.MaxRegressionDrop, 0.3),
		coalesceFloat(body.ErrorRateThreshold, 10.0),
		coalesceInt(body.MinSampleSize, 50),
		coalesceInt(body.ObservationWindowMin, 10),
		coalesceBool(body.AutoRollbackEnabled, true),
		coalesceBool(body.AutoPromoteEnabled, false),
		coalesceFloat(body.AutoPromoteMinUptime, 99.0),
		coalesceInt(body.AutoPromoteWindowMin, 30),
	)
	if err != nil {
		slog.Warn("failed to upsert gate config", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to save gate config")
		return
	}

	s.writeAuditLog(r, "update_gate_config", "evolution_gate_config", slug,
		fmt.Sprintf("Updated gate config for style %s", slug), map[string]any{
			"style_slug": slug,
			"body":       body,
		})

	slog.Info("gate config updated", "slug", slug)

	// Return updated config
	config := s.getGateConfig(r.Context(), slug)
	response.OK(w, config)
}

// handleAdminListGateConfigs returns all gate configurations.
//
// GET /api/v2/admin/evolution/gate-configs
func (s *Server) handleAdminListGateConfigs(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		response.OK(w, map[string]any{"configs": []any{}, "total": 0})
		return
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id::text, style_slug,
		       min_eval_score::float8, max_regression_drop::float8,
		       error_rate_threshold::float8, min_sample_size,
		       observation_window_min, auto_rollback_enabled,
		       auto_promote_enabled, auto_promote_min_uptime::float8,
		       auto_promote_window_min,
		       created_at::text, updated_at::text
		FROM evolution_gate_configs
		ORDER BY style_slug
	`)
	if err != nil {
		response.OK(w, map[string]any{"configs": []any{}, "total": 0})
		return
	}
	defer rows.Close()

	var configs []GateConfig
	for rows.Next() {
		var g GateConfig
		if err := rows.Scan(&g.ID, &g.StyleSlug, &g.MinEvalScore, &g.MaxRegressionDrop,
			&g.ErrorRateThreshold, &g.MinSampleSize, &g.ObservationWindowMin,
			&g.AutoRollbackEnabled, &g.AutoPromoteEnabled, &g.AutoPromoteMinUptime,
			&g.AutoPromoteWindowMin, &g.CreatedAt, &g.UpdatedAt); err != nil {
			continue
		}
		configs = append(configs, g)
	}

	response.OK(w, map[string]any{
		"configs": configs,
		"total":   len(configs),
	})
}

// handleAdminGetGateEvents returns the gate event audit trail for a candidate.
//
// GET /api/v2/admin/evolution/candidates/{id}/events
func (s *Server) handleAdminGetGateEvents(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		response.OK(w, map[string]any{"events": []any{}, "total": 0})
		return
	}

	candidateID := chi.URLParam(r, "id")
	if candidateID == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "candidate ID is required")
		return
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id::text, candidate_id::text, event_type, actor_id, actor_type,
		       detail, metadata, created_at::text
		FROM evolution_gate_events
		WHERE candidate_id = $1::uuid
		ORDER BY created_at DESC
		LIMIT 100
	`, candidateID)
	if err != nil {
		response.OK(w, map[string]any{"events": []any{}, "total": 0})
		return
	}
	defer rows.Close()

	type GateEvent struct {
		ID          string                 `json:"id"`
		CandidateID string                 `json:"candidate_id"`
		EventType   string                 `json:"event_type"`
		ActorID     string                 `json:"actor_id"`
		ActorType   string                 `json:"actor_type"`
		Detail      string                 `json:"detail"`
		Metadata    map[string]interface{} `json:"metadata"`
		CreatedAt   string                 `json:"created_at"`
	}

	var events []GateEvent
	for rows.Next() {
		var e GateEvent
		var metaJSON []byte
		if err := rows.Scan(&e.ID, &e.CandidateID, &e.EventType, &e.ActorID, &e.ActorType,
			&e.Detail, &metaJSON, &e.CreatedAt); err != nil {
			continue
		}
		if len(metaJSON) > 0 {
			json.Unmarshal(metaJSON, &e.Metadata)
		}
		events = append(events, e)
	}

	response.OK(w, map[string]any{
		"events": events,
		"total":  len(events),
	})
}

// handleAdminGetHealthSnapshots returns health snapshots for a candidate.
//
// GET /api/v2/admin/evolution/candidates/{id}/health
func (s *Server) handleAdminGetHealthSnapshots(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		response.OK(w, map[string]any{"snapshots": []any{}, "total": 0})
		return
	}

	candidateID := chi.URLParam(r, "id")
	if candidateID == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "candidate ID is required")
		return
	}

	limit := 60 // default 60 data points
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id::text, candidate_id::text, style_slug,
		       total_requests, new_version_hits, old_version_hits,
		       error_count, error_rate::float8, uptime_pct::float8,
		       triggered_rollback, COALESCE(rollback_reason, ''),
		       captured_at::text
		FROM canary_health_snapshots
		WHERE candidate_id = $1::uuid
		ORDER BY captured_at DESC
		LIMIT $2
	`, candidateID, limit)
	if err != nil {
		response.OK(w, map[string]any{"snapshots": []any{}, "total": 0})
		return
	}
	defer rows.Close()

	type Snapshot struct {
		ID                string  `json:"id"`
		CandidateID       string  `json:"candidate_id"`
		StyleSlug         string  `json:"style_slug"`
		TotalRequests     int     `json:"total_requests"`
		NewVersionHits    int     `json:"new_version_hits"`
		OldVersionHits    int     `json:"old_version_hits"`
		ErrorCount        int     `json:"error_count"`
		ErrorRate         float64 `json:"error_rate"`
		UptimePct         float64 `json:"uptime_pct"`
		TriggeredRollback bool    `json:"triggered_rollback"`
		RollbackReason    string  `json:"rollback_reason"`
		CapturedAt        string  `json:"captured_at"`
	}

	var snapshots []Snapshot
	for rows.Next() {
		var snap Snapshot
		if err := rows.Scan(&snap.ID, &snap.CandidateID, &snap.StyleSlug,
			&snap.TotalRequests, &snap.NewVersionHits, &snap.OldVersionHits,
			&snap.ErrorCount, &snap.ErrorRate, &snap.UptimePct,
			&snap.TriggeredRollback, &snap.RollbackReason, &snap.CapturedAt); err != nil {
			continue
		}
		snapshots = append(snapshots, snap)
	}

	response.OK(w, map[string]any{
		"snapshots": snapshots,
		"total":     len(snapshots),
	})
}

// ─── Helpers ────────────────────────────────────────────

func coalesceFloat(v *float64, def float64) float64 {
	if v == nil {
		return def
	}
	return *v
}

func coalesceInt(v *int, def int) int {
	if v == nil {
		return def
	}
	return *v
}

func coalesceBool(v *bool, def bool) bool {
	if v == nil {
		return def
	}
	return *v
}

// roundFloat rounds a float64 to 2 decimal places.
func roundFloat(v float64) float64 {
	return math.Round(v*100) / 100
}

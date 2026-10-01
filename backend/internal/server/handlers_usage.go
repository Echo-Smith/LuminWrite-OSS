package server

import (
	"net/http"
	"strconv"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── Per-user Usage Handlers ─────────────────────────────

// handleGetMyUsage returns the caller's own aggregate usage (trace count and
// token spend) over a lookback window, aggregated from agent_traces.
// GET /api/v2/usage?days=30
func (s *Server) handleGetMyUsage(w http.ResponseWriter, r *http.Request) {
	if s.traces == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}
	user := userFromContext(r.Context())
	if user == nil || user.Sub == "" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	days := 30
	if raw := r.URL.Query().Get("days"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			days = n
		}
	}

	stats, err := s.traces.GetUserUsageStats(r.Context(), user.Sub, days)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to aggregate usage")
		return
	}
	response.OK(w, map[string]interface{}{
		"days":  days,
		"usage": stats,
	})
}

package server

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── User Preferences Handlers ───────────────────────────
//
// GET  /api/v2/preferences        — get all preferences for the authenticated user
// PUT  /api/v2/preferences        — upsert preferences (merge mode)
//
// Preferences are stored as key-value pairs in the user_preferences table.
// Each key maps to a JSON-encoded value string.
//
// Currently used keys:
//   - agent_mode: "harness" | "pipeline" | "editorial"

// handleGetPreferences returns all preferences for the authenticated user.
func (s *Server) handleGetPreferences(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	if s.adminRepo == nil || s.adminRepo.DB() == nil {
		// DB not available — return defaults
		response.OK(w, map[string]interface{}{})
		return
	}

	rows, err := s.adminRepo.DB().QueryContext(r.Context(), `
		SELECT key, value FROM user_preferences WHERE user_id = $1
	`, user.Sub)
	if err != nil {
		slog.Warn("failed to query user preferences", "error", err, "user_id", user.Sub)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to load preferences")
		return
	}
	defer rows.Close()

	prefs := map[string]interface{}{}
	for rows.Next() {
		var key, rawValue string
		if err := rows.Scan(&key, &rawValue); err != nil {
			continue
		}
		// Try to JSON-decode the value; if it fails, use the raw string
		var val interface{}
		if json.Unmarshal([]byte(rawValue), &val) == nil {
			prefs[key] = val
		} else {
			prefs[key] = rawValue
		}
	}

	response.OK(w, prefs)
}

// handleUpdatePreferences upserts preferences for the authenticated user (merge mode).
// PUT /api/v2/preferences
// Body: { "agent_mode": "unified", ... }
func (s *Server) handleUpdatePreferences(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	if s.adminRepo == nil || s.adminRepo.DB() == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	if len(body) == 0 {
		response.Err(w, http.StatusBadRequest, "bad_request", "no preferences to update")
		return
	}

	// Upsert each key-value pair
	tx, err := s.adminRepo.DB().BeginTx(r.Context(), nil)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to start transaction")
		return
	}
	defer tx.Rollback()

	for key, val := range body {
		// Validate key length
		if len(key) > 64 || len(key) == 0 {
			continue
		}

		// JSON-encode the value for consistent storage
		valueBytes, err := json.Marshal(val)
		if err != nil {
			continue
		}

		_, err = tx.ExecContext(r.Context(), `
			INSERT INTO user_preferences (user_id, key, value, updated_at)
			VALUES ($1, $2, $3, NOW())
			ON CONFLICT (user_id, key) DO UPDATE
				SET value = EXCLUDED.value, updated_at = NOW()
		`, user.Sub, key, string(valueBytes))
		if err != nil {
			slog.Warn("failed to upsert preference", "error", err, "key", key, "user_id", user.Sub)
		}
	}

	if err := tx.Commit(); err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to save preferences")
		return
	}

	response.OK(w, map[string]interface{}{"saved": true, "count": len(body)})
}

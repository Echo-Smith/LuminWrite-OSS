package server

import (
	"net/http"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── Admin: Security Audit ──────────────────────────────
//
// This endpoint exposes the prompt injection interception statistics
// collected by the guardrails module. It allows administrators to
// monitor how many injection attempts have been detected and sanitized,
// and view recent interception records for forensic analysis.

// handleAdminSecurityAudit returns prompt injection interception stats.
//
// GET /api/v2/admin/security/audit
//
// Response:
//
//	{
//	  "external_content_interceptions": 42,
//	  "user_input_interceptions": 3,
//	  "unique_sources": 15,
//	  "recent_interceptions": [
//	    {
//	      "source": "search_result[0].snippet",
//	      "pattern_count": 2,
//	      "timestamp": "2026-08-21T10:30:00Z"
//	    }
//	  ]
//	}
func (s *Server) handleAdminSecurityAudit(w http.ResponseWriter, r *http.Request) {
	externalCount, userCount, uniqueSources, recent := engine.GetInjectionStats()

	// Convert recent interceptions to the response format
	recentList := make([]map[string]any, 0, len(recent))
	for _, entry := range recent {
		recentList = append(recentList, map[string]any{
			"source":        entry.Source,
			"pattern_count": entry.PatternCount,
			"timestamp":     entry.Timestamp,
		})
	}

	response.OK(w, map[string]any{
		"external_content_interceptions": externalCount,
		"user_input_interceptions":       userCount,
		"unique_sources":                uniqueSources,
		"total_interceptions":           externalCount + userCount,
		"recent_interceptions":          recentList,
		"defense_layers": []string{
			"input_sanitization (SanitizeExternalContent + SanitizeUserInput)",
			"system_prompt_directive (7 defense rules)",
			"red_team_evaluation (20 adversarial test cases)",
		},
	})
}

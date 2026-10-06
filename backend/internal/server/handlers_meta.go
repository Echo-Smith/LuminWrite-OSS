package server

import (
	"net/http"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// handleDeploymentMeta exposes public, non-sensitive deployment flags so the
// frontend can shape its UI (hide registration entry points, single-user-only
// surfaces) at startup instead of probing auth failures.
//
// GET /api/v2/meta/deployment — public.
func (s *Server) handleDeploymentMeta(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]interface{}{
		"registration_disabled": s.cfg.Server.DisableRegistration,
	})
}

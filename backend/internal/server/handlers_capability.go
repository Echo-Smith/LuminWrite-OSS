// Unified capability inventory endpoint (V3.0 M1.5 slices 2–3, docs/25): the
// first consumer of the capability.Resolver. One admin-facing answer to
// "what can this process do" — every converged surface, every manifest, with
// the single-authority guard surfaced as a hard failure instead of a silent
// inventory. An optional ?role= query returns the role's view through the
// role policy (plumbing only; dispatch enforcement is the Role Capability
// Policy milestone).
package server

import (
	"net/http"
	"strings"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/capability"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

func (s *Server) handleAdminCapabilities(w http.ResponseWriter, r *http.Request) {
	resolver, err := s.newCapabilityResolver()
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "CAPABILITY_INVENTORY_UNAVAILABLE", "capability resolver could not be assembled")
		return
	}
	// The standing guard runs on every read: a namespace violation means a
	// surface started claiming another's identity and the inventory must not
	// present itself as healthy.
	if err := resolver.CheckSingleAuthority(); err != nil {
		response.Err(w, http.StatusInternalServerError, "CAPABILITY_AUTHORITY_CONFLICT", err.Error())
		return
	}
	manifests := []capability.UnifiedManifest{}
	for _, source := range resolver.Sources() {
		manifests = append(manifests, resolver.BySource(source)...)
	}
	payload := map[string]any{
		"sources":      resolver.Sources(),
		"capabilities": manifests,
		"total":        len(manifests),
	}
	if role := strings.TrimSpace(r.URL.Query().Get("role")); role != "" {
		visible, viewErr := resolver.VisibleToRole(role, editorialRolePolicy())
		if viewErr != nil {
			response.Err(w, http.StatusInternalServerError, "CAPABILITY_ROLE_VIEW_FAILED", viewErr.Error())
			return
		}
		payload["role"] = role
		payload["visible"] = visible
		payload["total"] = len(visible)
	}
	response.OK(w, payload)
}

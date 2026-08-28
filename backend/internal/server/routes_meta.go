package server

import (
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── Route Metadata & Discovery ──────────────────────────
//
// The route registry provides a machine-readable catalog of all API
// endpoints. This enables:
//   - Frontend visualization of available API routes
//   - Conditional enable/disable of endpoints
//   - Authentication requirement visibility
//   - Route grouping by category for UI rendering
//
// Inspired by dsh's profile/bundle configuration where route metadata
// is declared alongside implementation, and Pi Agent's stream-json
// output where capabilities are self-describing.

// RouteMeta describes a single API route for discovery/visualization.
type RouteMeta struct {
	// Method is the HTTP method (GET, POST, PUT, DELETE).
	Method string `json:"method"`

	// Path is the full route path (e.g. "/api/v2/sessions").
	Path string `json:"path"`

	// Description is a human-readable summary of the endpoint.
	Description string `json:"description,omitempty"`

	// Auth indicates the authentication requirement.
	// "none" = no auth, "jwt" = JWT required, "admin" = admin token required.
	Auth string `json:"auth"`

	// Category groups routes for UI display.
	// Common: "system", "auth", "styles", "topics", "memory", "kb",
	//         "materials", "evaluation", "tools", "admin", "editorial".
	Category string `json:"category,omitempty"`

	// WebSocket indicates whether the route is a WebSocket upgrade endpoint.
	WebSocket bool `json:"websocket,omitempty"`

	// SSE indicates whether the route is a Server-Sent Events endpoint.
	SSE bool `json:"sse,omitempty"`
}

// routeRegistry collects route metadata during route registration.
// It is populated by the registerRoute helper and exposed via the
// /api/v2/admin/routes endpoint.
type routeRegistry struct {
	routes []RouteMeta
}

// newRouteRegistry creates an empty route registry.
func newRouteRegistry() *routeRegistry {
	return &routeRegistry{routes: make([]RouteMeta, 0, 80)}
}

// add records a route's metadata.
func (rr *routeRegistry) add(method, path, description, auth, category string) {
	rr.routes = append(rr.routes, RouteMeta{
		Method:      method,
		Path:        path,
		Description: description,
		Auth:        auth,
		Category:    category,
	})
}

// addWebSocket records a WebSocket route.
func (rr *routeRegistry) addWebSocket(path, description string) {
	rr.routes = append(rr.routes, RouteMeta{
		Method:      "GET",
		Path:        path,
		Description: description,
		Auth:        "none",
		Category:    "realtime",
		WebSocket:   true,
	})
}

// addSSE records an SSE route.
func (rr *routeRegistry) addSSE(method, path, description, auth string) {
	rr.routes = append(rr.routes, RouteMeta{
		Method:      method,
		Path:        path,
		Description: description,
		Auth:        auth,
		Category:    "realtime",
		SSE:         true,
	})
}

// All returns all registered routes, sorted by path then method.
func (rr *routeRegistry) All() []RouteMeta {
	result := make([]RouteMeta, len(rr.routes))
	copy(result, rr.routes)
	sort.Slice(result, func(i, j int) bool {
		if result[i].Path != result[j].Path {
			return result[i].Path < result[j].Path
		}
		return result[i].Method < result[j].Method
	})
	return result
}

// Categories returns all unique categories.
func (rr *routeRegistry) Categories() []string {
	seen := make(map[string]bool)
	for _, r := range rr.routes {
		if r.Category != "" {
			seen[r.Category] = true
		}
	}
	result := make([]string, 0, len(seen))
	for cat := range seen {
		result = append(result, cat)
	}
	sort.Strings(result)
	return result
}

// handleAdminRoutes returns all registered API routes as metadata.
//
// GET /api/v2/admin/routes
//
// This endpoint is protected by admin authentication. It returns the
// complete list of API routes with their methods, descriptions, and
// authentication requirements. The frontend uses this to render a
// route visualization panel in the admin dashboard.
func (s *Server) handleAdminRoutes(w http.ResponseWriter, r *http.Request) {
	if s.routeReg == nil {
		response.OK(w, map[string]interface{}{
			"routes":     []interface{}{},
			"categories": []interface{}{},
			"total":      0,
		})
		return
	}

	routes := s.routeReg.All()
	categories := s.routeReg.Categories()

	response.OK(w, map[string]interface{}{
		"routes":     routes,
		"categories": categories,
		"total":      len(routes),
	})
}

// registerRoutesFromChi walks the chi router to discover all registered
// routes and populates the route registry. This is called after all
// routes are registered in Router().
//
// Since chi doesn't expose middleware info per-route, we infer auth
// level from the path prefix (admin routes require admin token, etc).
func (s *Server) registerRoutesFromChi(r chi.Router) {
	if s.routeReg == nil {
		return
	}

	walkFunc := func(method string, route string, handler http.Handler, middlewares ...func(http.Handler) http.Handler) error {
		// Skip chi internal routes
		if route == "/*" || route == "/" {
			return nil
		}

		// Infer auth and category from path
		auth := "none"
		category := inferCategory(route)
		if strings.HasPrefix(route, "/api/v2/admin") {
			auth = "admin"
		} else if strings.Contains(route, "/my-") ||
			strings.Contains(route, "/documents") ||
			strings.Contains(route, "/contracts") ||
			strings.Contains(route, "/runs") ||
			strings.Contains(route, "/preferences") ||
			strings.Contains(route, "/materials") ||
			strings.Contains(route, "/sessions") ||
			strings.Contains(route, "/memories/file") ||
			strings.Contains(route, "/memories/global") ||
			strings.Contains(route, "/auth/verify") ||
			strings.Contains(route, "/auth/passkey/list") ||
			strings.Contains(route, "/auth/change-password") ||
			strings.Contains(route, "/auth/bind-email") ||
			strings.Contains(route, "/auth/unbind-email") ||
			strings.Contains(route, "/auth/my-email") ||
			strings.Contains(route, "/auth/update-profile") ||
			strings.Contains(route, "/auth/deactivate") {
			auth = "jwt"
		}

		// Check for WebSocket/SSE routes
		isWS := strings.Contains(route, "/ws/")
		isSSE := strings.Contains(route, "/sse/") || strings.Contains(route, "/stream") || strings.HasSuffix(route, "/events")

		if isWS {
			s.routeReg.addWebSocket(route, "WebSocket endpoint")
		} else if isSSE {
			s.routeReg.addSSE(method, route, "Server-Sent Events endpoint", auth)
		} else {
			s.routeReg.add(method, route, "", auth, category)
		}

		return nil
	}

	_ = chi.Walk(r, walkFunc)
}

// inferCategory determines the route category from the path prefix.
func inferCategory(route string) string {
	switch {
	case strings.HasPrefix(route, "/health"), strings.HasPrefix(route, "/metrics"):
		return "system"
	case strings.HasPrefix(route, "/api/v2/admin"):
		return "admin"
	case strings.HasPrefix(route, "/api/v2/auth"):
		return "auth"
	case strings.HasPrefix(route, "/api/v2/styles"), strings.HasPrefix(route, "/api/v2/my-styles"):
		return "styles"
	case strings.HasPrefix(route, "/api/v2/style-builder"):
		return "styles"
	case strings.HasPrefix(route, "/api/v2/topics"):
		return "topics"
	case strings.HasPrefix(route, "/api/v2/feedback"):
		return "feedback"
	case strings.HasPrefix(route, "/api/v2/memories"):
		return "memory"
	case strings.HasPrefix(route, "/api/v2/preferences"):
		return "memory"
	case strings.HasPrefix(route, "/api/v2/kb"), strings.HasPrefix(route, "/api/v2/weknora"):
		return "kb"
	case strings.HasPrefix(route, "/api/v2/materials"):
		return "materials"
	case strings.HasPrefix(route, "/api/v2/sessions"):
		return "sessions"
	case strings.HasPrefix(route, "/api/v2/models"):
		return "models"
	case strings.HasPrefix(route, "/api/v2/reputation"):
		return "reputation"
	case strings.HasPrefix(route, "/api/v2/workbuddy"):
		return "workbuddy"
	case strings.HasPrefix(route, "/api/v2/evaluation"):
		return "evaluation"
	case strings.HasPrefix(route, "/api/v2/tools"):
		return "tools"
	case strings.HasPrefix(route, "/api/v2/editorial"):
		return "editorial"
	case strings.HasPrefix(route, "/api/v2/documents"), strings.HasPrefix(route, "/api/v2/contracts"), strings.HasPrefix(route, "/api/v2/runs"):
		return "writing"
	case strings.HasPrefix(route, "/api/v2/ws/"):
		return "realtime"
	case strings.HasPrefix(route, "/api/v2/sse/"), strings.Contains(route, "/stream"):
		return "realtime"
	default:
		return "other"
	}
}

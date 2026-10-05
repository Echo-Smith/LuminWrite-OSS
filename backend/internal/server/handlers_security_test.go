package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/config"
)

// newSecurityGateFixture builds a minimal Server with the default (publicly
// known) ADMIN_TOKEN and the security-gated routes mounted, to prove the
// anonymous-access holes stay closed.
func newSecurityGateFixture(t *testing.T) (*Server, *chi.Mux) {
	t.Helper()
	cfg := &config.Config{}
	cfg.JWT.Secret = "security-gate-secret"
	cfg.JWT.Expiry = time.Hour
	// cfg.Admin.Token left at the zero value "" — different from the dev
	// default; the middleware must reject anonymous access either way.

	s := &Server{cfg: cfg}
	router := chi.NewRouter()
	router.Route("/api/v2", func(r chi.Router) {
		// One representative admin route per middleware semantics.
		r.With(s.adminAuthMiddleware).Get("/admin/models", s.handleAdminListModelConfigs)
		// KB (authenticated group — representative route)
		r.With(s.jwtAuthMiddleware).Post("/kb/search", s.handleKBSearch)
		// Workbuddy callback + adoption history
		r.With(s.workbuddyCallbackGate).Post("/workbuddy/adopt", s.handleWorkbuddyAdoption)
		r.With(s.jwtAuthMiddleware).Get("/workbuddy/adoptions/{traceId}", s.handleAdoptionHistory)
	})
	return s, router
}

// TestAdminAnonymousRejected: removing the default-token bypass means no
// token → 401 on admin routes, even with a default-ish token configured.
func TestAdminAnonymousRejected(t *testing.T) {
	s, router := newSecurityGateFixture(t)
	s.cfg.Admin.Token = "dev-admin-token" // the publicly known default

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v2/admin/models", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous admin access with default token: got %d, want 401", rec.Code)
	}

	// The static-token channel still authenticates a caller presenting the
	// configured token (that is the documented admin-token flow).
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/admin/models", nil)
	req.Header.Set("Authorization", "Bearer dev-admin-token")
	router.ServeHTTP(rec, req)
	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("static admin token channel broke: got 401 for the configured token")
	}
}

// TestKBAnonymousRejected: the KB group requires a JWT.
func TestKBAnonymousRejected(t *testing.T) {
	_, router := newSecurityGateFixture(t)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v2/kb/search", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous KB search: got %d, want 401", rec.Code)
	}
}

// TestWorkbuddyCallbackGate: disabled by default; token-checked when set.
func TestWorkbuddyCallbackGate(t *testing.T) {
	s, router := newSecurityGateFixture(t)

	// Default: callback disabled.
	s.cfg.Server.WorkbuddyCallbackToken = ""
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v2/workbuddy/adopt", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("callback with no token configured: got %d, want 403", rec.Code)
	}

	// Configured: missing/wrong token → 401, right token passes the gate.
	s.cfg.Server.WorkbuddyCallbackToken = "cb-secret"
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v2/workbuddy/adopt", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("callback without token: got %d, want 401", rec.Code)
	}
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v2/workbuddy/adopt", nil)
	req.Header.Set("X-Callback-Token", "cb-secret")
	router.ServeHTTP(rec, req)
	// Passing the gate reaches the handler: an empty body fails JSON decode
	// (400) — anything but 401/403 proves the gate admitted the request.
	if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
		t.Fatalf("callback with correct token blocked by gate: got %d", rec.Code)
	}
}

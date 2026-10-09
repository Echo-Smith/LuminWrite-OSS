package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/config"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database/dbtest"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/services/packages"
)

// newPackagesFixture wires a minimal server with the package registry,
// installer, user-style store and admin repo over an isolated database.
func newPackagesFixture(t *testing.T) (*Server, *chi.Mux, string) {
	t.Helper()
	db, cleanup, err := dbtest.Open(os.Getenv("TEST_DATABASE_URL"), 4, 2)
	if err != nil {
		if err == dbtest.ErrNoDatabaseURL {
			t.Skip("TEST_DATABASE_URL not set")
		}
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	var userID string
	if err := db.QueryRowContext(context.Background(),
		`INSERT INTO users (uid, name, role) VALUES ('pkg-user', 'pkg-user', 'user') RETURNING id::text`,
	).Scan(&userID); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	cfg.JWT.Secret = "packages-secret"
	cfg.JWT.Expiry = time.Hour
	cfg.Packages.Dir = filepath.Join(t.TempDir(), "packages")

	adminRepo := database.NewAdminRepo(db)
	repo := database.NewInstalledPackageRepo(db)
	s := &Server{
		cfg:             cfg,
		db:              db,
		adminRepo:       adminRepo,
		userStyleStore:  database.NewUserStyleStore(db),
		packageRepo:     repo,
		packageInstaller: packages.NewInstaller(cfg.Packages.Dir, repo, database.NewUserStyleStore(db), adminRepo),
	}
	if err := os.MkdirAll(cfg.Packages.Dir, 0o755); err != nil {
		t.Fatal(err)
	}

	router := chi.NewRouter()
	router.Route("/api/v2", func(r chi.Router) {
		r.Get("/packages/catalog", s.handleListPackageCatalog)
		r.With(s.jwtAuthMiddleware, s.rejectGuestMiddleware).Get("/packages", s.handleListInstalledPackages)
		r.With(s.jwtAuthMiddleware, s.rejectGuestMiddleware).Post("/packages/install", s.handleInstallPackage)
		r.With(s.jwtAuthMiddleware, s.rejectGuestMiddleware).Delete("/packages/{id}", s.handleUninstallPackage)
	})
	return s, router, userID
}

func packagesRequest(t *testing.T, s *Server, router *chi.Mux, userID, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	token, err := s.GenerateJWT(userID, "user", "packages_session")
	if err != nil {
		t.Fatal(err)
	}
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, path, reader)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestPackageInstallUninstallRoundTrip(t *testing.T) {
	s, router, userID := newPackagesFixture(t)

	// Builtin catalog is served.
	rec := packagesRequest(t, s, router, userID, http.MethodGet, "/api/v2/packages/catalog", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("catalog: got %d", rec.Code)
	}
	var catalog struct {
		Data struct {
			Packages []struct {
				Kind string `json:"kind"`
				Slug string `json:"slug"`
			} `json:"packages"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog.Data.Packages) == 0 {
		t.Fatal("empty catalog")
	}

	// Install the builtin style package.
	rec = packagesRequest(t, s, router, userID, http.MethodPost, "/api/v2/packages/install",
		`{"source":"builtin","slug":"writer-starter"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("install style: got %d %s", rec.Code, rec.Body.String())
	}
	var installed struct {
		Data struct {
			Package        map[string]any `json:"package"`
			StyleProfileID string         `json:"style_profile_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &installed); err != nil {
		t.Fatal(err)
	}
	packageID, _ := installed.Data.Package["id"].(string)
	if packageID == "" || installed.Data.StyleProfileID == "" {
		t.Fatalf("install result incomplete: %s", rec.Body.String())
	}

	// The package body landed under the fixed install root.
	installPath, _ := installed.Data.Package["install_path"].(string)
	if !strings.HasPrefix(installPath, s.cfg.Packages.Dir) {
		t.Fatalf("install path %q outside root %q", installPath, s.cfg.Packages.Dir)
	}
	if _, err := os.Stat(filepath.Join(installPath, "style.json")); err != nil {
		t.Fatalf("package body missing on disk: %v", err)
	}

	// The style profile was created with a saved version (config applied).
	var versionCount int
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM user_style_profile_versions WHERE profile_id = $1`,
		installed.Data.StyleProfileID).Scan(&versionCount); err != nil {
		t.Fatal(err)
	}
	if versionCount != 1 {
		t.Fatalf("style versions = %d, want 1", versionCount)
	}

	// Duplicate install is a 409.
	rec = packagesRequest(t, s, router, userID, http.MethodPost, "/api/v2/packages/install",
		`{"source":"builtin","slug":"writer-starter"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate install: got %d, want 409", rec.Code)
	}

	// Uninstall removes the registry row, the profile and the directory.
	rec = packagesRequest(t, s, router, userID, http.MethodDelete, "/api/v2/packages/"+packageID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("uninstall: got %d %s", rec.Code, rec.Body.String())
	}
	var profileCount int
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM user_style_profiles WHERE id = $1`, installed.Data.StyleProfileID).Scan(&profileCount); err != nil {
		t.Fatal(err)
	}
	if profileCount != 0 {
		t.Fatal("style profile survived uninstall")
	}
	if _, err := os.Stat(installPath); !os.IsNotExist(err) {
		t.Fatal("package directory survived uninstall")
	}

	// Uninstalling again is a 404 (ownership-scoped lookup).
	rec = packagesRequest(t, s, router, userID, http.MethodDelete, "/api/v2/packages/"+packageID, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("second uninstall: got %d, want 404", rec.Code)
	}
}

func TestPackageInstallRejectsUnsafeURL(t *testing.T) {
	s, router, userID := newPackagesFixture(t)

	// SSRF guard: a loopback package URL is rejected before any request.
	rec := packagesRequest(t, s, router, userID, http.MethodPost, "/api/v2/packages/install",
		`{"source":"url","url":"http://127.0.0.1:9/pkg.zip"}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("loopback url install: got %d, want 502", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "upstream_error") {
		t.Fatalf("expected upstream_error code, got: %s", rec.Body.String())
	}

	// Non-http scheme is rejected too.
	rec = packagesRequest(t, s, router, userID, http.MethodPost, "/api/v2/packages/install",
		`{"source":"url","url":"file:///etc/passwd"}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("file url install: got %d, want 502", rec.Code)
	}

	// Nothing was registered.
	var count int
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM installed_packages WHERE user_id = $1`, userID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("installed_packages rows = %d, want 0", count)
	}
}

func TestPackageInstallGuestRejected(t *testing.T) {
	s, router, userID := newPackagesFixture(t)
	token, err := s.GenerateJWT(userID, "guest", "packages_guest")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v2/packages/install",
		strings.NewReader(`{"source":"builtin","slug":"writer-starter"}`))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("guest install: got %d, want 403", recorder.Code)
	}
}

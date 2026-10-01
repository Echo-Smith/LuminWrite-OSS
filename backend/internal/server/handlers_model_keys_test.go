package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/config"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database/dbtest"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/services"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/crypto"
)

// newBYOKHandlerFixture builds a minimal Server carrying the BYOK repo and a
// hand-mounted model-keys router over an isolated migrated database, plus one
// seeded registered user.
func newBYOKHandlerFixture(t *testing.T) (*Server, *chi.Mux, string) {
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
		`INSERT INTO users (uid, name, role) VALUES ('byok-hdlr', 'byok-hdlr', 'user') RETURNING id::text`,
	).Scan(&userID); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	cfg.JWT.Secret = "byok-handler-secret"
	cfg.JWT.Expiry = time.Hour

	encKey := crypto.DeriveKey("byok-handler-encryption-key")
	userKeyRepo := database.NewUserModelKeyRepo(db, encKey)
	adminRepo := database.NewAdminRepo(db).WithEncryptionKey(encKey)
	llmSvc := services.NewLLMService(adminRepo, nil, 0).WithUserKeys(userKeyRepo)

	s := &Server{cfg: cfg, db: db, userKeyRepo: userKeyRepo, adminRepo: adminRepo, llmSvc: llmSvc}
	return s, byokRouter(s), userID
}

func byokRouter(s *Server) *chi.Mux {
	router := chi.NewRouter()
	router.Route("/api/v2", func(r chi.Router) {
		r.With(s.jwtAuthMiddleware, s.rejectGuestMiddleware).Get("/model-keys", s.handleListUserModelKeys)
		r.With(s.jwtAuthMiddleware, s.rejectGuestMiddleware).Post("/model-keys", s.handleCreateUserModelKey)
		r.With(s.jwtAuthMiddleware, s.rejectGuestMiddleware).Post("/model-keys/discover", s.handleDiscoverUserModels)
		r.With(s.jwtAuthMiddleware, s.rejectGuestMiddleware).Put("/model-keys/{keyID}", s.handleUpdateUserModelKey)
		r.With(s.jwtAuthMiddleware, s.rejectGuestMiddleware).Delete("/model-keys/{keyID}", s.handleDeleteUserModelKey)
		r.With(s.jwtAuthMiddleware, s.rejectGuestMiddleware).Put("/model-keys/{keyID}/default", s.handleSetDefaultUserModelKey)
		r.With(s.jwtAuthMiddleware, s.rejectGuestMiddleware).Post("/model-keys/{keyID}/test", s.handleTestUserModelKey)
	})
	return router
}

func byokRequest(t *testing.T, s *Server, router *chi.Mux, userID, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	token, err := s.GenerateJWT(userID, "user", "byok_session")
	if err != nil {
		t.Fatal(err)
	}
	var reader *bytes.Reader
	if body == "" {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader([]byte(body))
	}
	request := httptest.NewRequest(method, path, reader)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestUserModelKeysEndpoints(t *testing.T) {
	s, router, userID := newBYOKHandlerFixture(t)

	// Create (no encryption configured on this fixture? — fixture wires it, so 201).
	rec := byokRequest(t, s, router, userID, http.MethodPost, "/api/v2/model-keys",
		`{"provider":"deepseek","model_name":"my-model","api_key":"sk-user-secret","name":"mine"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: got %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Data struct {
			ID            string `json:"id"`
			APIKeyMasked  string `json:"api_key_masked"`
			HasAPIKey     bool   `json:"has_api_key"`
			APIKeyPlain   string `json:"api_key"`
			APIKeyEncrypt string `json:"api_key_encrypted"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if !created.Data.HasAPIKey || created.Data.APIKeyPlain != "" || created.Data.APIKeyEncrypt != "" {
		t.Fatalf("create response leaks key material: %s", rec.Body.String())
	}
	if !strings.Contains(created.Data.APIKeyMasked, "***") {
		t.Fatalf("masked key missing: %q", created.Data.APIKeyMasked)
	}

	// List shows the entry with masked key only.
	rec = byokRequest(t, s, router, userID, http.MethodGet, "/api/v2/model-keys", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list: got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "sk-user-secret") {
		t.Fatal("list leaks plaintext key")
	}

	// Duplicate model_name is a 409.
	rec = byokRequest(t, s, router, userID, http.MethodPost, "/api/v2/model-keys",
		`{"provider":"deepseek","model_name":"my-model","api_key":"sk-again"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate create: got %d, want 409", rec.Code)
	}

	// Update with empty api_key preserves the stored key.
	rec = byokRequest(t, s, router, userID, http.MethodPut, "/api/v2/model-keys/"+created.Data.ID,
		`{"provider":"deepseek","model_name":"my-model","name":"renamed"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: got %d %s", rec.Code, rec.Body.String())
	}

	// Set default, then foreign keyID is 404 (non-owned id shape → not found).
	other := strings.Replace(created.Data.ID, "-", "a", 1) // still a uuid-ish 404 probe
	rec = byokRequest(t, s, router, userID, http.MethodPut, "/api/v2/model-keys/"+other+"/default", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("foreign default: got %d, want 404", rec.Code)
	}
	rec = byokRequest(t, s, router, userID, http.MethodPut, "/api/v2/model-keys/"+created.Data.ID+"/default", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("set default: got %d %s", rec.Code, rec.Body.String())
	}

	// Delete, then the key is gone.
	rec = byokRequest(t, s, router, userID, http.MethodDelete, "/api/v2/model-keys/"+created.Data.ID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: got %d", rec.Code)
	}
	rec = byokRequest(t, s, router, userID, http.MethodGet, "/api/v2/model-keys", "")
	if strings.Contains(rec.Body.String(), "my-model") {
		t.Fatal("deleted key still listed")
	}
}

func TestUserModelKeysGateWithoutEncryption(t *testing.T) {
	s, _, userID := newBYOKHandlerFixture(t)
	// Drop the repo: deployment without API_KEY_ENCRYPTION_KEY must refuse
	// BYOK instead of storing plaintext.
	s.userKeyRepo = nil

	router := chi.NewRouter()
	router.Route("/api/v2", func(r chi.Router) {
		r.With(s.jwtAuthMiddleware, s.rejectGuestMiddleware).Post("/model-keys", s.handleCreateUserModelKey)
	})
	rec := byokRequest(t, s, router, userID, http.MethodPost, "/api/v2/model-keys",
		`{"provider":"deepseek","model_name":"m","api_key":"sk-x"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no-encryption create: got %d, want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "byok_unavailable") {
		t.Fatalf("expected byok_unavailable code, got: %s", rec.Body.String())
	}
}

func TestRegistrationDisabledFlag(t *testing.T) {
	s, _, _ := newBYOKHandlerFixture(t)
	s.cfg.Server.DisableRegistration = true

	router := chi.NewRouter()
	router.Post("/api/v2/auth/register", s.handleRegister)
	router.Post("/api/v2/auth/guest", s.handleGuestLogin)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v2/auth/guest", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest when disabled: got %d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "registration_disabled") {
		t.Fatalf("expected registration_disabled code, got: %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v2/auth/register",
		bytes.NewBufferString(`{"username":"u","password":"p"}`)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("register when disabled: got %d, want 403", rec.Code)
	}
}

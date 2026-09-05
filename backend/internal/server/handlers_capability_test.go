package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// adminCapabilitiesRequest issues an authenticated admin GET against the
// capabilities endpoint on a minimal admin router (admin middleware requires
// the admin role and a provisioned user).
func adminCapabilitiesRequest(t *testing.T, server *Server, userID, query string) *httptest.ResponseRecorder {
	t.Helper()
	token, err := server.GenerateJWT(userID, "admin", "session_cap")
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Route("/api/v2/admin", func(group chi.Router) {
		group.Use(server.jwtAuthMiddleware, server.adminAuthMiddleware)
		group.Get("/capabilities", server.handleAdminCapabilities)
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v2/admin/capabilities"+query, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func decodeJSON(t *testing.T, payload []byte) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode %q: %v", payload, err)
	}
	return decoded
}

// TestAdminCapabilitiesEndpoint drives the M1.5 slice-2/3/4 acceptance over
// the real router: the inventory lists every converged surface, passes the
// authority guard, and the role view (slice 3) returns the role's filtered
// subset with the mapped governed permissions (slice 4) visible on legacy
// entries.
func TestAdminCapabilitiesEndpoint(t *testing.T) {
	server, _, _ := newGovernedE2EServer(t)
	ctx := context.Background()

	// Provision the admin user (JWT sub must be a users.id).
	var userID string
	if err := server.db.QueryRowContext(ctx, `
		INSERT INTO users (uid, name) VALUES ($1, 'cap admin')
		ON CONFLICT (uid) DO UPDATE SET name = EXCLUDED.name
		RETURNING id::text
	`, e2eUser).Scan(&userID); err != nil {
		t.Fatal(err)
	}

	recorder := adminCapabilitiesRequest(t, server, userID, "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("inventory -> %d: %s", recorder.Code, recorder.Body.String())
	}
	body := decodeJSON(t, recorder.Body.Bytes())
	data := body["data"].(map[string]any)
	total := int(data["total"].(float64))
	if total == 0 {
		t.Fatal("empty capability inventory")
	}
	sources := data["sources"].([]any)
	if len(sources) < 4 {
		t.Fatalf("sources=%v", sources)
	}

	// Slice 3: the researcher role view is a strict subset that includes the
	// governed retrieval capability and excludes the governed draft one.
	recorder = adminCapabilitiesRequest(t, server, userID, "?role=researcher")
	if recorder.Code != http.StatusOK {
		t.Fatalf("role view -> %d: %s", recorder.Code, recorder.Body.String())
	}
	roleBody := decodeJSON(t, recorder.Body.Bytes())
	roleData, _ := roleBody["data"].(map[string]any)
	if roleData == nil {
		t.Fatalf("role view missing data: %s", recorder.Body.String())
	}
	visibleAny, ok := roleData["visible"].([]any)
	if !ok {
		t.Fatalf("role view missing visible array: %s", recorder.Body.String())
	}
	visible := visibleAny
	hasSearch, hasDraft := false, false
	for _, entry := range visible {
		id := entry.(map[string]any)["id"].(string)
		switch id {
		case "core.retrieval.search":
			hasSearch = true
		case "core.draft.generate":
			hasDraft = true
		}
	}
	if !hasSearch {
		t.Fatal("researcher view missing core.retrieval.search")
	}
	if hasDraft {
		t.Fatal("researcher view must not include core.draft.generate")
	}

	// Slice 4: legacy entries (tool or editorial.tool) carry governed
	// permission names, not the legacy marker.
	for _, entry := range visible {
		manifest := entry.(map[string]any)
		id, _ := manifest["id"].(string)
		if strings.HasPrefix(id, "tool.") || strings.HasPrefix(id, "editorial.tool.") {
			permissions := manifest["permissions"].([]any)
			if len(permissions) == 0 || permissions[0] == "legacy.tool" || permissions[0] == "legacy.editorial" {
				t.Fatalf("legacy entry %s permissions=%v", id, permissions)
			}
			return
		}
	}
	t.Fatal("no legacy tool entry in researcher view to verify mapping")
}

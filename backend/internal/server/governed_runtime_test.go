package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/config"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
)

// TestComposeGovernedRuntimeFailsClosedWithoutModel pins the fail-closed
// composition contract: without a model client the runtime reports
// WRITING_RUNTIME_NOT_READY and never exposes an orchestrator.
func TestComposeGovernedRuntimeFailsClosedWithoutModel(t *testing.T) {
	runtime := ComposeGovernedRuntime(GovernedRuntimeDeps{}, writingplan.DefaultCapabilityRegistry())
	if runtime.Ready() {
		t.Fatal("runtime composed without model or store reported ready")
	}
	if runtime.BlockedCode() != "WRITING_RUNTIME_NOT_READY" {
		t.Fatalf("blocked code=%s", runtime.BlockedCode())
	}
	if runtime.orchestrator != nil {
		t.Fatal("not-ready runtime exposed an orchestrator")
	}
	if err := runtime.Dispatch("run_x"); err == nil {
		t.Fatal("not-ready runtime accepted dispatch")
	}
}

// TestGovernedRuntimeNotReadyBlocksRunCreationPinsBeforePersistence pins the
// zombie-run prevention at the HTTP boundary: the create endpoint answers
// WRITING_RUNTIME_NOT_READY and never reaches the persistence service.
func TestGovernedRuntimeNotReadyBlocksRunCreationPinsBeforePersistence(t *testing.T) {
	fake := &fakeWritingAPI{}
	notReady := ComposeGovernedRuntime(GovernedRuntimeDeps{}, writingplan.DefaultCapabilityRegistry())
	server := &Server{cfg: &config.Config{JWT: config.JWTConfig{Secret: "test-secret", Expiry: time.Hour}},
		writingAPI: fake, governedRuntime: notReady, routeReg: newRouteRegistry()}
	router := chi.NewRouter()
	router.Route("/api/v2", func(router chi.Router) { server.registerWritingRoutes(router) })
	token, err := server.GenerateJWT("00000000-0000-0000-0000-000000000001", "user", "session_test")
	if err != nil {
		t.Fatal(err)
	}
	body := `{"document_id":"doc_test","contract_id":"ctr_test","contract_version":1,"contract_hash":"sha256:0000000000000000000000000000000000000000000000000000000000000000","base_version_id":"ver_test","budget":{"max_cost_usd":1,"max_duration_ms":1000,"max_concurrency":1,"max_nodes":1,"max_items":1},"permissions":["model.invoke"],"plan":{"schema_version":"lcp/1.0","intent_plan":{"intent_plan_id":"iplan_test","contract_ref":{"id":"ctr_test","version":1,"hash":"sha256:0000000000000000000000000000000000000000000000000000000000000000"},"summary":"s","created_by":"system","created_at":"2026-08-30T00:00:00Z","proposed_steps":[]},"executable_plan":{"plan_id":"plan_test","root_node_id":"n1","nodes":[{"node_id":"n1","kind":"action","capability":"core.draft.generate","capability_version":"1.0.0","depends_on":[],"input_artifact_types":["contract"],"output_artifact_types":["full_draft"],"bounds":{"max_attempts":1,"max_concurrency":1,"max_items":1,"max_cost_usd":1,"timeout_ms":1000},"failure_path":"fail"}]}}}`
	request := httptest.NewRequest(http.MethodPost, "/api/v2/runs", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Idempotency-Key", "idem_not_ready")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error.Code != "WRITING_RUNTIME_NOT_READY" {
		t.Fatalf("error code=%s", payload.Error.Code)
	}
	if fake.lastAccess.UserID != "" {
		t.Fatal("not-ready runtime reached the persistence service (zombie run risk)")
	}
}

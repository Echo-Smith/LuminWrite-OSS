package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/config"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database/dbtest"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// e2eUser is the JWT subject the writing routes authorize against. The
// integration fixture provisions a matching users row.
const e2eUser = "00000000-0000-0000-0000-00000000e2e1"

// newGovernedE2EServer boots a real Server (migrated one-shot database,
// governed shadow mode) and its HTTP handler. When the TASK13_LLM_* env is
// present the server's LLM client points at the real model, so governed runs
// execute for real; without it the run reaches the first model step and
// pauses into human recovery (the honest no-LLM deployment shape).
func newGovernedE2EServer(t *testing.T) (*Server, http.Handler, bool) {
	t.Helper()
	db, cleanup, err := dbtest.Open(os.Getenv("TEST_DATABASE_URL"), 10, 4)
	if err != nil {
		if err == dbtest.ErrNoDatabaseURL {
			t.Skip("TEST_DATABASE_URL not set")
		}
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	var isolatedName string
	if err := db.QueryRow("SELECT current_database()").Scan(&isolatedName); err != nil {
		t.Fatal(err)
	}
	isolatedURL, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	isolatedURL.Path = "/" + isolatedName
	cfg := &config.Config{Database: config.DatabaseConfig{URL: isolatedURL.String(), MaxOpenConns: 12, MaxIdleConns: 4}}
	cfg.JWT.Secret = "e2e-secret"
	cfg.JWT.Expiry = time.Hour
	cfg.WritingRuntime.Mode = "shadow"
	apiKey := strings.TrimSpace(os.Getenv("TASK13_LLM_API_KEY"))
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("TASK13_LLM_BASE_URL")), "/")
	model := strings.TrimSpace(os.Getenv("TASK13_LLM_MODEL"))
	live := false
	if apiKey != "" && baseURL != "" && model != "" && os.Getenv("P0_OFFLINE") != "1" {
		live = true
		maxTokens := 4096
		if configured := os.Getenv("TASK13_LLM_MAX_TOKENS"); configured != "" {
			parsed, err := strconv.Atoi(configured)
			if err != nil || parsed <= 0 {
				t.Fatal("TASK13_LLM_MAX_TOKENS must be a positive integer")
			}
			maxTokens = parsed
		}
		t.Logf("live HTTP model=%s max_tokens=%d", model, maxTokens)
		cfg.DeepSeek = config.DeepSeekConfig{BaseURL: baseURL, APIKey: apiKey, DefaultModel: model,
			MaxTokens: maxTokens, Temperature: .2, Timeout: 150 * time.Second}
	}
	server, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if server.db != nil {
			server.db.Close()
		}
	})
	if server.writingAPI == nil {
		t.Fatal("writing API was not constructed")
	}
	if server.governedTrigger == nil {
		t.Fatal("governed runtime was not mounted (trigger nil)")
	}
	if api, ok := server.writingAPI.(*persistentWritingAPI); ok {
		if _, available := api.capabilities.Get("core.retrieval.search"); !available {
			t.Fatal("compile registry missing executable search capability")
		}
	}
	if live {
		if raw := os.Getenv("TASK13_LLM_MIN_REQUEST_INTERVAL_MS"); raw != "" {
			interval, err := strconv.Atoi(raw)
			if err != nil || interval < 0 {
				t.Fatal("TASK13_LLM_MIN_REQUEST_INTERVAL_MS must be a nonnegative integer")
			}
			server.llm.SetMinRequestInterval(time.Duration(interval) * time.Millisecond)
			t.Logf("live HTTP min_request_interval_ms=%d", interval)
		}
	}
	if live != (server.llm != nil) {
		t.Fatalf("live=%v but llm client presence=%v", live, server.llm != nil)
	}
	router := http.Handler(newE2ERouter(server))
	return server, router, live
}

// governedE2EToken issues a valid JWT for the fixture user's id.
func governedE2EToken(t *testing.T, server *Server, userID string) string {
	t.Helper()
	token, err := server.GenerateJWT(userID, "user", "session_e2e")
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func e2eRequest(t *testing.T, router http.Handler, token, method, path string, body any) map[string]any {
	t.Helper()
	payload := []byte("null")
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		payload = encoded
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "e2e_"+path+"_"+method+"_"+time.Now().UTC().Format("150405.000000000"))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code >= 300 {
		t.Fatalf("%s %s -> %d: %s", method, path, recorder.Code, recorder.Body.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("%s %s: decode response %q: %v", method, path, recorder.Body.String(), err)
	}
	return decoded
}

// e2eJSONField pulls a string field out of the standard response envelope.
func e2eJSONField(t *testing.T, payload map[string]any, field string) string {
	t.Helper()
	data, _ := payload["data"].(map[string]any)
	if data == nil {
		t.Fatalf("response missing data object: %#v", payload)
	}
	value, _ := data[field].(string)
	if value == "" {
		t.Fatalf("response data missing %q: %#v", field, data)
	}
	return value
}

// e2eNestedField pulls a string field from data.<parent>.<field>.
func e2eNestedField(t *testing.T, payload map[string]any, parent, field string) string {
	t.Helper()
	data, _ := payload["data"].(map[string]any)
	if data == nil {
		t.Fatalf("response missing data object: %#v", payload)
	}
	nested, _ := data[parent].(map[string]any)
	if nested == nil {
		t.Fatalf("response data missing %q object: %#v", parent, data)
	}
	value, _ := nested[field].(string)
	if value == "" {
		t.Fatalf("response %s missing %q: %#v", parent, field, nested)
	}
	return value
}

// TestGovernedRunEndToEndThroughHTTP drives the M1.4 acceptance: a real
// Server in governed shadow mode serves the writing routes; an ordinary
// client compiles a plan, creates a run, and the approved-planned run
// executes in the background to a completed state with the whole delivery
// lineage (candidate version, promotion, revision_set) on the real store.
func TestGovernedRunEndToEndThroughHTTP(t *testing.T) {
	testGovernedHTTPMode(t, writingkernel.OrchestrationModeOutlineFirst)
}
func TestGovernedP0HTTPTemplates(t *testing.T) {
	t.Setenv("P0_OFFLINE", "1")
	for _, mode := range []writingkernel.OrchestrationMode{writingkernel.OrchestrationModeFast, writingkernel.OrchestrationModeSourced, writingkernel.OrchestrationModeStrictResearch} {
		t.Run(string(mode), func(t *testing.T) { testGovernedHTTPMode(t, mode) })
	}
}
func testGovernedHTTPMode(t *testing.T, mode writingkernel.OrchestrationMode) {
	server, router, live := newGovernedE2EServer(t)
	ctx := context.Background()

	// Provision the JWT user first: writingAccess.UserID is the users.id
	// every owned row references (FK), so the token must carry it.
	var userID string
	if err := server.db.QueryRowContext(ctx, `
		INSERT INTO users (uid, name) VALUES ($1, 'e2e user')
		ON CONFLICT (uid) DO UPDATE SET name = EXCLUDED.name
		RETURNING id::text
	`, e2eUser).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	token := governedE2EToken(t, server, userID)

	// 1. Document.
	document := e2eRequest(t, router, token, "POST", "/api/v2/documents", map[string]any{"title": "端到端验收"})
	documentID := e2eJSONField(t, document, "document_id")

	// 2. Contract fixture: loaded confirmed; the API accepts drafts, so the
	// fixture rides through as draft first and is re-confirmed over HTTP.
	contractBytes := e2eContractFixture(t)
	var contract writingkernel.WritingContract
	if err := json.Unmarshal(contractBytes, &contract); err != nil {
		t.Fatal(err)
	}
	contract.Status = writingkernel.ContractStatusDraft
	// The M1.4 acceptance template is tpl_fast_v1's family: standard
	// assurance compiles to outline→draft→quality→finalize, which needs no
	// user materials (the sourced template would demand a materials input).
	contract.Collaboration.AssuranceLevel = writingkernel.AssuranceLevelStandard
	contract.Collaboration.OrchestrationMode = mode
	contract.EvidencePolicy.Level = writingkernel.EvidenceLevelStandard
	// The source attributions pin field-value hashes; recompute them for the
	// fields the test just changed.
	for i := range contract.SourceAttributions {
		valueHash, hashErr := contract.FieldValueHash(contract.SourceAttributions[i].FieldPath)
		if hashErr != nil {
			t.Fatal(hashErr)
		}
		contract.SourceAttributions[i].ValueHash = valueHash
	}
	// The fixture pins a fixed contract id; immutable rows would collide
	// across runs, so each test gets a fresh id.
	contract.ContractID = writingstore.StableID("ctr_", documentID, time.Now().UTC().Format("150405.000000000"))
	draftContract, err := contract.WithComputedHash()
	if err != nil {
		t.Fatal(err)
	}
	baseVersion := e2eBaseVersion(t, documentID, "基线草稿")

	// 3. Contract put + confirm through the HTTP API. The contract record
	// nests the contract body: ids come from contract.contract_id.
	putted := e2eRequest(t, router, token, "POST", "/api/v2/documents/"+documentID+"/contracts", map[string]any{"contract": draftContract})
	contractID := e2eNestedField(t, putted, "contract", "contract_id")
	confirmed := e2eRequest(t, router, token, "POST", "/api/v2/contracts/"+contractID+"/confirm", map[string]any{"previous_version": 1, "contract": confirmedContract(t, draftContract)})
	if e2eNestedField(t, confirmed, "contract", "status") != string(writingkernel.ContractStatusConfirmed) {
		t.Fatalf("contract not confirmed: %#v", confirmed)
	}
	sealedContract := confirmedContract(t, draftContract)

	// 4. Provision the base version through the document version store.
	if _, err := server.writingStoreForTest().CommitDocumentVersion(ctx, writingstore.CommitDocumentVersionParams{
		Version: baseVersion, ContractID: contractID, ContractVersion: 2,
		Trace: writingstore.TraceContext{Actor: writingstore.Actor{Type: writingstore.ActorUser, ID: userID},
			Provenance: map[string]any{}, SourceRefs: []string{}}}); err != nil {
		t.Fatal(err)
	}

	// 5. Compile the plan over HTTP (fast template via intent plan).
	plan := e2eRequest(t, router, token, "POST", "/api/v2/documents/"+documentID+"/plans", map[string]any{
		"contract_id": contractID, "contract_version": 2, "base_version_id": baseVersion.VersionID,
		"intent_plan":             e2eIntentPlan(t, sealedContract),
		"budget":                  map[string]any{"max_cost_usd": 100, "max_duration_ms": 3000000, "max_concurrency": 2, "max_nodes": 10, "max_items": 10},
		"required_final_artifact": "revision_set",
	})
	envelopeData, _ := plan["data"].(map[string]any)["plan"].(map[string]any)
	if envelopeData == nil {
		t.Fatalf("plan preview missing envelope: %#v", plan)
	}

	// 6. Create the run: planned (no approval required) and triggered.
	run := e2eRequest(t, router, token, "POST", "/api/v2/runs", map[string]any{
		"document_id": documentID, "contract_id": contractID, "contract_version": 2,
		"contract_hash": sealedContract.ContractHash, "base_version_id": baseVersion.VersionID,
		"style_slug": "default", "plan": envelopeData,
		"budget":      map[string]any{"max_cost_usd": 100, "max_duration_ms": 3000000, "max_concurrency": 2, "max_nodes": 10, "max_items": 10},
		"permissions": plan["data"].(map[string]any)["permissions"],
	})
	runID := e2eJSONField(t, run, "run_id")

	// An expensive plan may require explicit approval even in the fixture.
	initialStatus := e2eJSONField(t, run, "status")
	if initialStatus == "awaiting_approval" {
		e2eRequest(t, router, token, "POST", "/api/v2/runs/"+runID+"/approve", map[string]any{"plan_id": envelopeData["executable_plan"].(map[string]any)["plan_id"], "plan_version": 1, "plan_hash": envelopeData["executable_plan"].(map[string]any)["plan_hash"], "permissions": plan["data"].(map[string]any)["permissions"]})
	}
	// 7. Poll until the background execution reaches a terminal state. The
	// expected terminal depends on the deployment: with a live model the run
	// completes; without one, the first model step fails and the governed
	// runtime pauses the run for human recovery (idempotency-required
	// nodes never blind-retry) — both are correct governed outcomes.
	deadline := time.Now().Add(240 * time.Second)
	final := ""
	for time.Now().Before(deadline) {
		status := e2eRequest(t, router, token, "GET", "/api/v2/runs/"+runID, nil)
		state := e2eJSONField(t, status, "status")
		switch state {
		case "completed", "failed", "cancelled", "paused":
			final = state
		}
		if final != "" {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	want := "paused"
	if live {
		want = "completed"
	}
	if final != want {
		t.Fatalf("run ended as %q (want %s)", final, want)
	}
	if !live {
		// Paused into human recovery is the verified offline outcome; the
		// execution chain (mount → trigger → orchestrator → node attempt →
		// pause) is exercised end to end. The live variant continues below.
		return
	}

	// 8. Delivery lineage: the document was promoted and a revision_set exists.
	store := server.writingStoreForTest()
	candidateID, err := store.CurrentDocumentVersionID(ctx, documentID)
	if err != nil || candidateID == "" {
		t.Fatalf("no current version after e2e run: %v", err)
	}
	version, err := store.GetDocumentVersion(ctx, documentID, candidateID)
	if err != nil {
		t.Fatal(err)
	}
	if version.QualityState != writingstore.QualityAcceptedDraft {
		t.Fatalf("quality state %q after e2e run", version.QualityState)
	}
	artifacts, err := store.ListRunArtifacts(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	revisionSets := 0
	for _, artifact := range artifacts {
		if artifact.ArtifactType == "revision_set" {
			revisionSets++
		}
	}
	if revisionSets != 1 {
		t.Fatalf("revision_set artifacts = %d, want 1", revisionSets)
	}
}

// e2eContractFixture loads the strict writing-contract fixture.
func e2eContractFixture(t *testing.T) []byte {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join("..", "..", "..", "specs", "lcp", "v1", "fixtures", "writing-contract.valid.json"))
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

// newE2ERouter registers the writing routes on a fresh chi router (the full
// server route table pulls in far more dependencies than the writing API
// needs, and the acceptance is scoped to the governed writing surface).
func newE2ERouter(server *Server) http.Handler {
	router := chi.NewRouter()
	router.Route("/api/v2", func(group chi.Router) {
		group.Use(server.jwtAuthMiddleware, server.rejectGuestMiddleware, server.requireWritingAPI)
		server.registerWritingRoutes(group)
	})
	return router
}

// writingStoreForTest exposes the governed store backing the writing API.
func (s *Server) writingStoreForTest() *writingstore.Store {
	if api, ok := s.writingAPI.(*persistentWritingAPI); ok {
		return api.store
	}
	return nil
}

// e2eBaseVersion builds the sealed one-section base version the writing API
// requires the run to anchor on (mirrors the writingruntime fixture shape).
func e2eBaseVersion(t *testing.T, documentID, text string) writingkernel.DocumentVersion {
	t.Helper()
	origin := writingkernel.Origin{Kind: writingkernel.OriginSystem, Ref: "e2e"}
	textNode := &writingkernel.DocumentNode{BlockID: "blk_e2e_text", Type: writingkernel.NodeTypeText,
		Text: text, Attrs: map[string]any{}, Children: []*writingkernel.DocumentNode{}, Origin: origin}
	paragraph := &writingkernel.DocumentNode{BlockID: "blk_e2e_paragraph", Type: writingkernel.NodeTypeParagraph,
		Attrs: map[string]any{}, Children: []*writingkernel.DocumentNode{textNode}, Origin: origin}
	section := &writingkernel.DocumentNode{BlockID: "blk_e2e_section", Type: writingkernel.NodeTypeSection,
		Attrs: map[string]any{"level": 1}, Children: []*writingkernel.DocumentNode{paragraph}, Origin: origin}
	version := writingkernel.DocumentVersion{SchemaVersion: writingkernel.SchemaVersionV1,
		DocumentID: documentID, VersionID: writingstore.StableID("ver_", documentID, "base"),
		Root: &writingkernel.DocumentNode{BlockID: "blk_e2e_root", Type: writingkernel.NodeTypeDocument,
			Attrs: map[string]any{}, Children: []*writingkernel.DocumentNode{section}, Origin: origin}}
	sealed, err := version.WithComputedHashes()
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

// confirmedContract reseals the contract at confirmed status and a bumped
// version (the transition validator requires a version increase).
func confirmedContract(t *testing.T, contract writingkernel.WritingContract) writingkernel.WritingContract {
	t.Helper()
	contract.Status = writingkernel.ContractStatusConfirmed
	contract.Version++
	sealed, err := contract.WithComputedHash()
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

// e2eIntentPlan builds the minimal intent plan that selects the fast
// template (single draft step hinting the draft capability).
func e2eIntentPlan(t *testing.T, contract writingkernel.WritingContract) writingplan.IntentPlan {
	t.Helper()
	now := time.Now().UTC()
	intent, err := (writingplan.IntentPlan{IntentPlanID: "iplan_e2e_" + now.Format("150405000000000"),
		ContractRef: writingplan.ObjectRef{ID: contract.ContractID, Version: contract.Version, Hash: contract.ContractHash},
		Summary:     "e2e fast run", CreatedBy: writingplan.ActorSystem, CreatedAt: now,
		ProposedSteps: []writingplan.ProposedStep{{StepID: "draft", Objective: "draft the document",
			CapabilityHint: "core.draft.generate", DependsOn: []string{}}},
	}).WithComputedHash()
	if err != nil {
		t.Fatal(err)
	}
	return intent
}

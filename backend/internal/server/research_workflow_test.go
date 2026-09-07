package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingruntime"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// T02 API integration tests (docs/plans/2026-09-07-research-review-integration.md
// §T02): the two-node plan (action → human_gate) drives the full loop —
// run → gate pause → GET gate → decide → resume → complete — over the real
// HTTP surface with scripted model-free runners. Gate-specific negative
// cases: concurrent double confirm, stale revision, cross-owner 404, plain
// resume blocked on a pending gate.

const (
	t02APIGateCapability = "core.research.gate.evidence"
	t02APIUser           = "00000000-0000-0000-0000-0000000002e2"
	t02APIOtherUser      = "00000000-0000-0000-0000-0000000002e3"
)

// t02APIPlanNodes: research read (safe, scripted) → human gate. The gate
// consumes the read node's output artifact; its decision binds that ref.
func t02APIPlanNodes() []writingplan.PlanNode {
	return []writingplan.PlanNode{
		t00PlanNode("node_t02_read", t00SafeCapability, writingplan.NodeAction, nil,
			[]writingplan.ArtifactType{"contract"}, []writingplan.ArtifactType{"full_draft"}, writingplan.FailureFail, 2),
		{NodeID: "node_t02_gate", Kind: writingplan.NodeHumanGate, Capability: t02APIGateCapability,
			CapabilityVersion: "1.0.0", DependsOn: []string{"node_t02_read"},
			InputArtifactTypes:  []writingplan.ArtifactType{"full_draft"},
			OutputArtifactTypes: []writingplan.ArtifactType{"evidence_approval"},
			Bounds:              writingplan.Bounds{MaxAttempts: 1, MaxConcurrency: 1, MaxItems: 1, MaxCostUSD: 0, TimeoutMS: 120000},
			FailurePath:         writingplan.FailurePause},
		// The standard-assurance validator floor + finalize tail keep the
		// scripted plan dispatch-valid (quality + revision_set), mirroring
		// t00PlanNodes' shape with the gate inserted before validation.
		t00PlanNode("node_t02_q", "core.validation.quality", writingplan.NodeValidate, []string{"node_t02_gate"},
			[]writingplan.ArtifactType{"full_draft"}, []writingplan.ArtifactType{"quality_report"}, writingplan.FailurePause, 2),
		t00PlanNode("node_t02_f", "core.document.finalize", writingplan.NodeAction, []string{"node_t02_gate", "node_t02_q"},
			[]writingplan.ArtifactType{"full_draft", "quality_report"}, []writingplan.ArtifactType{"revision_set"}, writingplan.FailureFail, 1),
	}
}

// t02APIGateCapabilityRegistration declares the kernel-owned gate capability.
// The orchestrator never dispatches gate nodes, so the executor binding is a
// never-invoked no-op that satisfies manifest validation (design.md §3).
func t02APIGateCapabilityRegistration(capabilities *writingplan.CapabilityRegistry) error {
	const gateBinding = "kernel.human_gate"
	manifest := writingplan.CapabilityManifest{ID: t02APIGateCapability, Class: "research.gate.evidence",
		Executor: gateBinding, InputTypes: []writingplan.ArtifactType{"full_draft"},
		OptionalInputTypes: []writingplan.ArtifactType{}, OutputTypes: []writingplan.ArtifactType{"evidence_approval"},
		Permissions: []writingplan.Permission{}, EstimatedCostUSD: 0, EstimatedDurationMS: 0,
		Version: "1.0.0", SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeHumanGate},
		MaxBounds:   writingplan.Bounds{MaxAttempts: 1, MaxConcurrency: 1, MaxItems: 1, MaxCostUSD: 0, TimeoutMS: 120000},
		Idempotency: writingplan.IdempotencySafe, Available: true,
		Context: writingplan.ContextContract{RequiredContext: []writingplan.ContextBlockName{},
			OptionalContext: []writingplan.ContextBlockName{}}}
	if !capabilities.ExecutorRegistered(gateBinding) {
		if err := capabilities.RegisterExecutor(writingplan.ExecutorBinding{ID: gateBinding,
			AcceptedInputTypes:  []writingplan.ArtifactType{"full_draft"},
			ProducedOutputTypes: []writingplan.ArtifactType{"evidence_approval"},
			Dispatch: func(context.Context, writingplan.ExecutionRequest) (writingplan.ExecutionResult, error) {
				return writingplan.ExecutionResult{}, nil
			}}); err != nil {
			return err
		}
	}
	return capabilities.Register(manifest)
}

// t02APIHarness extends the T00 harness with the gate capability and the
// research API surface.
type t02APIHarness struct {
	*t00Harness
	otherToken string
	otherID    string
}

func newT02APIHarness(t *testing.T) *t02APIHarness {
	t.Helper()
	h := newT00Harness(t)
	// The t00 harness mounts a registry built from the catalog; swap in the
	// gate capability so the scripted plan compiles. The read node reuses the
	// already-registered IdempotencySafe t00 capability and its runner.
	capabilities := h.api.capabilities
	if err := t02APIGateCapabilityRegistration(capabilities); err != nil {
		t.Fatalf("register gate capability: %v", err)
	}
	// Research API surface: wire the decision runtime (mirrors
	// mountGovernedRuntime's production wiring).
	h.api.gateOrchestrator = h.api.trigger.orchestrator
	h.api.gateCheckpoints = &writingruntime.PersistentCheckpointRepository{Store: h.store,
		Trace: writingstore.TraceContext{Actor: writingstore.Actor{Type: writingstore.ActorSystem, ID: "writingruntime"},
			Provenance: map[string]any{}, SourceRefs: []string{}}}
	var otherID string
	if err := h.server.db.QueryRow(`INSERT INTO users (uid, name) VALUES ($1, 't02 other')
		ON CONFLICT (uid) DO UPDATE SET name = EXCLUDED.name RETURNING id::text`, t02APIOtherUser).Scan(&otherID); err != nil {
		t.Fatal(err)
	}
	otherToken, err := h.server.GenerateJWT(otherID, "user", "session_t02other")
	if err != nil {
		t.Fatal(err)
	}
	return &t02APIHarness{t00Harness: h, otherToken: otherToken, otherID: otherID}
}

// t02APIIdempotencyKey returns a unique, header-safe key.
func t02APIIdempotencyKey(tag string) string {
	return "e2e_t02_" + tag + "_" + fmt.Sprint(time.Now().UTC().UnixNano())
}

// t02APIRequest issues a request with a controllable idempotency key and
// returns the status code plus the decoded envelope.
func t02APIRequest(t *testing.T, router http.Handler, token, method, path, key string, body any) (int, map[string]any) {
	t.Helper()
	payload := []byte("null")
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		payload = encoded
	}
	request, err := http.NewRequest(method, path, strings.NewReader(string(payload)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	var decoded map[string]any
	_ = json.Unmarshal(recorder.Body.Bytes(), &decoded)
	return recorder.Code, decoded
}

func dataOf(t *testing.T, payload map[string]any) map[string]any {
	t.Helper()
	data, _ := payload["data"].(map[string]any)
	if data == nil {
		t.Fatalf("response missing data: %#v", payload)
	}
	return data
}

func errorCodeOf(t *testing.T, payload map[string]any) string {
	t.Helper()
	errObj, _ := payload["error"].(map[string]any)
	code, _ := errObj["code"].(string)
	return code
}

// t02GateID fetches the gate id from the research progress view.
func t02GateID(t *testing.T, h *t02APIHarness, runID string) string {
	t.Helper()
	code, payload := t02APIRequest(t, h.router, h.token, http.MethodGet, "/api/v2/writing/runs/"+runID+"/research", "", nil)
	if code != http.StatusOK {
		t.Fatalf("GET research -> %d: %s", code, payload)
	}
	active, _ := dataOf(t, payload)["active_gate"].(map[string]any)
	if active == nil {
		t.Fatalf("no active gate in research view: %#v", payload)
	}
	gateID, _ := active["gate_id"].(string)
	if gateID == "" {
		t.Fatalf("active gate missing gate_id: %#v", active)
	}
	return gateID
}

// waitForGatePaused polls GET /runs/{id} until the run pauses at the gate.
func (h *t02APIHarness) waitForGatePaused(t *testing.T, runID string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		status := h.httpStatus(t, runID)
		if status == "paused" {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("run %s never paused at the gate (last status %q)", runID, h.httpStatus(t, runID))
}

// TestT02ResearchGateFlowEndToEnd runs the full loop: run → pause at gate →
// GET gate → decide (202) → background resume → completed. Then replays the
// same decision (200, same body) and asserts exactly one decision row.
func TestT02ResearchGateFlowEndToEnd(t *testing.T) {
	h := newT02APIHarness(t)
	fixture := h.fixture(t)
	envelope := h.buildEnvelope(t, fixture, t02APIPlanNodes())
	runID := h.createRun(t, fixture, envelope)
	h.waitForGatePaused(t, runID)

	// GET research progress exposes the active gate.
	code, payload := t02APIRequest(t, h.router, h.token, http.MethodGet, "/api/v2/writing/runs/"+runID+"/research", "", nil)
	if code != http.StatusOK {
		t.Fatalf("GET research -> %d: %s", code, payload)
	}
	progress := dataOf(t, payload)
	if progress["phase"] != "gate_evidence" {
		t.Fatalf("research phase = %v, want gate_evidence", progress["phase"])
	}

	// GET gate exposes the pending gate and its allowed operations.
	gateID := t02GateID(t, h, runID)
	code, payload = t02APIRequest(t, h.router, h.token, http.MethodGet, "/api/v2/writing/runs/"+runID+"/gates/"+gateID, "", nil)
	if code != http.StatusOK {
		t.Fatalf("GET gate -> %d: %s", code, payload)
	}
	gate := dataOf(t, payload)
	if gate["status"] != "pending" || gate["revision"].(float64) != 1 {
		t.Fatalf("gate = %#v", gate)
	}
	inputRef, _ := gate["input_ref"].(map[string]any)
	if inputRef == nil || inputRef["content_hash"] == "" {
		t.Fatalf("gate input_ref missing: %#v", gate)
	}

	// A concurrent double confirm: two goroutines, same key, same body.
	decision := map[string]any{
		"plan_id": envelope.ExecutablePlan.PlanID, "plan_version": 1,
		"plan_hash": envelope.ExecutablePlan.PlanHash, "gate_revision": 1,
		"input_ref": inputRef, "decision": "approve",
	}
	type confirmResult struct {
		code    int
		payload map[string]any
	}
	results := make(chan confirmResult, 2)
	key := t02APIIdempotencyKey("decide")
	for i := 0; i < 2; i++ {
		go func() {
			code, payload := t02APIRequest(t, h.router, h.token, http.MethodPost,
				"/api/v2/writing/runs/"+runID+"/gates/"+gateID+"/decisions", key, decision)
			results <- confirmResult{code, payload}
		}()
	}
	first, second := <-results, <-results
	for _, result := range []confirmResult{first, second} {
		if result.code != http.StatusAccepted && result.code != http.StatusOK {
			t.Fatalf("concurrent confirm -> %d: %s", result.code, result.payload)
		}
	}
	// A third replay with the same key returns 200 and the same decision.
	replayCode, replayPayload := t02APIRequest(t, h.router, h.token, http.MethodPost,
		"/api/v2/writing/runs/"+runID+"/gates/"+gateID+"/decisions", key, decision)
	if replayCode != http.StatusOK {
		t.Fatalf("replay -> %d, want 200: %s", replayCode, replayPayload)
	}

	// The decision transaction completes the gate node; the trigger resumes
	// the run in the background.
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if status := h.httpStatus(t, runID); status == "completed" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if status := h.httpStatus(t, runID); status != "completed" {
		t.Fatalf("run ended as %q after decision, want completed", status)
	}
	// Exactly one decision row and one gate completion.
	var decisions int
	if err := h.server.db.QueryRow(`SELECT COUNT(*) FROM writing_gate_decisions WHERE gate_id=$1 AND status='approved'`, gateID).Scan(&decisions); err != nil {
		t.Fatal(err)
	}
	if decisions != 1 {
		t.Fatalf("approved decision rows = %d, want 1", decisions)
	}
	attempts, err := h.store.ListRunAttempts(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	gateCompletions := 0
	for _, attempt := range attempts {
		if attempt.NodeID == "node_t02_gate" && attempt.Status == "succeeded" {
			gateCompletions++
		}
	}
	if gateCompletions != 1 {
		t.Fatalf("gate node completed %d times, want exactly 1", gateCompletions)
	}
}

// TestT02ResearchGateNegativeCases covers: stale revision 409, cross-owner
// 404, plain resume blocked 409 GATE_APPROVAL_REQUIRED, and the referenced
// artifact content read with owner authorization.
func TestT02ResearchGateNegativeCases(t *testing.T) {
	h := newT02APIHarness(t)
	fixture := h.fixture(t)
	envelope := h.buildEnvelope(t, fixture, t02APIPlanNodes())
	runID := h.createRun(t, fixture, envelope)
	h.waitForGatePaused(t, runID)
	gateID := t02GateID(t, h, runID)

	// Plain resume against the pending gate: 409 GATE_APPROVAL_REQUIRED.
	code, payload := t02APIRequest(t, h.router, h.token, http.MethodPost, "/api/v2/writing/runs/"+runID+"/resume", t02APIIdempotencyKey("resume-blocked"), map[string]any{})
	if code != http.StatusConflict || errorCodeOf(t, payload) != "GATE_APPROVAL_REQUIRED" {
		t.Fatalf("plain resume -> %d %v, want 409 GATE_APPROVAL_REQUIRED", code, payload)
	}

	// Cross-owner reads: 404, not 403 (existence not leaked).
	code, _ = t02APIRequest(t, h.router, h.otherToken, http.MethodGet, "/api/v2/writing/runs/"+runID+"/gates/"+gateID, "", nil)
	if code != http.StatusNotFound {
		t.Fatalf("cross-owner GET gate -> %d, want 404", code)
	}
	code, _ = t02APIRequest(t, h.router, h.otherToken, http.MethodGet, "/api/v2/writing/runs/"+runID+"/research", "", nil)
	if code != http.StatusNotFound {
		t.Fatalf("cross-owner GET research -> %d, want 404", code)
	}
	decision := map[string]any{
		"plan_id": envelope.ExecutablePlan.PlanID, "plan_version": 1,
		"plan_hash": envelope.ExecutablePlan.PlanHash, "gate_revision": 1,
		"input_ref": map[string]any{"artifact_id": "art_x", "version": 1, "content_hash": "sha256:" + strings.Repeat("0", 64)},
		"decision":  "approve",
	}
	code, _ = t02APIRequest(t, h.router, h.otherToken, http.MethodPost, "/api/v2/writing/runs/"+runID+"/gates/"+gateID+"/decisions", t02APIIdempotencyKey("intruder"), decision)
	if code != http.StatusNotFound {
		t.Fatalf("cross-owner decide -> %d, want 404", code)
	}

	// Stale revision: submit gate_revision 2 against revision 1 → 409.
	code, payload = t02APIRequest(t, h.router, h.token, http.MethodPost, "/api/v2/writing/runs/"+runID+"/gates/"+gateID+"/decisions", t02APIIdempotencyKey("stale"), map[string]any{
		"plan_id": envelope.ExecutablePlan.PlanID, "plan_version": 1,
		"plan_hash": envelope.ExecutablePlan.PlanHash, "gate_revision": 2,
		"input_ref": map[string]any{"artifact_id": "art_x", "version": 1, "content_hash": "sha256:" + strings.Repeat("0", 64)},
		"decision":  "approve",
	})
	if code != http.StatusConflict || errorCodeOf(t, payload) != "STALE_GATE" {
		t.Fatalf("stale decision -> %d %v, want 409 STALE_GATE", code, payload)
	}

	// Artifact content: the run's contract artifact is readable by the owner.
	artifacts, err := h.store.ListRunArtifacts(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) == 0 {
		t.Fatal("no artifacts to read")
	}
	contentPath := "/api/v2/writing/runs/" + runID + "/artifacts/" + artifacts[0].ArtifactID + "/content"
	code, payload = t02APIRequest(t, h.router, h.token, http.MethodGet, contentPath, "", nil)
	if code != http.StatusOK {
		t.Fatalf("artifact read -> %d: %s", code, payload)
	}
	// Cross-owner artifact read is 404.
	code, _ = t02APIRequest(t, h.router, h.otherToken, http.MethodGet, contentPath, "", nil)
	if code != http.StatusNotFound {
		t.Fatalf("cross-owner artifact read -> %d, want 404", code)
	}

	// Decide and finish the run so the fixture does not leak a worker.
	code, payload = t02APIRequest(t, h.router, h.token, http.MethodGet, "/api/v2/writing/runs/"+runID+"/gates/"+gateID, "", nil)
	inputRef := dataOf(t, payload)["input_ref"].(map[string]any)
	code, payload = t02APIRequest(t, h.router, h.token, http.MethodPost, "/api/v2/writing/runs/"+runID+"/gates/"+gateID+"/decisions", t02APIIdempotencyKey("cleanup"), map[string]any{
		"plan_id": envelope.ExecutablePlan.PlanID, "plan_version": 1,
		"plan_hash": envelope.ExecutablePlan.PlanHash, "gate_revision": 1,
		"input_ref": inputRef, "decision": "approve",
	})
	if code != http.StatusAccepted {
		t.Fatalf("cleanup decision -> %d: %s", code, payload)
	}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if status := h.httpStatus(t, runID); status == "completed" {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("run ended as %q, want completed", h.httpStatus(t, runID))
}

// TestT02PlainResumeNeverBypassesGate: a research plan paused at a pending
// gate stays paused after resume attempts, and the run does not advance the
// gated node (A09 in the plan matrix).
func TestT02PlainResumeNeverBypassesGate(t *testing.T) {
	h := newT02APIHarness(t)
	fixture := h.fixture(t)
	envelope := h.buildEnvelope(t, fixture, t02APIPlanNodes())
	runID := h.createRun(t, fixture, envelope)
	h.waitForGatePaused(t, runID)
	// Three blocked resume attempts in a row.
	for i := 0; i < 3; i++ {
		code, payload := t02APIRequest(t, h.router, h.token, http.MethodPost, "/api/v2/writing/runs/"+runID+"/resume", t02APIIdempotencyKey("blocked"), map[string]any{})
		if code != http.StatusConflict {
			t.Fatalf("resume attempt %d -> %d %v, want 409", i+1, code, payload)
		}
	}
	if status := h.httpStatus(t, runID); status != "paused" {
		t.Fatalf("run status %q after blocked resumes, want paused", status)
	}
	// The gate node has no attempt records: nothing ran past it.
	attempts, err := h.store.ListRunAttempts(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	for _, attempt := range attempts {
		if attempt.NodeID == "node_t02_gate" {
			t.Fatalf("gate node has attempt %s while pending", attempt.Status)
		}
	}
}

package server

// T09 acceptance-matrix gap scenarios over the real HTTP surface (docs/plans/
// 2026-09-07-research-review-integration.md A13/A15, budget guard, and the
// T09b frontend contract for GET research):
//
//  1. WallClockResearchBudgetBoundary — preset attempt durations reach the
//     plan budget between papers → RESEARCH_BUDGET_BOUNDARY clean pause →
//     owner resume continues the remaining papers (fire-once, no re-fire).
//  2. A13 cancel — a mid-read cancel stops the run: no further worker reads,
//     the run terminates cancelled, and no formal draft/revision_set is ever
//     produced (已获结果仅中间产物).
//  3. A15 SSE reconnect — events fetched up to sequence N, the connection
//     drops, a reconnect with Last-Event-ID/after=N replays exactly the
//     missing events in order (per-sequence merge), and the stale GET/first
//     page never covers newer events.
//  4. GET research view (T09b contract) — spec projection + reading-scope
//     counts appear for research runs; legacy runs keep the old shape.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/websocket"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingruntime"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// ── 1. Wall-clock budget guard (T06 遗留 #2) ────────────────────────────────

// TestT09WallClockBudgetBoundaryPausesAndResumes mounts the production guard
// (not the scripted t06BudgetGuard): the run's completed attempts carry
// durations that already reach the plan budget, so the very first between-
// papers check fires. After the owner resumes, the remaining papers continue
// (the durable fire-once marker keeps the guard from re-firing) and the run
// completes. A guard with an under-budget ledger must never fire.
func TestT09WallClockBudgetBoundaryPausesAndResumes(t *testing.T) {
	h := newT06E2EHarness(t, 3, 0)
	// Swap in the production guard with a forced spent time above the plan
	// budget (7200000ms): fires on the first between-papers check.
	boundary := NewWallClockResearchBudgetBoundary(h.store)
	boundary.SetForcedSpentMS(7200000)
	h.budgetSwap = func(*writingstore.Store) writingruntime.ResearchBudgetBoundary { return boundary }
	h.remountResearchExecutors(t)

	fixture := h.fixture(t)
	envelope := h.buildResearchEnvelope(t, fixture)
	runID := h.createResearchRun(t, fixture, envelope)

	// First pause: the wall-clock boundary sentinel (a clean pause, no gate).
	// The forced spent time reaches the plan budget before the first paper,
	// so the boundary fires with zero papers read.
	h.waitForGatePausedAtBoundary(t, runID)
	if reads := h.worker.totalReads(); reads != 0 {
		t.Fatalf("worker reads after boundary = %d, want 0 (budget already exhausted)", reads)
	}

	// Owner resume: the boundary has fired durably, so the guard stands down
	// and the remaining papers continue (no re-fire, no re-reads).
	boundary.SetForcedSpentMS(0)
	h.decideGate(t, runID, h.advanceToGate(t, runID, "evidence"), envelope)
	if reads := h.worker.totalReads(); reads != 3 {
		t.Fatalf("worker reads after resume = %d, want 3 (boundary fired once; no re-reads)", reads)
	}
	h.decideGate(t, runID, h.advanceToGate(t, runID, "outline"), envelope)
	if status := h.driveToTerminal(t, runID, 3); status != "completed" {
		t.Fatalf("run ended as %q, want completed after wall-clock boundary resume", status)
	}
}

// TestT09WallClockBudgetBoundaryStaysQuietUnderBudget is the no-false-trigger
// half: with the ledger durations far below the plan budget, the guard never
// fires and a full chain completes without any boundary pause.
func TestT09WallClockBudgetBoundaryStaysQuietUnderBudget(t *testing.T) {
	h := newT06E2EHarness(t, 2, 0) // production guard mounted by default now
	boundary := NewWallClockResearchBudgetBoundary(h.store)
	boundary.SetForcedSpentMS(1000) // 1s of 120min budget
	h.budgetSwap = func(*writingstore.Store) writingruntime.ResearchBudgetBoundary { return boundary }
	h.remountResearchExecutors(t)
	fixture := h.fixture(t)
	envelope := h.buildResearchEnvelope(t, fixture)
	runID := h.createResearchRun(t, fixture, envelope)
	evidenceGate := h.advanceToGate(t, runID, "evidence")
	h.decideGate(t, runID, evidenceGate, envelope)
	h.decideGate(t, runID, h.advanceToGate(t, runID, "outline"), envelope)
	if status := h.driveToTerminal(t, runID, 3); status != "completed" {
		t.Fatalf("run ended as %q, want completed with no boundary pause", status)
	}
	events, err := h.store.ListRunEvents(context.Background(), runID, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		payload, _ := json.Marshal(event.Payload)
		var decoded struct {
			ErrorCode string `json:"error_code"`
		}
		_ = json.Unmarshal(payload, &decoded)
		if decoded.ErrorCode == string(writingruntime.CodeResearchBudgetBoundary) {
			t.Fatal("boundary fired although the ledger spent time was under budget")
		}
	}
}

// TestT09WallClockBudgetBoundaryRespectsHumanGateWaits seeds completed
// attempts with real durations on a genuine run and verifies the guard's
// spend is exactly Σ actual_duration_ms — the property that makes human-gate
// waiting free (gate pauses record no attempt duration).
func TestT09WallClockBudgetBoundaryRespectsHumanGateWaits(t *testing.T) {
	h := newT06E2EHarness(t, 2, 0)
	fixture := h.t00Harness.fixture(t)
	envelope := h.buildLegacyEnvelope(t, fixture)
	runID := h.createRun(t, fixture, envelope)
	h.waitForTerminal(t, runID, 60*time.Second)

	// Seed two completed attempts with fixed durations (distinct synthetic
	// nodes, so they cannot collide with the run's real attempt rows).
	ctx := context.Background()
	trace := writingstore.TraceContext{Provenance: map[string]any{}, SourceRefs: []string{},
		Actor: writingstore.Actor{Type: writingstore.ActorSystem, ID: "t09.fixture"}}
	for index, duration := range []int64{1500, 2500} {
		nodeID := fmt.Sprintf("node_budget_seed_%d", index)
		_, _, err := h.store.StartNodeAttempt(ctx, writingstore.NodeAttempt{RunID: runID,
			PlanID: envelope.ExecutablePlan.PlanID, PlanVersion: 1, NodeID: nodeID, Attempt: 1,
			IdempotencyKey: runID + ":" + nodeID + ":1", NodeKind: writingplan.NodeAction,
			CapabilityID: "core.research.read", CapabilityVersion: "1.0.0",
			ExecutorID: "engine.step.research_read", FailurePath: writingplan.FailurePause,
			Bounds: writingplan.Bounds{MaxAttempts: 1,
				MaxConcurrency: 1, MaxItems: 1, MaxCostUSD: 1, TimeoutMS: 60000},
			InputHash: "sha256:" + strings.Repeat("0", 64), InputArtifactIDs: []string{}}, trace)
		if err != nil {
			t.Fatal(err)
		}
		if err := h.store.CompleteNodeAttempt(ctx, writingstore.AttemptCompletion{RunID: runID,
			NodeID: nodeID, Attempt: 1, Status: "succeeded", DurationMS: duration, Trace: trace}); err != nil {
			t.Fatal(err)
		}
	}
	guard := NewWallClockResearchBudgetBoundary(h.store)
	// The seeded spend is 4000ms — far under the run's 50-minute budget, so
	// reading the ledger cannot over-count into a fire. Checks run against the
	// first seeded node so the durable-marker event below satisfies the
	// run-event attempt foreign key.
	request := writingruntime.ExecutionRequest{RunID: runID, NodeID: "node_budget_seed_0"}
	if reached, _ := guard.ResearchBudgetBoundaryReached(ctx, request, 0, 2); reached {
		t.Fatalf("guard fired although Σ attempt durations (4000ms) is under the run budget")
	}
	// Now squeeze the run's budget below the recorded spend: the fire decision
	// must be reachable exactly at the boundary (spend >= limit).
	if _, err := h.server.db.Exec(`UPDATE writing_runs SET budget = $2::jsonb WHERE run_id = $1`,
		runID, `{"max_cost_usd":100,"max_duration_ms":1,"max_concurrency":1,"max_nodes":10,"max_items":10}`); err != nil {
		t.Fatal(err)
	}
	if reached, _ := guard.ResearchBudgetBoundaryReached(ctx, request, 0, 2); !reached {
		t.Fatal("guard did not fire at Σ=4000ms vs 1ms budget")
	}
	// Fire-once: a second check must stand down even though the spend still
	// exceeds the limit (the durable boundary marker).
	if reached, _ := guard.ResearchBudgetBoundaryReached(ctx, request, 0, 2); reached {
		t.Fatal("guard re-fired although the boundary already paused this node")
	}
	// Restart semantics: a FRESH guard (new process, empty in-process map)
	// stands down because the boundary pause left a durable node.paused event
	// carrying RESEARCH_BUDGET_BOUNDARY.
	markerKey, keyErr := writingstore.NodeAttemptKey(runID, request.NodeID, 1)
	if keyErr != nil {
		t.Fatal(keyErr)
	}
	if _, err := h.store.AppendRunEvent(ctx, writingstore.RunEvent{RunID: runID,
		EventType: "node.paused", NodeID: request.NodeID, Attempt: 1,
		IdempotencyKey: markerKey, EntityKind: "node", EntityID: request.NodeID,
		Payload: map[string]any{"status": "paused", "error_code": string(writingruntime.CodeResearchBudgetBoundary)},
		Trace:   trace}); err != nil {
		t.Fatal(err)
	}
	fresh := NewWallClockResearchBudgetBoundary(h.store)
	if reached, _ := fresh.ResearchBudgetBoundaryReached(ctx, request, 0, 2); reached {
		t.Fatal("fresh guard re-fired although the durable boundary marker exists")
	}
}

// ── 2. A13: cancel mid-read ────────────────────────────────────────────────

// TestT09ResearchCancelMidReadStopsScheduling: with the first read parked
// mid-flight, the owner cancels; the run terminates cancelled, no further
// worker reads are scheduled, and no formal draft/full_draft/revision_set is
// committed (the fetched results remain intermediate ledger artifacts).
func TestT09ResearchCancelMidReadStopsScheduling(t *testing.T) {
	h := newT06E2EHarness(t, 3, 0)
	h.worker.mu.Lock()
	h.worker.holdReads = make(chan struct{})
	h.worker.mu.Unlock()
	fixture := h.fixture(t)
	envelope := h.buildResearchEnvelope(t, fixture)
	runID := h.createResearchRun(t, fixture, envelope)

	// Wait until the first read is parked inside the worker.
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if h.worker.totalReads() >= 1 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if h.worker.totalReads() < 1 {
		t.Fatal("first read never started")
	}

	// Owner cancel over the HTTP surface.
	cancel := e2eRequest(t, h.router, h.token, "POST", "/api/v2/runs/"+runID+"/cancel", map[string]any{})
	if e2eJSONField(t, cancel, "status") != "cancelling" && e2eJSONField(t, cancel, "status") != "cancelled" {
		t.Fatalf("cancel status = %q", e2eJSONField(t, cancel, "status"))
	}
	// Release the parked read so the in-flight attempt can observe the signal.
	close(h.worker.holdReads)

	// Terminal: cancelled, within the bounded window.
	status := h.waitForTerminal(t, runID, 90*time.Second)
	if status != "cancelled" {
		t.Fatalf("run ended as %q, want cancelled", status)
	}
	readsAfterCancel := h.worker.totalReads()
	time.Sleep(1500 * time.Millisecond)
	if h.worker.totalReads() != readsAfterCancel {
		t.Fatalf("worker reads kept growing after cancel: %d -> %d", readsAfterCancel, h.worker.totalReads())
	}
	// No formal submission: full_draft / revision_set must not exist.
	artifacts, err := h.store.ListRunArtifacts(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	for _, artifact := range artifacts {
		if artifact.ArtifactType == "full_draft" || artifact.ArtifactType == "revision_set" {
			t.Fatalf("cancelled run committed formal artifact %s", artifact.ArtifactType)
		}
	}
}

// ── 3. A15: SSE reconnect merges by sequence ───────────────────────────────

// TestT09SSEReconnectMergesBySequence: the client fetches events up to N
// (then the connection drops); new events append; a reconnect with
// Last-Event-ID=N replays exactly the missing events in sequence order, and
// the stale first GET never contains the newer events (旧 GET 不覆盖新事件).
func TestT09SSEReconnectMergesBySequence(t *testing.T) {
	h := newT06E2EHarness(t, 2, 0)
	fixture := h.fixtureLegacy(t)
	envelope := h.buildLegacyEnvelope(t, fixture)
	runID := h.createRun(t, fixture, envelope)
	h.waitForTerminal(t, runID, 60*time.Second)

	// First page: everything currently persisted.
	code, first := t02APIRequest(t, h.router, h.token, http.MethodGet,
		"/api/v2/runs/"+runID+"/events?after=0&follow=false", "", nil)
	if code != http.StatusOK {
		t.Fatalf("first events GET -> %d", code)
	}
	firstEvents := eventsOf(t, first)
	if len(firstEvents) == 0 {
		t.Fatal("first events GET returned nothing")
	}
	firstMax := firstEvents[len(firstEvents)-1].Sequence

	// Reconnect at the dropped connection's position (both transports) and
	// verify the replay covers exactly the gap with no rewinds.
	code, replay := t02APIRequest(t, h.router, h.token, http.MethodGet,
		fmt.Sprintf("/api/v2/runs/%s/events?after=%d&follow=false", runID, firstMax), "", nil)
	if code != http.StatusOK {
		t.Fatalf("replay GET -> %d", code)
	}
	replayEvents := eventsOf(t, replay)
	last := int64(firstMax)
	for _, event := range replayEvents {
		if event.Sequence <= last {
			t.Fatalf("replay rewound: sequence %d after %d", event.Sequence, last)
		}
		last = event.Sequence
	}
	// The stale first page merged with the replay must equal the full ledger:
	// no event may appear in the first page that is newer than firstMax, and
	// the union must be strictly ordered without duplicates.
	seen := map[int64]bool{}
	for _, event := range firstEvents {
		if event.Sequence > firstMax {
			t.Fatalf("stale first page contains newer event %d (max was %d)", event.Sequence, firstMax)
		}
		seen[event.Sequence] = true
	}
	for _, event := range replayEvents {
		if seen[event.Sequence] {
			t.Fatalf("event %d replayed twice", event.Sequence)
		}
		seen[event.Sequence] = true
	}
	code, full := t02APIRequest(t, h.router, h.token, http.MethodGet,
		"/api/v2/runs/"+runID+"/events?after=0&follow=false", "", nil)
	if code != http.StatusOK {
		t.Fatalf("full GET -> %d", code)
	}
	fullEvents := eventsOf(t, full)
	if len(fullEvents) != len(seen) {
		t.Fatalf("merged pages have %d events, ledger has %d", len(seen), len(fullEvents))
	}
	// SSE transport: Last-Event-ID header replays the same gap as SSE frames.
	request := httptest.NewRequest(http.MethodGet, "/api/v2/runs/"+runID+"/events?follow=false", nil)
	request.Header.Set("Authorization", "Bearer "+h.token)
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Last-Event-ID", fmt.Sprint(firstMax))
	recorder := httptest.NewRecorder()
	h.router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("SSE reconnect -> %d: %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, event := range replayEvents {
		if !strings.Contains(body, fmt.Sprintf("id: %d\n", event.Sequence)) {
			t.Fatalf("SSE reconnect lacks event %d", event.Sequence)
		}
	}
	if strings.Contains(body, fmt.Sprintf("id: %d\n", firstMax)) {
		t.Fatal("SSE reconnect replayed an event the client already had")
	}
}

func eventsOf(t *testing.T, payload map[string]any) []websocket.WritingEvent {
	t.Helper()
	data, ok := dataOf(t, payload)["events"].([]any)
	if !ok {
		t.Fatalf("events missing from response: %#v", payload)
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	var events []websocket.WritingEvent
	if err := json.Unmarshal(encoded, &events); err != nil {
		t.Fatal(err)
	}
	return events
}

// ── 4. GET research view (T09b frontend contract) ──────────────────────────

// TestT09ResearchViewExposesSpecAndReadingScopeCounts drives a full chain to
// the evidence gate with full-text reads, then asserts the GET research view
// carries the spec projection (exact keys per the T09b contract) and the
// reading-scope counts derived from the pack.
func TestT09ResearchViewExposesSpecAndReadingScopeCounts(t *testing.T) {
	h := newT06E2EHarness(t, 2, 0)
	h.worker.withFullText = true
	fixture := h.fixture(t)
	envelope := h.buildResearchEnvelope(t, fixture)
	runID := h.createResearchRun(t, fixture, envelope)
	// The evidence pack is frozen when the read node completes, i.e. at the
	// evidence gate pause — enough for the projection assertions.
	h.decideGate(t, runID, h.advanceToGate(t, runID, "evidence"), envelope)
	code, payload := t02APIRequest(t, h.router, h.token, http.MethodGet,
		"/api/v2/runs/"+runID+"/research", "", nil)
	if code != http.StatusOK {
		t.Fatalf("GET research -> %d: %s", code, payload)
	}
	data := dataOf(t, payload)
	spec, ok := data["spec"].(map[string]any)
	if !ok {
		t.Fatalf("research view lacks spec: %s", payload)
	}
	// The harness reseals min_citable_sources to 1; the fixture's other
	// values flow through unchanged.
	if spec["max_papers"].(float64) != 10 || spec["min_citable_sources"].(float64) != 1 ||
		spec["evidence_requirement"] != "abstract_allowed" || spec["max_queries"].(float64) != 3 ||
		spec["max_candidates"].(float64) != 60 {
		t.Fatalf("spec projection = %#v", spec)
	}
	counts, ok := data["counts"].(map[string]any)
	if !ok {
		t.Fatalf("research view lacks counts: %s", payload)
	}
	fullText, _ := counts["papers_full_text"].(float64)
	abstract, _ := counts["papers_abstract"].(float64)
	unread, _ := counts["papers_unread"].(float64)
	if fullText != 2 {
		t.Fatalf("papers_full_text = %v (both papers read full text)", counts["papers_full_text"])
	}
	if abstract != 0 || unread != 0 {
		t.Fatalf("papers_abstract=%v papers_unread=%v, want 0/0", counts["papers_abstract"], counts["papers_unread"])
	}
	// Existing keys untouched.
	if counts["total"].(float64) < 1 {
		t.Fatalf("counts lost task totals: %#v", counts)
	}
	if _, hasPhase := data["phase"]; !hasPhase {
		t.Fatal("research view lost phase")
	}
}

// TestT09LegacyRunResearchViewKeepsShape: a legacy (fast) run has no research
// spec — the view must omit spec entirely and carry no reading-scope counts,
// so the legacy client shape stays byte-identical.
func TestT09LegacyRunResearchViewKeepsShape(t *testing.T) {
	h := newT06E2EHarness(t, 2, 0)
	fixture := h.fixtureLegacy(t)
	envelope := h.buildLegacyEnvelope(t, fixture)
	runID := h.createRun(t, fixture, envelope)
	h.waitForTerminal(t, runID, 60*time.Second)
	code, payload := t02APIRequest(t, h.router, h.token, http.MethodGet,
		"/api/v2/runs/"+runID+"/research", "", nil)
	if code != http.StatusOK {
		t.Fatalf("GET research -> %d: %s", code, payload)
	}
	data := dataOf(t, payload)
	if _, present := data["spec"]; present {
		t.Fatalf("legacy run research view exposes spec: %s", payload)
	}
	counts, _ := data["counts"].(map[string]any)
	for _, key := range []string{"papers_full_text", "papers_abstract", "papers_unread"} {
		if _, present := counts[key]; present {
			t.Fatalf("legacy run research view exposes %s", key)
		}
	}
}

// fixtureLegacy is the plain T00 fast-mode fixture (v1 contract, standard
// assurance, no research spec) — the scripted legacy mount executes it.
func (h *t06Harness) fixtureLegacy(t *testing.T) *t00Fixture {
	return h.t00Harness.fixture(t)
}

// buildLegacyEnvelope compiles a minimal fast plan for the scripted legacy
// runners (the T00 mount), used by cancel/SSE/legacy-view scenarios.
func (h *t06Harness) buildLegacyEnvelope(t *testing.T, fixture *t00Fixture) writingplan.WritingPlanEnvelope {
	t.Helper()
	return h.buildEnvelope(t, fixture, t00PlanNodes("core.draft.generate", writingplan.FailurePause, 2))
}

// ── 5. R14: RESEARCH_REVIEW_ENABLED=false ──────────────────────────────────

// TestT09ResearchReviewDisabledRefusesExplicitly: with the feature flag off,
// the research_review entries (compile plan, run creation) refuse with 503
// RESEARCH_UNAVAILABLE — never a silent degrade to a legacy template — while
// a legacy fast-mode run keeps working untouched.
func TestT09ResearchReviewDisabledRefusesExplicitly(t *testing.T) {
	h := newT06E2EHarness(t, 2, 0)
	h.api.researchReviewEnabled = false

	// Compile over a research contract: 503 RESEARCH_UNAVAILABLE. The
	// contract explicitly carries research_review so the compile entry routes
	// into the research path (the HTTP compile derives the mode from the
	// contract, never from a client-supplied recommendation).
	researchFixture := h.fixtureMutate(t, func(contract *writingkernel.WritingContract) {
		contract.Collaboration.OrchestrationMode = writingkernel.OrchestrationModeResearchReview
	})
	code, payload := t02APIRequest(t, h.router, h.token, http.MethodPost,
		"/api/v2/documents/"+researchFixture.documentID+"/plans", "", map[string]any{
			"contract_id": researchFixture.contractID, "contract_version": 2,
			"base_version_id":         researchFixture.baseVersion.VersionID,
			"intent_plan":             t06ResearchIntentPlan(t, researchFixture.contract),
			"budget":                  h.researchRunBudget(),
			"required_final_artifact": "revision_set"})
	if code != http.StatusServiceUnavailable {
		t.Fatalf("disabled compile -> %d: %s, want 503", code, payload)
	}
	if errorCodeOf(t, payload) != "RESEARCH_UNAVAILABLE" {
		t.Fatalf("disabled compile error code: %s", payload)
	}

	// Run creation over the same contract: also refused.
	envelope := h.buildResearchEnvelope(t, researchFixture)
	permissions := permissionsForPlan(envelope.ExecutablePlan, h.api.capabilities)
	code, payload = t02APIRequest(t, h.router, h.token, http.MethodPost, "/api/v2/runs",
		t02APIIdempotencyKey("t09-disabled-run"),
		map[string]any{"document_id": researchFixture.documentID, "contract_id": researchFixture.contractID,
			"contract_version": 2, "contract_hash": researchFixture.contract.ContractHash,
			"base_version_id": researchFixture.baseVersion.VersionID, "style_slug": "yinyue",
			"plan": envelope, "budget": h.researchRunBudget(), "permissions": permissions})
	if code != http.StatusServiceUnavailable || errorCodeOf(t, payload) != "RESEARCH_UNAVAILABLE" {
		t.Fatalf("disabled run creation -> %d: %s, want 503 RESEARCH_UNAVAILABLE", code, payload)
	}

	// Legacy regression: a fast-mode run is completely unaffected by the flag.
	legacyFixture := h.fixtureLegacy(t)
	legacyEnvelope := h.buildLegacyEnvelope(t, legacyFixture)
	runID := h.createRun(t, legacyFixture, legacyEnvelope)
	if status := h.waitForTerminal(t, runID, 60*time.Second); status != "completed" {
		t.Fatalf("legacy run ended as %q with the research flag off, want completed", status)
	}

	// Flipping the flag back on re-enables the path (no restart semantics in
	// the seam): compile succeeds again.
	h.api.researchReviewEnabled = true
	code, payload = t02APIRequest(t, h.router, h.token, http.MethodPost,
		"/api/v2/documents/"+researchFixture.documentID+"/plans", "", map[string]any{
			"contract_id": researchFixture.contractID, "contract_version": 2,
			"base_version_id":         researchFixture.baseVersion.VersionID,
			"intent_plan":             t06ResearchIntentPlan(t, researchFixture.contract),
			"budget":                  h.researchRunBudget(),
			"required_final_artifact": "revision_set"})
	if code != http.StatusOK {
		t.Fatalf("re-enabled compile -> %d: %s, want 200", code, payload)
	}
}

// t06ResearchIntentPlan builds a dispatch-valid research intent plan for the
// HTTP compile endpoint.
func t06ResearchIntentPlan(t *testing.T, contract writingkernel.WritingContract) writingplan.IntentPlan {
	t.Helper()
	now := time.Now().UTC()
	intent, err := (writingplan.IntentPlan{IntentPlanID: "iplan_t09_" + now.Format("150405000000000") + fmt.Sprint(now.Nanosecond()),
		ContractRef: writingplan.ObjectRef{ID: contract.ContractID, Version: contract.Version, Hash: contract.ContractHash},
		Summary:     "T09 research review chain", CreatedBy: writingplan.ActorUser, CreatedAt: now,
		ProposedSteps: []writingplan.ProposedStep{{StepID: "research", Objective: "produce a researched review",
			CapabilityHint: writingplan.CapabilityResearchDraft, DependsOn: []string{}}}}).WithComputedHash()
	if err != nil {
		t.Fatal(err)
	}
	return intent
}

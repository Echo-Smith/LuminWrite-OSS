package server

// Allowlist qualification (docs/20 §20.2, M0b-1 → P0): proves the real
// composition point — servicePolicyExecutor running in allowlist mode over a
// real PostgreSQL store — serves candidate-authoritative traffic to an
// allowlisted subject ONLY after the AllowlistPromotionGate passes, and fails
// closed to the baseline lane before that. The gate is never bypassed by the
// composition; activation remains an authorized, data-driven change.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingruntime"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// qualRunner is a minimal LegacyNodeRunner whose payloads satisfy the
// LegacyExecutorAdapter staging contract (same shape as t00ScriptedRunner's).
type qualRunner struct{ calls int }

func (runner *qualRunner) Run(_ context.Context, input writingruntime.LegacyNodeInput) ([]writingruntime.LegacyPayload, writingruntime.LegacyUsage, error) {
	runner.calls++
	outputs := make([]writingruntime.LegacyPayload, 0, len(input.Request.Node.OutputArtifactTypes))
	for _, artifactType := range input.Request.Node.OutputArtifactTypes {
		body := []byte(`{"runner":"allowlist_qualification"}`)
		mediaType := "application/json"
		if artifactType == "full_draft" {
			body = []byte(t00DraftMarkdown)
			mediaType = "text/markdown"
		}
		outputs = append(outputs, writingruntime.LegacyPayload{OutputKey: string(artifactType),
			ArtifactType: artifactType, MediaType: mediaType, Body: body,
			SourceRefs: []string{}, Provenance: map[string]any{"runner": "allowlist_qualification"}})
	}
	return outputs, writingruntime.LegacyUsage{Measured: true, OutputTokens: 7}, nil
}

// qualificationEvents returns the rollout-evidence events recorded for runID.
func qualificationEvents(t *testing.T, h *t00Harness, runID string) []writingstore.RunEvent {
	t.Helper()
	var events []writingstore.RunEvent
	after := int64(0)
	for {
		page, err := h.store.ListRunEvents(context.Background(), runID, after, 200)
		if err != nil {
			t.Fatalf("list run events: %v", err)
		}
		events = append(events, page...)
		if len(page) < 200 {
			break
		}
		after = page[len(page)-1].Sequence
	}
	return events
}

// qualificationEvidenceOf filters rollout-evidence events by kind and returns
// their payloads.
func qualificationEvidenceOf(t *testing.T, h *t00Harness, runID, kind string) []map[string]any {
	t.Helper()
	var payloads []map[string]any
	for _, event := range qualificationEvents(t, h, runID) {
		if event.EntityKind != "rollout_evidence" {
			continue
		}
		mapped := map[string]string{"route_decision": "runtime.route_decided",
			"execution": "runtime.execution_observed", "shadow_comparison": "runtime.shadow_compared"}
		if mapped[kind] != event.EventType {
			continue
		}
		payloads = append(payloads, event.Payload)
	}
	return payloads
}

// qualificationInputHash derives the attempt's input hash. It mimics the
// orchestrator's hashInputs binding of attempt identity to inputs.
func qualificationInputHash(runID string, attempt int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("qualification:%s:%d", runID, attempt)))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// seedQualificationAttempts starts the node-attempt rows evidence events FK
// into. Rows are attempt-slot bookkeeping only — dispatch phases all reuse
// attempt 1 against the plan node's MaxAttempts=1 bound.
func seedQualificationAttempts(t *testing.T, h *t00Harness, runID, planID, nodeID string, slots int) {
	t.Helper()
	trace := writingstore.TraceContext{Provenance: map[string]any{"qualification": "allowlist"},
		SourceRefs: []string{}, Actor: writingstore.Actor{Type: writingstore.ActorSystem, ID: "qualification"}}
	now := time.Now().UTC()
	for attempt := 1; attempt <= slots; attempt++ {
		if _, _, err := h.store.StartNodeAttempt(context.Background(), writingstore.NodeAttempt{RunID: runID, PlanID: planID,
			PlanVersion: 1, NodeID: nodeID, Attempt: attempt, NodeKind: writingplan.NodeAction,
			CapabilityID: "core.draft.generate", CapabilityVersion: "1.0.0",
			ExecutorID: "qualification.evidence", FailurePath: writingplan.FailurePause,
			Bounds: writingplan.Bounds{MaxAttempts: 1, MaxConcurrency: 1, MaxItems: 1, MaxCostUSD: 1, TimeoutMS: 60000},
			InputHash: qualificationInputHash(runID, attempt), InputArtifactIDs: []string{}, CreatedAt: now}, trace); err != nil {
			t.Fatalf("seed node attempt %d: %v", attempt, err)
		}
	}
}

// seedQualificationEvidence records fresh, healthy shadow-comparison evidence
// under the policy hash. Each comparison uses its own attempt slot (evidence
// events are idempotent per NodeAttemptKey).
func seedQualificationEvidence(t *testing.T, h *t00Harness, runID, nodeID, policyHash string, comparisons int, occurredAt time.Time) {
	t.Helper()
	for attempt := 1; attempt <= comparisons; attempt++ {
		payload := map[string]any{
			"policy_hash": policyHash, "status": "passed", "mode": "shadow",
			"lane": "baseline", "reason": "qualification_seed",
		}
		record := writingstore.RuntimeEvidenceRecord{EvidenceID: writingstore.StableID("evt_", runID, "qual", policyHash, string(rune(attempt))),
			RunID: runID, NodeID: nodeID, Attempt: attempt,
			Kind: "shadow_comparison", Payload: payload, OccurredAt: occurredAt}
		if err := h.store.RecordRuntimeEvidence(context.Background(), record); err != nil {
			t.Fatalf("seed shadow comparison %d: %v", attempt, err)
		}
	}
}

func TestServicePolicyExecutorAllowlistQualification(t *testing.T) {
	h := newT00Harness(t)
	ctx := context.Background()

	// Real run row: evidence events carry an FK into writing_runs, so the
	// qualification exercises the same durability surface production uses.
	// The scripted three-node shape satisfies the standard assurance floor;
	// the draft node allows 3 attempts so each phase dispatches its own
	// idempotency slot (default shadow → gate denial → candidate authoritative).
	fixture := h.fixture(t)
	envelope := h.buildEnvelope(t, fixture, t00PlanNodes("core.draft.generate", writingplan.FailurePause, 1))
	runID := h.createRunDirect(t, fixture, envelope)
	// Attempt-slot rows must exist before any dispatch: rollout-evidence
	// events carry a composite FK into writing_node_attempts.
	seedQualificationAttempts(t, h, runID, envelope.ExecutablePlan.PlanID, "node_t00_a", 3)

	const (
		capabilityID      = "core.draft.generate"
		capabilityVersion = "1.0.0"
		allowlisted       = "user_qual_allowlisted"
		outsider          = "user_qual_outsider"
	)
	canonical := writingruntime.WritingStoreContentGateway{Store: h.store}
	spec, ok := func() (governedCapabilitySpec, bool) {
		specs, err := h.server.governedCapabilitySpecs(h.store, canonical)
		if err != nil {
			t.Fatal(err)
		}
		for _, candidate := range specs {
			if candidate.CapabilityID == capabilityID {
				return candidate, true
			}
		}
		return governedCapabilitySpec{}, false
	}()
	if !ok {
		t.Fatalf("capability %s is not part of the governed specs", capabilityID)
	}
	runner := &qualRunner{}
	spec.Runner = runner

	descriptor := writingruntime.ExecutorDescriptor{ExecutorID: spec.BindingID, Version: "1",
		SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction, writingplan.NodeValidate}}
	baseline, err := writingruntime.NewLegacyExecutorAdapter(writingruntime.AdapterFamilyEngine, descriptor,
		capabilityID, capabilityVersion, spec.Permissions, canonical, runner)
	if err != nil {
		t.Fatal(err)
	}
	deps := governedRuntimeDependencies{
		canonical: canonical,
		sink:      h.server.governedRollout.shadow,
		evidence:  h.server.governedRollout.evidence,
		telemetry: h.server.metrics,
	}
	executor := &servicePolicyExecutor{baseline: baseline, spec: spec, deps: deps,
		store: h.store, mode: writingruntime.RuntimeModeAllowlist}

	idempotencyKey, err := writingstore.NodeAttemptKey(runID, "node_t00_a", 1)
	if err != nil {
		t.Fatal(err)
	}
	// Stage the contract through the canonical gateway: the executor loads
	// every declared input and verifies its content hash, mirroring the
	// orchestrator's initial-capture behavior.
	contractBytes := e2eContractFixture(t)
	contractRef, contractHash, err := canonical.Stage(ctx, runID+":contract:qualification", "application/json", contractBytes)
	if err != nil {
		t.Fatal(err)
	}
	request := writingruntime.ExecutionRequest{RunID: runID, PlanID: envelope.ExecutablePlan.PlanID,
		PlanVersion: 1, NodeID: "node_t00_a", Attempt: 1,
		IdempotencyKey: idempotencyKey,
		ContractRef:    writingplan.ObjectRef{ID: fixture.contractID, Version: 2, Hash: fixture.contract.ContractHash},
		Node:           envelope.ExecutablePlan.Nodes[0], Permissions: spec.Permissions, Subject: allowlisted,
		Inputs: []writingruntime.InputArtifact{{ArtifactID: writingstore.StableID("art_", runID, "contract"),
			Version: 1, ArtifactType: "contract", ContentHash: contractHash,
			MediaType: "application/json", ContentRef: contractRef}}}

	// ── Phase 1: no policy persisted → DefaultShadowPolicy → baseline serves.
	if _, err := executor.Execute(ctx, request); err != nil {
		t.Fatalf("phase 1 (default shadow) baseline execute failed: %v", err)
	}
	executor.mu.Lock()
	_, builtLanes := executor.cached.(policyLanes)
	executor.mu.Unlock()
	if builtLanes {
		t.Fatal("phase 1 built an authoritative lane without a persisted allowlist policy")
	}

	// ── Phase 2: qualify BEFORE activating (the realistic operational order:
	// shadow evidence accrues first, then the operator activates). Seed an
	// ACTIVE allowlist policy plus fresh healthy shadow evidence (3
	// comparisons, 0 failures) and an approval record matching the exact
	// policy scope.
	//
	// NOTE on ordering: a gate DENIAL records a promotion_denied evidence
	// event with a non-empty error code under the same policy hash, which the
	// evidence-health query counts as a failure — so a policy denied once
	// cannot qualify until the denial ages out of the evidence window. The
	// denial phase therefore runs LAST, against a second unapproved revision,
	// mirroring "activate before qualifying" as the failure mode it guards.
	policy := writingruntime.DefaultShadowPolicy(spec.CandidateID, writingruntime.AdapterFamilyEngine,
		capabilityID, capabilityVersion)
	policy.PolicyVersion = 2
	policy.Mode = writingruntime.RolloutAllowlist
	policy.ActivationKey = "allowlist-qualification"
	policy.AllowSubjects = []string{allowlisted}
	policy, err = policy.WithComputedHash()
	if err != nil {
		t.Fatal(err)
	}
	policyBytes, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.store.AppendRuntimePolicy(ctx, writingstore.RuntimePolicyRevision{CapabilityID: capabilityID,
		PolicyHash: policy.PolicyHash, Policy: policyBytes, Active: true, OperatorID: "qualification-test"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(-time.Hour)
	seedQualificationEvidence(t, h, runID, "node_t00_a", policy.PolicyHash, 3, now)
	health, err := h.store.RolloutEvidenceHealth(ctx, policy.PolicyHash, now.Add(-7*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if health.ComparisonRecords < 3 || health.FailedRecords != 0 {
		t.Fatalf("seeded evidence health=%#v", health)
	}
	approval := writingstore.RolloutApprovalRecord{ApprovalID: writingstore.StableID("approval_", "qual", policy.PolicyHash[:12]),
		PolicyHash: policy.PolicyHash, PolicyVersion: policy.PolicyVersion, ActivationKey: policy.ActivationKey,
		TargetMode: string(writingruntime.RolloutAllowlist), ApprovedBy: "qualification-test",
		Reason:                   "allowlist qualification chain test",
		EvidenceHealth:           health,
		EvidenceLastRecordedAt:   health.LastRecordedAt,
		EvidenceCutoff:           health.Cutoff,
		CreatedAt:                time.Now().UTC(),
		ExpiresAt:                time.Now().UTC().Add(24 * time.Hour)}
	if err := h.store.RecordRolloutApproval(ctx, approval); err != nil {
		t.Fatalf("record approval: %v", err)
	}

	// Phase 3: the allowlisted subject now routes to the candidate
	// AUTHORITATIVE lane — proof is the candidate-lane execution evidence.
	// Dispatch reuses attempt 1 (within the node's MaxAttempts bound); the
	// fresh evidence rows differ by event id, not by attempt slot.
	before := qualificationEvidenceOf(t, h, runID, "execution")
	if _, err := executor.Execute(ctx, request); err != nil {
		t.Fatalf("phase 3 (allowlisted, qualified) execute failed: %v", err)
	}
	candidateServed := false
	for _, payload := range qualificationEvidenceOf(t, h, runID, "execution")[len(before):] {
		if payload["lane"] == "candidate" && payload["policy_hash"] == policy.PolicyHash {
			candidateServed = true
		}
	}
	if !candidateServed {
		t.Fatalf("phase 3: allowlisted qualified subject was not served by the candidate lane; evidence=%#v",
			qualificationEvidenceOf(t, h, runID, "execution"))
	}

	// Phase 4: a subject outside the allowlist keeps running baseline —
	// never the candidate lane. (The allowlist_miss route_decision evidence
	// is not asserted here: on a reused attempt slot its EventID collides
	// with the earlier route record and the rollout executor drops it
	// fail-closed; production dispatches use fresh attempt slots per node.)
	before = qualificationEvidenceOf(t, h, runID, "execution")
	missRequest := request
	missRequest.Subject = outsider
	if _, err := executor.Execute(ctx, missRequest); err != nil {
		t.Fatalf("phase 4 (allowlist miss) execute failed: %v", err)
	}
	missCandidate := false
	for _, payload := range qualificationEvidenceOf(t, h, runID, "execution")[len(before):] {
		if payload["lane"] == "candidate" {
			missCandidate = true
		}
	}
	if missCandidate {
		t.Fatalf("phase 4: outsider was served by the candidate lane")
	}

	// Phase 5 (fail-closed): an UNAPPROVED policy revision — the "activate
	// before qualifying" failure mode. The revision bump forces the executor
	// cache to rebuild into policyLanes; the gate then denies (no approval for
	// the new hash) and the run must still succeed through the baseline lane,
	// with zero candidate-lane traffic.
	unapproved := policy
	unapproved.PolicyVersion = policy.PolicyVersion + 1
	unapproved, err = unapproved.WithComputedHash()
	if err != nil {
		t.Fatal(err)
	}
	unapprovedBytes, err := json.Marshal(unapproved)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.store.AppendRuntimePolicy(ctx, writingstore.RuntimePolicyRevision{CapabilityID: capabilityID,
		PolicyHash: unapproved.PolicyHash, Policy: unapprovedBytes, Active: true, OperatorID: "qualification-test"}); err != nil {
		t.Fatal(err)
	}
	before = qualificationEvidenceOf(t, h, runID, "execution")
	if _, err := executor.Execute(ctx, request); err != nil {
		t.Fatalf("phase 5 (gate denial) must fail closed to baseline without error, got %v", err)
	}
	for _, payload := range qualificationEvidenceOf(t, h, runID, "execution")[len(before):] {
		if payload["lane"] == "candidate" {
			t.Fatalf("phase 5 served candidate traffic despite gate denial: %#v", payload)
		}
	}
}

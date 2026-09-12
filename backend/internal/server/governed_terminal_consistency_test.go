package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingruntime"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// T00 run terminal-consistency and recovery baseline
// (docs/plans/2026-09-07-research-review-integration.md §T00).
//
// These tests drive the governed run lifecycle end to end — HTTP run
// creation, the background governed worker, the store's projections, and the
// HTTP GET projection — with fully scripted, model-free node runners, so the
// terminal behavior is deterministic and independent of any LLM deployment.
//
// Terminal scenarios assert the four projections the workbench depends on
// agree inside a bounded test window:
//  1. the persisted run status (writing_runs.status — what HTTP GET serves),
//  2. the event ledger (contiguous sequences from 1, no gaps, no losses),
//  3. the latest checkpoint snapshot (when the terminal path writes one), and
//  4. the HTTP GET /runs/{id} projection itself.
//
// The scripted-runner E2E fixture mirrors governed_runners_test.go's HTTP
// flow; the controlled capability dispatches through a t00ScriptedRunner that
// fails, blocks, or completes per script.

// t00User is the JWT subject for these tests; dbtest isolates each process's
// database so the fixed uid cannot collide across packages.
const t00User = "00000000-0000-0000-0000-00000000t000"

const (
	// t00SafeCapability is the test-owned draft capability with
	// IdempotencySafe semantics. It is the only way a scripted node failure
	// reaches the failNode path: every catalog capability is
	// IdempotencyRequired, and required nodes park into the unsafe_retry
	// pause instead of failing the run (never blind-retry).
	t00SafeCapability = "core.t00.draft_safe"
	t00SafeBinding    = "governed.baseline.core.t00.draft_safe"
	// t00DraftMarkdown is the scripted draft body the delivery protocol
	// parses into the candidate document version.
	t00DraftMarkdown = "# T00 候选标题\n\n这是T00终态一致性验证用的候选正文段落。\n"
)

// t00ScriptedRunner is the controllable no-model node runner. Per capability
// it can fail the first N attempts and/or block the in-flight attempt until
// released; successful completions emit every declared output type with
// measured usage.
type t00ScriptedRunner struct {
	mu       sync.Mutex
	failures map[string]int
	blocked  map[string]chan struct{}
	calls    map[string]int
}

func newT00ScriptedRunner() *t00ScriptedRunner {
	return &t00ScriptedRunner{failures: map[string]int{}, blocked: map[string]chan struct{}{}, calls: map[string]int{}}
}

func (runner *t00ScriptedRunner) failNext(capability string, failures int) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.failures[capability] += failures
}

func (runner *t00ScriptedRunner) blockOn(capability string) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.blocked[capability] = make(chan struct{})
}

func (runner *t00ScriptedRunner) release(capability string) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if ch := runner.blocked[capability]; ch != nil {
		close(ch)
		runner.blocked[capability] = nil
	}
}

// releaseAll unblocks every parked attempt; harness cleanup uses it so a
// failing test never leaks a blocked worker goroutine.
func (runner *t00ScriptedRunner) releaseAll() {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	for capability, ch := range runner.blocked {
		if ch != nil {
			close(ch)
			runner.blocked[capability] = nil
		}
	}
}

func (runner *t00ScriptedRunner) callCount(capability string) int {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return runner.calls[capability]
}

func (runner *t00ScriptedRunner) Run(ctx context.Context, input writingruntime.LegacyNodeInput) ([]writingruntime.LegacyPayload, writingruntime.LegacyUsage, error) {
	capability := input.Request.Node.Capability
	runner.mu.Lock()
	runner.calls[capability]++
	if failures := runner.failures[capability]; failures > 0 {
		runner.failures[capability] = failures - 1
		runner.mu.Unlock()
		return nil, writingruntime.LegacyUsage{}, fmt.Errorf("t00 scripted failure for %s", capability)
	}
	block := runner.blocked[capability]
	runner.mu.Unlock()
	if block != nil {
		select {
		case <-ctx.Done():
			return nil, writingruntime.LegacyUsage{}, ctx.Err()
		case <-block:
		}
	}
	outputs := make([]writingruntime.LegacyPayload, 0, len(input.Request.Node.OutputArtifactTypes))
	for _, artifactType := range input.Request.Node.OutputArtifactTypes {
		body := []byte(fmt.Sprintf(`{"capability":%q,"node":%q,"attempt":%d}`, capability, input.Request.NodeID, input.Request.Attempt))
		if artifactType == "full_draft" {
			body = []byte(t00DraftMarkdown)
		}
		outputs = append(outputs, writingruntime.LegacyPayload{OutputKey: string(artifactType),
			ArtifactType: artifactType, MediaType: "application/json", Body: body,
			SourceRefs: []string{}, Provenance: map[string]any{"runner": "t00_scripted"}})
		if artifactType == "full_draft" {
			outputs[len(outputs)-1].MediaType = "text/markdown"
		}
	}
	return outputs, writingruntime.LegacyUsage{Measured: true, OutputTokens: 7}, nil
}

// t00Harness assembles the real governed runtime over the migrated test
// database with the scripted runners, plus the HTTP writing surface.
type t00Harness struct {
	server  *Server
	router  http.Handler
	store   *writingstore.Store
	runners map[string]*t00ScriptedRunner
	api     *persistentWritingAPI
	token   string
	userID  string
}

// newT00Harness boots the E2E server (skips without TEST_DATABASE_URL),
// swaps every factory-served runner for the scripted one, re-mounts the
// governed runtime, and provisions the fixture user.
func newT00Harness(t *testing.T) *t00Harness {
	t.Helper()
	server, _, _ := newGovernedE2EServer(t)
	store := server.writingStoreForTest()
	if store == nil {
		t.Fatal("governed store missing")
	}
	api, ok := server.writingAPI.(*persistentWritingAPI)
	if !ok || api.controller == nil || api.trigger == nil || server.governedRollout == nil {
		t.Fatal("governed runtime not mounted (shadow mode expected)")
	}
	var userID string
	if err := server.db.QueryRow(`INSERT INTO users (uid, name) VALUES ($1, 't00 user')
		ON CONFLICT (uid) DO UPDATE SET name = EXCLUDED.name RETURNING id::text`, t00User).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	canonical := writingruntime.WritingStoreContentGateway{Store: store}
	deps := governedRuntimeDependencies{
		canonical:       canonical,
		sink:            server.governedRollout.shadow,
		evidence:        server.governedRollout.evidence,
		transitionStore: writingruntime.WritingStoreTransitionRecorder{Store: store},
		checkpoints: writingruntime.PersistentCheckpointRepository{Store: store,
			Trace: writingstore.TraceContext{Actor: writingstore.Actor{Type: writingstore.ActorSystem, ID: "writingruntime"},
				Provenance: map[string]any{}, SourceRefs: []string{}}},
		initial:   governedInitialProvider{store: store, server: server},
		materials: store,
		context:   writingruntime.StoreContextSource{Store: store},
		telemetry: server.metrics,
	}
	specs, err := server.governedCapabilitySpecs(store, canonical)
	if err != nil {
		t.Fatal(err)
	}
	capabilities := writingplan.DefaultCapabilityRegistry()
	executors := writingruntime.NewExecutorRegistry()
	mount := func(spec governedCapabilitySpec, runner writingruntime.LegacyNodeRunner) {
		t.Helper()
		if err := t00MountScriptedCapability(capabilities, executors, spec, runner, deps, canonical); err != nil {
			t.Fatalf("mount scripted capability %s: %v", spec.CapabilityID, err)
		}
	}
	runners := map[string]*t00ScriptedRunner{}
	for index := range specs {
		runner := newT00ScriptedRunner()
		runners[specs[index].CapabilityID] = runner
		specs[index].Runner = runner
		mount(specs[index], runner)
	}
	safeRunner := newT00ScriptedRunner()
	runners[t00SafeCapability] = safeRunner
	if err := t00RegisterSafeDraftCapability(capabilities); err != nil {
		t.Fatalf("declare scripted safe capability: %v", err)
	}
	mount(t00SafeSpec(), safeRunner)
	orchestrator := &writingruntime.Orchestrator{Store: store, Capabilities: capabilities, Executors: executors,
		State: writingruntime.NewStateMachine(deps.transitionStore), Checkpoints: deps.checkpoints, Initial: deps.initial,
		Materials: deps.materials, Telemetry: deps.telemetry,
		Subject: func(run writingstore.RuntimeRun) string { return run.OwnerUserID },
		Context: deps.context, Envelopes: store,
		ContextRuntime: &writingruntime.ContextRuntime{},
		Delivery: &writingruntime.DeliveryProtocol{Store: store, Content: canonical,
			Actor: writingstore.Actor{Type: writingstore.ActorSystem, ID: "writingruntime.delivery"}}}
	runtime := &governedWritingRuntime{orchestrator: orchestrator,
		controller: governedRunController{orchestrator: orchestrator, store: store}, capabilities: capabilities, mode: writingruntime.RuntimeModeShadow}
	api.controller = runtime.controller
	api.trigger = &governedRunTrigger{orchestrator: runtime.orchestrator, store: store}
	api.capabilities = runtime.capabilities
	server.governedTrigger = api.trigger
	token, err := server.GenerateJWT(userID, "user", "session_t00")
	if err != nil {
		t.Fatal(err)
	}
	harness := &t00Harness{server: server, router: newE2ERouter(server), store: store, runners: runners, api: api, token: token, userID: userID}
	t.Cleanup(func() {
		for _, runner := range runners {
			runner.releaseAll()
		}
	})
	return harness
}

// t00MountScriptedCapability wires one capability's manifest + scripted
// runner into the test runtime with a deterministic off-policy rollout
// executor. The production composition's default shadow policy would run a
// shadow observation lane concurrently with the baseline, and both lanes
// share the scripted runner — the shadow lane would nondeterministically
// consume scripted failure slots. The off policy keeps the exact production
// registration shape (offline adapter behind a rollout executor) while
// routing every dispatch to the baseline lane only.
func t00MountScriptedCapability(capabilities *writingplan.CapabilityRegistry, executors *writingruntime.ExecutorRegistry, spec governedCapabilitySpec, runner writingruntime.LegacyNodeRunner, deps governedRuntimeDependencies, canonical writingruntime.ContentGateway) error {
	manifest, ok := capabilities.Get(spec.CapabilityID)
	if !ok {
		return fmt.Errorf("capability %s is not in the catalog", spec.CapabilityID)
	}
	if !capabilities.ExecutorRegistered(spec.BindingID) {
		if err := capabilities.RegisterExecutor(writingplan.ExecutorBinding{ID: spec.BindingID,
			AcceptedInputTypes:  append(append([]writingplan.ArtifactType(nil), manifest.InputTypes...), manifest.OptionalInputTypes...),
			ProducedOutputTypes: append([]writingplan.ArtifactType(nil), manifest.OutputTypes...),
			Dispatch: func(context.Context, writingplan.ExecutionRequest) (writingplan.ExecutionResult, error) {
				return writingplan.ExecutionResult{}, nil
			}}); err != nil {
			return err
		}
	}
	if err := capabilities.Activate(spec.CapabilityID, spec.BindingID); err != nil {
		return err
	}
	baseline, err := writingruntime.NewLegacyExecutorAdapter(writingruntime.AdapterFamilyEngine,
		writingruntime.ExecutorDescriptor{ExecutorID: spec.BindingID, Version: "1",
			SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction, writingplan.NodeValidate}},
		spec.CapabilityID, spec.CapabilityVersion, spec.Permissions, canonical, runner)
	if err != nil {
		return err
	}
	policy := writingruntime.DefaultShadowPolicy(spec.CandidateID, writingruntime.AdapterFamilyEngine, spec.CapabilityID, spec.CapabilityVersion)
	policy.Mode = writingruntime.RolloutOff
	policy.Reason = "t00_scripted_capability"
	policy, err = policy.WithComputedHash()
	if err != nil {
		return err
	}
	provider, err := writingruntime.NewMutableRolloutPolicyProvider(policy)
	if err != nil {
		return err
	}
	rollout, err := writingruntime.NewRolloutExecutor(baseline, baseline, provider, deps.evidence, deps.telemetry)
	if err != nil {
		return err
	}
	return executors.Register(rollout)
}

// t00SafeSpec is the test-owned IdempotencySafe draft capability spec.
func t00SafeSpec() governedCapabilitySpec {
	return governedCapabilitySpec{
		BindingID: t00SafeBinding, CandidateID: "governed.candidate." + t00SafeCapability,
		CapabilityID: t00SafeCapability, CapabilityVersion: "1.0.0",
		Inputs:      []writingplan.ArtifactType{"contract"},
		Outputs:     []writingplan.ArtifactType{"full_draft"},
		Permissions: []writingplan.Permission{"model.invoke", "materials.read"},
	}
}

// t00RegisterSafeDraftCapability declares the test-owned IdempotencySafe
// draft capability in the registry the plan compiler validates against.
func t00RegisterSafeDraftCapability(capabilities *writingplan.CapabilityRegistry) error {
	manifest := writingplan.CapabilityManifest{ID: t00SafeCapability, Class: "writing.draft", Executor: t00SafeBinding,
		InputTypes: []writingplan.ArtifactType{"contract"}, OptionalInputTypes: []writingplan.ArtifactType{"outline", "source_pack"},
		OutputTypes: []writingplan.ArtifactType{"full_draft"}, Permissions: []writingplan.Permission{"model.invoke", "materials.read"},
		EstimatedCostUSD: .5, EstimatedDurationMS: 30000, PreservesVoice: true, Version: "1.0.0",
		SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction},
		MaxBounds:          writingplan.Bounds{MaxAttempts: 2, MaxConcurrency: 1, MaxItems: 1, MaxCostUSD: 2, TimeoutMS: 120000},
		Idempotency:        writingplan.IdempotencySafe, Available: true,
		Context: writingplan.ContextContract{RequiredContext: []writingplan.ContextBlockName{writingplan.ContextContractDigest, writingplan.ContextDocumentState},
			OptionalContext:        []writingplan.ContextBlockName{writingplan.ContextTerminology},
			EnforceRequiredContext: true}}
	if err := capabilities.RegisterExecutor(writingplan.ExecutorBinding{ID: t00SafeBinding,
		AcceptedInputTypes:  append(append([]writingplan.ArtifactType(nil), manifest.InputTypes...), manifest.OptionalInputTypes...),
		ProducedOutputTypes: append([]writingplan.ArtifactType(nil), manifest.OutputTypes...),
		Dispatch: func(context.Context, writingplan.ExecutionRequest) (writingplan.ExecutionResult, error) {
			return writingplan.ExecutionResult{}, nil
		}}); err != nil {
		return err
	}
	return capabilities.Register(manifest)
}

// t00Fixture holds the document/contract/base-version ids every run anchors on.
type t00Fixture struct {
	documentID  string
	contractID  string
	contract    writingkernel.WritingContract
	baseVersion writingkernel.DocumentVersion
}

func (h *t00Harness) fixture(t *testing.T) *t00Fixture {
	t.Helper()
	document := e2eRequest(t, h.router, h.token, "POST", "/api/v2/documents", map[string]any{"title": "T00 终态一致性"})
	documentID := e2eJSONField(t, document, "document_id")
	var contract writingkernel.WritingContract
	if err := json.Unmarshal(e2eContractFixture(t), &contract); err != nil {
		t.Fatal(err)
	}
	contract.Status = writingkernel.ContractStatusDraft
	contract.Collaboration.AssuranceLevel = writingkernel.AssuranceLevelStandard
	contract.Collaboration.OrchestrationMode = writingkernel.OrchestrationModeFast
	contract.EvidencePolicy.Level = writingkernel.EvidenceLevelStandard
	for i := range contract.SourceAttributions {
		valueHash, hashErr := contract.FieldValueHash(contract.SourceAttributions[i].FieldPath)
		if hashErr != nil {
			t.Fatal(hashErr)
		}
		contract.SourceAttributions[i].ValueHash = valueHash
	}
	contract.ContractID = writingstore.StableID("ctr_", documentID, "t00", time.Now().UTC().Format("150405.000000000"))
	draftContract, err := contract.WithComputedHash()
	if err != nil {
		t.Fatal(err)
	}
	putted := e2eRequest(t, h.router, h.token, "POST", "/api/v2/documents/"+documentID+"/contracts", map[string]any{"contract": draftContract})
	contractID := e2eNestedField(t, putted, "contract", "contract_id")
	confirmed := e2eRequest(t, h.router, h.token, "POST", "/api/v2/contracts/"+contractID+"/confirm",
		map[string]any{"previous_version": 1, "contract": confirmedContract(t, draftContract)})
	if e2eNestedField(t, confirmed, "contract", "status") != string(writingkernel.ContractStatusConfirmed) {
		t.Fatalf("contract not confirmed: %#v", confirmed)
	}
	sealed := confirmedContract(t, draftContract)
	baseVersion := e2eBaseVersion(t, documentID, "T00 基线草稿")
	if _, err := h.store.CommitDocumentVersion(context.Background(), writingstore.CommitDocumentVersionParams{
		Version: baseVersion, ContractID: contractID, ContractVersion: 2,
		Trace: writingstore.TraceContext{Actor: writingstore.Actor{Type: writingstore.ActorUser, ID: h.userID},
			Provenance: map[string]any{}, SourceRefs: []string{}}}); err != nil {
		t.Fatal(err)
	}
	return &t00Fixture{documentID: documentID, contractID: contractID, contract: sealed, baseVersion: baseVersion}
}

// t00PlanNode builds one plan node inside the catalog's max bounds.
func t00PlanNode(nodeID, capability string, kind writingplan.NodeKind, deps []string,
	inputs, outputs []writingplan.ArtifactType, failure writingplan.FailurePath, maxAttempts int) writingplan.PlanNode {
	return writingplan.PlanNode{NodeID: nodeID, Kind: kind, Capability: capability, CapabilityVersion: "1.0.0",
		DependsOn: deps, InputArtifactTypes: inputs, OutputArtifactTypes: outputs,
		Bounds:      writingplan.Bounds{MaxAttempts: maxAttempts, MaxConcurrency: 1, MaxItems: 1, MaxCostUSD: 2, TimeoutMS: 120000},
		FailurePath: failure}
}

// t00PlanNodes is the scripted three-node shape: draft → quality validator →
// finalize. The quality validator satisfies the standard assurance floor and
// the finalize node produces the required revision_set artifact.
func t00PlanNodes(draftCapability string, draftFailure writingplan.FailurePath, draftAttempts int) []writingplan.PlanNode {
	return []writingplan.PlanNode{
		t00PlanNode("node_t00_a", draftCapability, writingplan.NodeAction, nil,
			[]writingplan.ArtifactType{"contract"}, []writingplan.ArtifactType{"full_draft"}, draftFailure, draftAttempts),
		t00PlanNode("node_t00_q", "core.validation.quality", writingplan.NodeValidate, []string{"node_t00_a"},
			[]writingplan.ArtifactType{"full_draft"}, []writingplan.ArtifactType{"quality_report"}, writingplan.FailurePause, 2),
		t00PlanNode("node_t00_f", "core.document.finalize", writingplan.NodeAction, []string{"node_t00_a", "node_t00_q"},
			[]writingplan.ArtifactType{"full_draft", "quality_report"}, []writingplan.ArtifactType{"revision_set"}, writingplan.FailureFail, 1),
	}
}

// buildEnvelope assembles a dispatch-valid plan envelope over the scripted
// nodes (the same compile→validate→seal pipeline writingplan.Compile runs,
// with explicit node control the intent-plan templates cannot express).
func (h *t00Harness) buildEnvelope(t *testing.T, fixture *t00Fixture, nodes []writingplan.PlanNode) writingplan.WritingPlanEnvelope {
	t.Helper()
	now := time.Now().UTC()
	steps := make([]writingplan.ProposedStep, 0, len(nodes))
	stepToNode := map[string]string{}
	for _, node := range nodes {
		stepID := strings.TrimPrefix(node.NodeID, "node_")
		stepToNode[node.NodeID] = stepID
		steps = append(steps, writingplan.ProposedStep{StepID: stepID,
			Objective: "T00 scripted step", CapabilityHint: node.Capability, DependsOn: []string{}})
	}
	for index, node := range nodes {
		for _, dep := range node.DependsOn {
			steps[index].DependsOn = append(steps[index].DependsOn, stepToNode[dep])
		}
	}
	intent, err := (writingplan.IntentPlan{IntentPlanID: "iplan_t00_" + now.Format("150405000000000") + fmt.Sprint(now.Nanosecond()),
		ContractRef: writingplan.ObjectRef{ID: fixture.contractID, Version: 2, Hash: fixture.contract.ContractHash},
		Summary:     "T00 terminal-consistency scripted plan", CreatedBy: writingplan.ActorUser, CreatedAt: now,
		ProposedSteps: steps}).WithComputedHash()
	if err != nil {
		t.Fatal(err)
	}
	budget := writingplan.PlanBudget{MaxCostUSD: 100, MaxDurationMS: 3000000, MaxConcurrency: 2, MaxNodes: 10, MaxItems: 10}
	validationContext := writingplan.ValidationContext{Registry: h.api.capabilities,
		InitialArtifactTypes: []writingplan.ArtifactType{"contract", "materials"}, AllowedPermissions: governedWritingPermissions,
		Budget: budget, RequiredValidators: writingplan.RequiredValidatorsForAssurance(fixture.contract.Collaboration.AssuranceLevel),
		RequiredFinalArtifact: "revision_set", ExternalResearchAllowed: fixture.contract.MaterialPolicy.AllowExternalResearch, Now: now}
	plan := writingplan.ExecutablePlan{PlanID: writingstore.StableID("plan_", intent.IntentPlanID),
		IntentPlanRef: writingplan.ObjectRef{ID: intent.IntentPlanID, Version: 1, Hash: intent.IntentPlanHash},
		Status:        writingplan.PlanDraft, TrustLevel: writingplan.TrustT1, RootNodeID: nodes[0].NodeID, Nodes: nodes}
	validation := writingplan.ValidatePlan(plan, validationContext)
	if !validation.Valid {
		t.Fatalf("scripted plan failed static validation: %v", validation.Errors)
	}
	plan.StaticValidation = validation
	plan.Status = writingplan.PlanValidated
	plan, err = plan.WithComputedHash()
	if err != nil {
		t.Fatal(err)
	}
	decision := writingplan.StrategyDecision{DecisionID: writingstore.StableID("decision_", plan.PlanHash),
		IntentPlanRef: plan.IntentPlanRef,
		Candidates: []writingplan.StrategyCandidate{{PlanHash: plan.PlanHash, TrustLevel: plan.TrustLevel,
			EstimatedCostUSD: 1.5, EstimatedDurationMS: 90000, EstimatedConfidence: .95}},
		SelectedPlanHash: plan.PlanHash, SelectionSource: writingplan.SelectionUser,
		RequestedOrchestration: writingkernel.OrchestrationModeFast, EffectiveOrchestration: writingkernel.OrchestrationModeFast,
		UserOverride: true, ReasonCode: "selected_fast", Summary: "T00 scripted plan", Confidence: .95,
		ApprovalRequired: false, DegradationConditions: []string{}, CreatedAt: now}
	envelope := writingplan.WritingPlanEnvelope{SchemaVersion: writingplan.SchemaVersion, IntentPlan: intent,
		ExecutablePlan: plan, StrategyDecision: decision}
	if err := envelope.Validate(); err != nil {
		t.Fatalf("scripted envelope invalid: %v", err)
	}
	if err := envelope.ValidateForDispatch(validationContext); err != nil {
		t.Fatalf("scripted envelope not dispatchable: %v", err)
	}
	return envelope
}

func (h *t00Harness) runBudget() writingplan.PlanBudget {
	return writingplan.PlanBudget{MaxCostUSD: 100, MaxDurationMS: 3000000, MaxConcurrency: 2, MaxNodes: 10, MaxItems: 10}
}

// createRun drives the HTTP run creation (planned, no approval required) and
// returns the run id.
func (h *t00Harness) createRun(t *testing.T, fixture *t00Fixture, envelope writingplan.WritingPlanEnvelope) string {
	t.Helper()
	permissions := permissionsForPlan(envelope.ExecutablePlan, h.api.capabilities)
	run := e2eRequest(t, h.router, h.token, "POST", "/api/v2/runs", map[string]any{
		"document_id": fixture.documentID, "contract_id": fixture.contractID, "contract_version": 2,
		"contract_hash": fixture.contract.ContractHash, "base_version_id": fixture.baseVersion.VersionID,
		"style_slug": "yinyue", "plan": envelope, "budget": h.runBudget(), "permissions": permissions,
	})
	return e2eJSONField(t, run, "run_id")
}

// createRunDirect inserts the run + plan straight through the store — the
// restart fixtures use it so no worker is triggered at creation time and the
// "crashed worker" state can be constructed deterministically.
func (h *t00Harness) createRunDirect(t *testing.T, fixture *t00Fixture, envelope writingplan.WritingPlanEnvelope) string {
	t.Helper()
	ctx := context.Background()
	permissions := permissionsForPlan(envelope.ExecutablePlan, h.api.capabilities)
	trace := writingstore.TraceContext{Provenance: map[string]any{"t00": "restart-fixture"}, SourceRefs: []string{},
		Actor: writingstore.Actor{Type: writingstore.ActorUser, ID: h.userID}}
	runID := writingstore.StableID("run_", h.userID, "t00restart", fmt.Sprint(time.Now().UTC().UnixNano()))
	run := writingstore.RunRecord{RunID: runID, DocumentID: fixture.documentID, ContractID: fixture.contractID,
		ContractVersion: 2, ContractHash: fixture.contract.ContractHash, BaseVersionID: fixture.baseVersion.VersionID,
		StyleSlug: "yinyue", Status: "planned", ApprovalMode: fixture.contract.Collaboration.ApprovalMode,
		RequestedAssurance: fixture.contract.Collaboration.AssuranceLevel, Budget: h.runBudget(),
		Permissions: permissions, Trace: trace}
	plan := writingstore.PlanRecord{RunID: runID, PlanVersion: 1, Envelope: envelope, Budget: h.runBudget(),
		Permissions: permissions, Trace: trace}
	if err := h.store.CreateRunWithPlan(ctx, run, plan, "planned"); err != nil {
		t.Fatal(err)
	}
	return runID
}

// transitionRun moves the run projection through the same state machine the
// orchestrator uses (restart fixtures simulate the dispatch start).
func (h *t00Harness) transitionRun(t *testing.T, runID string, from, to writingruntime.RunState, cause string) {
	t.Helper()
	machine := writingruntime.NewStateMachine(writingruntime.WritingStoreTransitionRecorder{Store: h.store})
	if _, err := machine.Transition(context.Background(), writingruntime.TransitionRequest{CommandID: writingstore.StableID("command_", runID, cause, fmt.Sprint(time.Now().UTC().UnixNano())),
		RunID: runID, From: from, To: to, Cause: cause, ReasonCode: cause, Summary: "T00 fixture transition",
		Actor: writingstore.Actor{Type: writingstore.ActorSystem, ID: "t00.fixture"}}); err != nil {
		t.Fatalf("transition %s→%s: %v", from, to, err)
	}
}

func (h *t00Harness) httpGetRun(t *testing.T, runID string) map[string]any {
	t.Helper()
	return e2eRequest(t, h.router, h.token, "GET", "/api/v2/runs/"+runID, nil)
}

func (h *t00Harness) httpStatus(t *testing.T, runID string) string {
	t.Helper()
	return e2eJSONField(t, h.httpGetRun(t, runID), "status")
}

// waitForTerminal polls the HTTP GET projection until the run reaches a
// terminal state inside the window; a window close without a terminal state
// is a hard failure — the exact T00 symptom under test.
func (h *t00Harness) waitForTerminal(t *testing.T, runID string, window time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		status := h.httpStatus(t, runID)
		switch status {
		case "completed", "failed", "cancelled", "paused":
			return status
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("run %s never reached a terminal state within %v (last GET status %q)", runID, window, h.httpStatus(t, runID))
	return ""
}

// t00TerminalAssert configures the shared terminal consistency check.
type t00TerminalAssert struct {
	// requireCheckpoint fails when the terminal path should have persisted a
	// checkpoint snapshot but none exists.
	requireCheckpoint bool
	// unsafeInFlight, when non-nil, must equal the checkpoint's recorded
	// unsafe-in-flight node list.
	unsafeInFlight []string
	// wantEventTypes must all appear in the terminal ledger.
	wantEventTypes []string
}

// assertTerminalConsistency verifies the four projections agree on the
// terminal state: persisted status, event ledger (contiguous, complete),
// checkpoint snapshot (when required), and the HTTP GET projection — then
// re-checks stability so a zombie writer cannot slip through after the window.
func (h *t00Harness) assertTerminalConsistency(t *testing.T, runID string, wantStatus string, assert t00TerminalAssert) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(5 * time.Second)
	var events []writingstore.RunEvent
	var run writingstore.RuntimeRun
	for {
		loaded, err := h.store.LoadRuntimeRun(ctx, runID)
		if err != nil {
			t.Fatalf("load run: %v", err)
		}
		if loaded.Status == wantStatus && loaded.LastEventSequence > 0 {
			run = loaded
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("terminal consistency window closed: status=%q last_event_sequence=%d", loaded.Status, loaded.LastEventSequence)
		}
		time.Sleep(100 * time.Millisecond)
	}
	// 1+4. HTTP GET projection must equal the persisted status.
	if projected := h.httpStatus(t, runID); projected != wantStatus {
		t.Fatalf("HTTP GET status %q disagrees with persisted status %q", projected, wantStatus)
	}
	// 2. Event ledger: contiguous sequences from 1 with no gaps or losses.
	after := int64(0)
	for {
		page, err := h.store.ListRunEvents(ctx, runID, after, 200)
		if err != nil {
			t.Fatalf("list events: %v", err)
		}
		events = append(events, page...)
		if len(page) < 200 {
			break
		}
		after = page[len(page)-1].Sequence
	}
	if len(events) == 0 {
		t.Fatal("terminal run has no ledger events")
	}
	for index, event := range events {
		if event.Sequence != int64(index+1) {
			t.Fatalf("event ledger gap: sequence %d at index %d (expected %d)", event.Sequence, index, index+1)
		}
	}
	if events[len(events)-1].Sequence != run.LastEventSequence {
		t.Fatalf("run last_event_sequence %d disagrees with ledger max %d", run.LastEventSequence, events[len(events)-1].Sequence)
	}
	observed := map[string]bool{}
	lastTransition := map[string]any{}
	for _, event := range events {
		observed[event.EventType] = true
		if event.EventType == "run.transitioned" {
			payloadBytes, _ := json.Marshal(event.Payload)
			_ = json.Unmarshal(payloadBytes, &lastTransition)
		}
	}
	for _, eventType := range assert.wantEventTypes {
		if !observed[eventType] {
			t.Fatalf("terminal ledger lacks event %q (have %d events)", eventType, len(events))
		}
	}
	if effective, _ := lastTransition["effective_state"].(string); effective != "" && effective != wantStatus {
		t.Fatalf("final run.transitioned ended in %q, want %q", effective, wantStatus)
	}
	// 3. Checkpoint snapshot bindings and manifest (when the terminal path
	// writes one).
	snapshot, snapErr := h.store.LoadLatestSnapshot(ctx, runID)
	switch {
	case snapErr == nil:
		if snapshot.RunID != runID || snapshot.PlanID != run.ActivePlanID || snapshot.PlanVersion != run.ActivePlanVersion {
			t.Fatalf("checkpoint bindings disagree: run=%s plan=%s v%d", snapshot.RunID, snapshot.PlanID, snapshot.PlanVersion)
		}
		var checkpoint writingruntime.Checkpoint
		checkpointBytes, _ := json.Marshal(snapshot.Manifest)
		if err := json.Unmarshal(checkpointBytes, &checkpoint); err != nil {
			t.Fatalf("decode checkpoint manifest: %v", err)
		}
		if checkpoint.CompletedNodes == nil || checkpoint.UnsafeInFlight == nil {
			t.Fatal("checkpoint manifest lost node bookkeeping")
		}
		if assert.unsafeInFlight != nil && strings.Join(checkpoint.UnsafeInFlight, ",") != strings.Join(assert.unsafeInFlight, ",") {
			t.Fatalf("checkpoint unsafe_in_flight = %v, want %v", checkpoint.UnsafeInFlight, assert.unsafeInFlight)
		}
	case assert.requireCheckpoint:
		t.Fatalf("terminal %s run has no checkpoint snapshot: %v", wantStatus, snapErr)
	}
	// Stability: no zombie writer may move the run after the terminal window.
	time.Sleep(400 * time.Millisecond)
	if status := h.httpStatus(t, runID); status != wantStatus {
		t.Fatalf("run moved after terminal: %q → %q", wantStatus, status)
	}
	afterEvents, err := h.store.ListRunEvents(ctx, runID, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterEvents) != len(events) {
		t.Fatalf("ledger grew after terminal: %d → %d events", len(events), len(afterEvents))
	}
}

// t00TerminalScenarios are the four terminal states with their scripting.
func TestGovernedRunTerminalConsistency(t *testing.T) {
	scenarios := []struct {
		name       string
		nodes      func() []writingplan.PlanNode
		script     func(t *testing.T, h *t00Harness)
		control    func(t *testing.T, h *t00Harness, runID string)
		wantStatus string
		assert     t00TerminalAssert
	}{
		{
			// Active failure: a scripted failure on the IdempotencySafe draft
			// node retries once (safe semantics) and then fails the run via
			// the node's FailurePath=fail.
			name:  "fail",
			nodes: func() []writingplan.PlanNode { return t00PlanNodes(t00SafeCapability, writingplan.FailureFail, 2) },
			script: func(t *testing.T, h *t00Harness) {
				h.runners[t00SafeCapability].failNext(t00SafeCapability, 2)
			},
			wantStatus: "failed",
			assert:     t00TerminalAssert{wantEventTypes: []string{"node.started", "node.failed", "run.transitioned"}},
		},
		{
			// Pause: the idempotency-required draft node fails once — the
			// governed runtime must never blind-retry it, parking the run
			// into the human-recovery pause with the node recorded unsafe.
			name:  "pause",
			nodes: func() []writingplan.PlanNode { return t00PlanNodes("core.draft.generate", writingplan.FailurePause, 2) },
			script: func(t *testing.T, h *t00Harness) {
				h.runners["core.draft.generate"].failNext("core.draft.generate", 1)
			},
			wantStatus: "paused",
			assert:     t00TerminalAssert{requireCheckpoint: true, unsafeInFlight: []string{"node_t00_a"}, wantEventTypes: []string{"node.started", "node.failed", "run.transitioned"}},
		},
		{
			// Cancel: the API cancels a run with an in-flight node; the
			// worker observes the control intent and lands cancelled.
			name:  "cancel",
			nodes: func() []writingplan.PlanNode { return t00PlanNodes("core.draft.generate", writingplan.FailurePause, 2) },
			script: func(t *testing.T, h *t00Harness) {
				h.runners["core.draft.generate"].blockOn("core.draft.generate")
			},
			control: func(t *testing.T, h *t00Harness, runID string) {
				deadline := time.Now().Add(30 * time.Second)
				for h.runners["core.draft.generate"].callCount("core.draft.generate") < 1 {
					if time.Now().After(deadline) {
						t.Fatal("blocked node never dispatched")
					}
					time.Sleep(50 * time.Millisecond)
				}
				e2eRequest(t, h.router, h.token, "POST", "/api/v2/runs/"+runID+"/cancel", map[string]any{})
				h.runners["core.draft.generate"].release("core.draft.generate")
			},
			wantStatus: "cancelled",
			assert:     t00TerminalAssert{requireCheckpoint: true, wantEventTypes: []string{"node.started", "node.cancelled", "run.transitioned"}},
		},
		{
			// Complete: every scripted node succeeds; the delivery lineage
			// (candidate version, quality report, promotion) commits.
			name:       "complete",
			nodes:      func() []writingplan.PlanNode { return t00PlanNodes("core.draft.generate", writingplan.FailurePause, 2) },
			script:     func(t *testing.T, h *t00Harness) {},
			wantStatus: "completed",
			assert:     t00TerminalAssert{requireCheckpoint: true, wantEventTypes: []string{"node.started", "node.completed", "run.transitioned"}},
		},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			h := newT00Harness(t)
			fixture := h.fixture(t)
			scenario.script(t, h)
			runID := h.createRun(t, fixture, h.buildEnvelope(t, fixture, scenario.nodes()))
			if scenario.control != nil {
				scenario.control(t, h, runID)
			}
			if final := h.waitForTerminal(t, runID, 60*time.Second); final != scenario.wantStatus {
				t.Fatalf("run ended as %q, want %q", final, scenario.wantStatus)
			}
			h.assertTerminalConsistency(t, runID, scenario.wantStatus, scenario.assert)
			// Scenario-specific deep assertions. ListRunAttempts includes the
			// synthetic "initial" capture attempt — plan-node assertions
			// filter it out (mirroring recovery.go's bookkeeping rule).
			ctx := context.Background()
			allAttempts, err := h.store.ListRunAttempts(ctx, runID)
			if err != nil {
				t.Fatal(err)
			}
			attempts := make([]writingstore.NodeAttempt, 0, len(allAttempts))
			for _, attempt := range allAttempts {
				if attempt.NodeID != writingruntime.InitialCaptureNodeID {
					attempts = append(attempts, attempt)
				}
			}
			switch scenario.name {
			case "fail":
				// Both safe attempts ran and failed; no node output exists.
				if len(attempts) != 2 {
					t.Fatalf("fail scenario attempts = %d, want 2", len(attempts))
				}
				for _, attempt := range attempts {
					if attempt.Status != "failed" {
						t.Fatalf("attempt %s#%d status %q, want failed", attempt.NodeID, attempt.Attempt, attempt.Status)
					}
				}
			case "pause":
				if len(attempts) != 1 || attempts[0].Status != "failed" {
					t.Fatalf("pause scenario attempts = %#v, want one failed attempt", attempts)
				}
			case "cancelled":
				if len(attempts) != 1 || attempts[0].Status != "cancelled" {
					t.Fatalf("cancel scenario attempts = %#v, want one cancelled attempt", attempts)
				}
			case "complete":
				artifacts, err := h.store.ListRunArtifacts(ctx, runID)
				if err != nil {
					t.Fatal(err)
				}
				var revisionSets int
				for _, artifact := range artifacts {
					if artifact.ArtifactType == "revision_set" {
						revisionSets++
					}
				}
				if revisionSets != 1 {
					t.Fatalf("complete scenario revision_set artifacts = %d, want 1", revisionSets)
				}
				report, err := h.store.GetLatestQualityReport(ctx, fixture.documentID)
				if err != nil || report.RunID != runID {
					t.Fatalf("quality report missing after completion: %v", err)
				}
				candidateID, err := h.store.CurrentDocumentVersionID(ctx, fixture.documentID)
				if err != nil || candidateID == "" || candidateID == fixture.baseVersion.VersionID {
					t.Fatalf("candidate version not committed: %q %v", candidateID, err)
				}
				version, err := h.store.GetDocumentVersion(ctx, fixture.documentID, candidateID)
				if err != nil || version.QualityState != writingstore.QualityAcceptedDraft {
					t.Fatalf("candidate quality state %q (err %v)", version.QualityState, err)
				}
			}
		})
	}
}

// TestGovernedRunResumeAfterPauseAdvances proves the previously-uncovered
// resume path end to end: node A completes, node B (the quality validator)
// parks the run with a pause-path failure, and the API resume drives the SAME
// plan version forward — node B re-dispatches once within bounds, node C
// completes, and every artifact committed before the pause survives. Ledger
// sequences keep increasing without gaps.
//
// Root cause fixed for this capability (writingruntime/recovery.go): the
// failure-pause checkpoint marks the failed node UnsafeInFlight, and Recover
// used to merge that marker into HumanRequired unconditionally, so every
// resume re-entered unsafe_recovery and re-paused without progress. Recovery
// now drops the marker when the node's attempt ledger shows a terminal
// outcome; genuinely in-flight attempts still require human recovery.
func TestGovernedRunResumeAfterPauseAdvances(t *testing.T) {
	h := newT00Harness(t)
	fixture := h.fixture(t)
	h.runners["core.validation.quality"].failNext("core.validation.quality", 1)
	envelope := h.buildEnvelope(t, fixture, t00PlanNodes("core.draft.generate", writingplan.FailurePause, 2))
	runID := h.createRun(t, fixture, envelope)
	if final := h.waitForTerminal(t, runID, 60*time.Second); final != "paused" {
		t.Fatalf("pre-resume run ended as %q, want paused", final)
	}
	ctx := context.Background()
	pausedRun, err := h.store.LoadRuntimeRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if pausedRun.Status != "paused" {
		t.Fatalf("run status %q before resume", pausedRun.Status)
	}
	// Node A's completed work survived the pause: artifact, candidate
	// version, and the checkpoint's completed-node map.
	preArtifacts, err := h.store.ListRunArtifacts(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	var preDraft int
	for _, artifact := range preArtifacts {
		if artifact.ArtifactType == "full_draft" {
			preDraft++
		}
	}
	if preDraft != 1 {
		t.Fatalf("pre-resume full_draft artifacts = %d, want 1", preDraft)
	}
	preCandidate, err := h.store.CurrentDocumentVersionID(ctx, fixture.documentID)
	if err != nil || preCandidate == "" || preCandidate == fixture.baseVersion.VersionID {
		t.Fatalf("pre-resume candidate version missing: %q %v", preCandidate, err)
	}
	snapshot, err := h.store.LoadLatestSnapshot(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	var pausedCheckpoint writingruntime.Checkpoint
	checkpointBytes, _ := json.Marshal(snapshot.Manifest)
	if err := json.Unmarshal(checkpointBytes, &pausedCheckpoint); err != nil {
		t.Fatal(err)
	}
	if pausedCheckpoint.CompletedNodes["node_t00_a"] != 1 {
		t.Fatalf("pause checkpoint lost node A: %#v", pausedCheckpoint.CompletedNodes)
	}
	preEvents := 0
	{
		events, err := h.store.ListRunEvents(ctx, runID, 0, 500)
		if err != nil {
			t.Fatal(err)
		}
		preEvents = len(events)
	}

	// The HTTP resume runs synchronously under the execution lock:
	// ControlRun → governedRunController.Resume → Orchestrator.Resume.
	e2eRequest(t, h.router, h.token, "POST", "/api/v2/runs/"+runID+"/resume", map[string]any{})
	if final := h.waitForTerminal(t, runID, 60*time.Second); final != "completed" {
		t.Fatalf("post-resume run ended as %q, want completed", final)
	}
	resumedRun, err := h.store.LoadRuntimeRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if resumedRun.Status != "completed" {
		t.Fatalf("post-resume status %q", resumedRun.Status)
	}
	if resumedRun.ActivePlanID != pausedRun.ActivePlanID || resumedRun.ActivePlanVersion != pausedRun.ActivePlanVersion {
		t.Fatalf("resume changed the active plan: %s v%d → %s v%d", pausedRun.ActivePlanID, pausedRun.ActivePlanVersion, resumedRun.ActivePlanID, resumedRun.ActivePlanVersion)
	}
	// Completed artifacts are preserved; the ledger never shrank.
	postArtifacts, err := h.store.ListRunArtifacts(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(postArtifacts) <= len(preArtifacts) {
		t.Fatalf("resume did not append artifacts: pre=%d post=%d", len(preArtifacts), len(postArtifacts))
	}
	postCandidate, err := h.store.CurrentDocumentVersionID(ctx, fixture.documentID)
	if err != nil || postCandidate != preCandidate {
		t.Fatalf("resume moved the candidate version: %q → %q (%v)", preCandidate, postCandidate, err)
	}
	postEvents, err := h.store.ListRunEvents(ctx, runID, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(postEvents) <= preEvents {
		t.Fatalf("resume appended no events: pre=%d post=%d", preEvents, len(postEvents))
	}
	for index, event := range postEvents {
		if event.Sequence != int64(index+1) {
			t.Fatalf("post-resume ledger gap: sequence %d at index %d", event.Sequence, index)
		}
	}
	// The paused node re-dispatched exactly once (fail + resume), and node A
	// never re-ran.
	if calls := h.runners["core.validation.quality"].callCount("core.validation.quality"); calls != 2 {
		t.Fatalf("quality runner calls = %d, want 2 (pause failure + resume)", calls)
	}
	if calls := h.runners["core.draft.generate"].callCount("core.draft.generate"); calls != 1 {
		t.Fatalf("draft runner calls = %d, want 1 (no re-run after resume)", calls)
	}
	h.assertTerminalConsistency(t, runID, "completed", t00TerminalAssert{requireCheckpoint: true,
		wantEventTypes: []string{"node.completed", "run.transitioned"}})
}

// TestGovernedRunRestartRecovery simulates worker death against the durable
// queue and asserts the run converges to a legal terminal state inside the
// test window — never a permanently running zombie projection:
//
//	crash_mid_attempt — the crashed worker leaves an unresolved running
//	  attempt; a restarted worker re-dispatches, recovery flags the
//	  ambiguous non-safe attempt for human recovery, and the run parks
//	  paused (the legal IdempotencySafe-semantics terminal for this shape).
//	crash_after_node  — the crashed worker had already committed node A's
//	  attempt and candidate version (but no checkpoint); a restarted worker
//	  consumes the attempt ledger, preserves the completed node, and the run
//	  completes ("重启不丢已完成节点").
func TestGovernedRunRestartRecovery(t *testing.T) {
	t.Run("crash_mid_attempt_parks_for_human_recovery", func(t *testing.T) {
		h := newT00Harness(t)
		fixture := h.fixture(t)
		envelope := h.buildEnvelope(t, fixture, t00PlanNodes("core.draft.generate", writingplan.FailurePause, 2))
		runID := h.createRunDirect(t, fixture, envelope)
		ctx := context.Background()
		// The crashed worker had started the dispatch and the first attempt.
		h.transitionRun(t, runID, writingruntime.StatePlanned, writingruntime.StateRunning, "t00_crash_dispatch")
		h.startAttempt(t, runID, envelope, "node_t00_a", 1, "core.draft.generate")
		// Restarted worker: the durable queue scan re-discovers the running run.
		trigger := &governedRunTrigger{orchestrator: h.api.trigger.orchestrator, store: h.store}
		scanCtx, cancelScan := context.WithCancel(context.Background())
		t.Cleanup(cancelScan)
		go trigger.Serve(scanCtx)
		final := h.waitForTerminal(t, runID, 60*time.Second)
		if final != "paused" {
			t.Fatalf("crashed run converged to %q, want paused (human recovery)", final)
		}
		// The unresolved attempt stays in the ledger, but the run projection
		// is terminal — no permanently running zombie. (The synthetic
		// "initial" capture attempt is bookkeeping, not a plan node.)
		attempts, err := h.store.ListRunAttempts(ctx, runID)
		if err != nil {
			t.Fatal(err)
		}
		var running int
		for _, attempt := range attempts {
			if attempt.NodeID != writingruntime.InitialCaptureNodeID {
				running++
				if attempt.Status != "running" {
					t.Fatalf("attempt %s status %q, want the unresolved running attempt", attempt.NodeID, attempt.Status)
				}
			}
		}
		if running != 1 {
			t.Fatalf("plan-node attempts = %d, want the single unresolved running attempt", running)
		}
		h.assertTerminalConsistency(t, runID, "paused", t00TerminalAssert{wantEventTypes: []string{"node.started", "run.transitioned"}})
	})
	t.Run("crash_after_node_preserves_completed_work", func(t *testing.T) {
		h := newT00Harness(t)
		fixture := h.fixture(t)
		envelope := h.buildEnvelope(t, fixture, t00PlanNodes("core.draft.generate", writingplan.FailurePause, 2))
		runID := h.createRunDirect(t, fixture, envelope)
		ctx := context.Background()
		h.transitionRun(t, runID, writingruntime.StatePlanned, writingruntime.StateRunning, "t00_crash_dispatch")
		h.startAttempt(t, runID, envelope, "node_t00_a", 1, "core.draft.generate")
		candidateID := h.completeDraftAttempt(t, runID, envelope)
		// No checkpoint: the crash landed between the node commit and the
		// checkpoint save. Recovery must still consume the attempt ledger.
		if _, err := h.store.LoadLatestSnapshot(ctx, runID); err == nil {
			t.Fatal("restart fixture expected no checkpoint before the crash window closes")
		}
		trigger := &governedRunTrigger{orchestrator: h.api.trigger.orchestrator, store: h.store}
		scanCtx, cancelScan := context.WithCancel(context.Background())
		t.Cleanup(cancelScan)
		go trigger.Serve(scanCtx)
		final := h.waitForTerminal(t, runID, 60*time.Second)
		if final != "completed" {
			t.Fatalf("restarted run converged to %q, want completed", final)
		}
		// The completed node's artifact and committed candidate version
		// survive the restart untouched.
		artifacts, err := h.store.ListRunArtifacts(ctx, runID)
		if err != nil {
			t.Fatal(err)
		}
		var drafts int
		for _, artifact := range artifacts {
			if artifact.ArtifactType == "full_draft" {
				drafts++
			}
		}
		if drafts != 1 {
			t.Fatalf("post-restart full_draft artifacts = %d, want 1 (completed node preserved)", drafts)
		}
		postCandidate, err := h.store.CurrentDocumentVersionID(ctx, fixture.documentID)
		if err != nil || postCandidate != candidateID {
			t.Fatalf("restart moved the candidate version: %q → %q (%v)", candidateID, postCandidate, err)
		}
		h.assertTerminalConsistency(t, runID, "completed", t00TerminalAssert{requireCheckpoint: true,
			wantEventTypes: []string{"node.started", "node.completed", "run.transitioned"}})
	})
}

// startAttempt records an in-flight (running) attempt row the way
// StartNodeAttempt does for a live worker — the crash window's durable mark.
func (h *t00Harness) startAttempt(t *testing.T, runID string, envelope writingplan.WritingPlanEnvelope, nodeID string, attempt int, capability string) {
	t.Helper()
	var node writingplan.PlanNode
	for _, candidate := range envelope.ExecutablePlan.Nodes {
		if candidate.NodeID == nodeID {
			node = candidate
			break
		}
	}
	key, err := writingstore.NodeAttemptKey(runID, nodeID, attempt)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(key))
	row := writingstore.NodeAttempt{RunID: runID, PlanID: envelope.ExecutablePlan.PlanID, PlanVersion: 1,
		NodeID: nodeID, Attempt: attempt, IdempotencyKey: key, NodeKind: node.Kind, CapabilityID: capability,
		CapabilityVersion: "1.0.0", ExecutorID: "governed.baseline." + capability, FailurePath: node.FailurePath,
		Bounds: node.Bounds, InputHash: "sha256:" + hex.EncodeToString(sum[:]), InputArtifactIDs: []string{}}
	trace := writingstore.TraceContext{Provenance: map[string]any{"t00": "crash-fixture"}, SourceRefs: []string{},
		Actor: writingstore.Actor{Type: writingstore.ActorSystem, ID: "t00.fixture"}}
	saved, dispatch, err := h.store.StartNodeAttempt(context.Background(), row, trace)
	if err != nil {
		t.Fatal(err)
	}
	if !dispatch || saved.Status != "running" {
		t.Fatalf("attempt dispatch = %v status %q, want dispatched running", dispatch, saved.Status)
	}
}

// completeDraftAttempt finishes the crashed worker's draft node the way the
// orchestrator would: attempt succeeded, artifact staged, candidate document
// version committed via the delivery protocol. It returns the candidate id.
func (h *t00Harness) completeDraftAttempt(t *testing.T, runID string, envelope writingplan.WritingPlanEnvelope) string {
	t.Helper()
	ctx := context.Background()
	key, err := writingstore.NodeAttemptKey(runID, "node_t00_a", 1)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(t00DraftMarkdown))
	contentHash := "sha256:" + hex.EncodeToString(sum[:])
	if err := h.store.PutArtifactContent(ctx, contentHash, "text/markdown", []byte(t00DraftMarkdown)); err != nil {
		t.Fatal(err)
	}
	trace := writingstore.TraceContext{Provenance: map[string]any{"t00": "crash-fixture"}, SourceRefs: []string{},
		Actor: writingstore.Actor{Type: writingstore.ActorCapability, ID: "core.draft.generate"}}
	artifact := writingstore.ArtifactRecord{ArtifactID: writingstore.StableID("art_", key, "full_draft"), Version: 1,
		RunID: runID, PlanID: envelope.ExecutablePlan.PlanID, PlanVersion: 1, NodeID: "node_t00_a", Attempt: 1,
		IdempotencyKey: key, OutputKey: "full_draft", ArtifactType: "full_draft", Status: "provisional",
		ContentHash: contentHash, MediaType: "text/markdown", ContentRef: "artifact://" + contentHash,
		Parents: []writingstore.ArtifactRef{}, InputHashes: []string{}, Producer: "core.draft.generate",
		CapabilityVersion: "1.0.0", Trace: trace}
	if err := h.store.CompleteNodeAttempt(ctx, writingstore.AttemptCompletion{RunID: runID, NodeID: "node_t00_a",
		Attempt: 1, Status: "succeeded", Artifacts: []writingstore.ArtifactRecord{artifact}, Trace: trace,
		CompletedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	// The crashed worker's last act: the M1.0 draft delivery commit.
	run, err := h.store.LoadRuntimeRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := h.store.ListRunArtifacts(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	protocol := &writingruntime.DeliveryProtocol{Store: h.store, Content: writingruntime.WritingStoreContentGateway{Store: h.store},
		Actor: writingstore.Actor{Type: writingstore.ActorSystem, ID: "writingruntime.delivery"}}
	var node writingplan.PlanNode
	for _, candidate := range envelope.ExecutablePlan.Nodes {
		if candidate.NodeID == "node_t00_a" {
			node = candidate
			break
		}
	}
	if err := protocol.CommitDraftCandidate(ctx, run, node, artifacts); err != nil {
		t.Fatal(err)
	}
	candidateID, err := h.store.CurrentDocumentVersionID(ctx, run.DocumentID)
	if err != nil || candidateID == "" {
		t.Fatalf("draft delivery committed no candidate: %v", err)
	}
	return candidateID
}

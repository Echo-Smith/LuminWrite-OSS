package writingruntime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/contextcompiler"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database/dbtest"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

func contextcompilerInput(digest string) contextcompiler.Input {
	return contextcompiler.Input{ContractDigest: digest}
}

// deliveryBaseVersion builds a sealed one-section document version to serve
// as the run's base (mirrors the writingstore fixture's shape).
func deliveryBaseVersion(t *testing.T, documentID, versionID, text string) writingkernel.DocumentVersion {
	t.Helper()
	origin := writingkernel.Origin{Kind: writingkernel.OriginSystem, Ref: "delivery-test"}
	textNode := &writingkernel.DocumentNode{BlockID: "blk_text", Type: writingkernel.NodeTypeText,
		Text: text, Attrs: map[string]any{}, Children: []*writingkernel.DocumentNode{}, Origin: origin}
	paragraph := &writingkernel.DocumentNode{BlockID: "blk_paragraph", Type: writingkernel.NodeTypeParagraph,
		Attrs: map[string]any{}, Children: []*writingkernel.DocumentNode{textNode}, Origin: origin}
	section := &writingkernel.DocumentNode{BlockID: "blk_section", Type: writingkernel.NodeTypeSection,
		Attrs: map[string]any{"level": 1}, Children: []*writingkernel.DocumentNode{paragraph}, Origin: origin}
	document := writingkernel.DocumentVersion{SchemaVersion: writingkernel.SchemaVersionV1,
		DocumentID: documentID, VersionID: versionID,
		Root: &writingkernel.DocumentNode{BlockID: "blk_root", Type: writingkernel.NodeTypeDocument,
			Attrs: map[string]any{}, Children: []*writingkernel.DocumentNode{section}, Origin: origin}}
	sealed, err := document.WithComputedHashes()
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

// deliveryTestContract mirrors the writingstore fixture: the strict LCP
// writing-contract fixture, sealed.
func deliveryTestContract(t *testing.T) writingkernel.WritingContract {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join("..", "..", "..", "specs", "lcp", "v1", "fixtures", "writing-contract.valid.json"))
	if err != nil {
		t.Fatal(err)
	}
	contract, err := writingkernel.DecodeWritingContractStrict(payload)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := contract.WithComputedHash()
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

// deliveryTestPlan builds a validated plan (draft → [validators] → quality →
// finalize) whose nodes reference the governed registry's built-in
// capabilities. headClasses inserts validator nodes between draft and quality
// (e.g. evidence); tailClasses appends extra node classes after quality.
func deliveryTestPlan(t *testing.T, contract writingkernel.WritingContract, headClasses []string, tailClasses ...string) writingplan.WritingPlanEnvelope {
	t.Helper()
	now := time.Now().UTC()
	intent, err := (writingplan.IntentPlan{IntentPlanID: "iplan_delivery",
		ContractRef: writingplan.ObjectRef{ID: contract.ContractID, Version: contract.Version, Hash: contract.ContractHash},
		Summary:     "draft and quality-check the delivery", CreatedBy: writingplan.ActorSystem, CreatedAt: now,
		ProposedSteps: []writingplan.ProposedStep{{StepID: "draft", Objective: "draft the document",
			CapabilityHint: "core.draft.generate", DependsOn: []string{}}},
	}).WithComputedHash()
	if err != nil {
		t.Fatal(err)
	}
	planNodes := []writingplan.PlanNode{
		{NodeID: "node_draft", Kind: writingplan.NodeAction, Capability: "core.draft.generate",
			CapabilityVersion: "1.0.0", DependsOn: []string{},
			InputArtifactTypes:  []writingplan.ArtifactType{"contract"},
			OutputArtifactTypes: []writingplan.ArtifactType{"full_draft"},
			Bounds:              writingplan.Bounds{MaxAttempts: 1, MaxConcurrency: 1, MaxItems: 1, MaxCostUSD: 5, TimeoutMS: 300000},
			FailurePath:         writingplan.FailureFail},
	}
	// Validators between draft and quality mirror the sourced template: each
	// consumes the draft plus the source pack and hands its report to quality.
	lastID := "node_draft"
	validatorReports := []writingplan.ArtifactType{}
	for _, class := range headClasses {
		nodeID := "node_" + class[strings.LastIndex(class, ".")+1:]
		output := "evidence_report"
		if class == "core.validation.fact" {
			output = "fact_report"
		}
		planNodes = append(planNodes, writingplan.PlanNode{NodeID: nodeID,
			Kind: writingplan.NodeValidate, Capability: class, CapabilityVersion: "1.0.0",
			DependsOn:           []string{"node_draft"},
			InputArtifactTypes:  []writingplan.ArtifactType{"full_draft", "source_pack"},
			OutputArtifactTypes: []writingplan.ArtifactType{writingplan.ArtifactType(output)},
			Bounds:              writingplan.Bounds{MaxAttempts: 1, MaxConcurrency: 1, MaxItems: 1, MaxCostUSD: 5, TimeoutMS: 300000},
			FailurePath:         writingplan.FailureFail})
		validatorReports = append(validatorReports, writingplan.ArtifactType(output))
		lastID = nodeID
	}
	qualityInputs := append([]writingplan.ArtifactType{"full_draft"}, validatorReports...)
	planNodes = append(planNodes, writingplan.PlanNode{NodeID: "node_quality", Kind: writingplan.NodeValidate,
		Capability: "core.validation.quality", CapabilityVersion: "1.0.0", DependsOn: []string{lastID},
		InputArtifactTypes:  qualityInputs,
		OutputArtifactTypes: []writingplan.ArtifactType{"quality_report"},
		Bounds:              writingplan.Bounds{MaxAttempts: 1, MaxConcurrency: 1, MaxItems: 1, MaxCostUSD: 5, TimeoutMS: 300000},
		FailurePath:         writingplan.FailureFail})
	lastID = "node_quality"
	for _, class := range tailClasses {
		nodeID := "node_" + class[strings.LastIndex(class, ".")+1:]
		inputs := []writingplan.ArtifactType{"full_draft", "quality_report"}
		outputs := []writingplan.ArtifactType{"revision_set"}
		planNodes = append(planNodes, writingplan.PlanNode{NodeID: nodeID,
			Kind: writingplan.NodeAction, Capability: class, CapabilityVersion: "1.0.0",
			DependsOn: []string{lastID}, InputArtifactTypes: inputs, OutputArtifactTypes: outputs,
			Bounds:      writingplan.Bounds{MaxAttempts: 1, MaxConcurrency: 1, MaxItems: 1, MaxCostUSD: 5, TimeoutMS: 300000},
			FailurePath: writingplan.FailureFail})
		lastID = nodeID
	}
	plan, err := (writingplan.ExecutablePlan{PlanID: "plan_delivery",
		IntentPlanRef: writingplan.ObjectRef{ID: intent.IntentPlanID, Version: 1, Hash: intent.IntentPlanHash},
		TrustLevel:    writingplan.TrustT1, Status: writingplan.PlanValidated, RootNodeID: "node_draft",
		Nodes: planNodes,
		StaticValidation: writingplan.StaticValidation{Valid: true, CheckedAt: now, Errors: []string{},
			CapabilityRegistryVersion: "delivery-test", BudgetValid: true, PermissionsValid: true,
			ArtifactFlowValid: true, FailurePathsValid: true},
	}).WithComputedHash()
	if err != nil {
		t.Fatal(err)
	}
	decision := writingplan.StrategyDecision{DecisionID: "decision_delivery", IntentPlanRef: plan.IntentPlanRef,
		Candidates: []writingplan.StrategyCandidate{{PlanHash: plan.PlanHash, TrustLevel: plan.TrustLevel,
			EstimatedCostUSD: 1, EstimatedDurationMS: 1000, EstimatedConfidence: .8}},
		SelectedPlanHash: plan.PlanHash, SelectionSource: writingplan.SelectionSystem,
		RequestedOrchestration: writingkernel.OrchestrationModeAuto,
		EffectiveOrchestration: writingkernel.OrchestrationModeFast,
		ReasonCode:             "delivery-test", Summary: "delivery protocol plan", Confidence: .8,
		DegradationConditions: []string{}, CreatedAt: now}
	envelope := writingplan.WritingPlanEnvelope{SchemaVersion: writingplan.SchemaVersion,
		IntentPlan: intent, ExecutablePlan: plan, StrategyDecision: decision}
	if err := envelope.Validate(); err != nil {
		t.Fatal(err)
	}
	return envelope
}

// deliveryContractSource implements ContractArtifactSource over the store.
type deliveryContractSource struct{ store *writingstore.Store }

func (source deliveryContractSource) GetContract(ctx context.Context, contractID string, version int) (writingstore.ContractRecord, error) {
	return source.store.GetContract(ctx, contractID, version)
}

// scriptedRunner returns a LegacyNodeRunner whose "engine step" emits the
// given artifacts: Article for full_draft, ReviewResult for quality_report —
// mirroring how CollectLegacyPayloads maps ExecutionContext fields. The
// runners under test here are the delivery commits around the step, not the
// step's model behavior.
func scriptedRunner(kind string) LegacyNodeRunner {
	return EngineStepRunner{
		StepFactory: func(StepEnv) (engine.Step, error) {
			return &scriptedDeliveryStep{kind: kind}, nil
		},
		Usage: func(*engine.ExecutionContext) (LegacyUsage, error) {
			return LegacyUsage{Measured: true, InputTokens: 10, OutputTokens: 10}, nil
		},
	}
}

type scriptedDeliveryStep struct{ kind string }

func (step *scriptedDeliveryStep) Name() engine.StepName { return "scripted_delivery" }
func (step *scriptedDeliveryStep) CanPause() bool        { return false }
func (step *scriptedDeliveryStep) Execute(_ context.Context, execCtx *engine.ExecutionContext, _ engine.EventEmitter) error {
	switch step.kind {
	case "core.draft.generate":
		execCtx.Article = "# 第一章\n\n治理运行时的交付提交协议在真实存储上验证。\n\n## 背景\n\n质量门要求报告与版本互相引用。\n"
	case "core.validation.quality":
		execCtx.ReviewResult = &engine.ReviewResult{Scores: map[string]float64{"overall": 0.9},
			Issues: []engine.ReviewIssue{{Severity: "low", Type: "style", Message: "可再精炼"}}, Passed: true}
	}
	return nil
}

// runDeliveryScenario builds the whole governed rig: fresh user/document/
// contract/base version, a run whose plan has the given validator head, style
// slug, and finalize tail, and the standard shadow-rollout composition wiring
// every listed capability through the given runner. Requires TEST_DATABASE_URL.
func runDeliveryScenario(t *testing.T, runID string, headClasses []string, runners map[string]LegacyNodeRunner, sourcePack []byte, styleSlug string) (*writingstore.Store, *Orchestrator, string, func()) {
	t.Helper()
	db, cleanup, err := dbtest.Open(strings.TrimSpace(os.Getenv("TEST_DATABASE_URL")), 5, 2)
	if errors.Is(err, dbtest.ErrNoDatabaseURL) {
		t.Skip("TEST_DATABASE_URL not set")
	}
	if err != nil {
		t.Fatal(err)
	}
	integrationDB := db.DB
	ctx := context.Background()
	now := time.Date(2026, 9, 4, 9, 0, 0, 0, time.UTC)

	var userID string
	uid := "delivery_protocol_" + time.Now().UTC().Format("150405.000000000")
	if err := integrationDB.QueryRowContext(ctx, `
		INSERT INTO users (uid, name) VALUES ($1, 'delivery protocol') RETURNING id::text
	`, uid).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationDB.ExecContext(ctx, `TRUNCATE writing_artifact_contents, writing_documents, writing_projects CASCADE`); err != nil {
		t.Fatalf("reset documents: %v", err)
	}
	documentID := "doc_delivery"
	trace := writingstore.TraceContext{Actor: writingstore.Actor{Type: writingstore.ActorSystem, ID: "delivery-test"},
		Provenance: map[string]any{}, SourceRefs: []string{}}
	store, err := writingstore.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateDocument(ctx, writingstore.DocumentRecord{DocumentID: documentID,
		OwnerUserID: userID, Title: "Delivery protocol", Actor: trace.Actor}); err != nil {
		t.Fatal(err)
	}
	// The context source scopes canon lookups through the document project:
	// attach one so document_state resolves for the draft node.
	projectID := "prj_delivery"
	if err := store.CreateProject(ctx, writingstore.ProjectRecord{ProjectID: projectID,
		OwnerUserID: userID, Title: "delivery protocol", Actor: trace.Actor}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDocumentProject(ctx, documentID, projectID); err != nil {
		t.Fatal(err)
	}
	contract := deliveryTestContract(t)
	if err := store.PutContract(ctx, writingstore.ContractRecord{DocumentID: documentID,
		Contract: contract, Trace: trace}); err != nil {
		t.Fatal(err)
	}
	baseVersion := deliveryBaseVersion(t, documentID, "ver_delivery_base", "基线草稿")
	if _, err := store.CommitDocumentVersion(ctx, writingstore.CommitDocumentVersionParams{
		Version: baseVersion, ContractID: contract.ContractID, ContractVersion: contract.Version, Trace: trace}); err != nil {
		t.Fatal(err)
	}

	envelope := deliveryTestPlan(t, contract, headClasses, "core.document.finalize")
	nodeCount := 3 + len(headClasses)
	budget := writingplan.PlanBudget{MaxCostUSD: 40, MaxDurationMS: 600000, MaxConcurrency: 1, MaxNodes: nodeCount + 2, MaxItems: nodeCount + 2}
	permissions := []writingplan.Permission{"model.invoke", "materials.read", "document.revision", "validation.run", "external.research"}
	if err := store.CreateRunWithPlan(ctx, writingstore.RunRecord{RunID: runID, DocumentID: documentID,
		ContractID: contract.ContractID, ContractVersion: contract.Version, ContractHash: contract.ContractHash,
		BaseVersionID: baseVersion.VersionID, StyleSlug: styleSlug, ApprovalMode: writingkernel.ApprovalModeAuto,
		RequestedAssurance: writingkernel.AssuranceLevelStandard, Budget: budget, Permissions: permissions,
		Trace: trace}, writingstore.PlanRecord{RunID: runID, PlanVersion: 1, Envelope: envelope,
		Budget: budget, Permissions: permissions, ApprovalStatus: "not_required", Trace: trace}, "running"); err != nil {
		t.Fatal(err)
	}

	// ── governed composition: real store everywhere, given runners ──
	canonical := WritingStoreContentGateway{Store: store}
	// The kernel finalize executor is always the real one; callers only
	// supply the step runners (draft/validators/quality).
	allRunners := map[string]LegacyNodeRunner{"core.document.finalize": &FinalizeRunner{Store: store, Content: canonical}}
	for id, runner := range runners {
		allRunners[id] = runner
	}
	defaults := writingplan.DefaultCapabilityRegistry()
	capabilities := writingplan.NewCapabilityRegistry("delivery-test")
	executors := NewExecutorRegistry()
	for _, capability := range append(append([]string{"core.draft.generate"}, headClasses...), "core.validation.quality", "core.document.finalize") {
		manifest, ok := defaults.Get(capability)
		if !ok {
			t.Fatalf("capability %s missing", capability)
		}
		bindingID := "delivery.baseline." + capability
		acceptedTypes := append(append([]writingplan.ArtifactType(nil), manifest.InputTypes...), manifest.OptionalInputTypes...)
		if err := capabilities.RegisterExecutor(writingplan.ExecutorBinding{ID: bindingID,
			AcceptedInputTypes:  acceptedTypes,
			ProducedOutputTypes: manifest.OutputTypes,
			Dispatch: func(context.Context, writingplan.ExecutionRequest) (writingplan.ExecutionResult, error) {
				return writingplan.ExecutionResult{}, nil
			}}); err != nil {
			t.Fatal(err)
		}
		manifest.Executor = bindingID
		manifest.Available = true
		policy := DefaultShadowPolicy("delivery.candidate."+capability, AdapterFamilyEngine, capability, "1.0.0")
		baseline, err := NewLegacyExecutorAdapter(AdapterFamilyEngine,
			ExecutorDescriptor{ExecutorID: bindingID, Version: "1",
				SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction, writingplan.NodeValidate}},
			capability, "1.0.0", manifest.Permissions, canonical, allRunners[capability])
		if err != nil {
			t.Fatal(err)
		}
		sink := MemoryShadowContentSink{}
		gateway, err := NewShadowContentGateway(canonical, &sink, policy)
		if err != nil {
			t.Fatal(err)
		}
		candidate, err := NewShadowIsolatedExecutorAdapter(AdapterFamilyEngine,
			ExecutorDescriptor{ExecutorID: "delivery.candidate." + capability, Version: "1",
				SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction, writingplan.NodeValidate}},
			capability, "1.0.0", manifest.Permissions, gateway, allRunners[capability])
		if err != nil {
			t.Fatal(err)
		}
		provider, err := NewMutableRolloutPolicyProvider(policy)
		if err != nil {
			t.Fatal(err)
		}
		rollout, err := NewShadowRolloutExecutor(baseline, candidate, provider,
			WritingStoreEvidenceStore{Recorder: store}, &metricCapture{})
		if err != nil {
			t.Fatal(err)
		}
		if err := executors.Register(rollout); err != nil {
			t.Fatal(err)
		}
		if err := capabilities.Register(manifest); err != nil {
			t.Fatal(err)
		}
	}
	// The contract artifact (and, for sourced shapes, the initial source pack)
	// must be loadable through the canonical content store: stage the bytes
	// under the hash the initial provider will advertise.
	initialArtifacts := []InputArtifact{}
	stage := func(artifactID, artifactType, mediaType string, body []byte) {
		hash := contentHash(body)
		if err := store.PutArtifactContent(ctx, hash, mediaType, body); err != nil {
			t.Fatal(err)
		}
		initialArtifacts = append(initialArtifacts, InputArtifact{ArtifactID: artifactID,
			Version: 1, ArtifactType: writingplan.ArtifactType(artifactType), ContentHash: hash,
			MediaType: mediaType, ContentRef: "memory://" + artifactType})
	}
	contractBytes, err := json.Marshal(contract)
	if err != nil {
		t.Fatal(err)
	}
	stage("art_"+runID+"_contract", "contract", "application/json", contractBytes)
	if sourcePack != nil {
		stage("art_"+runID+"_source_pack", "source_pack", "application/json", sourcePack)
	}
	initial, err := NewCompositeInitialArtifactProvider(fixedInitialProvider(initialArtifacts))
	if err != nil {
		t.Fatal(err)
	}
	orchestrator := &Orchestrator{Store: store, Capabilities: capabilities, Executors: executors,
		State:       NewStateMachine(WritingStoreTransitionRecorder{Store: store}),
		Checkpoints: PersistentCheckpointRepository{Store: store, Trace: trace},
		Initial:     initial, Materials: store, Now: func() time.Time { return now },
		Context:   StoreContextSource{Store: store},
		Envelopes: store, ContextRuntime: &ContextRuntime{},
		Delivery: &DeliveryProtocol{Store: store, Content: canonical, Now: func() time.Time { return now }}}
	return store, orchestrator, documentID, cleanup
}

// assertDeliveredRun checks the full M1.0+M1.1 lineage on the delivered run:
// candidate version promoted to accepted, quality report cross-referenced,
// revision_set consistent with the delivered version.
func assertDeliveredRun(t *testing.T, store *writingstore.Store, runID, documentID string) {
	t.Helper()
	ctx := context.Background()
	candidateID, err := store.CurrentDocumentVersionID(ctx, documentID)
	if err != nil || candidateID == "" {
		t.Fatalf("no current version after delivery: %v", err)
	}
	stored, err := store.GetDocumentVersion(ctx, documentID, candidateID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.QualityState != writingstore.QualityAcceptedDraft {
		t.Fatalf("quality state %q, want accepted via promotion", stored.QualityState)
	}
	if stored.Version.Root == nil || len(stored.Version.Root.Children) == 0 {
		t.Fatal("promoted version lost its document AST")
	}
	runArtifacts, err := store.ListRunArtifacts(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	var revisionSetHash string
	for _, artifact := range runArtifacts {
		if artifact.ArtifactType == "revision_set" {
			revisionSetHash = artifact.ContentHash
		}
	}
	if revisionSetHash == "" {
		t.Fatal("finalize produced no revision_set artifact")
	}
	mediaType, revisionSet, err := store.GetArtifactContent(ctx, revisionSetHash)
	if err != nil {
		t.Fatalf("load revision_set body: %v", err)
	}
	if mediaType != "application/json" {
		t.Fatalf("revision_set media type %q", mediaType)
	}
	var payload struct {
		DocumentID   string `json:"document_id"`
		VersionID    string `json:"version_id"`
		QualityState string `json:"quality_state"`
	}
	if err := json.Unmarshal(revisionSet, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.VersionID != candidateID || payload.DocumentID != documentID || payload.QualityState != "accepted_draft" {
		t.Fatalf("revision_set mismatch: %#v", payload)
	}
}

// executeDeliveredRun executes the run and fails with the unwrapped error
// chain when the outcome is not completion.
func executeDeliveredRun(t *testing.T, orchestrator *Orchestrator, runID string) {
	t.Helper()
	outcome, err := orchestrator.Execute(context.Background(), runID)
	if err != nil || outcome.State != StateCompleted {
		chain := ""
		for cause := err; cause != nil; cause = errors.Unwrap(cause) {
			chain += " -> " + cause.Error()
		}
		t.Fatalf("governed run failed: outcome=%#v err-chain=%s", outcome, chain)
	}
}

// TestDeliveryProtocolThroughOrchestrator drives the M1.0 delivery protocol
// through a real orchestrator over a real store: a draft node followed by a
// quality node must commit the candidate document version and then the
// delivery bundle (accepted quality report + document promotion), leaving
// every cross-reference the finalize gate requires. Skipped without
// TEST_DATABASE_URL.
func TestDeliveryProtocolThroughOrchestrator(t *testing.T) {
	store, orchestrator, documentID, cleanup := runDeliveryScenario(t, "run_delivery", nil, map[string]LegacyNodeRunner{
		"core.draft.generate":     scriptedRunner("core.draft.generate"),
		"core.validation.quality": scriptedRunner("core.validation.quality"),
	}, nil, "")
	defer cleanup()
	executeDeliveredRun(t, orchestrator, "run_delivery")
	assertDeliveredRun(t, store, "run_delivery", documentID)
}

// TestSourcedDeliveryWithValidatorThroughOrchestrator drives the M1.2
// acceptance (docs/22): the sourced shape (draft → evidence validator →
// quality → finalize) completes over a real store, the evidence_report
// artifact lands canonical with its degraded-mode provenance recorded, and
// the whole delivery lineage stays consistent. The validator runs with no LLM
// wired, exercising the degrade-don't-fail contract end to end. Skipped
// without TEST_DATABASE_URL.
func TestSourcedDeliveryWithValidatorThroughOrchestrator(t *testing.T) {
	pack, err := json.Marshal(map[string]any{"query": "信源", "count": 1, "results": []map[string]any{
		{"title": "信源一", "snippet": "治理运行时的信源内容", "url": "https://example.com/src", "source": "tavily"}}})
	if err != nil {
		t.Fatal(err)
	}
	store, orchestrator, documentID, cleanup := runDeliveryScenario(t, "run_delivery_sourced", []string{"core.validation.evidence"},
		map[string]LegacyNodeRunner{
			"core.draft.generate":      scriptedRunner("core.draft.generate"),
			"core.validation.evidence": &ValidatorRunner{},
			"core.validation.quality":  scriptedRunner("core.validation.quality"),
		}, pack, "")
	defer cleanup()
	executeDeliveredRun(t, orchestrator, "run_delivery_sourced")
	assertDeliveredRun(t, store, "run_delivery_sourced", documentID)

	// The evidence report landed as a real canonical artifact whose body
	// records the degraded mode honestly.
	artifacts, err := store.ListRunArtifacts(context.Background(), "run_delivery_sourced")
	if err != nil {
		t.Fatal(err)
	}
	var evidenceHash string
	for _, artifact := range artifacts {
		if artifact.ArtifactType == "evidence_report" {
			evidenceHash = artifact.ContentHash
		}
	}
	if evidenceHash == "" {
		t.Fatal("validator produced no evidence_report artifact")
	}
	mediaType, body, err := store.GetArtifactContent(context.Background(), evidenceHash)
	if err != nil || mediaType != "application/json" {
		t.Fatalf("evidence report load: media=%q err=%v", mediaType, err)
	}
	var report validatorReport
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatal(err)
	}
	if report.Validator != ValidatorCapabilityEvidence || report.Mode != "degraded" || !report.Passed {
		t.Fatalf("evidence report mismatch: %#v", report)
	}
}

// scriptedStyleProbeRunner records every StepEnv the runner saw before
// delegating to the scripted step — the M1.3 acceptance probe.
func scriptedStyleProbeRunner(kind string, seen *[]StepEnv, resolver StyleResolver) LegacyNodeRunner {
	return EngineStepRunner{
		Styles: resolver,
		StepFactory: func(env StepEnv) (engine.Step, error) {
			*seen = append(*seen, env)
			return &scriptedDeliveryStep{kind: kind}, nil
		},
		Usage: func(*engine.ExecutionContext) (LegacyUsage, error) {
			return LegacyUsage{Measured: true, InputTokens: 10, OutputTokens: 10}, nil
		},
	}
}

// TestStyledRunResolvesProfileThroughOrchestrator drives the M1.3 acceptance
// end to end over a real store: the run's style slug round-trips through
// migration 105's column, the orchestrator stamps it into every request, and
// the step factory observes the resolved profile. Skipped without
// TEST_DATABASE_URL.
func TestStyledRunResolvesProfileThroughOrchestrator(t *testing.T) {
	marker := &profile.StyleProfile{Slug: "delivery-style", Name: "验收风格"}
	resolver := &fakeStyleResolver{slug: "delivery-style", profile: marker}
	seen := []StepEnv{}
	store, orchestrator, documentID, cleanup := runDeliveryScenario(t, "run_delivery_styled", nil, map[string]LegacyNodeRunner{
		"core.draft.generate":     scriptedStyleProbeRunner("core.draft.generate", &seen, resolver),
		"core.validation.quality": scriptedRunner("core.validation.quality"),
	}, nil, "delivery-style")
	defer cleanup()
	executeDeliveredRun(t, orchestrator, "run_delivery_styled")
	assertDeliveredRun(t, store, "run_delivery_styled", documentID)

	// The column round-trips: LoadRuntimeRun reports the run's style slug.
	run, err := store.LoadRuntimeRun(context.Background(), "run_delivery_styled")
	if err != nil {
		t.Fatal(err)
	}
	if run.StyleSlug != "delivery-style" {
		t.Fatalf("style slug did not round-trip: %q", run.StyleSlug)
	}
	// The factory saw the stamped slug and the resolved profile on every
	// attempt (draft runs once; quality's runner is scripted and untapped).
	if len(seen) == 0 {
		t.Fatal("step factory was never invoked")
	}
	for _, env := range seen {
		if env.Request.StyleSlug != "delivery-style" {
			t.Fatalf("request carried slug %q", env.Request.StyleSlug)
		}
		if env.Profile != marker {
			t.Fatalf("factory observed wrong profile: %#v", env.Profile)
		}
	}
	if len(resolver.asked) == 0 || resolver.asked[0] != "delivery-style" {
		t.Fatalf("resolver was not asked the run's slug: %#v", resolver.asked)
	}
}

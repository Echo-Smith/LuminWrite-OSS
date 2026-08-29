package writingruntime

// Real vertical scenarios for Task12: every production node dispatches
// through a governed RolloutExecutor whose baseline and shadow candidates are
// real B2 adapters, materials flow through the real MaterialArtifactProvider,
// and quality reports are produced by the real writingquality validator
// registry. Content is deterministic (no LLM); the adapters, gateways,
// snapshot, validation, and evidence flow under test are production code.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingquality"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// ── shared vertical fixtures ─────────────────────────────

func verticalContract(t *testing.T) writingkernel.WritingContract {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join("..", "..", "..", "specs", "lcp", "v1", "fixtures", "writing-contract.valid.json"))
	if err != nil {
		t.Fatal(err)
	}
	contract, err := writingkernel.DecodeWritingContractStrict(payload)
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

// verticalGateway is the canonical content gateway: it serves the initial
// contract bytes, the bodies staged by the material snapshot, and counts
// every canonical Stage call so tests can prove the shadow lane never
// touches it. It is concurrency-safe: shadow goroutines may read while a
// later node's baseline lane stages.
type verticalGateway struct {
	mu     sync.Mutex
	bodies map[string][]byte
	staged map[string][]byte
	stages int
}

func newVerticalGateway(bodies map[string][]byte) *verticalGateway {
	return &verticalGateway{bodies: bodies, staged: map[string][]byte{}}
}

func (gateway *verticalGateway) Load(_ context.Context, input InputArtifact) ([]byte, error) {
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	if body, ok := gateway.bodies[input.ContentRef]; ok {
		return append([]byte(nil), body...), nil
	}
	if body, ok := gateway.staged[input.ContentRef]; ok {
		return append([]byte(nil), body...), nil
	}
	return nil, fmt.Errorf("vertical gateway has no body for ref %s", input.ContentRef)
}

func (gateway *verticalGateway) Stage(_ context.Context, key, _ string, body []byte) (string, string, error) {
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	gateway.stages++
	ref := "vertical://" + key
	gateway.staged[ref] = append([]byte(nil), body...)
	return ref, contentHash(body), nil
}

func (gateway *verticalGateway) stageCount() int {
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	return gateway.stages
}

func (gateway *verticalGateway) stagedBody(ref string) []byte {
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	return append([]byte(nil), gateway.staged[ref]...)
}

type fakeMaterialSource struct{ bodies map[string]MaterialContent }

func (source fakeMaterialSource) LoadMaterial(_ context.Context, _ string, descriptor MaterialDescriptor) (MaterialContent, error) {
	body, ok := source.bodies[descriptor.MaterialID]
	if !ok {
		return MaterialContent{}, fmt.Errorf("no material body for %s", descriptor.MaterialID)
	}
	return body, nil
}

// verticalDocumentVersion parses the deterministic markdown produced by the
// draft steps into a real document AST for the real validators.
func verticalDocumentVersion(documentID, versionID, markdown string) writingkernel.DocumentVersion {
	origin := writingkernel.Origin{Kind: writingkernel.OriginSystem, Ref: "writingruntime/vertical"}
	root := &writingkernel.DocumentNode{BlockID: "blk_root", Type: writingkernel.NodeTypeDocument,
		Attrs: map[string]any{}, Children: []*writingkernel.DocumentNode{}, Origin: origin}
	sectionIndex := 0
	for _, line := range strings.Split(markdown, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			sectionIndex++
			title := strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))
			section := &writingkernel.DocumentNode{BlockID: fmt.Sprintf("blk_section_%d", sectionIndex),
				Type: writingkernel.NodeTypeSection, Attrs: map[string]any{"title": title, "level": 1},
				Children: []*writingkernel.DocumentNode{}, Origin: origin}
			text := &writingkernel.DocumentNode{BlockID: fmt.Sprintf("blk_text_%d", sectionIndex),
				Type: writingkernel.NodeTypeText, Text: title + "：本节完整覆盖该要点并提供背景与结论。",
				Attrs: map[string]any{}, Children: []*writingkernel.DocumentNode{}, Origin: origin}
			paragraph := &writingkernel.DocumentNode{BlockID: fmt.Sprintf("blk_paragraph_%d", sectionIndex),
				Type: writingkernel.NodeTypeParagraph, Attrs: map[string]any{},
				Children: []*writingkernel.DocumentNode{text}, Origin: origin}
			section.Children = append(section.Children, paragraph)
			root.Children = append(root.Children, section)
		}
	}
	return writingkernel.DocumentVersion{SchemaVersion: writingkernel.SchemaVersionV1, DocumentID: documentID,
		VersionID: versionID, Root: root}
}

// ── real quality gate runner ─────────────────────────────

// verticalQualityRunner produces quality_report artifacts through the real
// writingquality validator registry and real gate finalization.
type verticalQualityRunner struct {
	documentID string
	base       func(input LegacyNodeInput) *writingkernel.DocumentVersion
}

func (runner *verticalQualityRunner) Run(ctx context.Context, input LegacyNodeInput) ([]LegacyPayload, LegacyUsage, error) {
	var contractJSON, draft []byte
	var outline engine.OutlineData
	for _, artifactType := range sortedPayloadTypes(input.Payloads) {
		for _, body := range input.Payloads[artifactType] {
			switch artifactType {
			case "contract":
				contractJSON = body
			case "full_draft":
				draft = body
			case "outline":
				if err := json.Unmarshal(body, &outline); err != nil {
					return nil, LegacyUsage{}, err
				}
			}
		}
	}
	contract, err := writingkernel.DecodeWritingContractStrict(contractJSON)
	if err != nil {
		return nil, LegacyUsage{}, err
	}
	candidate, err := verticalDocumentVersion(runner.documentID, "ver_candidate", string(draft)).WithComputedHashes()
	if err != nil {
		return nil, LegacyUsage{}, err
	}
	registry := writingquality.DefaultValidatorRegistry()
	sections := make([]string, 0, len(outline.Outline))
	for _, item := range outline.Outline {
		sections = append(sections, item.Point)
	}
	covered := map[string]bool{}
	prohibited := map[string]bool{}
	normalizedDraft := strings.ToLower(string(draft))
	for _, point := range contract.Content.RequiredPoints {
		covered[strings.ToLower(point)] = strings.Contains(normalizedDraft, strings.ToLower(point))
	}
	for _, point := range contract.Content.ProhibitedPoints {
		prohibited[strings.ToLower(point)] = strings.Contains(normalizedDraft, strings.ToLower(point))
	}
	validationInput := writingquality.ValidationInput{Candidate: &candidate, Contract: &contract,
		RequiredSections: sections, CoveredRequiredPoints: covered, PresentProhibitedPoints: prohibited,
		ValidatedVersionID: "ver_candidate", CommittedVersionID: "ver_candidate"}
	if runner.base != nil {
		validationInput.Base = runner.base(input)
		validationInput.SemanticChecker = containmentSemanticChecker{}
	}
	validatorIDs := []string{writingquality.ValidatorASTIntegrity}
	if len(sections) > 0 {
		validatorIDs = append(validatorIDs, writingquality.ValidatorRequiredSections)
	}
	validatorIDs = append(validatorIDs, writingquality.ValidatorContractConsistency)
	if validationInput.Base != nil {
		validatorIDs = append(validatorIDs, writingquality.ValidatorSemanticPreservation)
	}
	results := make([]writingkernel.ValidatorResult, 0, len(validatorIDs))
	findings := make([]writingkernel.QualityFinding, 0)
	for _, id := range validatorIDs {
		execution, err := registry.Run(ctx, id, validationInput)
		if err != nil {
			return nil, LegacyUsage{}, err
		}
		results = append(results, execution.Result)
		findings = append(findings, execution.Findings...)
	}
	versionID := "ver_candidate"
	snapshotID := "snap_" + input.Request.RunID
	report := writingkernel.QualityReport{SchemaVersion: writingkernel.SchemaVersionV1,
		ReportID: "qr_" + input.Request.RunID, RunID: input.Request.RunID,
		DocumentVersions: writingkernel.QualityDocumentVersions{CandidateVersionID: versionID,
			ValidatedVersionID: &versionID, CommittedVersionID: &versionID, VersionConsistent: true},
		RequestedAssurance: writingkernel.AssuranceLevelStandard, AchievedAssurance: writingkernel.AssuranceLevelStandard,
		AssuranceSatisfied: true, QualityState: writingkernel.QualityStateVerifiedDeliverable,
		Validators: results, Degradations: []writingkernel.QualityDegradation{}, Findings: findings,
		DecisionRecords: []writingkernel.DecisionRecord{}, SnapshotManifestID: &snapshotID, SnapshotPersisted: true,
		CreatedAt: time.Now().UTC()}
	finalized, err := writingquality.FinalizeReport(report, writingkernel.QualityStateVerifiedDeliverable)
	if err != nil {
		return nil, LegacyUsage{}, fmt.Errorf("governed quality gate blocked the draft: %w", err)
	}
	body, err := json.Marshal(finalized)
	if err != nil {
		return nil, LegacyUsage{}, err
	}
	return []LegacyPayload{{OutputKey: "quality_report", ArtifactType: "quality_report",
		MediaType: "application/json", Body: body, Provenance: map[string]any{"adapter": "engine_step", "validator_registry": "writingquality.DefaultValidatorRegistry"},
		SourceRefs: []string{}}}, LegacyUsage{Measured: true, InputTokens: 8, OutputTokens: 12}, nil
}

// containmentSemanticChecker is a real deterministic implementation of the
// semantic preservation seam: the candidate preserves meaning when every base
// section title and paragraph token appears in the candidate text.
type containmentSemanticChecker struct{}

func (containmentSemanticChecker) Check(_ context.Context, base, candidate writingkernel.DocumentVersion) (writingquality.SemanticPreservationResult, error) {
	baseTokens := verticalTokens(base)
	candidateText := verticalAllText(candidate.Root)
	for token := range baseTokens {
		if !strings.Contains(candidateText, token) {
			return writingquality.SemanticPreservationResult{Preserved: false,
				Explanation: "candidate lost base content: " + token}, nil
		}
	}
	return writingquality.SemanticPreservationResult{Preserved: true,
		Explanation: "candidate retains every base section and token"}, nil
}

func verticalTokens(version writingkernel.DocumentVersion) map[string]struct{} {
	tokens := map[string]struct{}{}
	var walk func(node *writingkernel.DocumentNode)
	walk = func(node *writingkernel.DocumentNode) {
		if node == nil {
			return
		}
		if node.Type == writingkernel.NodeTypeSection {
			if title, ok := node.Attrs["title"].(string); ok {
				tokens[title] = struct{}{}
			}
		}
		if node.Text != "" {
			tokens[node.Text] = struct{}{}
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(version.Root)
	return tokens
}

func verticalAllText(node *writingkernel.DocumentNode) string {
	if node == nil {
		return ""
	}
	parts := []string{node.Text}
	for key := range node.Attrs {
		if value, ok := node.Attrs[key].(string); ok {
			parts = append(parts, value)
		}
	}
	for _, child := range node.Children {
		parts = append(parts, verticalAllText(child))
	}
	return strings.Join(parts, "\n")
}

// ── deterministic content steps ──────────────────────────

type verticalOutlineStep struct{}

func (*verticalOutlineStep) Name() engine.StepName { return engine.StepName("vertical_outline") }
func (*verticalOutlineStep) CanPause() bool        { return false }
func (*verticalOutlineStep) Execute(_ context.Context, execCtx *engine.ExecutionContext, _ engine.EventEmitter) error {
	contract, err := writingkernel.DecodeWritingContractStrict([]byte(execCtx.UserInput))
	if err != nil {
		return err
	}
	outline := engine.OutlineData{Title: " governed vertical draft", Outline: []engine.OutlineItem{}}
	for _, point := range contract.Content.RequiredPoints {
		outline.Outline = append(outline.Outline, engine.OutlineItem{Point: point, Type: "required_point"})
	}
	execCtx.Outline = &outline
	return nil
}

type verticalDraftStep struct{}

func (*verticalDraftStep) Name() engine.StepName { return engine.StepName("vertical_draft") }
func (*verticalDraftStep) CanPause() bool        { return false }
func (*verticalDraftStep) Execute(_ context.Context, execCtx *engine.ExecutionContext, _ engine.EventEmitter) error {
	contract, err := writingkernel.DecodeWritingContractStrict([]byte(execCtx.UserInput))
	if err != nil {
		return err
	}
	var builder strings.Builder
	builder.WriteString("# Governed vertical draft\n")
	if execCtx.Outline != nil {
		builder.WriteString("\n依据大纲： " + execCtx.Outline.Title + "\n")
	}
	for _, point := range contract.Content.RequiredPoints {
		builder.WriteString("\n## " + point + "\n\n" + point + "：本节完整覆盖该要点并提供背景与结论。\n")
	}
	execCtx.Article = builder.String()
	return nil
}

type verticalSourcePackStep struct{}

func (*verticalSourcePackStep) Name() engine.StepName { return engine.StepName("vertical_source_pack") }
func (*verticalSourcePackStep) CanPause() bool        { return false }
func (*verticalSourcePackStep) Execute(_ context.Context, execCtx *engine.ExecutionContext, _ engine.EventEmitter) error {
	for index, material := range execCtx.UserMaterials {
		execCtx.SearchResults = append(execCtx.SearchResults, engine.SearchResult{Title: fmt.Sprintf("材料检索 %d", index+1),
			URL: fmt.Sprintf("https://vertical.example.com/source/%d", index+1), Snippet: material, Source: "vertical_material"})
	}
	return nil
}

type verticalRewriteStep struct{}

func (*verticalRewriteStep) Name() engine.StepName { return engine.StepName("vertical_rewrite") }
func (*verticalRewriteStep) CanPause() bool        { return false }
func (*verticalRewriteStep) Execute(_ context.Context, execCtx *engine.ExecutionContext, _ engine.EventEmitter) error {
	var manifest MaterialManifest
	for _, material := range execCtx.UserMaterials {
		if err := json.Unmarshal([]byte(material), &manifest); err != nil {
			return err
		}
	}
	var builder strings.Builder
	builder.WriteString("# 忠实改写\n")
	for _, material := range manifest.Materials {
		builder.WriteString("\n## " + material.Title + "\n\n忠实保留 " + material.Title + " 的原意与全部要点，仅调整表述。\n")
	}
	execCtx.Article = builder.String()
	return nil
}

// ── scenario graph builder ───────────────────────────────

type verticalNode struct {
	name     string
	kind     writingplan.NodeKind
	inputs   []writingplan.ArtifactType
	output   writingplan.ArtifactType
	runner   func(t *testing.T, documentID string) LegacyNodeRunner
	validate func(t *testing.T, ctx verticalResult)
}

type verticalResult struct {
	store     *fakeRuntimeStore
	canonical *verticalGateway
	evidence  *MemoryRolloutEvidenceStore
	sink      *MemoryShadowContentSink
	outcome   RunOutcome
	runID     string
}

type verticalMaterialSelection struct{ materials []MaterialDescriptor }

func (selection verticalMaterialSelection) MaterialsForRun(_ context.Context, run writingstore.RuntimeRun, _ writingstore.PlanRecord) (MaterialSnapshotRequest, error) {
	return MaterialSnapshotRequest{RunID: run.RunID, OwnerID: "user_vertical",
		ConflictHandling: "ask_user", Materials: selection.materials}, nil
}

func runVerticalScenario(t *testing.T, name string, nodes []verticalNode, withMaterials bool) verticalResult {
	t.Helper()
	now := time.Date(2026, 8, 29, 16, 0, 0, 0, time.UTC)
	contractBytes, err := os.ReadFile(filepath.Join("..", "..", "..", "specs", "lcp", "v1", "fixtures", "writing-contract.valid.json"))
	if err != nil {
		t.Fatal(err)
	}
	runID := "run_vertical_" + name
	documentID := "doc_vertical_" + name
	capabilityVersion := "1.0.0"
	plan := writingplan.ExecutablePlan{PlanID: "plan_vertical_" + name, Status: writingplan.PlanValidated,
		TrustLevel: writingplan.TrustT1, RootNodeID: "node_" + nodes[0].name, Nodes: []writingplan.PlanNode{},
		StaticValidation: writingplan.StaticValidation{Valid: true, CheckedAt: now, Errors: []string{},
			CapabilityRegistryVersion: "vertical-" + name, BudgetValid: true, PermissionsValid: true,
			ArtifactFlowValid: true, FailurePathsValid: true}}
	accepted, produced := []writingplan.ArtifactType{}, []writingplan.ArtifactType{}
	for index, node := range nodes {
		dependencies := []string{}
		if index > 0 {
			dependencies = []string{"node_" + nodes[index-1].name}
		}
		capability := "core.vertical." + name + "." + node.name
		plan.Nodes = append(plan.Nodes, writingplan.PlanNode{NodeID: "node_" + node.name, Kind: node.kind,
			Capability: capability, CapabilityVersion: capabilityVersion, DependsOn: dependencies,
			InputArtifactTypes: append([]writingplan.ArtifactType(nil), node.inputs...),
			OutputArtifactTypes: []writingplan.ArtifactType{node.output},
			Bounds:              writingplan.Bounds{MaxAttempts: 1, MaxConcurrency: 1, MaxItems: 1, MaxCostUSD: 5, TimeoutMS: 2000},
			FailurePath:         writingplan.FailureFail})
		for _, input := range node.inputs {
			accepted = appendUniqueArtifact(accepted, input)
		}
		produced = appendUniqueArtifact(produced, node.output)
	}
	plan, err = plan.WithComputedHash()
	if err != nil {
		t.Fatal(err)
	}
	canonical := newVerticalGateway(map[string][]byte{"memory://contract": contractBytes})
	sink := NewMemoryShadowContentSink()
	store := &fakeRuntimeStore{run: writingstore.RuntimeRun{RunID: runID, DocumentID: documentID,
		ContractID: "ctr_vertical_" + name, ContractVersion: 1, ContractHash: hashForTest("contract"),
		Status: string(StatePlanned), ActivePlanID: plan.PlanID, ActivePlanVersion: 1,
		Budget:      writingplan.PlanBudget{MaxCostUSD: 40, MaxDurationMS: 60000, MaxConcurrency: 1, MaxNodes: len(nodes) + 2, MaxItems: 4},
		Permissions: []writingplan.Permission{"model.invoke", "materials.read"}},
		plan: writingstore.PlanRecord{RunID: runID, PlanVersion: 1, ApprovalStatus: "not_required",
			Envelope: writingplan.WritingPlanEnvelope{IntentPlan: writingplan.IntentPlan{ContractRef: writingplan.ObjectRef{ID: "ctr_vertical_" + name, Version: 1, Hash: hashForTest("contract")}}, ExecutablePlan: plan}}}
	capabilities := writingplan.NewCapabilityRegistry("vertical-" + name)
	executors := NewExecutorRegistry()
	evidence := &MemoryRolloutEvidenceStore{}
	policyHashes := make([]string, 0, len(nodes))
	for index, node := range nodes {
		capability := "core.vertical." + name + "." + node.name
		bindingID := fmt.Sprintf("vertical.%s.baseline.%d", name, index)
		if err := capabilities.RegisterExecutor(writingplan.ExecutorBinding{ID: bindingID,
			AcceptedInputTypes: append([]writingplan.ArtifactType(nil), node.inputs...), ProducedOutputTypes: []writingplan.ArtifactType{node.output},
			Dispatch: func(context.Context, writingplan.ExecutionRequest) (writingplan.ExecutionResult, error) {
				return writingplan.ExecutionResult{}, nil
			}}); err != nil {
			t.Fatal(err)
		}
		if err := capabilities.Register(writingplan.CapabilityManifest{ID: capability, Class: "vertical." + name,
			Executor: bindingID, InputTypes: append([]writingplan.ArtifactType(nil), node.inputs...), OptionalInputTypes: []writingplan.ArtifactType{},
			OutputTypes: []writingplan.ArtifactType{node.output}, Permissions: []writingplan.Permission{"model.invoke", "materials.read"},
			Validator: node.kind == writingplan.NodeValidate, EstimatedCostUSD: .1, EstimatedDurationMS: 10, Version: capabilityVersion,
			SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction, writingplan.NodeValidate}, MaxBounds: plan.Nodes[index].Bounds,
			Idempotency: writingplan.IdempotencySafe, Available: true}); err != nil {
			t.Fatal(err)
		}
		// Each node binds its own policy version and shadow namespace; all
		// nodes share one shadow sink. The policy executor id must match the
		// candidate descriptor for the binding check.
		candidateID := fmt.Sprintf("vertical.%s.candidate.%d", name, index)
		nodePolicy := DefaultShadowPolicy(candidateID, AdapterFamilyEngine, capability, capabilityVersion)
		policyHashes = append(policyHashes, strings.TrimPrefix(nodePolicy.PolicyHash, "sha256:"))
		nodeGateway, err := NewShadowContentGateway(canonical, sink, nodePolicy)
		if err != nil {
			t.Fatal(err)
		}
		runner := node.runner(t, documentID)
		baseline, err := NewLegacyExecutorAdapter(AdapterFamilyEngine,
			ExecutorDescriptor{ExecutorID: bindingID, Version: "1", SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction, writingplan.NodeValidate}},
			capability, capabilityVersion, []writingplan.Permission{"model.invoke", "materials.read"}, canonical, runner)
		if err != nil {
			t.Fatal(err)
		}
		candidate, err := NewShadowIsolatedExecutorAdapter(AdapterFamilyEngine,
			ExecutorDescriptor{ExecutorID: candidateID, Version: "1",
				SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction, writingplan.NodeValidate}},
			capability, capabilityVersion, []writingplan.Permission{"model.invoke", "materials.read"}, nodeGateway, runner)
		if err != nil {
			t.Fatal(err)
		}
		nodeProvider, err := NewMutableRolloutPolicyProvider(nodePolicy)
		if err != nil {
			t.Fatal(err)
		}
		rollout, err := NewShadowRolloutExecutor(baseline, candidate, nodeProvider, evidence, &metricCapture{})
		if err != nil {
			t.Fatal(err)
		}
		if err := executors.Register(rollout); err != nil {
			t.Fatal(err)
		}
	}
	providers := []InitialArtifactProvider{fixedInitialProvider{{ArtifactID: "art_" + runID + "_contract", Version: 1,
		ArtifactType: "contract", ContentHash: contentHash(contractBytes), MediaType: "application/json", ContentRef: "memory://contract"}}}
	if withMaterials {
		adapter, err := NewMaterialAdapter(fakeMaterialSource{bodies: map[string]MaterialContent{"mat_forest": {Body: []byte("森林深处的狐狸在晨雾中活动，狐狸是森林生态的重要成员。"), SourceRefs: []string{"https://vertical.example.com/forest"}}}}, canonical)
		if err != nil {
			t.Fatal(err)
		}
		providers = append(providers, &MaterialArtifactProvider{Adapter: adapter,
			Selection: verticalMaterialSelection{materials: []MaterialDescriptor{{MaterialID: "mat_forest",
				OwnerID: "user_vertical", Title: "森林狐狸观察", SourceKind: MaterialSourceText,
				SourceRef: "mem://forest", MediaType: "text/plain", UpdatedAt: now}}}})
	}
	initial, err := NewCompositeInitialArtifactProvider(providers...)
	if err != nil {
		t.Fatal(err)
	}
	orchestrator := &Orchestrator{Store: store, Capabilities: capabilities, Executors: executors,
		State: NewStateMachine(store), Checkpoints: &memoryCheckpoints{}, Initial: initial,
		Materials: store, Now: func() time.Time { return now }}
	out, err := orchestrator.Execute(context.Background(), runID)
	if err != nil || out.State != StateCompleted || len(out.CompletedNodes) != len(nodes) {
		t.Fatalf("vertical scenario %s: out=%#v err=%v", name, out, err)
	}
	result := verticalResult{store: store, canonical: canonical, evidence: evidence, sink: sink, outcome: out, runID: runID}
	persisted, err := store.ListRunArtifacts(context.Background(), runID)
	if err != nil || len(persisted) != len(nodes) {
		t.Fatalf("artifacts=%#v err=%v", persisted, err)
	}
	for _, artifact := range persisted {
		if artifact.Status != "provisional" || IsShadowContentRef(artifact.ContentRef) {
			t.Fatalf("canonical artifact polluted by shadow lane: %#v", artifact)
		}
	}
	expectedComparisons := 4 * len(nodes)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(evidence.Records()) >= expectedComparisons {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	records := evidence.Records()
	if len(records) < expectedComparisons {
		for _, record := range records {
			t.Logf("record kind=%s lane=%s status=%s node=%s", record.Kind, record.Lane, record.Status, record.Identity.NodeID)
		}
		t.Fatalf("evidence records=%d want >= %d", len(records), expectedComparisons)
	}
	for _, key := range sink.Keys() {
		matched := false
		for _, prefix := range policyHashes {
			if strings.HasPrefix(key, prefix+"/"+runID) {
				matched = true
			}
		}
		if !matched {
			t.Fatalf("sink key %s escapes every node shadow namespace", key)
		}
	}
	for _, node := range nodes {
		if node.validate != nil {
			node.validate(t, result)
		}
	}
	return result
}

func verticalEngineRunner(step engine.Step) func(t *testing.T, documentID string) LegacyNodeRunner {
	return func(t *testing.T, documentID string) LegacyNodeRunner {
		return EngineStepRunner{StepFactory: func() engine.Step { return step },
			Usage: func(*engine.ExecutionContext) (LegacyUsage, error) { return LegacyUsage{Measured: true, InputTokens: 5, OutputTokens: 5}, nil }}
	}
}

func verticalQualityNodeRunner(base func(input LegacyNodeInput) *writingkernel.DocumentVersion) func(t *testing.T, documentID string) LegacyNodeRunner {
	return func(t *testing.T, documentID string) LegacyNodeRunner {
		return &verticalQualityRunner{documentID: documentID, base: base}
	}
}

// ── the three vertical scenarios ─────────────────────────

func TestVerticalLongFormCreationThroughRealAdapters(t *testing.T) {
	result := runVerticalScenario(t, "long_form", []verticalNode{
		{name: "outline", kind: writingplan.NodeAction, inputs: []writingplan.ArtifactType{"contract"}, output: "outline", runner: verticalEngineRunner(&verticalOutlineStep{})},
		{name: "draft", kind: writingplan.NodeAction, inputs: []writingplan.ArtifactType{"contract", "outline"}, output: "full_draft", runner: verticalEngineRunner(&verticalDraftStep{})},
		{name: "quality", kind: writingplan.NodeValidate, inputs: []writingplan.ArtifactType{"contract", "outline", "full_draft"}, output: "quality_report", runner: verticalQualityNodeRunner(nil),
			validate: func(t *testing.T, ctx verticalResult) {
				comparison := verticalLastComparison(t, ctx)
				verticalAssertValidator(t, comparison, writingquality.ValidatorASTIntegrity, writingkernel.ValidatorStatusPassed)
				verticalAssertValidator(t, comparison, writingquality.ValidatorContractConsistency, writingkernel.ValidatorStatusPassed)
			}},
	}, false)
	// The baseline quality report passed the real FinalizeReport gate.
	var qualityArtifact writingstore.ArtifactRecord
	persisted, _ := result.store.ListRunArtifacts(context.Background(), result.runID)
	for _, artifact := range persisted {
		if artifact.ArtifactType == "quality_report" {
			qualityArtifact = artifact
		}
	}
	body := result.canonical.stagedBody(qualityArtifact.ContentRef)
	if len(body) == 0 {
		t.Fatal("baseline quality report was not staged through the canonical gateway")
	}
	var report writingkernel.QualityReport
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatal(err)
	}
	if report.QualityState != writingkernel.QualityStateVerifiedDeliverable {
		t.Fatalf("quality state=%s", report.QualityState)
	}
}

func TestVerticalMultiMaterialSynthesisThroughRealAdapters(t *testing.T) {
	var sourcePackRef string
	result := runVerticalScenario(t, "multi_material", []verticalNode{
		{name: "research", kind: writingplan.NodeAction, inputs: []writingplan.ArtifactType{"contract", "materials"}, output: "source_pack", runner: verticalEngineRunner(&verticalSourcePackStep{}),
			validate: func(t *testing.T, ctx verticalResult) {
				persisted, _ := ctx.store.ListRunArtifacts(context.Background(), ctx.runID)
				for _, artifact := range persisted {
					if artifact.ArtifactType == "source_pack" {
						sourcePackRef = artifact.ContentRef
					}
				}
			}},
		{name: "synthesis", kind: writingplan.NodeAction, inputs: []writingplan.ArtifactType{"contract", "source_pack"}, output: "full_draft", runner: verticalEngineRunner(&verticalDraftStep{})},
		{name: "quality", kind: writingplan.NodeValidate, inputs: []writingplan.ArtifactType{"contract", "full_draft"}, output: "quality_report", runner: verticalQualityNodeRunner(nil),
			validate: func(t *testing.T, ctx verticalResult) {
				comparison := verticalLastComparison(t, ctx)
				verticalAssertValidator(t, comparison, writingquality.ValidatorASTIntegrity, writingkernel.ValidatorStatusPassed)
			}},
	}, true)
	// Real conflict detection over the real staged source pack content.
	body := result.canonical.stagedBody(sourcePackRef)
	if len(body) == 0 {
		t.Fatal("source pack was not staged through the canonical gateway")
	}
	var pack struct {
		Results []engine.SearchResult `json:"results"`
	}
	if err := json.Unmarshal(body, &pack); err != nil {
		t.Fatal(err)
	}
	if len(pack.Results) == 0 || pack.Results[0].URL == "" {
		t.Fatalf("source pack is empty: %s", body)
	}
	claims := []MaterialClaim{{ClaimID: "claim_user", Subject: "forest", Predicate: "actor", Value: "fox",
		UserMaterial: true, SourceRefs: []string{"memory://materials"}},
		{ClaimID: "claim_source", Subject: "forest", Predicate: "actor", Value: "wolf", SourceRefs: []string{pack.Results[0].URL}}}
	findings := DetectClaimConflicts(claims)
	if len(findings) != 1 {
		t.Fatalf("findings=%#v", findings)
	}
	if ErrorCodeOf(EnforceConflictHandling("ask_user", findings)) != CodeSourceConflictRequiresDecision {
		t.Fatal("conflict handling did not require a user decision")
	}
	// The material snapshot came from the real MaterialArtifactProvider.
	saved, err := result.store.LoadInitialMaterialSnapshot(context.Background(), result.runID)
	if err != nil || len(saved.Artifacts) != 2 {
		t.Fatalf("material snapshot=%#v err=%v", saved, err)
	}
}

func TestVerticalFaithfulRewriteThroughRealAdapters(t *testing.T) {
	result := runVerticalScenario(t, "faithful_rewrite", []verticalNode{
		{name: "rewrite", kind: writingplan.NodeAction, inputs: []writingplan.ArtifactType{"contract", "materials"}, output: "full_draft", runner: verticalEngineRunner(&verticalRewriteStep{})},
		{name: "semantic_quality", kind: writingplan.NodeValidate, inputs: []writingplan.ArtifactType{"contract", "materials", "full_draft"}, output: "quality_report", runner: verticalQualityNodeRunner(
			func(input LegacyNodeInput) *writingkernel.DocumentVersion {
				var manifest MaterialManifest
				if err := json.Unmarshal(input.Payloads["materials"][0], &manifest); err != nil {
					return nil
				}
				origin := writingkernel.Origin{Kind: writingkernel.OriginSystem, Ref: "writingruntime/vertical"}
				root := &writingkernel.DocumentNode{BlockID: "blk_base_root", Type: writingkernel.NodeTypeDocument,
					Attrs: map[string]any{}, Children: []*writingkernel.DocumentNode{}, Origin: origin}
				for index, material := range manifest.Materials {
					text := &writingkernel.DocumentNode{BlockID: fmt.Sprintf("blk_base_text_%d", index),
						Type: writingkernel.NodeTypeText, Text: material.Title, Attrs: map[string]any{}, Children: []*writingkernel.DocumentNode{}, Origin: origin}
					paragraph := &writingkernel.DocumentNode{BlockID: fmt.Sprintf("blk_base_paragraph_%d", index),
						Type: writingkernel.NodeTypeParagraph, Attrs: map[string]any{}, Children: []*writingkernel.DocumentNode{text}, Origin: origin}
					section := &writingkernel.DocumentNode{BlockID: fmt.Sprintf("blk_base_section_%d", index),
						Type: writingkernel.NodeTypeSection, Attrs: map[string]any{"title": material.Title, "level": 1},
						Children: []*writingkernel.DocumentNode{paragraph}, Origin: origin}
					root.Children = append(root.Children, section)
				}
				return &writingkernel.DocumentVersion{SchemaVersion: writingkernel.SchemaVersionV1,
					DocumentID: "doc_vertical_faithful_rewrite", VersionID: "ver_base", Root: root}
			}),
			validate: func(t *testing.T, ctx verticalResult) {
				comparison := verticalLastComparison(t, ctx)
				verticalAssertValidator(t, comparison, writingquality.ValidatorSemanticPreservation, writingkernel.ValidatorStatusPassed)
			}},
	}, true)
	if len(result.sink.Keys()) < 2 {
		t.Fatal("shadow lane did not stage both nodes")
	}
}

func verticalLastComparison(t *testing.T, ctx verticalResult) *ShadowComparison {
	t.Helper()
	var comparison *ShadowComparison
	for _, record := range ctx.evidence.Records() {
		if record.Kind == "shadow_comparison" && record.Comparison != nil {
			comparison = record.Comparison
		}
	}
	if comparison == nil {
		t.Fatal("no shadow comparison evidence")
	}
	return comparison
}

func verticalAssertValidator(t *testing.T, comparison *ShadowComparison, validatorID string, status writingkernel.ValidatorStatus) {
	t.Helper()
	for _, line := range comparison.ValidatorSummary {
		if line.ValidatorID == validatorID {
			if line.Status != string(status) {
				t.Fatalf("validator %s status=%s want %s", validatorID, line.Status, status)
			}
			return
		}
	}
	t.Fatalf("validator %s missing from shadow evidence: %#v", validatorID, comparison.ValidatorSummary)
}

package writingruntime

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingquality"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

func TestTask12WritingScenariosTraverseGovernedArtifactGraph(t *testing.T) {
	scenarios := []scenarioDefinition{
		{name: "long_form_creation", initial: []writingplan.ArtifactType{"contract"}, nodes: []scenarioNode{
			{"outline", writingplan.NodeAction, []writingplan.ArtifactType{"contract"}, "outline"},
			{"draft", writingplan.NodeAction, []writingplan.ArtifactType{"contract", "outline"}, "full_draft"},
			{"quality", writingplan.NodeValidate, []writingplan.ArtifactType{"full_draft"}, "quality_report"},
			{"finalize", writingplan.NodeAction, []writingplan.ArtifactType{"full_draft", "quality_report"}, "revision_set"},
		}},
		{name: "multi_material_synthesis", initial: []writingplan.ArtifactType{"contract", "materials"}, nodes: []scenarioNode{
			{"research", writingplan.NodeAction, []writingplan.ArtifactType{"contract", "materials"}, "source_pack"},
			{"claims", writingplan.NodeAction, []writingplan.ArtifactType{"source_pack"}, "claim_map"},
			{"synthesis", writingplan.NodeAction, []writingplan.ArtifactType{"contract", "source_pack", "claim_map"}, "full_draft"},
			{"evidence", writingplan.NodeValidate, []writingplan.ArtifactType{"source_pack", "full_draft"}, "evidence_report"},
			{"quality", writingplan.NodeValidate, []writingplan.ArtifactType{"full_draft", "evidence_report"}, "quality_report"},
			{"finalize", writingplan.NodeAction, []writingplan.ArtifactType{"full_draft", "quality_report"}, "revision_set"},
		}},
		{name: "faithful_rewrite", initial: []writingplan.ArtifactType{"contract", "materials"}, nodes: []scenarioNode{
			{"meaning", writingplan.NodeAction, []writingplan.ArtifactType{"materials"}, "brief"},
			{"rewrite", writingplan.NodeAction, []writingplan.ArtifactType{"contract", "materials", "brief"}, "full_draft"},
			{"semantic_validation", writingplan.NodeValidate, []writingplan.ArtifactType{"materials", "full_draft"}, "quality_report"},
			{"finalize", writingplan.NodeAction, []writingplan.ArtifactType{"full_draft", "quality_report"}, "revision_set"},
		}},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			out, store := executeScenario(t, scenario)
			if out.State != StateCompleted || len(out.CompletedNodes) != len(scenario.nodes) || len(store.artifacts) != len(scenario.nodes) {
				t.Fatalf("out=%#v artifacts=%#v", out, store.artifacts)
			}
			if store.artifacts[len(store.artifacts)-1].ArtifactType != "revision_set" {
				t.Fatalf("final artifact=%#v", store.artifacts[len(store.artifacts)-1])
			}
			for _, artifact := range store.artifacts {
				if artifact.Status != "provisional" || artifact.Trace.Provenance == nil {
					t.Fatalf("artifact bypassed governed provisional contract: %#v", artifact)
				}
			}
		})
	}
}

func TestTask12QualityLifecycleAndScenarioBlockers(t *testing.T) {
	verified := scenarioQualityReport("run_long", false)
	final, err := writingquality.FinalizeReport(verified, writingkernel.QualityStateVerifiedDeliverable)
	if err != nil || final.QualityState != writingkernel.QualityStateVerifiedDeliverable {
		t.Fatalf("verified=%#v error=%v", final, err)
	}

	blocked := scenarioQualityReport("run_rewrite", true)
	if decision := writingquality.EvaluateGate(blocked, writingkernel.QualityStateAcceptedDraft); decision.Allowed || decision.BlockerCount != 1 {
		t.Fatalf("rewrite blocker decision=%#v", decision)
	}
	if decision := writingquality.EvaluateGate(blocked, writingkernel.QualityStateVerifiedDeliverable); decision.Allowed {
		t.Fatalf("BLOCKER reached verified: %#v", decision)
	}

	findings := DetectClaimConflicts([]MaterialClaim{
		{ClaimID: "claim_user", Subject: "release", Predicate: "date", Value: "September", UserMaterial: true, SourceRefs: []string{"material://user"}},
		{ClaimID: "claim_source", Subject: "release", Predicate: "date", Value: "October", SourceRefs: []string{"source://external"}},
	})
	if len(findings) != 1 || ErrorCodeOf(EnforceConflictHandling("ask_user", findings)) != CodeSourceConflictRequiresDecision {
		t.Fatalf("conflict findings=%#v", findings)
	}
}

type scenarioNode struct {
	name   string
	kind   writingplan.NodeKind
	inputs []writingplan.ArtifactType
	output writingplan.ArtifactType
}

type scenarioDefinition struct {
	name    string
	initial []writingplan.ArtifactType
	nodes   []scenarioNode
}

func executeScenario(t *testing.T, scenario scenarioDefinition) (RunOutcome, *fakeRuntimeStore) {
	t.Helper()
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	contractHash := hashForTest(scenario.name + ":contract")
	plan := writingplan.ExecutablePlan{PlanID: "plan_" + scenario.name, Status: writingplan.PlanValidated,
		TrustLevel: writingplan.TrustT1, RootNodeID: "node_" + scenario.nodes[0].name,
		Nodes: []writingplan.PlanNode{}, StaticValidation: writingplan.StaticValidation{Valid: true, CheckedAt: now,
			Errors: []string{}, CapabilityRegistryVersion: "task12-scenarios", BudgetValid: true,
			PermissionsValid: true, ArtifactFlowValid: true, FailurePathsValid: true}}
	accepted, produced := []writingplan.ArtifactType{}, []writingplan.ArtifactType{}
	for _, artifactType := range scenario.initial {
		accepted = appendUniqueArtifact(accepted, artifactType)
	}
	for index, spec := range scenario.nodes {
		dependencies := []string{}
		if index > 0 {
			dependencies = []string{"node_" + scenario.nodes[index-1].name}
		}
		capability := "scenario." + scenario.name + "." + spec.name
		plan.Nodes = append(plan.Nodes, writingplan.PlanNode{NodeID: "node_" + spec.name, Kind: spec.kind,
			Capability: capability, CapabilityVersion: "1.0.0", DependsOn: dependencies,
			InputArtifactTypes:  append([]writingplan.ArtifactType(nil), spec.inputs...),
			OutputArtifactTypes: []writingplan.ArtifactType{spec.output},
			Bounds:              writingplan.Bounds{MaxAttempts: 1, MaxConcurrency: 1, MaxItems: 1, MaxCostUSD: 1, TimeoutMS: 1000},
			FailurePath:         writingplan.FailureFail})
		for _, input := range spec.inputs {
			accepted = appendUniqueArtifact(accepted, input)
		}
		produced = appendUniqueArtifact(produced, spec.output)
	}
	var err error
	plan, err = plan.WithComputedHash()
	if err != nil {
		t.Fatal(err)
	}
	runID := "run_" + scenario.name
	store := &fakeRuntimeStore{run: writingstore.RuntimeRun{RunID: runID, DocumentID: "doc_" + scenario.name,
		ContractID: "ctr_" + scenario.name, ContractVersion: 1, ContractHash: contractHash,
		Status: string(StatePlanned), ActivePlanID: plan.PlanID, ActivePlanVersion: 1,
		Budget:      writingplan.PlanBudget{MaxCostUSD: 20, MaxDurationMS: 20000, MaxConcurrency: 1, MaxNodes: 10, MaxItems: 10},
		Permissions: []writingplan.Permission{"model.invoke"}},
		plan: writingstore.PlanRecord{RunID: runID, PlanVersion: 1, ApprovalStatus: "not_required",
			Envelope: writingplan.WritingPlanEnvelope{IntentPlan: writingplan.IntentPlan{ContractRef: writingplan.ObjectRef{ID: "ctr_" + scenario.name, Version: 1, Hash: contractHash}}, ExecutablePlan: plan}}}
	capabilities := writingplan.NewCapabilityRegistry("task12-scenarios")
	executorID := "scenario.executor." + scenario.name
	if err := capabilities.RegisterExecutor(writingplan.ExecutorBinding{ID: executorID, AcceptedInputTypes: accepted,
		ProducedOutputTypes: produced, Dispatch: func(context.Context, writingplan.ExecutionRequest) (writingplan.ExecutionResult, error) {
			return writingplan.ExecutionResult{}, nil
		}}); err != nil {
		t.Fatal(err)
	}
	for _, node := range plan.Nodes {
		if err := capabilities.Register(writingplan.CapabilityManifest{ID: node.Capability, Class: "scenario." + scenario.name,
			Executor: executorID, InputTypes: node.InputArtifactTypes, OptionalInputTypes: []writingplan.ArtifactType{},
			OutputTypes: node.OutputArtifactTypes, Permissions: []writingplan.Permission{"model.invoke"}, Validator: node.Kind == writingplan.NodeValidate,
			EstimatedCostUSD: .1, EstimatedDurationMS: 10, Version: node.CapabilityVersion,
			SupportedNodeKinds: []writingplan.NodeKind{node.Kind}, MaxBounds: node.Bounds,
			Idempotency: writingplan.IdempotencySafe, Available: true}); err != nil {
			t.Fatal(err)
		}
	}
	executor := &scenarioExecutor{descriptor: ExecutorDescriptor{ExecutorID: executorID, Version: "task12-1",
		SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction, writingplan.NodeValidate}}}
	executors := NewExecutorRegistry()
	if err := executors.Register(executor); err != nil {
		t.Fatal(err)
	}
	initial := make([]InputArtifact, 0, len(scenario.initial))
	for _, artifactType := range scenario.initial {
		hash := contractHash
		if artifactType != "contract" {
			hash = hashForTest(scenario.name + ":" + string(artifactType))
		}
		initial = append(initial, InputArtifact{ArtifactID: "art_" + scenario.name + "_" + string(artifactType), Version: 1,
			ArtifactType: artifactType, ContentHash: hash, MediaType: "application/json", ContentRef: "memory://" + string(artifactType)})
	}
	orchestrator := &Orchestrator{Store: store, Capabilities: capabilities, Executors: executors,
		State: NewStateMachine(store), Checkpoints: &memoryCheckpoints{}, Initial: fixedInitialProvider(initial), Now: func() time.Time { return now }}
	out, err := orchestrator.Execute(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	return out, store
}

type scenarioExecutor struct{ descriptor ExecutorDescriptor }

func (executor *scenarioExecutor) Descriptor() ExecutorDescriptor { return executor.descriptor }
func (executor *scenarioExecutor) Execute(_ context.Context, request ExecutionRequest) (ExecutionResult, error) {
	now := time.Now().UTC()
	parents := make([]writingstore.ArtifactRef, len(request.Inputs))
	hashes := make([]string, len(request.Inputs))
	for index, input := range request.Inputs {
		parents[index], hashes[index] = writingstore.ArtifactRef{ArtifactID: input.ArtifactID, Version: input.Version}, input.ContentHash
	}
	artifacts := make([]OutputArtifactDraft, 0, len(request.Node.OutputArtifactTypes))
	for _, artifactType := range request.Node.OutputArtifactTypes {
		artifacts = append(artifacts, OutputArtifactDraft{OutputKey: string(artifactType), ArtifactType: artifactType,
			ContentHash: hashForTest(request.NodeID + ":" + string(artifactType)), MediaType: "application/json",
			ContentRef: "memory://" + request.NodeID + "/" + string(artifactType), Parents: append([]writingstore.ArtifactRef(nil), parents...),
			Producer: request.Node.Capability, CapabilityVersion: request.Node.CapabilityVersion,
			InputHashes: append([]string(nil), hashes...), Provenance: map[string]any{"scenario": true}, SourceRefs: []string{}})
	}
	return ExecutionResult{Artifacts: artifacts, Usage: ExecutionUsage{CostUSD: .1, InputTokens: 10, OutputTokens: 20, DurationMS: 1}, StartedAt: now.Add(-time.Millisecond), CompletedAt: now}, nil
}

func appendUniqueArtifact(values []writingplan.ArtifactType, value writingplan.ArtifactType) []writingplan.ArtifactType {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func scenarioQualityReport(runID string, blocker bool) writingkernel.QualityReport {
	version, snapshot := "ver_candidate", "snap_task12"
	report := writingkernel.QualityReport{SchemaVersion: writingkernel.SchemaVersionV1,
		ReportID: "qr_" + runID, RunID: runID,
		DocumentVersions: writingkernel.QualityDocumentVersions{CandidateVersionID: version,
			ValidatedVersionID: &version, CommittedVersionID: &version, VersionConsistent: true},
		RequestedAssurance: writingkernel.AssuranceLevelStandard, AchievedAssurance: writingkernel.AssuranceLevelStandard,
		AssuranceSatisfied: true, QualityState: writingkernel.QualityStateVerifiedDeliverable,
		Validators: []writingkernel.ValidatorResult{
			{ValidatorID: "core.ast.integrity", Version: "1.0.0", Required: true, Status: writingkernel.ValidatorStatusPassed, Equivalence: writingkernel.ValidatorEquivalencePrimary},
			{ValidatorID: "core.semantic.preservation", Version: "1.0.0", Required: true, Status: writingkernel.ValidatorStatusPassed, Equivalence: writingkernel.ValidatorEquivalencePrimary},
		}, Degradations: []writingkernel.QualityDegradation{}, Findings: []writingkernel.QualityFinding{},
		DecisionRecords: []writingkernel.DecisionRecord{}, SnapshotManifestID: &snapshot, SnapshotPersisted: true,
		CreatedAt: time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)}
	if blocker {
		report.QualityState = writingkernel.QualityStateAcceptedDraft
		report.Validators[1].Status = writingkernel.ValidatorStatusFailed
		report.Findings = append(report.Findings, writingkernel.QualityFinding{FindingID: "finding_meaning_changed",
			Severity: writingkernel.FindingSeverityBlocker, Category: writingkernel.FindingCategorySemanticPreservation,
			Code: "SEMANTIC_PRESERVATION_FAILED", Message: "core meaning changed", ValidatorID: "core.semantic.preservation",
			ValidatorStatus: writingkernel.ValidatorStatusFailed, RuleVersion: "1.0.0", Explanation: "protected meaning differs",
			FixScope: "restore original meaning", Status: writingkernel.FindingStatusOpen})
	}
	return report
}

func (scenario scenarioDefinition) String() string {
	return fmt.Sprintf("%s(%d)", scenario.name, len(scenario.nodes))
}

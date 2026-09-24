package writingruntime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/contextcompiler"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database/dbtest"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// WP1/WP2 production-closure regression tests (store-backed; they skip
// without TEST_DATABASE_URL like the rest of the package's integration
// tests):
//
//  1. the evidence view of a research draft node reads the upstream
//     research_evidence_pack through its INPUT REFERENCES — even though the
//     node itself has produced nothing yet;
//  2. the quality node reaches the same pack through its TRANSITIVE PLAN
//     dependencies (quality declares no pack input);
//  3. the pack's content hash lands in the compiled envelope's
//     source_evidence block, so the envelope hash (computed over the
//     rendered blocks) binds to the exact pack revision;
//  4. the offline replayer projects the SAME evidence view from the stored
//     plan and artifacts as the live compile path.

// closurePlanEnvelope builds a validated research-chain plan (read → draft →
// citations → quality) in the deliveryTestPlan shape, with the pack-producing
// read node and NO pack input on the quality node.
func closurePlanEnvelope(t *testing.T, contract writingkernel.WritingContract) writingplan.WritingPlanEnvelope {
	t.Helper()
	now := time.Now().UTC()
	intent, err := (writingplan.IntentPlan{IntentPlanID: "iplan_closure",
		ContractRef: writingplan.ObjectRef{ID: contract.ContractID, Version: contract.Version, Hash: contract.ContractHash},
		Summary:     "closure: pack flows into draft and quality evidence views", CreatedBy: writingplan.ActorSystem, CreatedAt: now,
		ProposedSteps: []writingplan.ProposedStep{{StepID: "read", Objective: "read papers into a pack",
			CapabilityHint: "core.research.read", DependsOn: []string{}}},
	}).WithComputedHash()
	if err != nil {
		t.Fatal(err)
	}
	readNode := writingplan.PlanNode{NodeID: "node_research_read", Kind: writingplan.NodeAction,
		Capability: "core.research.read", CapabilityVersion: "1.0.0", DependsOn: []string{},
		InputArtifactTypes:  []writingplan.ArtifactType{"contract"},
		OutputArtifactTypes: []writingplan.ArtifactType{"research_evidence_pack"},
		Bounds:              writingplan.Bounds{MaxAttempts: 1, MaxConcurrency: 1, MaxItems: 1, MaxCostUSD: 5, TimeoutMS: 300000},
		FailurePath:         writingplan.FailurePause}
	draftNode := writingplan.PlanNode{NodeID: "node_research_draft", Kind: writingplan.NodeAction,
		Capability: "core.research.draft", CapabilityVersion: "1.0.0",
		DependsOn:           []string{"node_research_read"},
		InputArtifactTypes:  []writingplan.ArtifactType{"contract", "research_evidence_pack", "evidence_approval", "approved_research_outline"},
		OutputArtifactTypes: []writingplan.ArtifactType{"full_draft"},
		Bounds:              writingplan.Bounds{MaxAttempts: 1, MaxConcurrency: 1, MaxItems: 1, MaxCostUSD: 5, TimeoutMS: 300000},
		FailurePath:         writingplan.FailurePause}
	citationsNode := writingplan.PlanNode{NodeID: "node_research_citations", Kind: writingplan.NodeValidate,
		Capability: "core.research.validate_citations", CapabilityVersion: "1.0.0",
		DependsOn:           []string{"node_research_read", "node_research_draft"},
		InputArtifactTypes:  []writingplan.ArtifactType{"research_evidence_pack", "full_draft", "research_citation_index"},
		OutputArtifactTypes: []writingplan.ArtifactType{"evidence_report"},
		Bounds:              writingplan.Bounds{MaxAttempts: 1, MaxConcurrency: 1, MaxItems: 1, MaxCostUSD: 5, TimeoutMS: 300000},
		FailurePath:         writingplan.FailurePause}
	qualityNode := writingplan.PlanNode{NodeID: "node_quality", Kind: writingplan.NodeValidate,
		Capability: "core.validation.quality", CapabilityVersion: "1.0.0",
		DependsOn:           []string{"node_research_draft", "node_research_citations"},
		InputArtifactTypes:  []writingplan.ArtifactType{"full_draft", "evidence_report"},
		OutputArtifactTypes: []writingplan.ArtifactType{"quality_report"},
		Bounds:              writingplan.Bounds{MaxAttempts: 1, MaxConcurrency: 1, MaxItems: 1, MaxCostUSD: 5, TimeoutMS: 300000},
		FailurePath:         writingplan.FailurePause}
	plan, err := (writingplan.ExecutablePlan{PlanID: "plan_closure",
		IntentPlanRef: writingplan.ObjectRef{ID: intent.IntentPlanID, Version: 1, Hash: intent.IntentPlanHash},
		TrustLevel:    writingplan.TrustT1, Status: writingplan.PlanValidated, RootNodeID: readNode.NodeID,
		Nodes: []writingplan.PlanNode{readNode, draftNode, citationsNode, qualityNode},
		StaticValidation: writingplan.StaticValidation{Valid: true, CheckedAt: now, Errors: []string{},
			CapabilityRegistryVersion: "closure-test", BudgetValid: true, PermissionsValid: true,
			ArtifactFlowValid: true, FailurePathsValid: true},
	}).WithComputedHash()
	if err != nil {
		t.Fatal(err)
	}
	decision := writingplan.StrategyDecision{DecisionID: "decision_closure", IntentPlanRef: plan.IntentPlanRef,
		Candidates: []writingplan.StrategyCandidate{{PlanHash: plan.PlanHash, TrustLevel: plan.TrustLevel,
			EstimatedCostUSD: 1, EstimatedDurationMS: 1000, EstimatedConfidence: .8}},
		SelectedPlanHash: plan.PlanHash, SelectionSource: writingplan.SelectionSystem,
		RequestedOrchestration: writingkernel.OrchestrationModeAuto,
		EffectiveOrchestration: writingkernel.OrchestrationModeFast,
		ReasonCode:             "closure-test", Summary: "closure plan", Confidence: .8,
		DegradationConditions: []string{}, CreatedAt: now}
	envelope := writingplan.WritingPlanEnvelope{SchemaVersion: writingplan.SchemaVersion,
		IntentPlan: intent, ExecutablePlan: plan, StrategyDecision: decision}
	if err := envelope.Validate(); err != nil {
		t.Fatal(err)
	}
	return envelope
}

type closureFixture struct {
	store       *writingstore.Store
	gateway     WritingStoreContentGateway
	run         writingstore.RuntimeRun
	plan        writingplan.WritingPlanEnvelope
	packVersion int
}

// newClosureFixture builds the minimal store state for the closure tests:
// user, document, contract, base version, run WITH the research-chain plan
// record (the dependency walk loads it through LoadActivePlan).
func newClosureFixture(t *testing.T) *closureFixture {
	t.Helper()
	db, cleanup, err := dbtest.Open(os.Getenv("TEST_DATABASE_URL"), 6, 2)
	if err != nil {
		if errors.Is(err, dbtest.ErrNoDatabaseURL) {
			t.Skip("TEST_DATABASE_URL not set")
		}
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	ctx := context.Background()
	store, err := writingstore.New(db)
	if err != nil {
		t.Fatal(err)
	}
	var userID string
	if err := db.QueryRowContext(ctx, `INSERT INTO users (uid, name) VALUES ($1,'closure') RETURNING id::text`,
		fmt.Sprintf("closure_%d", time.Now().UnixNano())).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	contract := deliveryTestContract(t)
	trace := writingstore.TraceContext{Provenance: map[string]any{}, SourceRefs: []string{},
		Actor: writingstore.Actor{Type: writingstore.ActorUser, ID: userID}}
	documentID := writingstore.StableID("doc_", "closure", fmt.Sprint(time.Now().UnixNano()))
	if err := store.CreateDocument(ctx, writingstore.DocumentRecord{DocumentID: documentID,
		OwnerUserID: userID, Title: "Closure", Actor: trace.Actor}); err != nil {
		t.Fatal(err)
	}
	// CompileInputs scopes canon lookups through the document project and
	// returns early without one — attach it so the evidence view is reached.
	projectID := writingstore.StableID("prj_", "closure", fmt.Sprint(time.Now().UnixNano()))
	if err := store.CreateProject(ctx, writingstore.ProjectRecord{ProjectID: projectID,
		OwnerUserID: userID, Title: "Closure", Actor: trace.Actor}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDocumentProject(ctx, documentID, projectID); err != nil {
		t.Fatal(err)
	}
	if err := store.PutContract(ctx, writingstore.ContractRecord{DocumentID: documentID,
		Contract: contract, Trace: trace}); err != nil {
		t.Fatal(err)
	}
	baseVersion := deliveryBaseVersion(t, documentID, writingstore.StableID("ver_", documentID), "Closure base")
	if _, err := store.CommitDocumentVersion(ctx, writingstore.CommitDocumentVersionParams{
		Version: baseVersion, ContractID: contract.ContractID, ContractVersion: contract.Version, Trace: trace}); err != nil {
		t.Fatal(err)
	}
	runID := writingstore.StableID("run_", userID, "closure", fmt.Sprint(time.Now().UnixNano()))
	envelope := closurePlanEnvelope(t, contract)
	budget := writingplan.PlanBudget{MaxCostUSD: 40, MaxDurationMS: 600000, MaxConcurrency: 1, MaxNodes: 8, MaxItems: 8}
	permissions := []writingplan.Permission{"model.invoke", "materials.read", "external.research", "validation.run", "document.revision"}
	if err := store.CreateRunWithPlan(ctx, writingstore.RunRecord{RunID: runID, DocumentID: documentID,
		ContractID: contract.ContractID, ContractVersion: contract.Version, ContractHash: contract.ContractHash,
		BaseVersionID: baseVersion.VersionID, ApprovalMode: writingkernel.ApprovalModeAuto,
		RequestedAssurance: writingkernel.AssuranceLevelStandard, Budget: budget, Permissions: permissions,
		Trace: trace}, writingstore.PlanRecord{RunID: runID, PlanVersion: 1, Envelope: envelope,
		Budget: budget, Permissions: permissions, ApprovalStatus: "not_required", Trace: trace}, "planned"); err != nil {
		t.Fatal(err)
	}
	run, err := store.LoadRuntimeRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	return &closureFixture{store: store, gateway: WritingStoreContentGateway{Store: store}, run: run, plan: envelope}
}

// node returns the stored plan's node record for id.
func (fixture *closureFixture) node(id string) writingplan.PlanNode {
	for _, node := range fixture.plan.ExecutablePlan.Nodes {
		if node.NodeID == id {
			return node
		}
	}
	panic("unknown node " + id)
}

// putResearchPack stages a new pack revision content-addressed, records the
// read node's attempt (artifacts FK-reference their node attempt), and
// commits the producing artifact row. Each call raises the artifact version,
// mirroring a re-run of the read node.
func (fixture *closureFixture) putResearchPack(t *testing.T, claimsJSON string) string {
	t.Helper()
	ctx := context.Background()
	fixture.packVersion++
	version := fixture.packVersion
	body := []byte(fmt.Sprintf(`{"schema_version":"v1","run_id":%q,"revision":%d,"claims":%s,"evidence":[]}`,
		fixture.run.RunID, version, claimsJSON))
	hash := contentHash(body)
	if err := fixture.store.PutArtifactContent(ctx, hash, "application/json", body); err != nil {
		t.Fatal(err)
	}
	readNode := fixture.node("node_research_read")
	trace := writingstore.TraceContext{Provenance: map[string]any{}, SourceRefs: []string{},
		Actor: writingstore.Actor{Type: writingstore.ActorCapability, ID: "core.research.read"}}
	if _, _, err := fixture.store.StartNodeAttempt(ctx, writingstore.NodeAttempt{
		RunID: fixture.run.RunID, PlanID: fixture.plan.ExecutablePlan.PlanID, PlanVersion: 1,
		NodeID: readNode.NodeID, Attempt: version, NodeKind: readNode.Kind,
		CapabilityID: readNode.Capability, CapabilityVersion: readNode.CapabilityVersion,
		ExecutorID: "engine.step.research_read", FailurePath: readNode.FailurePath,
		Bounds: readNode.Bounds, InputHash: hash,
	}, trace); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.PutArtifact(ctx, writingstore.ArtifactRecord{
		ArtifactID: writingstore.StableID("art_", fixture.run.RunID, "pack", fmt.Sprint(version)), Version: version,
		RunID: fixture.run.RunID, PlanID: fixture.plan.ExecutablePlan.PlanID, PlanVersion: 1,
		NodeID: readNode.NodeID, Attempt: version, OutputKey: "research_evidence_pack",
		ArtifactType: "research_evidence_pack", ContentHash: hash, MediaType: "application/json",
		ContentRef: "artifact://" + hash, Producer: "engine.step.research_read", CapabilityVersion: "1.0.0",
		Trace: trace,
	}); err != nil {
		t.Fatal(err)
	}
	return hash
}

// evidenceShortHash renders the short hash the evidence lines carry (the
// first 12 characters of the content hash, prefix included).
func evidenceShortHash(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}

// TestClosureDraftEnvelopeCarriesPackHash: the research draft node sees the
// upstream pack via its input references, and the pack hash survives into
// the compiled envelope's source_evidence block — the block set the
// envelope hash is taken over.
func TestClosureDraftEnvelopeCarriesPackHash(t *testing.T) {
	fixture := newClosureFixture(t)
	ctx := context.Background()
	packHash := fixture.putResearchPack(t,
		`[{"claim_id":"c1","text":"检索优于重排","kind":"finding","paper_id":"paper-1","review_status":"pending","evidence_ids":["e1"]}]`)

	source := StoreContextSource{Store: fixture.store, Content: fixture.gateway}
	// Surface the view error directly first: CompileInputs degrades silently,
	// so a broken view must be diagnosed here.
	if _, err := source.renderEvidenceView(ctx, fixture.run, fixture.node("node_research_draft")); err != nil {
		t.Fatalf("render evidence view: %v", err)
	}
	input, err := source.CompileInputs(ctx, fixture.run, fixture.node("node_research_draft"))
	if err != nil {
		t.Fatal(err)
	}
	shortHash := evidenceShortHash(packHash)
	if len(input.EvidenceLines) != 1 {
		t.Fatalf("evidence lines = %#v, want exactly the pack claim", input.EvidenceLines)
	}
	if !strings.Contains(input.EvidenceLines[0], "检索优于重排") || !strings.Contains(input.EvidenceLines[0], shortHash) {
		t.Fatalf("evidence line %q lacks claim text or pack hash %s", input.EvidenceLines[0], shortHash)
	}

	// The envelope hash mechanism covers the rendered blocks: the pack hash
	// must appear in the source_evidence block body, and swapping the pack
	// revision must move the envelope hash.
	envelope, err := contextcompiler.Compile(input)
	if err != nil {
		t.Fatal(err)
	}
	var sourceEvidence string
	for _, block := range envelope.Blocks {
		if block.Name == "source_evidence" {
			sourceEvidence = block.Body
		}
	}
	if !strings.Contains(sourceEvidence, shortHash) {
		t.Fatalf("source_evidence block %q lacks pack hash %s", sourceEvidence, shortHash)
	}

	repacked := fixture.putResearchPack(t,
		`[{"claim_id":"c1","text":"检索优于重排（修订）","kind":"finding","paper_id":"paper-1","review_status":"pending","evidence_ids":["e1"]}]`)
	revised, err := source.CompileInputs(ctx, fixture.run, fixture.node("node_research_draft"))
	if err != nil {
		t.Fatal(err)
	}
	revisedEnvelope, err := contextcompiler.Compile(revised)
	if err != nil {
		t.Fatal(err)
	}
	if len(revised.EvidenceLines) != 1 || !strings.Contains(revised.EvidenceLines[0], evidenceShortHash(repacked)) {
		t.Fatalf("revised evidence lines %#v lack the new pack hash", revised.EvidenceLines)
	}
	if revisedEnvelope.Hash == envelope.Hash {
		t.Fatal("envelope hash did not move with the pack revision")
	}
}

// TestClosureQualitySeesPackThroughPlanDeps: the quality node declares no
// research_evidence_pack input, but its transitive plan dependencies
// (quality → citations/draft → read) bring the pack into its evidence view.
func TestClosureQualitySeesPackThroughPlanDeps(t *testing.T) {
	fixture := newClosureFixture(t)
	ctx := context.Background()
	packHash := fixture.putResearchPack(t,
		`[{"claim_id":"c1","text":"检索优于重排","kind":"finding","paper_id":"paper-1","review_status":"pending","evidence_ids":["e1"]}]`)

	source := StoreContextSource{Store: fixture.store, Content: fixture.gateway}
	input, err := source.CompileInputs(ctx, fixture.run, fixture.node("node_quality"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, line := range input.EvidenceLines {
		if strings.Contains(line, evidenceShortHash(packHash)) {
			found = true
		}
	}
	if !found {
		t.Fatalf("quality evidence lines %#v lack the upstream pack hash %s", input.EvidenceLines, evidenceShortHash(packHash))
	}
}

// TestClosureOfflineReplayMatchesLiveEvidenceView: the offline replayer
// projects the identical evidence items from the persisted plan + artifacts
// as the live StoreContextSource compile path.
func TestClosureOfflineReplayMatchesLiveEvidenceView(t *testing.T) {
	fixture := newClosureFixture(t)
	ctx := context.Background()
	fixture.putResearchPack(t,
		`[{"claim_id":"c1","text":"检索优于重排","kind":"finding","paper_id":"paper-1","review_status":"pending","evidence_ids":["e1"]}]`)

	source := StoreContextSource{Store: fixture.store, Content: fixture.gateway}
	replayer := OfflineReplayer{Store: fixture.store, Content: fixture.gateway}
	for _, nodeID := range []string{"node_research_draft", "node_quality"} {
		node := fixture.node(nodeID)
		liveView, err := source.renderEvidenceView(ctx, fixture.run, node)
		if err != nil {
			t.Fatal(err)
		}
		replayView, err := replayer.renderEvidenceView(ctx, fixture.run, node, &fixture.plan.ExecutablePlan)
		if err != nil {
			t.Fatal(err)
		}
		if len(liveView.Items) != len(replayView.Items) {
			t.Fatalf("%s: item count live=%d replay=%d", nodeID, len(liveView.Items), len(replayView.Items))
		}
		for index, item := range liveView.Items {
			other := replayView.Items[index]
			if item.EvidenceID != other.EvidenceID || item.ClaimOrTopic != other.ClaimOrTopic ||
				item.ContentHash != other.ContentHash || item.SourceRef != other.SourceRef {
				t.Fatalf("%s: item %d live=%+v replay=%+v", nodeID, index, item, other)
			}
		}
		if len(liveView.Items) == 0 {
			t.Fatalf("%s: empty evidence view", nodeID)
		}
	}
}

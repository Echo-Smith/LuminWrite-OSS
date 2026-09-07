package writingruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database/dbtest"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// T06 runtime tests (docs/plans/2026-09-07-research-review-integration.md
// §T06): research_outline assembly from pack+approval with binding checks,
// and the research draft adapter — bounded evidence context, [@ev_]
// markers confined to actually-sent evidence, the citation index bound to
// the draft hash, and the two pause paths (unsupported key section,
// unavailable generator) with resume-through-deterministic-generator proof.

const (
	t06OutlineNode = "node_t06_outline"
	t06DraftNode   = "node_t06_draft"
)

// deterministicGenerator produces fixed prose with markers for exactly the
// evidence in each section's context — the mock-friendly seam the deliverable
// requires (两执行器可注入确定性生成器).
type deterministicGenerator struct {
	fail bool
}

func (generator deterministicGenerator) GenerateResearchDraft(_ context.Context, input ResearchDraftInput) (ResearchDraftOutput, error) {
	if generator.fail {
		return ResearchDraftOutput{}, errors.New("injected generator failure")
	}
	text := map[string]string{}
	for _, section := range input.Sections {
		var builder strings.Builder
		builder.WriteString(section.Title + "一节按论点展开：" + section.CentralPoint + " ")
		for _, evidence := range section.Evidence {
			builder.WriteString("证据表明相关结论 [@" + evidence.EvidenceID + "]。")
		}
		if len(section.Omitted) > 0 {
			builder.WriteString("本节证据受上下文预算限制。")
		}
		text[section.SectionID] = builder.String()
	}
	return ResearchDraftOutput{SectionText: text, ModelRef: "deterministic"}, nil
}

// rogueGenerator emits a marker for evidence never placed in the context.
type rogueGenerator struct{}

func (rogueGenerator) GenerateResearchDraft(_ context.Context, input ResearchDraftInput) (ResearchDraftOutput, error) {
	text := map[string]string{}
	for _, section := range input.Sections {
		text[section.SectionID] = section.Title + "正文 [@ev_rogue_ghost_marker]。"
	}
	return ResearchDraftOutput{SectionText: text}, nil
}

// t06Fixture reuses the T05 store-backed fixture shape: migrated test
// database, v1.1 research contract, run + document + base version.
type t06Fixture struct {
	store    *writingstore.Store
	gateway  WritingStoreContentGateway
	runID    string
	userID   string
	contract writingkernel.WritingContract
	planID   string
}

func newT06Fixture(t *testing.T) *t06Fixture {
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
	if err := db.QueryRowContext(ctx, `INSERT INTO users (uid, name) VALUES ($1,'t06 runtime') RETURNING id::text`,
		fmt.Sprintf("t06rt_%d", time.Now().UnixNano())).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(filepath.Join("..", "..", "..", "specs", "lcp", "v1.1", "fixtures", "writing-contract.research-review.valid.json"))
	if err != nil {
		t.Fatal(err)
	}
	contract, err := writingkernel.DecodeWritingContractResearchStrict(payload)
	if err != nil {
		t.Fatal(err)
	}
	contract.Research.MinCitableSources = 1
	sealed, err := contract.WithComputedHash()
	if err != nil {
		t.Fatal(err)
	}
	documentID := writingstore.StableID("doc_", "t06rt", fmt.Sprint(time.Now().UnixNano()))
	if err := store.CreateDocument(ctx, writingstore.DocumentRecord{DocumentID: documentID,
		OwnerUserID: userID, Title: "T06 runtime", Actor: writingstore.Actor{Type: writingstore.ActorUser, ID: userID}}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutContract(ctx, writingstore.ContractRecord{DocumentID: documentID, Contract: sealed,
		Trace: writingstore.TraceContext{Provenance: map[string]any{}, SourceRefs: []string{},
			Actor: writingstore.Actor{Type: writingstore.ActorUser, ID: userID}}}); err != nil {
		t.Fatal(err)
	}
	version := deliveryBaseVersion(t, documentID, writingstore.StableID("ver_", documentID, "t06"), "T06 base")
	if _, err := store.CommitDocumentVersion(ctx, writingstore.CommitDocumentVersionParams{
		Version: version, ContractID: sealed.ContractID, ContractVersion: sealed.Version,
		Trace: writingstore.TraceContext{Provenance: map[string]any{}, SourceRefs: []string{},
			Actor: writingstore.Actor{Type: writingstore.ActorUser, ID: userID}}}); err != nil {
		t.Fatal(err)
	}
	runID := writingstore.StableID("run_", userID, "t06rt", fmt.Sprint(time.Now().UnixNano()))
	budget := writingplan.PlanBudget{MaxCostUSD: 100, MaxDurationMS: 3000000, MaxConcurrency: 1, MaxNodes: 12, MaxItems: 20}
	if err := store.CreateRun(ctx, writingstore.RunRecord{RunID: runID, DocumentID: documentID,
		ContractID: sealed.ContractID, ContractVersion: sealed.Version, ContractHash: sealed.ContractHash,
		BaseVersionID: version.VersionID, Status: "planned", ApprovalMode: sealed.Collaboration.ApprovalMode,
		RequestedAssurance: sealed.Collaboration.AssuranceLevel, Budget: budget,
		Permissions: []writingplan.Permission{"model.invoke", "materials.read", "external.research", "validation.run", "document.revision"},
		Trace:       writingstore.TraceContext{Provenance: map[string]any{}, SourceRefs: []string{}, Actor: writingstore.Actor{Type: writingstore.ActorUser, ID: userID}}}); err != nil {
		t.Fatal(err)
	}
	contractBody, err := json.Marshal(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutArtifactContent(ctx, contentHash(contractBody), "application/json", contractBody); err != nil {
		t.Fatal(err)
	}
	return &t06Fixture{store: store, gateway: WritingStoreContentGateway{Store: store},
		runID: runID, userID: userID, contract: sealed,
		planID: writingstore.StableID("plan_", documentID, "t06")}
}

func (fixture *t06Fixture) contractInput(t *testing.T) InputArtifact {
	t.Helper()
	body, err := json.Marshal(fixture.contract)
	if err != nil {
		t.Fatal(err)
	}
	return InputArtifact{ArtifactID: writingstore.StableID("art_", fixture.runID, "contract"),
		Version: 1, ArtifactType: "contract", ContentHash: contentHash(body),
		MediaType: "application/json", ContentRef: "artifact://" + strings.TrimPrefix(contentHash(body), "sha256:")}
}

func (fixture *t06Fixture) requestFor(t *testing.T, node writingplan.PlanNode, inputs []InputArtifact) ExecutionRequest {
	t.Helper()
	key, err := writingstore.NodeAttemptKey(fixture.runID, node.NodeID, 1)
	if err != nil {
		t.Fatal(err)
	}
	request := ExecutionRequest{RunID: fixture.runID, PlanID: fixture.planID, PlanVersion: 1,
		NodeID: node.NodeID, Attempt: 1, IdempotencyKey: key,
		ContractRef: writingplan.ObjectRef{ID: fixture.contract.ContractID, Version: fixture.contract.Version, Hash: fixture.contract.ContractHash},
		Node:        node, Inputs: inputs,
		Permissions: []writingplan.Permission{"model.invoke", "materials.read", "external.research", "validation.run", "document.revision"},
		UserID:      fixture.userID}
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	return request
}

func (fixture *t06Fixture) outlineNode() writingplan.PlanNode {
	return writingplan.PlanNode{NodeID: t06OutlineNode, Kind: writingplan.NodeAction,
		Capability: "core.research.outline", CapabilityVersion: "1.0.0",
		DependsOn:           []string{},
		InputArtifactTypes:  []writingplan.ArtifactType{"contract", "research_evidence_pack", "evidence_approval"},
		OutputArtifactTypes: []writingplan.ArtifactType{"research_outline"},
		Bounds:              writingplan.Bounds{MaxAttempts: 1, MaxConcurrency: 1, MaxItems: 1, MaxCostUSD: 5, TimeoutMS: 600000},
		FailurePath:         writingplan.FailurePause}
}

func (fixture *t06Fixture) draftNode() writingplan.PlanNode {
	return writingplan.PlanNode{NodeID: t06DraftNode, Kind: writingplan.NodeAction,
		Capability: "core.research.draft", CapabilityVersion: "1.0.0",
		DependsOn:           []string{},
		InputArtifactTypes:  []writingplan.ArtifactType{"contract", "research_evidence_pack", "evidence_approval", "approved_research_outline"},
		OutputArtifactTypes: []writingplan.ArtifactType{"full_draft", "research_citation_index"},
		Bounds:              writingplan.Bounds{MaxAttempts: 1, MaxConcurrency: 1, MaxItems: 1, MaxCostUSD: 5, TimeoutMS: 600000},
		FailurePath:         writingplan.FailurePause}
}

// stageAsInput stages JSON content through the store content table and
// returns the input artifact identity.
func (fixture *t06Fixture) stageAsInput(t *testing.T, artifactID string, artifactType writingplan.ArtifactType, body []byte) InputArtifact {
	t.Helper()
	hash := contentHash(body)
	if err := fixture.store.PutArtifactContent(context.Background(), hash, "application/json", body); err != nil {
		t.Fatal(err)
	}
	return InputArtifact{ArtifactID: artifactID, Version: 1, ArtifactType: artifactType,
		ContentHash: hash, MediaType: "application/json", ContentRef: "artifact://" + strings.TrimPrefix(hash, "sha256:")}
}

// approvalFor builds the evidence_approval content for a pack input.
func approvalFor(t *testing.T, fixture *t06Fixture, packInput InputArtifact) []byte {
	t.Helper()
	approval := writingkernel.EvidenceApproval{SchemaVersion: writingkernel.EvidenceApprovalSchemaVersion,
		GateID: "gate_" + fixture.runID, ActorID: fixture.userID,
		DecidedAt: time.Now().UTC().Format(time.RFC3339), PlanHash: "sha256:" + strings.Repeat("a", 64),
		EvidencePackRef: writingkernel.ArtifactRef{ArtifactID: packInput.ArtifactID, Version: packInput.Version, ContentHash: packInput.ContentHash},
		Decision:        "approve"}
	body, err := json.Marshal(approval)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// t06Pack is a valid pack with two papers, each with one verified evidence
// entry whose quote matches its block content.
func t06Pack(t *testing.T, fixture *t06Fixture) writingkernel.ResearchEvidencePack {
	t.Helper()
	papers := []writingkernel.PaperEvidence{}
	evidence := []writingkernel.Evidence{}
	for index, paperID := range []string{"paper-a", "paper-b"} {
		blockText := "核心结论" + fmt.Sprint(index) + "：确定性证据引用文本。"
		blockHash := contentHash([]byte(blockText))
		paper := writingkernel.PaperEvidence{PaperID: paperID,
			Bibliography: writingkernel.PaperBibliography{Title: "论文 " + paperID, Authors: []string{"作者"}},
			Origin:       writingkernel.PaperOriginExternal, SelectionReason: "直接相关",
			RelevanceStatus: writingkernel.RelevanceScored, ReadingScope: writingkernel.ReadingScopeFullText,
			DocumentRef: &writingkernel.ArtifactRef{ArtifactID: "art_" + paperID + "_doc", Version: 1,
				ContentHash: "sha256:" + strings.Repeat(fmt.Sprint(index+1), 64)[:64]},
			ReadBlockIDs: []string{"blk-" + paperID}, TotalBlocks: 1,
		}
		papers = append(papers, paper)
		runes := []rune(blockText)
		evidence = append(evidence, writingkernel.Evidence{EvidenceID: "ev_" + paperID, PaperID: paperID,
			DocumentRef: *paper.DocumentRef, BlockID: "blk-" + paperID, BlockHash: blockHash,
			Quote: blockText, StartChar: 0, EndChar: len(runes), EvidenceScope: writingkernel.EvidenceScopeFullText})
	}
	claims := []writingkernel.Claim{{ClaimID: "clm-a", PaperID: "paper-a", Text: "确定性结论 A",
		Kind: writingkernel.ClaimKindSourceAssertion, EvidenceIDs: []string{"ev_paper-a"},
		ReviewStatus: writingkernel.ClaimReviewPending, Limitations: []string{}}}
	pack := writingkernel.ResearchEvidencePack{SchemaVersion: writingkernel.ResearchEvidencePackSchemaVersion,
		ContractHash: fixture.contract.ContractHash,
		CandidatesRef: writingkernel.ArtifactRef{ArtifactID: "art_candidates", Version: 1,
			ContentHash: "sha256:" + strings.Repeat("2", 64)},
		Papers: papers, Evidence: evidence, Claims: claims,
		Coverage:   writingkernel.EvidenceCoverage{Topics: []string{"确定性证据"}, Gaps: []string{"覆盖受限（测试包）"}, Contradictions: []string{}},
		Provenance: []string{"test"}}
	if err := pack.Validate(); err != nil {
		t.Fatal(err)
	}
	return pack
}

func TestResearchOutlineExecutorAssemblesAndValidates(t *testing.T) {
	fixture := newT06Fixture(t)
	ctx := context.Background()
	pack := t06Pack(t, fixture)
	packBody, err := json.Marshal(pack)
	if err != nil {
		t.Fatal(err)
	}
	packInput := fixture.stageAsInput(t, writingstore.StableID("art_", fixture.runID, "pack"), "research_evidence_pack", packBody)
	approvalInput := fixture.stageAsInput(t, writingstore.StableID("art_", fixture.runID, "approval"), "evidence_approval", approvalFor(t, fixture, packInput))
	executor, err := NewResearchOutlineExecutor(fixture.gateway, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := fixture.requestFor(t, fixture.outlineNode(), []InputArtifact{fixture.contractInput(t), packInput, approvalInput})
	result, err := executor.Execute(ctx, request)
	if err != nil {
		t.Fatalf("outline execute: %v", err)
	}
	body, err := fixture.gateway.Load(ctx, InputArtifact{ContentHash: result.Artifacts[0].ContentHash})
	if err != nil {
		t.Fatal(err)
	}
	var outline writingkernel.ResearchOutline
	if err := json.Unmarshal(body, &outline); err != nil {
		t.Fatal(err)
	}
	if err := writingkernel.ValidateOutlineAgainstPack(outline, pack); err != nil {
		t.Fatalf("outline does not bind its pack: %v", err)
	}
	if outline.EvidencePackRef.ArtifactID != packInput.ArtifactID || outline.EvidencePackRef.ContentHash != packInput.ContentHash {
		t.Fatalf("outline pack ref mismatch: %#v", outline.EvidencePackRef)
	}
	// Section structure: intro + one section per evidence-carrying paper + gap.
	if len(outline.Sections) != 4 {
		t.Fatalf("outline sections = %d, want 4 (intro, two papers, gap)", len(outline.Sections))
	}
	supported := 0
	for _, section := range outline.Sections {
		if len(section.EvidenceIDs) > 0 {
			supported++
		}
	}
	if supported != 2 {
		t.Fatalf("supported sections = %d, want 2", supported)
	}
	// Evidence-less sections are only the pinned non-factual ones.
	for _, section := range outline.Sections {
		if len(section.EvidenceIDs) == 0 &&
			section.SectionID != researchOutlineIntroSectionID && section.SectionID != researchOutlineResearchGapID {
			t.Fatalf("evidence-less section %s is not a non-factual purpose", section.SectionID)
		}
	}
	// Deterministic: same inputs → identical bytes.
	second, err := executor.Execute(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if second.Artifacts[0].ContentHash != result.Artifacts[0].ContentHash {
		t.Fatal("deterministic outline changed content hash across re-execution")
	}
}

func TestResearchOutlineExecutorRejectsApprovalForDifferentPack(t *testing.T) {
	fixture := newT06Fixture(t)
	ctx := context.Background()
	pack := t06Pack(t, fixture)
	packBody, err := json.Marshal(pack)
	if err != nil {
		t.Fatal(err)
	}
	packInput := fixture.stageAsInput(t, writingstore.StableID("art_", fixture.runID, "pack"), "research_evidence_pack", packBody)
	approvalInput := fixture.stageAsInput(t, writingstore.StableID("art_", fixture.runID, "approval"), "evidence_approval", approvalFor(t, fixture, InputArtifact{ArtifactID: "art_other", Version: 1, ContentHash: "sha256:" + strings.Repeat("3", 64)}))
	executor, err := NewResearchOutlineExecutor(fixture.gateway, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := fixture.requestFor(t, fixture.outlineNode(), []InputArtifact{fixture.contractInput(t), packInput, approvalInput})
	if _, err := executor.Execute(ctx, request); err == nil {
		t.Fatal("outline accepted an approval bound to a different pack version")
	}
}

func TestResearchDraftExecutorWritesDraftAndCitationIndex(t *testing.T) {
	fixture := newT06Fixture(t)
	ctx := context.Background()
	pack := t06Pack(t, fixture)
	packBody, err := json.Marshal(pack)
	if err != nil {
		t.Fatal(err)
	}
	packInput := fixture.stageAsInput(t, writingstore.StableID("art_", fixture.runID, "pack"), "research_evidence_pack", packBody)
	approvalInput := fixture.stageAsInput(t, writingstore.StableID("art_", fixture.runID, "approval"), "evidence_approval", approvalFor(t, fixture, packInput))
	outlineExecutor, err := NewResearchOutlineExecutor(fixture.gateway, nil)
	if err != nil {
		t.Fatal(err)
	}
	outlineResult, err := outlineExecutor.Execute(ctx, fixture.requestFor(t, fixture.outlineNode(), []InputArtifact{fixture.contractInput(t), packInput, approvalInput}))
	if err != nil {
		t.Fatalf("outline: %v", err)
	}
	outlineBody, err := fixture.gateway.Load(ctx, InputArtifact{ContentHash: outlineResult.Artifacts[0].ContentHash})
	if err != nil {
		t.Fatal(err)
	}
	var outline writingkernel.ResearchOutline
	if err := json.Unmarshal(outlineBody, &outline); err != nil {
		t.Fatal(err)
	}
	approved := writingkernel.ApprovedResearchOutline{ResearchOutline: outline,
		SourceOutlineRef: writingkernel.ArtifactRef{ArtifactID: "art_outline", Version: 1, ContentHash: contentHash(outlineBody)},
		GateDecisionRef:  writingkernel.ArtifactRef{ArtifactID: "art_decision", Version: 1, ContentHash: "sha256:" + strings.Repeat("4", 64)}}
	approvedBody, err := json.Marshal(approved)
	if err != nil {
		t.Fatal(err)
	}
	approvedInput := fixture.stageAsInput(t, writingstore.StableID("art_", fixture.runID, "approved"), "approved_research_outline", approvedBody)
	draftExecutor, err := NewResearchDraftExecutor(fixture.gateway, deterministicGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := draftExecutor.Execute(ctx, fixture.requestFor(t, fixture.draftNode(),
		[]InputArtifact{fixture.contractInput(t), packInput, approvalInput, approvedInput}))
	if err != nil {
		t.Fatalf("draft execute: %v", err)
	}
	if len(result.Artifacts) != 2 {
		t.Fatalf("draft produced %d artifacts, want full_draft + citation index", len(result.Artifacts))
	}
	draftBody, err := fixture.gateway.Load(ctx, InputArtifact{ContentHash: result.Artifacts[0].ContentHash})
	if err != nil {
		t.Fatal(err)
	}
	draftText := string(draftBody)
	// Every pack evidence id appears as a marker in the draft.
	for _, evidence := range pack.Evidence {
		if !strings.Contains(draftText, "[@"+evidence.EvidenceID+"]") {
			t.Fatalf("draft lacks marker for context evidence %s", evidence.EvidenceID)
		}
	}
	indexBody, err := fixture.gateway.Load(ctx, InputArtifact{ContentHash: result.Artifacts[1].ContentHash})
	if err != nil {
		t.Fatal(err)
	}
	var index writingkernel.ResearchCitationIndex
	if err := json.Unmarshal(indexBody, &index); err != nil {
		t.Fatal(err)
	}
	if index.DraftHash != result.Artifacts[0].ContentHash {
		t.Fatalf("citation index draft hash %s does not bind the draft %s", index.DraftHash, result.Artifacts[0].ContentHash)
	}
	if index.EvidencePackRef.ContentHash != packInput.ContentHash {
		t.Fatal("citation index does not reference the evidence pack")
	}
	if err := writingkernel.ValidateCitationIndexAgainstPack(index, pack); err != nil {
		t.Fatalf("citation index invalid against pack: %v", err)
	}
	if len(index.Citations) == 0 {
		t.Fatal("citation index is empty despite in-context markers")
	}
	// Citation intervals are code point intervals inside the draft text.
	runes := []rune(draftText)
	for _, citation := range index.Citations {
		if citation.EndChar > len(runes) {
			t.Fatalf("citation %s interval exceeds draft length", citation.CitationID)
		}
		marker := string(runes[citation.StartChar:citation.EndChar])
		if !strings.HasPrefix(marker, "[@") || !strings.HasSuffix(marker, "]") || marker[2:len(marker)-1] != citation.EvidenceID {
			t.Fatalf("citation %s does not locate its marker: %q", citation.CitationID, marker)
		}
	}
}

func TestResearchDraftExecutorPausesOnUnsupportedKeySection(t *testing.T) {
	fixture := newT06Fixture(t)
	ctx := context.Background()
	pack := t06Pack(t, fixture)
	// Add a factual (non-intro/gap) section with no evidence to the outline.
	outline := writingkernel.ResearchOutline{SchemaVersion: writingkernel.ResearchOutlineSchemaVersion,
		ContractHash: pack.ContractHash,
		EvidencePackRef: writingkernel.ArtifactRef{ArtifactID: "art_pack", Version: 1,
			ContentHash: contentHash(mustJSON(t, pack))},
		Sections: []writingkernel.OutlineSection{
			{SectionID: researchOutlineIntroSectionID, Title: "引言", CentralPoint: "背景。", EvidenceIDs: []string{}, Gaps: []string{}},
			{SectionID: "sec_method_debate", Title: "方法学争论", CentralPoint: "需要证据支持的核心章节。", EvidenceIDs: []string{}, Gaps: []string{}},
			{SectionID: "sec_supported", Title: "证据一", CentralPoint: "有证据。", EvidenceIDs: []string{"ev_paper-a"}, Gaps: []string{}},
		},
		Limitations: []string{}}
	approved := writingkernel.ApprovedResearchOutline{ResearchOutline: outline,
		SourceOutlineRef: writingkernel.ArtifactRef{ArtifactID: "art_outline", Version: 1, ContentHash: "sha256:" + strings.Repeat("5", 64)},
		GateDecisionRef:  writingkernel.ArtifactRef{ArtifactID: "art_decision", Version: 1, ContentHash: "sha256:" + strings.Repeat("6", 64)}}
	inputs, err := fixture.draftRequestWith(t, pack, approved)
	if err != nil {
		t.Fatal(err)
	}
	draftExecutor, err := NewResearchDraftExecutor(fixture.gateway, deterministicGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	_, execErr := draftExecutor.Execute(ctx, inputs)
	if execErr == nil {
		t.Fatal("draft accepted an unsupported key section")
	}
	typed, ok := execErr.(*RuntimeError)
	if !ok || typed.Code != CodeInsufficientEvidence {
		t.Fatalf("unsupported section error = %v, want INSUFFICIENT_EVIDENCE", execErr)
	}
	if !errors.Is(execErr, ErrDraftSectionUnsupported) {
		t.Fatalf("error does not carry the ErrDraftSectionUnsupported sentinel: %v", execErr)
	}
}

// draftRequestWith builds the draft executor request for a synthetic outline.
// The outline's evidence pack ref is rebound to the staged pack input so the
// cross-artifact binding check sees the version under review.
func (fixture *t06Fixture) draftRequestWith(t *testing.T, pack writingkernel.ResearchEvidencePack, approved writingkernel.ApprovedResearchOutline) (ExecutionRequest, error) {
	t.Helper()
	packBody := mustJSON(t, pack)
	packInput := fixture.stageAsInput(t, writingstore.StableID("art_", fixture.runID, "pack"), "research_evidence_pack", packBody)
	approved.EvidencePackRef = writingkernel.ArtifactRef{ArtifactID: packInput.ArtifactID,
		Version: packInput.Version, ContentHash: packInput.ContentHash}
	approvalInput := fixture.stageAsInput(t, writingstore.StableID("art_", fixture.runID, "approval"), "evidence_approval", approvalFor(t, fixture, packInput))
	approvedBody := mustJSON(t, approved)
	approvedInput := fixture.stageAsInput(t, writingstore.StableID("art_", fixture.runID, "approved"), "approved_research_outline", approvedBody)
	return fixture.requestFor(t, fixture.draftNode(), []InputArtifact{fixture.contractInput(t), packInput, approvalInput, approvedInput}), nil
}

func TestResearchDraftExecutorPausesWithoutGenerator(t *testing.T) {
	fixture := newT06Fixture(t)
	ctx := context.Background()
	pack := t06Pack(t, fixture)
	outline := writingkernel.ResearchOutline{SchemaVersion: writingkernel.ResearchOutlineSchemaVersion,
		ContractHash: pack.ContractHash,
		EvidencePackRef: writingkernel.ArtifactRef{ArtifactID: "art_pack", Version: 1,
			ContentHash: contentHash(mustJSON(t, pack))},
		Sections: []writingkernel.OutlineSection{
			{SectionID: "sec_supported", Title: "证据一", CentralPoint: "有证据。", EvidenceIDs: []string{"ev_paper-a", "ev_paper-b"}, Gaps: []string{}},
		},
		Limitations: []string{}}
	approved := writingkernel.ApprovedResearchOutline{ResearchOutline: outline,
		SourceOutlineRef: writingkernel.ArtifactRef{ArtifactID: "art_outline", Version: 1, ContentHash: "sha256:" + strings.Repeat("5", 64)},
		GateDecisionRef:  writingkernel.ArtifactRef{ArtifactID: "art_decision", Version: 1, ContentHash: "sha256:" + strings.Repeat("6", 64)}}
	request, err := fixture.draftRequestWith(t, pack, approved)
	if err != nil {
		t.Fatal(err)
	}
	draftExecutor, err := NewResearchDraftExecutor(fixture.gateway, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, execErr := draftExecutor.Execute(ctx, request)
	if execErr == nil {
		t.Fatal("draft without generator must pause")
	}
	typed, ok := execErr.(*RuntimeError)
	if !ok || typed.Code != CodeResearchUnavailable {
		t.Fatalf("no-generator error = %v, want RESEARCH_UNAVAILABLE", execErr)
	}
}

func TestResearchDraftExecutorRejectsContextExternalMarkers(t *testing.T) {
	fixture := newT06Fixture(t)
	ctx := context.Background()
	pack := t06Pack(t, fixture)
	outline := writingkernel.ResearchOutline{SchemaVersion: writingkernel.ResearchOutlineSchemaVersion,
		ContractHash: pack.ContractHash,
		EvidencePackRef: writingkernel.ArtifactRef{ArtifactID: "art_pack", Version: 1,
			ContentHash: contentHash(mustJSON(t, pack))},
		Sections: []writingkernel.OutlineSection{
			{SectionID: "sec_supported", Title: "证据一", CentralPoint: "有证据。", EvidenceIDs: []string{"ev_paper-a"}, Gaps: []string{}},
		},
		Limitations: []string{}}
	approved := writingkernel.ApprovedResearchOutline{ResearchOutline: outline,
		SourceOutlineRef: writingkernel.ArtifactRef{ArtifactID: "art_outline", Version: 1, ContentHash: "sha256:" + strings.Repeat("5", 64)},
		GateDecisionRef:  writingkernel.ArtifactRef{ArtifactID: "art_decision", Version: 1, ContentHash: "sha256:" + strings.Repeat("6", 64)}}
	request, err := fixture.draftRequestWith(t, pack, approved)
	if err != nil {
		t.Fatal(err)
	}
	draftExecutor, err := NewResearchDraftExecutor(fixture.gateway, rogueGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	_, execErr := draftExecutor.Execute(ctx, request)
	if execErr == nil {
		t.Fatal("context-external markers must fail the node")
	}
	typed, ok := execErr.(*RuntimeError)
	if !ok || typed.Code != CodeEvidenceInvalid {
		t.Fatalf("ghost-marker error = %v, want EVIDENCE_INVALID", execErr)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestDeterministicOutlineSectionIDsAreStable(t *testing.T) {
	// The draft executor's non-factual detection keys on the pinned section
	// ids the deterministic outline mints; keep the seam locked.
	if researchOutlineIntroSectionID == researchOutlineResearchGapID {
		t.Fatal("pinned section ids collide")
	}
	if !strings.HasPrefix(researchOutlineIntroSectionID, "sec_") {
		t.Fatal("pinned section ids must keep the sec_ prefix")
	}
}

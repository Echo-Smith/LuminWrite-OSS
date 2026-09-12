package writingruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// T07 unit tests (docs/plans/2026-09-07-research-review-integration.md §T07):
// the five failure classes run offline over a fixed pack/draft/index triple,
// the quality gate consumes the evidence_report blocker mapping, and the fact
// validator's reviewer seam covers consistent / inconsistent / unavailable
// without any model dependency.

// ── Fixture ─────────────────────────────────────────────────────────────────

// t07Fixture is one consistent research chain: pack (2 papers, 2 evidence),
// parsed blocks matching the evidence hashes, a draft citing both markers,
// and the citation index the draft executor would have committed.
type t07Fixture struct {
	pack     writingkernel.ResearchEvidencePack
	draft    string
	index    writingkernel.ResearchCitationIndex
	contract *writingkernel.ResearchSpec
	parsed   citationParsedDocs
}

func t07PaperID(n int) string    { return fmt.Sprintf("p_%02d", n) }
func t07EvidenceID(n int) string { return fmt.Sprintf("ev_%02d", n) }

func newT07Fixture() t07Fixture {
	f := t07Fixture{contract: &writingkernel.ResearchSpec{
		Version:             writingkernel.ResearchSpecVersionV1,
		EvidenceRequirement: writingkernel.EvidenceRequirementAbstractAllowed,
	}, parsed: citationParsedDocs{}}
	quote := "这是用于引用验证的原文摘录。"
	blockHash := contentHash([]byte(quote))
	documentHash := contentHash([]byte("document body " + quote))
	for n := 1; n <= 2; n++ {
		paperID := t07PaperID(n)
		year := 2020 + n
		paper := writingkernel.PaperEvidence{
			PaperID: paperID,
			Bibliography: writingkernel.PaperBibliography{
				Title: fmt.Sprintf("论文 %s", paperID), Authors: []string{"作者"},
				Year: &year, Venue: t07StrPtr("会议"), DOI: t07StrPtr("10.0000/x"),
				CanonicalURL: t07StrPtr("https://example.org/" + paperID)},
			Origin: writingkernel.PaperOriginExternal, SelectionReason: "relevant",
			RelevanceStatus: writingkernel.RelevanceScored,
			ReadingScope:    writingkernel.ReadingScopeFullText,
			DocumentRef:     refT07(documentHash),
			ReadBlockIDs:    []string{"blk_1"}, TotalBlocks: 1,
		}
		f.pack.Papers = append(f.pack.Papers, paper)
		f.pack.Evidence = append(f.pack.Evidence, writingkernel.Evidence{
			EvidenceID: t07EvidenceID(n), PaperID: paperID,
			DocumentRef: *refT07(documentHash), BlockID: "blk_1", BlockHash: blockHash,
			Quote: quote, StartChar: 0, EndChar: len([]rune(quote)),
			EvidenceScope: writingkernel.EvidenceScopeFullText,
		})
		f.pack.Claims = append(f.pack.Claims, writingkernel.Claim{
			ClaimID:      fmt.Sprintf("clm_%02d", n),
			PaperID:      paperID,
			Text:         fmt.Sprintf("论文 %s 的结论", paperID),
			Kind:         writingkernel.ClaimKindSourceAssertion,
			EvidenceIDs:  []string{t07EvidenceID(n)},
			ReviewStatus: writingkernel.ClaimReviewPending,
			Limitations:  []string{},
		})
		f.parsed[paperID] = map[string]ParsedBlock{"blk_1": {BlockID: "blk_1",
			Text: quote, BlockHash: blockHash}}
	}
	f.pack.SchemaVersion = writingkernel.ResearchEvidencePackSchemaVersion
	f.pack.ContractHash = contentHash([]byte("t07-contract"))
	f.pack.CandidatesRef = refT07Val("art_candidates")
	f.pack.Coverage = writingkernel.EvidenceCoverage{Topics: []string{}, Gaps: []string{}, Contradictions: []string{}}
	f.pack.Provenance = []string{"reader-test"}
	// The draft cites both markers in one sentence each.
	f.draft = "## 摘要证据\n\n论文一的证据：" + quote + " [" + "@" + t07EvidenceID(1) + "]。论文二的证据：" + quote + " [" + "@" + t07EvidenceID(2) + "]。\n"
	f.index = writingkernel.ResearchCitationIndex{
		SchemaVersion: writingkernel.ResearchCitationIndexSchemaVersion,
		ContractHash:  f.pack.ContractHash,
		DraftHash:     contentHash([]byte(f.draft)),
		EvidencePackRef: writingkernel.ArtifactRef{ArtifactID: "art_pack", Version: 1,
			ContentHash: contentHash([]byte("pack-bytes"))},
		Citations: []writingkernel.CitationEntry{
			{CitationID: "cit_1", EvidenceID: t07EvidenceID(1), BlockID: "sec_1", StartChar: 0, EndChar: 5},
			{CitationID: "cit_2", EvidenceID: t07EvidenceID(2), BlockID: "sec_1", StartChar: 10, EndChar: 15},
		},
	}
	return f
}

func refT07(hash string) *writingkernel.ArtifactRef {
	ref := refT07Val("art_doc")
	ref.ContentHash = hash
	return &ref
}

func refT07Val(id string) writingkernel.ArtifactRef {
	return writingkernel.ArtifactRef{ArtifactID: id, Version: 1, ContentHash: contentHash([]byte(id))}
}

func t07StrPtr(s string) *string { return &s }

// inspect runs the fixture through the deterministic check.
func (f t07Fixture) inspect(t *testing.T) citationInspection {
	t.Helper()
	return inspectCitations([]byte(f.draft), f.index, f.pack, f.contract, f.parsed, time.Date(2026, 9, 7, 9, 0, 0, 0, time.UTC))
}

// ── Clean path + bibliography ───────────────────────────────────────────────

func TestT07CleanInspectionHasNoFindings(t *testing.T) {
	f := newT07Fixture()
	inspection := f.inspect(t)
	if inspection.blocker {
		t.Fatalf("clean fixture reported blockers: %+v", inspection.details.Findings)
	}
	// The fixture's claims are pending and cited: the unreviewed projection
	// (plus its review-class issues) is expected — never blockers.
	if len(inspection.details.UnreviewedClaims) != 2 {
		t.Fatalf("unreviewed claims = %v, want both pending claims", inspection.details.UnreviewedClaims)
	}
	for _, issue := range inspection.issues {
		if issue.Severity == "blocker" {
			t.Fatalf("clean fixture issue at blocker severity: %+v", issue)
		}
		if issue.Type != FindingUnreviewedClaim {
			t.Fatalf("clean fixture produced an unexpected issue: %+v", issue)
		}
	}
}

func TestT07BibliographyNumbersFollowFirstCitationOrder(t *testing.T) {
	f := newT07Fixture()
	entries := f.inspect(t).details.Bibliography
	if len(entries) != 2 {
		t.Fatalf("bibliography entries = %d, want 2", len(entries))
	}
	if entries[0].Number != 1 || entries[0].PaperID != t07PaperID(1) {
		t.Fatalf("first entry = %+v, want paper p_01 numbered 1", entries[0])
	}
	if entries[1].Number != 2 || entries[1].PaperID != t07PaperID(2) {
		t.Fatalf("second entry = %+v, want paper p_02 numbered 2", entries[1])
	}
	// The display numbers carry no binding authority: every entry keeps its
	// EvidenceID/CitationID links even if rendering renumbers later.
	if got := strings.Join(entries[0].CitationIDs, ","); got != "cit_1" {
		t.Fatalf("entry 1 citation ids = %q, want cit_1", got)
	}
	if len(entries[1].CitationIDs) != 1 || entries[1].CitationIDs[0] != "cit_2" {
		t.Fatalf("entry 2 citation ids = %v, want [cit_2]", entries[1].CitationIDs)
	}
	if entries[1].Title == "" || entries[1].DOI == "" || entries[1].URL == "" {
		t.Fatalf("bibliography entry lost its pack bibliography data: %+v", entries[1])
	}
}

// ── Class a: nonexistent citation ───────────────────────────────────────────

func TestT07ClassA_DraftMarkerWithoutIndexEntry(t *testing.T) {
	f := newT07Fixture()
	// The draft keeps both markers; the index lost the second entry.
	f.index.Citations = f.index.Citations[:1]
	inspection := f.inspect(t)
	if !inspection.blocker {
		t.Fatal("missing index entry must be a blocker")
	}
	if len(inspection.details.InvalidCitations) == 0 {
		t.Fatal("invalid_citations empty")
	}
	if !strings.Contains(inspection.details.InvalidCitations[0], t07EvidenceID(2)) {
		t.Fatalf("invalid citations = %v, want entry for %s", inspection.details.InvalidCitations, t07EvidenceID(2))
	}
}

func TestT07ClassA_IndexEntryWithoutDraftMarker(t *testing.T) {
	f := newT07Fixture()
	// The index keeps both entries; the draft lost the second marker.
	f.draft = strings.Replace(f.draft, " ["+"@"+t07EvidenceID(2)+"]", "", 1)
	f.index.DraftHash = contentHash([]byte(f.draft))
	inspection := f.inspect(t)
	if !inspection.blocker {
		t.Fatal("orphaned index entry must be a blocker")
	}
	found := false
	for _, invalid := range inspection.details.InvalidCitations {
		if strings.Contains(invalid, "cit_2") {
			found = true
		}
	}
	if !found {
		t.Fatalf("invalid citations %v lack the orphaned entry cit_2", inspection.details.InvalidCitations)
	}
}

// ── Class b: wrong-pack citation ────────────────────────────────────────────

func TestT07ClassB_WrongPackCitation(t *testing.T) {
	f := newT07Fixture()
	// The second citation points at evidence from another pack version.
	f.index.Citations[1].EvidenceID = "ev_otherpack"
	inspection := f.inspect(t)
	if !inspection.blocker {
		t.Fatal("wrong-pack citation must be a blocker")
	}
	found := false
	for _, finding := range inspection.details.Findings {
		if finding.Type == FindingWrongPack && strings.Contains(finding.Message, "ev_otherpack") {
			found = true
		}
	}
	if !found {
		t.Fatalf("wrong-pack finding missing: %+v", inspection.details.Findings)
	}
}

// ── Class c: hash tampering ─────────────────────────────────────────────────

func TestT07ClassC_BlockHashTamper(t *testing.T) {
	f := newT07Fixture()
	tampered := "sha256:" + strings.Repeat("ff", 32)
	if tampered == f.pack.Evidence[0].BlockHash {
		t.Fatal("tamper fixture produced the same hash")
	}
	f.pack.Evidence[0].BlockHash = tampered
	inspection := f.inspect(t)
	if !inspection.blocker {
		t.Fatal("hash tamper must be a blocker")
	}
	found := false
	for _, finding := range inspection.details.Findings {
		if finding.Type == FindingHashTamper && strings.Contains(finding.Message, t07EvidenceID(1)) {
			found = true
		}
	}
	if !found {
		t.Fatalf("hash-tamper finding missing: %+v", inspection.details.Findings)
	}
}

func TestT07ClassC_QuoteTamper(t *testing.T) {
	f := newT07Fixture()
	f.pack.Evidence[0].Quote = "被篡改过的摘录文本。"
	inspection := f.inspect(t)
	if !inspection.blocker {
		t.Fatal("quote tamper must be a blocker")
	}
	found := false
	for _, finding := range inspection.details.Findings {
		if finding.Type == FindingHashTamper && strings.Contains(finding.Message, "quote") {
			found = true
		}
	}
	if !found {
		t.Fatalf("quote-tamper finding missing: %+v", inspection.details.Findings)
	}
}

// ── Class d: abstract-scope overclaim ───────────────────────────────────────

func TestT07ClassD_AbstractEvidenceUnderFullTextRequired(t *testing.T) {
	f := newT07Fixture()
	f.contract.EvidenceRequirement = writingkernel.EvidenceRequirementFullTextRequired
	f.pack.Evidence[0].EvidenceScope = writingkernel.EvidenceScopeAbstract
	f.pack.Papers[0].ReadingScope = writingkernel.ReadingScopeAbstract
	inspection := f.inspect(t)
	// Scope overclaims are REVIEW findings: they surface in the quality
	// report but never block formal delivery on their own.
	if inspection.blocker {
		t.Fatalf("scope overclaim must not block: %+v", inspection.details.Findings)
	}
	if len(inspection.details.ScopeOverclaims) != 1 || inspection.details.ScopeOverclaims[0] != t07EvidenceID(1) {
		t.Fatalf("scope overclaims = %v, want [%s]", inspection.details.ScopeOverclaims, t07EvidenceID(1))
	}
}

func TestT07ClassD_AbstractEvidenceAllowedDoesNotFlag(t *testing.T) {
	f := newT07Fixture()
	f.pack.Evidence[0].EvidenceScope = writingkernel.EvidenceScopeAbstract
	f.pack.Papers[0].ReadingScope = writingkernel.ReadingScopeAbstract
	inspection := f.inspect(t)
	if inspection.blocker || len(inspection.details.ScopeOverclaims) != 0 {
		t.Fatalf("abstract_allowed contract flagged scope overclaims: %+v", inspection.details.ScopeOverclaims)
	}
}

// ── Class e: heuristic unsupported strong assertion ─────────────────────────

func TestT07ClassE_UnsupportedStrongAssertion(t *testing.T) {
	f := newT07Fixture()
	// A numeric strong-causal sentence without any marker.
	f.draft += "实验证明该方法将错误率降低了 32%，因此可以断定其稳定性。\n"
	inspection := f.inspect(t)
	if inspection.blocker {
		t.Fatalf("heuristic unsupported claims must not block: %+v", inspection.details.Findings)
	}
	if len(inspection.details.UnsupportedClaims) == 0 {
		t.Fatal("unsupported claims empty for a strong sentence")
	}
	if !strings.Contains(inspection.details.UnsupportedClaims[0], "32%") {
		t.Fatalf("unsupported claim lost its sentence: %q", inspection.details.UnsupportedClaims[0])
	}
	// The finding is labeled heuristic so no consumer mistakes it for a
	// verified semantic result.
	heuristic := false
	for _, finding := range inspection.details.Findings {
		if finding.Type == FindingUnsupportedAssertion && finding.Source == findingSourceHeuristic {
			heuristic = true
		}
	}
	if !heuristic {
		t.Fatal("unsupported-assertion finding is not labeled heuristic")
	}
}

func TestT07ClassE_MarkedSentencesAreSupported(t *testing.T) {
	f := newT07Fixture()
	// The same strong sentence with a marker stays supported.
	f.draft += "实验表明性能显著提升 [@" + t07EvidenceID(1) + "]。\n"
	inspection := f.inspect(t)
	for _, claim := range inspection.details.UnsupportedClaims {
		if strings.Contains(claim, "显著提升") {
			t.Fatalf("marked sentence flagged unsupported: %q", claim)
		}
	}
}

// ── Omitted-evidence passthrough ────────────────────────────────────────────

func TestT07OmittedEvidencePassthrough(t *testing.T) {
	f := newT07Fixture()
	f.draft += "_（本节有 1 条证据因上下文预算未纳入：" + t07EvidenceID(2) + "。）_\n"
	inspection := f.inspect(t)
	if len(inspection.details.OmittedEvidence) != 1 || inspection.details.OmittedEvidence[0] != t07EvidenceID(2) {
		t.Fatalf("omitted evidence = %v, want [%s]", inspection.details.OmittedEvidence, t07EvidenceID(2))
	}
}

// ── Quality gate runner ─────────────────────────────────────────────────────

type t07ScriptedInner struct {
	called bool
}

func (inner *t07ScriptedInner) Run(context.Context, LegacyNodeInput) ([]LegacyPayload, LegacyUsage, error) {
	inner.called = true
	return []LegacyPayload{{OutputKey: "quality_report", ArtifactType: "quality_report",
		MediaType: "application/json", Body: []byte(`{"passed":true}`)}}, LegacyUsage{Measured: true}, nil
}

func t07QualityInput(evidenceReport []byte) LegacyNodeInput {
	return LegacyNodeInput{Payloads: map[writingplan.ArtifactType][][]byte{
		"evidence_report": {evidenceReport},
		"full_draft":      {[]byte("draft")}},
	}
}

func TestT07QualityGateBlocksOnBlockerIssue(t *testing.T) {
	inner := &t07ScriptedInner{}
	runner := ResearchQualityGateRunner{Inner: inner}
	report := validatorReport{Validator: "core.research.validate.citations", Mode: "structural",
		Scores: map[string]float64{}, Passed: false,
		Issues: []validatorIssue{{Severity: "blocker", Type: FindingInvalidCitation, Message: "orphaned marker"}}}
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = runner.Run(context.Background(), t07QualityInput(body))
	if err == nil {
		t.Fatal("quality gate accepted a blocker report")
	}
	if !strings.Contains(err.Error(), FindingInvalidCitation) {
		t.Fatalf("block error lost the finding: %v", err)
	}
	if inner.called {
		t.Fatal("inner quality runner ran behind a blocked report")
	}
}

func TestT07QualityGatePassesCleanReport(t *testing.T) {
	inner := &t07ScriptedInner{}
	runner := ResearchQualityGateRunner{Inner: inner}
	report := validatorReport{Validator: "core.research.validate.citations", Mode: "structural",
		Scores: map[string]float64{"citation_integrity": 1}, Passed: true, Issues: []validatorIssue{}}
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	payloads, _, err := runner.Run(context.Background(), t07QualityInput(body))
	if err != nil {
		t.Fatalf("clean report blocked: %v", err)
	}
	if !inner.called {
		t.Fatal("inner quality runner did not run on a clean report")
	}
	if len(payloads) == 0 || payloads[0].ArtifactType != "quality_report" {
		t.Fatalf("quality payloads = %+v", payloads)
	}
}

func TestT07QualityGateIgnoresLegacyValidatorReports(t *testing.T) {
	inner := &t07ScriptedInner{}
	runner := ResearchQualityGateRunner{Inner: inner}
	// The legacy evidence validator never carries research blockers; the gate
	// must not consume its report as one.
	report := validatorReport{Validator: "core.validation.evidence", Mode: "llm",
		Scores: map[string]float64{}, Passed: true,
		Issues: []validatorIssue{{Severity: "blocker", Type: "unsupported_claim", Message: "legacy finding"}}}
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := runner.Run(context.Background(), t07QualityInput(body)); err != nil {
		t.Fatalf("legacy report wrongly blocked: %v", err)
	}
	if !inner.called {
		t.Fatal("inner runner skipped for a legacy report")
	}
}

// ── Fact validator reviewer seam (consistent / inconsistent / unavailable) ──

type t07FakeReviewer struct {
	conclusion ResearchFactConclusion
	err        error
}

func (reviewer t07FakeReviewer) ReviewClaim(context.Context, ResearchFactClaimInput) (ResearchFactConclusion, error) {
	return reviewer.conclusion, reviewer.err
}

// t07ContentGateway is the in-memory content gateway backing the fact
// validator fixture: it serves the pack body by content hash.
type t07ContentGateway struct{ bodies map[string][]byte }

func (gateway t07ContentGateway) Load(_ context.Context, artifact InputArtifact) ([]byte, error) {
	body, ok := gateway.bodies[artifact.ContentHash]
	if !ok {
		return nil, fmt.Errorf("content %s not staged", artifact.ContentHash)
	}
	return body, nil
}

func (gateway t07ContentGateway) Stage(_ context.Context, _, _ string, body []byte) (string, string, error) {
	hash := contentHash(body)
	t07FactGatewayBodies[hash] = body
	return "artifact://" + hash, hash, nil
}

// t07DecodeArtifact decodes a staged validator artifact body via the gateway's
// record (OutputArtifactDraft carries the hash, not the bytes).
func t07DecodeArtifact(t *testing.T, draft OutputArtifactDraft) validatorReport {
	t.Helper()
	body, ok := t07FactGatewayBodies[draft.ContentHash]
	if !ok {
		t.Fatalf("artifact body %s not staged", draft.ContentHash)
	}
	var report validatorReport
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatal(err)
	}
	return report
}

// t07FactGatewayBodies records what the fact validator's gateway staged.
var t07FactGatewayBodies = map[string][]byte{}

// t07FactRequest builds a dispatch-valid request whose only input is the
// fixture's evidence pack.
func t07FactRequest() ExecutionRequest {
	packBody, _ := json.Marshal(newT07Fixture().pack)
	packHash := contentHash(packBody)
	node := writingplan.PlanNode{NodeID: "node_research_fact", Kind: writingplan.NodeValidate,
		Capability: "core.research.validate.fact", CapabilityVersion: "1.0.0",
		DependsOn:           []string{},
		InputArtifactTypes:  []writingplan.ArtifactType{"research_evidence_pack", "full_draft"},
		OutputArtifactTypes: []writingplan.ArtifactType{"fact_report"},
		Bounds:              writingplan.Bounds{MaxAttempts: 1, MaxConcurrency: 1, MaxItems: 1, MaxCostUSD: 1, TimeoutMS: 60000},
		FailurePath:         writingplan.FailurePause}
	key, err := writingstore.NodeAttemptKey("run_t07fact", node.NodeID, 1)
	if err != nil {
		panic(err)
	}
	draftHash := contentHash([]byte("t07-draft"))
	return ExecutionRequest{RunID: "run_t07fact", PlanID: "plan_t07fact", PlanVersion: 1,
		NodeID: node.NodeID, Attempt: 1, IdempotencyKey: key,
		ContractRef: writingplan.ObjectRef{ID: "ctr_t07", Version: 1, Hash: contentHash([]byte("t07-contract"))},
		Node:        node, Inputs: []InputArtifact{
			{ArtifactID: "art_t07_pack", Version: 1, ArtifactType: "research_evidence_pack",
				ContentHash: packHash, MediaType: "application/json", ContentRef: "artifact://" + packHash},
			{ArtifactID: "art_t07_draft", Version: 1, ArtifactType: "full_draft",
				ContentHash: draftHash, MediaType: "text/markdown", ContentRef: "artifact://" + draftHash}},
		Permissions: []writingplan.Permission{"validation.run"}}
}

func t07FactValidator(t *testing.T, reviewer ResearchFactReviewer) (ExecutionResult, error) {
	t.Helper()
	packBody, _ := json.Marshal(newT07Fixture().pack)
	gateway := t07ContentGateway{bodies: map[string][]byte{contentHash(packBody): packBody}}
	validator, err := NewResearchFactValidator(gateway, reviewer)
	if err != nil {
		t.Fatal(err)
	}
	return validator.Execute(context.Background(), t07FactRequest())
}

func TestT07FactReviewerConsistentStaysPending(t *testing.T) {
	result, err := t07FactValidator(t, t07FakeReviewer{conclusion: ResearchFactConclusion{Status: "pending", ModelRef: "fake@1", Note: "一致"}})
	if err != nil {
		t.Fatal(err)
	}
	report := t07DecodeArtifact(t, result.Artifacts[0])
	if report.Mode != "model_pending" {
		t.Fatalf("fact mode = %q, want model_pending", report.Mode)
	}
	for _, issue := range report.Issues {
		if issue.Type == "fact_pending" && !strings.Contains(issue.Message, "待人工复核") {
			t.Fatalf("pending conclusion lost its human-review note: %q", issue.Message)
		}
		if issue.Type == "fact_review_model" && !strings.Contains(issue.Message, "fake@1") {
			t.Fatalf("model identity missing from the report: %q", issue.Message)
		}
	}
}

func TestT07FactReviewerInconsistentFlagsHigh(t *testing.T) {
	result, err := t07FactValidator(t, t07FakeReviewer{conclusion: ResearchFactConclusion{Status: "inconsistent", ModelRef: "fake@1", Note: "矛盾"}})
	if err != nil {
		t.Fatal(err)
	}
	report := t07DecodeArtifact(t, result.Artifacts[0])
	high := false
	for _, issue := range report.Issues {
		if issue.Type == "fact_inconsistent" && issue.Severity == "high" {
			high = true
		}
	}
	if !high {
		t.Fatalf("inconsistent conclusion not flagged high: %+v", report.Issues)
	}
}

func TestT07FactReviewerUnavailableDegrades(t *testing.T) {
	result, err := t07FactValidator(t, t07FakeReviewer{err: context.DeadlineExceeded})
	if err != nil {
		t.Fatalf("reviewer failure must degrade, not fail the node: %v", err)
	}
	report := t07DecodeArtifact(t, result.Artifacts[0])
	if report.Mode != "degraded" {
		t.Fatalf("fact mode = %q, want degraded after a failed review", report.Mode)
	}
	skipped := false
	for _, issue := range report.Issues {
		if issue.Type == "review_degraded" || issue.Type == "review_skipped" {
			skipped = true
		}
	}
	if !skipped {
		t.Fatalf("degradation not recorded: %+v", report.Issues)
	}
}

func TestT07FactValidatorWithoutReviewerIsDegraded(t *testing.T) {
	result, err := t07FactValidator(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	report := t07DecodeArtifact(t, result.Artifacts[0])
	if report.Mode != "degraded" {
		t.Fatalf("no-reviewer fact mode = %q, want degraded", report.Mode)
	}
	if !report.Passed {
		t.Fatal("degraded fact report must not fail the node")
	}
}

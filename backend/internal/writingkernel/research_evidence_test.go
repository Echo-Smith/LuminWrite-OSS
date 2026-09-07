package writingkernel

import (
	"strings"
	"testing"
)

func testArtifactRef(id string) ArtifactRef {
	return ArtifactRef{ArtifactID: id, Version: 1, ContentHash: "sha256:" + strings.Repeat("1", 64)}
}

func testHash64(seed byte) string {
	hex := "0123456789abcdef"
	return "sha256:" + strings.Repeat(string(hex[seed%16]), 64)
}

func testPaperEvidence(paperID string) PaperEvidence {
	return PaperEvidence{
		PaperID: paperID,
		Bibliography: PaperBibliography{
			Title:   "Evidence Integrity Under Multilingual Offsets",
			Authors: []string{"A. Researcher"},
		},
		Origin:          PaperOriginExternal,
		SelectionReason: "relevant to the research question",
		RelevanceStatus: RelevanceScored,
		ReadingScope:    ReadingScopeFullText,
		DocumentRef:     refOf(testArtifactRef("art_doc_" + paperID)),
		ReadBlockIDs:    []string{"blk_1"},
		TotalBlocks:     4,
	}
}

func refOf(r ArtifactRef) *ArtifactRef { return &r }

func testEvidence(evidenceID, paperID string) Evidence {
	return Evidence{
		EvidenceID:    evidenceID,
		PaperID:       paperID,
		DocumentRef:   testArtifactRef("art_doc_" + paperID),
		BlockID:       "blk_1",
		BlockHash:     testHash64(2),
		Quote:         "quoted text",
		StartChar:     0,
		EndChar:       11,
		EvidenceScope: EvidenceScopeFullText,
	}
}

func testClaim(claimID, paperID string) Claim {
	return Claim{
		ClaimID:      claimID,
		PaperID:      paperID,
		Text:         "The offset invariant holds.",
		Kind:         ClaimKindSourceAssertion,
		EvidenceIDs:  []string{"ev_1"},
		ReviewStatus: ClaimReviewApproved,
		Limitations:  []string{},
	}
}

func testEvidencePack() ResearchEvidencePack {
	return ResearchEvidencePack{
		SchemaVersion: ResearchEvidencePackSchemaVersion,
		ContractHash:  testHash64(3),
		CandidatesRef: testArtifactRef("art_candidates"),
		Papers:        []PaperEvidence{testPaperEvidence("p_1")},
		Evidence:      []Evidence{testEvidence("ev_1", "p_1")},
		Claims:        []Claim{testClaim("claim_1", "p_1")},
		Coverage: EvidenceCoverage{
			Topics:         []string{"offset semantics"},
			Gaps:           []string{"scanned PDFs"},
			Contradictions: []string{},
		},
		Provenance: []string{"reader/1"},
	}
}

func TestEvidencePackValidateAcceptsWellFormed(t *testing.T) {
	pack := testEvidencePack()
	if err := pack.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestEvidencePackValidateRejections(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ResearchEvidencePack)
	}{
		{"bad schema version", func(p *ResearchEvidencePack) { p.SchemaVersion = "research-evidence-pack/2" }},
		{"bad contract hash", func(p *ResearchEvidencePack) { p.ContractHash = "sha256:zz" }},
		{"bad candidates ref", func(p *ResearchEvidencePack) { p.CandidatesRef.Version = 0 }},
		{"nil evidence", func(p *ResearchEvidencePack) { p.Evidence = nil }},
		{"duplicate evidence id", func(p *ResearchEvidencePack) {
			p.Evidence = append(p.Evidence, testEvidence("ev_1", "p_1"))
		}},
		{"duplicate paper id", func(p *ResearchEvidencePack) {
			p.Papers = append(p.Papers, testPaperEvidence("p_1"))
		}},
		{"evidence unknown paper", func(p *ResearchEvidencePack) {
			p.Evidence[0].PaperID = "p_missing"
		}},
		{"evidence on unread paper", func(p *ResearchEvidencePack) {
			p.Papers[0].ReadingScope = ReadingScopeUnread
			p.Papers[0].DocumentRef = nil
			p.Papers[0].ReadBlockIDs = []string{}
		}},
		{"full text evidence without full text reading", func(p *ResearchEvidencePack) {
			p.Papers[0].ReadingScope = ReadingScopeAbstract
		}},
		{"empty quote", func(p *ResearchEvidencePack) { p.Evidence[0].Quote = "  " }},
		{"inverted offsets", func(p *ResearchEvidencePack) { p.Evidence[0].StartChar = 5; p.Evidence[0].EndChar = 4 }},
		{"negative offset", func(p *ResearchEvidencePack) { p.Evidence[0].StartChar = -1 }},
		{"bad block hash", func(p *ResearchEvidencePack) { p.Evidence[0].BlockHash = "md5:abc" }},
		{"page zero", func(p *ResearchEvidencePack) { page := 0; p.Evidence[0].Page = &page }},
		{"bad evidence scope", func(p *ResearchEvidencePack) { p.Evidence[0].EvidenceScope = "page_image" }},
		{"user material without material ref", func(p *ResearchEvidencePack) {
			p.Papers[0].Origin = PaperOriginUserMaterial
			p.Papers[0].MaterialRef = nil
		}},
		{"read blocks exceed total", func(p *ResearchEvidencePack) { p.Papers[0].TotalBlocks = 0 }},
		{"unread paper with read blocks", func(p *ResearchEvidencePack) {
			p.Papers[0].ReadingScope = ReadingScopeUnread
			p.Papers[0].DocumentRef = nil
		}},
		{"source assertion without evidence", func(p *ResearchEvidencePack) { p.Claims[0].EvidenceIDs = []string{} }},
		{"source assertion cross-paper evidence", func(p *ResearchEvidencePack) {
			p.Papers = append(p.Papers, testPaperEvidence("p_2"))
			p.Claims[0].PaperID = "p_2"
		}},
		{"claim unknown evidence", func(p *ResearchEvidencePack) { p.Claims[0].EvidenceIDs = []string{"ev_missing"} }},
		{"claim unknown paper", func(p *ResearchEvidencePack) { p.Claims[0].PaperID = "p_missing" }},
		{"bad claim kind", func(p *ResearchEvidencePack) { p.Claims[0].Kind = "fact" }},
		{"bad review status", func(p *ResearchEvidencePack) { p.Claims[0].ReviewStatus = "auto_verified" }},
		{"contradiction unknown ref", func(p *ResearchEvidencePack) { p.Coverage.Contradictions = []string{"claim_missing"} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pack := testEvidencePack()
			test.mutate(&pack)
			if err := pack.Validate(); err == nil {
				t.Fatalf("expected %q to be rejected", test.name)
			}
		})
	}
}

func TestEvidencePackContradictionAcceptsClaimAndPaperRefs(t *testing.T) {
	pack := testEvidencePack()
	pack.Coverage.Contradictions = []string{"claim_1", "p_1"}
	if err := pack.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestEvidencePackInterpretationAllowsMultiEvidence(t *testing.T) {
	pack := testEvidencePack()
	pack.Papers = append(pack.Papers, testPaperEvidence("p_2"))
	pack.Evidence = append(pack.Evidence, testEvidence("ev_2", "p_2"))
	claim := testClaim("claim_2", "p_1")
	claim.Kind = ClaimKindInterpretation
	claim.EvidenceIDs = []string{"ev_1", "ev_2"}
	pack.Claims = append(pack.Claims, claim)
	if err := pack.Validate(); err != nil {
		t.Fatal(err)
	}
}

// VerifyEvidenceQuote uses Unicode code point offsets. The samples pin the
// semantics for CJK text, emoji (multi-byte and multi-code-point), and CRLF
// line endings: offsets count code points, not bytes, not graphemes.
func TestVerifyEvidenceQuoteCodePointOffsets(t *testing.T) {
	blockText := "中文摘录\r\nEmoji 🚀 start\nFamily 👨‍👩‍👧 and café\nEnd"
	runes := []rune(blockText)

	// CRLF spans two code points.
	crlfStart := strings.Index(blockText, "\r\n")
	crlfIndex := len([]rune(blockText[:crlfStart]))
	evidence := testEvidence("ev_crlf", "p_1")
	evidence.Quote = string(runes[crlfIndex : crlfIndex+2])
	evidence.StartChar = crlfIndex
	evidence.EndChar = crlfIndex + 2
	if !VerifyEvidenceQuote(evidence, blockText) {
		t.Fatal("CRLF quote must verify under code point offsets")
	}

	// One rocket emoji is a single code point (four UTF-8 bytes).
	rocketOffset := len([]rune(blockText[:strings.Index(blockText, "🚀")]))
	evidence = testEvidence("ev_emoji", "p_1")
	evidence.Quote = "🚀"
	evidence.StartChar = rocketOffset
	evidence.EndChar = rocketOffset + 1
	if !VerifyEvidenceQuote(evidence, blockText) {
		t.Fatal("single emoji quote must verify as exactly one code point")
	}

	// A ZWJ family emoji is one grapheme but five code points.
	familyOffset := len([]rune(blockText[:strings.Index(blockText, "👨‍👩‍👧")]))
	evidence = testEvidence("ev_zwj", "p_1")
	evidence.Quote = string(runes[familyOffset : familyOffset+5])
	evidence.StartChar = familyOffset
	evidence.EndChar = familyOffset + 5
	if !VerifyEvidenceQuote(evidence, blockText) {
		t.Fatal("ZWJ emoji must span five code points")
	}
	evidence.EndChar = familyOffset + 1
	if VerifyEvidenceQuote(evidence, blockText) {
		t.Fatal("grapheme-based end offset must not verify")
	}

	// Chinese excerpt spanning CJK characters.
	quote := string(runes[0:4])
	evidence = testEvidence("ev_zh", "p_1")
	evidence.Quote = quote
	evidence.StartChar = 0
	evidence.EndChar = 4
	if quote != "中文摘录" || !VerifyEvidenceQuote(evidence, blockText) {
		t.Fatalf("Chinese quote must verify: %q", quote)
	}

	// Byte-based slicing must NOT be accepted: a byte offset into a
	// multi-byte character produces invalid UTF-8 and fails the
	// exact-equality check against the code point slice.
	evidence = testEvidence("ev_bytes", "p_1")
	evidence.Quote = blockText[0:1] // first byte of 中 — invalid UTF-8
	evidence.StartChar = 0
	evidence.EndChar = 1
	if VerifyEvidenceQuote(evidence, blockText) {
		t.Fatal("byte-based offset must not verify")
	}

	// Out-of-range end offset is rejected.
	evidence = testEvidence("ev_range", "p_1")
	evidence.Quote = string(runes)
	evidence.StartChar = 0
	evidence.EndChar = len(runes) + 1
	if VerifyEvidenceQuote(evidence, blockText) {
		t.Fatal("out-of-range offset must not verify")
	}
}

func TestResearchOutlineValidate(t *testing.T) {
	outline := ResearchOutline{
		SchemaVersion:   ResearchOutlineSchemaVersion,
		ContractHash:    testHash64(3),
		EvidencePackRef: testArtifactRef("art_pack"),
		Sections: []OutlineSection{
			{SectionID: "sec_intro", Title: "引言", CentralPoint: "背景与动机", EvidenceIDs: []string{}, Gaps: []string{}},
			{SectionID: "sec_body", Title: "证据对比", CentralPoint: "两种结论的分歧", EvidenceIDs: []string{"ev_1"}, Gaps: []string{"缺扫描件"}},
		},
		Limitations: []string{"仅摘要覆盖的来源"},
	}
	if err := outline.Validate(); err != nil {
		t.Fatal(err)
	}
	pack := testEvidencePack()
	if err := ValidateOutlineAgainstPack(outline, pack); err != nil {
		t.Fatal(err)
	}

	bad := outline
	bad.Sections[1].EvidenceIDs = []string{"ev_missing"}
	if err := ValidateOutlineAgainstPack(bad, pack); err == nil {
		t.Fatal("outline must reject unknown evidence references")
	}

	wrongContract := outline
	wrongContract.ContractHash = testHash64(4)
	if err := ValidateOutlineAgainstPack(wrongContract, pack); err == nil {
		t.Fatal("outline must reject a different-contract pack")
	}

	dupSection := outline
	dupSection.Sections = append(dupSection.Sections, dupSection.Sections[0])
	if err := dupSection.Validate(); err == nil {
		t.Fatal("duplicate section_id must be rejected")
	}
}

func TestApprovedResearchOutlineRequiresRefs(t *testing.T) {
	outline := ResearchOutline{
		SchemaVersion:   ResearchOutlineSchemaVersion,
		ContractHash:    testHash64(3),
		EvidencePackRef: testArtifactRef("art_pack"),
		Sections:        []OutlineSection{{SectionID: "s1", Title: "t", CentralPoint: "c", EvidenceIDs: []string{}, Gaps: []string{}}},
		Limitations:     []string{},
	}
	approved := ApprovedResearchOutline{ResearchOutline: outline}
	if err := approved.Validate(); err == nil {
		t.Fatal("approved outline must require source and gate refs")
	}
	approved.SourceOutlineRef = testArtifactRef("art_outline_v1")
	approved.GateDecisionRef = testArtifactRef("art_gate_decision")
	if err := approved.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestEvidenceApprovalValidate(t *testing.T) {
	approval := EvidenceApproval{
		SchemaVersion:   EvidenceApprovalSchemaVersion,
		GateID:          "gate_evidence",
		ActorID:         "usr_1",
		DecidedAt:       "2026-09-07T00:00:00Z",
		PlanHash:        testHash64(5),
		EvidencePackRef: testArtifactRef("art_pack"),
		Decision:        "approve",
	}
	if err := approval.Validate(); err != nil {
		t.Fatal(err)
	}
	if approval.Decision = "reject"; approval.Validate() == nil {
		t.Fatal("only approve is accepted")
	}
	approval = EvidenceApproval{
		SchemaVersion: EvidenceApprovalSchemaVersion, GateID: "gate_evidence", ActorID: "usr_1",
		DecidedAt: "not-a-time", PlanHash: testHash64(5), EvidencePackRef: testArtifactRef("art_pack"), Decision: "approve",
	}
	if err := approval.Validate(); err == nil {
		t.Fatal("decided_at must be RFC3339")
	}
}

func TestResearchCitationIndexAgainstPack(t *testing.T) {
	pack := testEvidencePack()
	index := ResearchCitationIndex{
		SchemaVersion:   ResearchCitationIndexSchemaVersion,
		ContractHash:    pack.ContractHash,
		DraftHash:       testHash64(6),
		EvidencePackRef: testArtifactRef("art_pack"),
		Citations: []CitationEntry{
			{CitationID: "cite_1", EvidenceID: "ev_1", BlockID: "draft_blk_1", StartChar: 0, EndChar: 9},
		},
	}
	if err := ValidateCitationIndexAgainstPack(index, pack); err != nil {
		t.Fatal(err)
	}
	bad := index
	bad.Citations[0].EvidenceID = "ev_missing"
	if err := ValidateCitationIndexAgainstPack(bad, pack); err == nil {
		t.Fatal("citation index must reject unknown evidence")
	}
	bad = index
	bad.Citations[0].StartChar = 10
	bad.Citations[0].EndChar = 9
	if err := bad.Validate(); err == nil {
		t.Fatal("citation index must reject inverted intervals")
	}
}

func TestResearchCandidatesValidate(t *testing.T) {
	score := 2.5
	candidates := ResearchCandidates{
		SchemaVersion: ResearchCandidatesSchemaVersion,
		ContractHash:  testHash64(3),
		QueryPlan: []QueryPlanEntry{
			{QueryID: "q_1", Text: "unicode code point offsets in evidence contracts", Origin: QueryOriginOriginal},
		},
		ProviderResults: []ProviderResult{{Provider: "openalex", Status: "ok"}},
		Papers: []PaperCandidate{{
			PaperID:  "p_1",
			DOI:      refOfString("10.1234/example"),
			Title:    "Evidence Integrity",
			Authors:  []string{"A. Researcher"},
			Year:     refOfInt(2024),
			Venue:    refOfString("J. of Offsets"),
			Aliases:  []string{"provider-id-42"},
			Abstract: refOfString("We study offsets."),
			Selection: PaperSelection{
				Status: SelectionStatusSelected, Score: &score, Reason: "top ranked",
			},
			Acquisition:     PaperAcquisition{Status: AcquisitionFullTextAvailable},
			RelevanceStatus: RelevanceScored,
		}},
		PolicyVersion: "selection/1",
		Provenance:    []string{"discover/1"},
	}
	if err := candidates.Validate(); err != nil {
		t.Fatal(err)
	}

	unscoredFallback := candidates
	unscoredFallback.Papers[0].RelevanceStatus = RelevanceUnscored
	unscoredFallback.Papers[0].Selection.Score = nil
	if err := unscoredFallback.Validate(); err != nil {
		t.Fatalf("unscored papers must remain valid candidates: %v", err)
	}

	badScore := candidates
	over := 3.5
	badScore.Papers[0].Selection.Score = &over
	if err := badScore.Validate(); err == nil {
		t.Fatal("selection score must stay within 0..3")
	}

	badOrigin := candidates
	badOrigin.QueryPlan[0].Origin = "rewritten"
	if err := badOrigin.Validate(); err == nil {
		t.Fatal("query origin enum must be enforced")
	}

	dupPaper := candidates
	dupPaper.Papers = append(dupPaper.Papers, dupPaper.Papers[0])
	if err := dupPaper.Validate(); err == nil {
		t.Fatal("duplicate paper_id must be rejected")
	}
}

func refOfString(v string) *string { return &v }
func refOfInt(v int) *int          { return &v }

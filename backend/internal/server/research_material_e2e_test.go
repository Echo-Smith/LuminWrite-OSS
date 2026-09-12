package server

// F5 user-material research path E2E (docs/reviews/2026-09-08-research-review.md):
// a research_review contract that FORBIDS external research compiles the
// material-discovery branch and completes the whole chain — evidence gate →
// outline gate → draft/finalize — from the owner's uploaded papers alone.
// The fake scholar worker's call counts prove the boundary: discover, rank,
// and fetch_full_text stay at ZERO for the entire run (R02: 只读取用户授权
// 材料); only parse/read may consume the locally staged material bytes.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
)

func TestF5UserMaterialOnlyResearchChainWithoutExternalResearch(t *testing.T) {
	h := newT06E2EHarness(t, 0, 0) // zero external papers: discover must never be called anyway
	fixture := h.fixtureMutate(t, func(contract *writingkernel.WritingContract) {
		contract.MaterialPolicy.AllowExternalResearch = false
	})
	// The two owner papers (fixture content) become the run's material
	// manifest: kb-backed user materials referenced from the document
	// metadata, resolved server-side into a structured snapshot.
	h.seedUserMaterials(t, fixture.documentID)
	h.userMaterials = true
	envelope := h.buildResearchEnvelope(t, fixture)
	runID := h.createResearchRun(t, fixture, envelope)

	// The whole chain over HTTP: material discover (no external calls) →
	// read (local bytes → parse → read) → evidence gate → outline gate →
	// draft → citations → fact → quality → finalize.
	evidenceGate := h.advanceToGate(t, runID, "evidence")
	if phase := h.researchPhase(t, runID); phase != "gate_evidence" {
		t.Fatalf("research phase = %q, want gate_evidence", phase)
	}
	h.decideGate(t, runID, evidenceGate, envelope)
	outlineGate := h.advanceToGate(t, runID, "outline")
	h.decideGate(t, runID, outlineGate, envelope)
	if status := h.driveToTerminal(t, runID, 3); status != "completed" {
		t.Fatalf("run ended as %q, want completed on the user-material path", status)
	}

	// Call-count assertions: the worker only ever parsed and read local
	// material bytes. discover / rank / fetch_full_text stayed at zero.
	discover, ranks, fetches, reads := h.worker.callCounts()
	if discover != 0 || ranks != 0 {
		t.Fatalf("external discovery calls: discover=%d rank=%d, want 0/0", discover, ranks)
	}
	if fetches != 0 {
		t.Fatalf("fetch_full_text calls = %d, want 0 (user materials never download)", fetches)
	}
	if reads != 2 {
		t.Fatalf("worker reads = %d, want 2 (both materials parsed and read)", reads)
	}

	// The frozen candidates are the material-only projection.
	ctx := context.Background()
	var candidatesHash string
	artifacts, err := h.store.ListRunArtifacts(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, artifact := range artifacts {
		counts[artifact.ArtifactType]++
		if artifact.ArtifactType == "research_candidates" {
			candidatesHash = artifact.ContentHash
		}
	}
	_, candidatesBody, err := h.store.GetArtifactContent(ctx, candidatesHash)
	if err != nil {
		t.Fatal(err)
	}
	var candidates writingkernel.ResearchCandidates
	if err := json.Unmarshal(candidatesBody, &candidates); err != nil {
		t.Fatal(err)
	}
	if err := candidates.Validate(); err != nil {
		t.Fatalf("material candidates invalid: %v", err)
	}
	if len(candidates.Papers) != 2 || len(candidates.QueryPlan) != 0 || len(candidates.ProviderResults) != 0 {
		t.Fatalf("candidates shape: papers=%d queries=%d providers=%d",
			len(candidates.Papers), len(candidates.QueryPlan), len(candidates.ProviderResults))
	}
	for _, paper := range candidates.Papers {
		if paper.Origin != writingkernel.PaperOriginUserMaterial || paper.MaterialRef == nil ||
			paper.Selection.Reason != "user_material" || paper.RelevanceStatus != writingkernel.RelevanceUnscored {
			t.Fatalf("candidate %s = origin %q reason %q relevance %q",
				paper.PaperID, paper.Origin, paper.Selection.Reason, paper.RelevanceStatus)
		}
	}
	// The frozen pack: both materials full-text read with owner-authorized
	// material refs and verified evidence.
	pack := h.loadEvidencePack(t, runID)
	if len(pack.Papers) != 2 || len(pack.Evidence) < 2 {
		t.Fatalf("pack shape: papers=%d evidence=%d", len(pack.Papers), len(pack.Evidence))
	}
	for _, paper := range pack.Papers {
		if paper.Origin != writingkernel.PaperOriginUserMaterial || paper.MaterialRef == nil {
			t.Fatalf("pack paper %s origin=%q material_ref=%v", paper.PaperID, paper.Origin, paper.MaterialRef)
		}
		if paper.ReadingScope != writingkernel.ReadingScopeFullText {
			t.Fatalf("pack paper %s scope = %q, want full_text", paper.PaperID, paper.ReadingScope)
		}
	}
	// The formal deliverable exists (the evidence gate confirmed the
	// material-only corpus and the chain finalized).
	for _, artifactType := range []string{"full_draft", "research_citation_index", "revision_set"} {
		if counts[artifactType] == 0 {
			t.Fatalf("run artifacts lack %s (have %#v)", artifactType, counts)
		}
	}
	// Call counts stayed frozen through the whole tail of the chain.
	time.Sleep(200 * time.Millisecond)
	discover2, ranks2, fetches2, reads2 := h.worker.callCounts()
	if discover2 != 0 || ranks2 != 0 || fetches2 != 0 || reads2 != 2 {
		t.Fatalf("post-completion call counts drifted: discover=%d rank=%d fetch=%d read=%d",
			discover2, ranks2, fetches2, reads2)
	}
}

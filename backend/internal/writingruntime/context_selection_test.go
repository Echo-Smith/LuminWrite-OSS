package writingruntime

import (
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// Unit coverage for the WP1/WP2 evidence-view selection: the view must
// follow the node's resolved input references and its transitive plan
// dependencies — not just the node's own artifacts.

func packArtifact(id, nodeID string, version int) writingstore.ArtifactRecord {
	return writingstore.ArtifactRecord{ArtifactID: id, Version: version, RunID: "run_x",
		PlanID: "plan_x", PlanVersion: 1, NodeID: nodeID, Attempt: 1,
		OutputKey: "research_evidence_pack", ArtifactType: "research_evidence_pack",
		ContentHash: "sha256:" + id, MediaType: "application/json", ContentRef: "artifact://x",
		Producer: "engine.step.research_read", CapabilityVersion: "1.0.0"}
}

// researchClosurePlan mirrors the tpl_research_review_v1 spine around the
// pack: read produces the pack, draft consumes it directly, citations and
// quality do NOT declare it as an input but reach it through dependencies.
func researchClosurePlan() *writingplan.ExecutablePlan {
	node := func(id string, deps []string, inputs []writingplan.ArtifactType) writingplan.PlanNode {
		return writingplan.PlanNode{NodeID: id, Kind: writingplan.NodeAction,
			Capability: "core.research.test", CapabilityVersion: "1.0.0",
			DependsOn: deps, InputArtifactTypes: inputs,
			OutputArtifactTypes: []writingplan.ArtifactType{"research_evidence_pack"},
			Bounds:              writingplan.Bounds{MaxAttempts: 1, MaxConcurrency: 1, MaxItems: 1, MaxCostUSD: 5, TimeoutMS: 60000},
			FailurePath:         writingplan.FailurePause}
	}
	return &writingplan.ExecutablePlan{PlanID: "plan_x", RootNodeID: "node_read", Nodes: []writingplan.PlanNode{
		node("node_read", nil, []writingplan.ArtifactType{"contract"}),
		node("node_draft", []string{"node_read"},
			[]writingplan.ArtifactType{"contract", "research_evidence_pack", "evidence_approval", "approved_research_outline"}),
		node("node_citations", []string{"node_read", "node_draft"},
			[]writingplan.ArtifactType{"research_evidence_pack", "full_draft", "research_citation_index"}),
		node("node_quality", []string{"node_draft", "node_citations"},
			[]writingplan.ArtifactType{"full_draft", "evidence_report", "fact_report"}),
	}}
}

func TestSelectEvidenceArtifactsFollowsInputReferences(t *testing.T) {
	pack := packArtifact("art_pack", "node_read", 1)
	draftOwn := writingstore.ArtifactRecord{ArtifactID: "art_own", Version: 1, RunID: "run_x",
		NodeID: "node_draft", OutputKey: "full_draft", ArtifactType: "full_draft",
		ContentHash: "sha256:own", MediaType: "text/markdown", ContentRef: "artifact://own",
		Producer: "engine.step.research_draft", CapabilityVersion: "1.0.0"}
	all := []writingstore.ArtifactRecord{pack, draftOwn}

	draft := researchClosurePlan().Nodes[1]
	selected := selectEvidenceArtifacts(all, draft, researchClosurePlan())
	if len(selected) != 2 {
		t.Fatalf("draft selection = %#v, want pack + own artifact", selected)
	}
	if !containsArtifact(selected, "art_pack") || !containsArtifact(selected, "art_own") {
		t.Fatalf("draft selection missing pack or own artifact: %#v", selected)
	}
}

func TestSelectEvidenceArtifactsReachesPackThroughDependencyClosure(t *testing.T) {
	pack := packArtifact("art_pack", "node_read", 1)
	report := writingstore.ArtifactRecord{ArtifactID: "art_report", Version: 1, RunID: "run_x",
		NodeID: "node_citations", OutputKey: "evidence_report", ArtifactType: "evidence_report",
		ContentHash: "sha256:report", MediaType: "application/json", ContentRef: "artifact://report",
		Producer: "engine.step.research_validate_citation", CapabilityVersion: "1.0.0"}
	all := []writingstore.ArtifactRecord{pack, report}

	// Quality declares neither research_evidence_pack nor its own products as
	// inputs here — only the transitive plan closure (quality → citations →
	// read) brings the pack into the view.
	quality := researchClosurePlan().Nodes[3]
	selected := selectEvidenceArtifacts(all, quality, researchClosurePlan())
	if !containsArtifact(selected, "art_pack") {
		t.Fatalf("quality selection lost upstream pack through dependency closure: %#v", selected)
	}
	if !containsArtifact(selected, "art_report") {
		t.Fatalf("quality selection lost direct dependency product: %#v", selected)
	}
	if len(selected) != 2 {
		t.Fatalf("quality selection = %#v", selected)
	}
}

func TestSelectEvidenceArtifactsPrefersLatestInputVersion(t *testing.T) {
	packV1 := packArtifact("art_pack_v1", "node_read", 1)
	packV2 := packArtifact("art_pack_v2", "node_read", 2)
	all := []writingstore.ArtifactRecord{packV1, packV2}

	draft := researchClosurePlan().Nodes[1]
	selected := selectEvidenceArtifacts(all, draft, researchClosurePlan())
	// Input references resolve latest-version-per-type (selectInputs rule):
	// the stale v1 row must not enter the view twice or at all.
	if !containsArtifact(selected, "art_pack_v2") {
		t.Fatalf("latest pack version missing: %#v", selected)
	}
	if containsArtifact(selected, "art_pack_v1") {
		t.Fatalf("stale pack version leaked into the view: %#v", selected)
	}
}

func TestSelectEvidenceArtifactsDegradesWithoutPlan(t *testing.T) {
	pack := packArtifact("art_pack", "node_read", 1)
	all := []writingstore.ArtifactRecord{pack}

	// Without the plan the dependency products contribute nothing; the
	// input-referenced pack still reaches the draft view (nil plan is the
	// loadEvidencePlan degradation path).
	draft := researchClosurePlan().Nodes[1]
	if selected := selectEvidenceArtifacts(all, draft, nil); !containsArtifact(selected, "art_pack") {
		t.Fatalf("input-referenced pack lost without a plan: %#v", selected)
	}
	// Quality without a plan has no path to the pack: the view degrades to
	// empty rather than failing.
	quality := researchClosurePlan().Nodes[3]
	if selected := selectEvidenceArtifacts(all, quality, nil); len(selected) != 0 {
		t.Fatalf("quality selection without plan = %#v, want empty", selected)
	}
}

func TestSelectEvidenceArtifactsIsDeterministicAndDeduped(t *testing.T) {
	// An artifact that is simultaneously own + input-referenced + dependency
	// product appears exactly once, in stable list order.
	pack := packArtifact("art_pack", "node_draft", 1)
	all := []writingstore.ArtifactRecord{pack}
	draft := writingplan.PlanNode{NodeID: "node_draft", DependsOn: []string{"node_draft"},
		InputArtifactTypes: []writingplan.ArtifactType{"research_evidence_pack"}}
	first := selectEvidenceArtifacts(all, draft, researchClosurePlan())
	second := selectEvidenceArtifacts(all, draft, researchClosurePlan())
	if len(first) != 1 || first[0].ArtifactID != "art_pack" {
		t.Fatalf("selection = %#v, want the pack exactly once", first)
	}
	if len(first) != len(second) || first[0].ArtifactID != second[0].ArtifactID || first[0].Version != second[0].Version {
		t.Fatalf("selection not deterministic: %#v vs %#v", first, second)
	}
}

func containsArtifact(artifacts []writingstore.ArtifactRecord, id string) bool {
	for _, art := range artifacts {
		if art.ArtifactID == id {
			return true
		}
	}
	return false
}

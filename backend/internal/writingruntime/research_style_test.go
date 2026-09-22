package writingruntime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// styleCapturingGenerator records the input the executor passed down, so a
// test can assert the resolved style profile (or its deliberate absence).
type styleCapturingGenerator struct {
	input ResearchDraftInput
}

func (generator *styleCapturingGenerator) GenerateResearchDraft(_ context.Context, input ResearchDraftInput) (ResearchDraftOutput, error) {
	generator.input = input
	text := map[string]string{}
	for _, section := range input.Sections {
		var builder strings.Builder
		builder.WriteString(section.Title + "一节按论点展开：" + section.CentralPoint + " ")
		for _, evidence := range section.Evidence {
			builder.WriteString("证据表明相关结论 [@" + evidence.EvidenceID + "]。")
		}
		text[section.SectionID] = builder.String()
	}
	return ResearchDraftOutput{SectionText: text, ModelRef: "capturing"}, nil
}

type stubStyleResolver struct {
	profile *profile.StyleProfile
	err     error
}

func (resolver stubStyleResolver) ResolveProfile(string, string) (*profile.StyleProfile, error) {
	return resolver.profile, resolver.err
}

func researchStyleName(name string) *profile.StyleProfile {
	return &profile.StyleProfile{Slug: "yinyue", Name: name, Description: "温和叙事", SystemPrompt: "以讲故事的方式组织段落。"}
}

// The style instructions must carry the profile fields and must rank facts,
// marker discipline and the JSON contract above the style.
func TestResearchStyleInstructionRanksFactsAboveStyle(t *testing.T) {
	instruction := researchStyleInstruction(researchStyleName("印月三谈"))
	if !strings.Contains(instruction, "印月三谈") {
		t.Fatalf("style instruction lacks the profile name: %q", instruction)
	}
	if !strings.Contains(instruction, "风格说明：温和叙事") || !strings.Contains(instruction, "风格要点：以讲故事") {
		t.Fatalf("style instruction lacks description/system prompt: %q", instruction)
	}
	if !strings.Contains(instruction, "事实与证据归属 > 引用标记纪律 > JSON 输出结构 > 文风") {
		t.Fatalf("style instruction lacks the priority ladder: %q", instruction)
	}
}

// Full executor drive over the T06 fixture: opted-in slug resolves the
// profile into the generator input; empty slug and resolver failure both
// degrade to nil (neutral voice) without pausing the node.
func TestResearchDraftStyleOptInResolution(t *testing.T) {
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
	approvedInput := fixture.stageAsInput(t, writingstore.StableID("art_", fixture.runID, "approved"), "approved_research_outline", mustJSON(t, approved))

	capturing := &styleCapturingGenerator{}
	stub := stubStyleResolver{profile: researchStyleName("印月三谈")}
	executor, err := NewResearchDraftExecutor(fixture.gateway, capturing, stub)
	if err != nil {
		t.Fatal(err)
	}
	request := fixture.requestFor(t, fixture.draftNode(), []InputArtifact{fixture.contractInput(t), packInput, approvalInput, approvedInput})

	// 1) Non-empty slug + resolving resolver → profile flows into the input.
	request.StyleSlug = "yinyue"
	if _, err := executor.Execute(ctx, request); err != nil {
		t.Fatalf("draft execute with style: %v", err)
	}
	if capturing.input.StyleProfile == nil || capturing.input.StyleProfile.Name != "印月三谈" {
		t.Fatalf("expected resolved style profile, got %+v", capturing.input.StyleProfile)
	}
	if capturing.input.StyleSlug != "yinyue" {
		t.Fatalf("expected style slug passthrough, got %q", capturing.input.StyleSlug)
	}

	// 2) Empty slug → nil profile (neutral voice).
	capturing.input = ResearchDraftInput{}
	request.StyleSlug = ""
	if _, err := executor.Execute(ctx, request); err != nil {
		t.Fatalf("draft execute without style: %v", err)
	}
	if capturing.input.StyleProfile != nil {
		t.Fatalf("expected nil profile for empty slug, got %+v", capturing.input.StyleProfile)
	}

	// 3) Resolver failure → nil profile; the node degrades, never pauses.
	failingExecutor, err := NewResearchDraftExecutor(fixture.gateway, capturing, stubStyleResolver{err: context.DeadlineExceeded})
	if err != nil {
		t.Fatal(err)
	}
	request.StyleSlug = "yinyue"
	if _, err := failingExecutor.Execute(ctx, request); err != nil {
		t.Fatalf("resolver failure must degrade, not fail the node: %v", err)
	}
	if capturing.input.StyleProfile != nil {
		t.Fatalf("expected nil profile on resolver failure, got %+v", capturing.input.StyleProfile)
	}
}

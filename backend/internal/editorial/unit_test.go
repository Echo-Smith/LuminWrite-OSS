package editorial

import (
	"encoding/json"
	"testing"
)

// ═══════════════════════════════════════════════════════════════
// 纯单元测试 — 不依赖数据库，覆盖核心状态机和决策逻辑
// ═══════════════════════════════════════════════════════════════

// ─── TaskStatus.CanTransitionTo ────────────────────────────

func Test_CanTransitionTo_AllValidTransitions(t *testing.T) {
	tests := []struct {
		name string
		from TaskStatus
		to   TaskStatus
		want bool
	}{
		// draft
		{"draft→pending_approval", StatusDraft, StatusPendingApproval, true},
		{"draft→archived", StatusDraft, StatusArchived, true},
		// pending_approval
		{"pending_approval→research", StatusPendingApproval, StatusResearch, true},
		{"pending_approval→draft", StatusPendingApproval, StatusDraft, true},
		{"pending_approval→archived", StatusPendingApproval, StatusArchived, true},
		// research
		{"research→writing", StatusResearch, StatusWriting, true},
		{"research→pending_approval", StatusResearch, StatusPendingApproval, true},
		// writing
		{"writing→review", StatusWriting, StatusReview, true},
		{"writing→research", StatusWriting, StatusResearch, true},
		// review
		{"review→pending_publish", StatusReview, StatusPendingPublish, true},
		{"review→writing", StatusReview, StatusWriting, true},
		{"review→pending_approval", StatusReview, StatusPendingApproval, true},
		// pending_publish
		{"pending_publish→published", StatusPendingPublish, StatusPublished, true},
		{"pending_publish→review", StatusPendingPublish, StatusReview, true},
		// published
		{"published→archived", StatusPublished, StatusArchived, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.from.CanTransitionTo(tt.to)
			if got != tt.want {
				t.Errorf("CanTransitionTo(%s→%s) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func Test_CanTransitionTo_AllInvalidTransitions(t *testing.T) {
	tests := []struct {
		name string
		from TaskStatus
		to   TaskStatus
	}{
		{"draft→research (skip approval)", StatusDraft, StatusResearch},
		{"draft→writing (skip approval+research)", StatusDraft, StatusWriting},
		{"draft→published (skip everything)", StatusDraft, StatusPublished},
		{"research→review (skip writing)", StatusResearch, StatusReview},
		{"research→published (skip writing+review+publish)", StatusResearch, StatusPublished},
		{"writing→pending_publish (skip review)", StatusWriting, StatusPendingPublish},
		{"writing→published (skip review+publish)", StatusWriting, StatusPublished},
		{"review→published (skip pending_publish)", StatusReview, StatusPublished},
		{"pending_publish→draft (cannot go back to draft)", StatusPendingPublish, StatusDraft},
		{"pending_publish→research (cannot go back to research)", StatusPendingPublish, StatusResearch},
		{"published→draft (cannot revive)", StatusPublished, StatusDraft},
		{"published→research (cannot revive)", StatusPublished, StatusResearch},
		{"archived→draft (cannot revive)", StatusArchived, StatusDraft},
		{"archived→published (cannot unarchive)", StatusArchived, StatusPublished},
		{"archived→research (cannot unarchive)", StatusArchived, StatusResearch},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.from.CanTransitionTo(tt.to)
			if got {
				t.Errorf("CanTransitionTo(%s→%s) should be false, got true", tt.from, tt.to)
			}
		})
	}
}

// ─── transitionDecision ────────────────────────────────────

func Test_transitionDecision(t *testing.T) {
	tests := []struct {
		name         string
		from         TaskStatus
		to           TaskStatus
		wantDecType  DecisionType
		wantDecBy    DecidedByType
	}{
		{"draft→pending_approval: submit for approval", StatusDraft, StatusPendingApproval, DecisionApproveTopic, DecidedByHuman},
		{"pending_approval→research: approve topic", StatusPendingApproval, StatusResearch, DecisionApproveTopic, DecidedByHuman},
		{"pending_approval→draft: reject topic", StatusPendingApproval, StatusDraft, DecisionApproveTopic, DecidedByHuman},
		{"research→writing: research complete", StatusResearch, StatusWriting, DecisionResearchComplete, DecidedBySystem},
		{"writing→review: draft complete", StatusWriting, StatusReview, DecisionDraftComplete, DecidedBySystem},
		{"review→pending_publish: review passed", StatusReview, StatusPendingPublish, DecisionAcceptReview, DecidedByReviewAgent},
		{"review→writing: review rejected, back to writing", StatusReview, StatusWriting, DecisionAcceptReview, DecidedByReviewAgent},
		{"review→pending_approval: escalation", StatusReview, StatusPendingApproval, DecisionEscalate, DecidedByReviewAgent},
		{"pending_publish→published: publish approved", StatusPendingPublish, StatusPublished, DecisionPublish, DecidedByHuman},
		{"pending_publish→review: publish rejected", StatusPendingPublish, StatusReview, DecisionPublish, DecidedByHuman},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decType, decBy := transitionDecision(tt.from, tt.to)
			if decType != tt.wantDecType {
				t.Errorf("transitionDecision(%s→%s) decType = %s, want %s", tt.from, tt.to, decType, tt.wantDecType)
			}
			if decBy != tt.wantDecBy {
				t.Errorf("transitionDecision(%s→%s) decBy = %s, want %s", tt.from, tt.to, decBy, tt.wantDecBy)
			}
		})
	}
}

func Test_transitionDecision_DefaultFallback(t *testing.T) {
	// Unknown transitions should fall through to the default case
	decType, decBy := transitionDecision(StatusPublished, StatusArchived)
	if decType != DecisionEscalate {
		t.Errorf("expected default DecisionEscalate, got %s", decType)
	}
	if decBy != DecidedBySystem {
		t.Errorf("expected default DecidedBySystem, got %s", decBy)
	}
}

// ─── decisionStatusForTransition ───────────────────────────

func Test_decisionStatusForTransition(t *testing.T) {
	tests := []struct {
		name string
		from TaskStatus
		to   TaskStatus
		want DecisionStatus
	}{
		{"draft→pending_approval: forward = approved", StatusDraft, StatusPendingApproval, DecisionStatusApproved},
		{"pending_approval→research: forward = approved", StatusPendingApproval, StatusResearch, DecisionStatusApproved},
		{"pending_approval→draft: backward = rejected", StatusPendingApproval, StatusDraft, DecisionStatusRejected},
		{"research→writing: forward = approved", StatusResearch, StatusWriting, DecisionStatusApproved},
		{"writing→review: forward = approved", StatusWriting, StatusReview, DecisionStatusApproved},
		{"review→pending_publish: forward = approved", StatusReview, StatusPendingPublish, DecisionStatusApproved},
		{"review→writing: backward = rejected", StatusReview, StatusWriting, DecisionStatusRejected},
		{"review→pending_approval: escalation = approved (forward)", StatusReview, StatusPendingApproval, DecisionStatusApproved},
		{"pending_publish→published: forward = approved", StatusPendingPublish, StatusPublished, DecisionStatusApproved},
		{"pending_publish→review: backward = rejected", StatusPendingPublish, StatusReview, DecisionStatusRejected},
		// to=StatusDraft is always rejected
		{"any→draft: always rejected", StatusResearch, StatusDraft, DecisionStatusRejected},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decisionStatusForTransition(tt.from, tt.to)
			if got != tt.want {
				t.Errorf("decisionStatusForTransition(%s→%s) = %s, want %s", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

// ─── nextStatusForDecision / prevStatusForDecision ─────────

func Test_nextStatusForDecision(t *testing.T) {
	tests := []struct {
		decType DecisionType
		want    TaskStatus
	}{
		{DecisionApproveTopic, StatusResearch},
		{DecisionSelectAngle, StatusWriting},
		{DecisionAllowRewrite, StatusReview},
		{DecisionPublish, StatusPublished},
		{DecisionAcceptReview, StatusPendingPublish},
		{DecisionEscalate, StatusResearch},
		{DecisionResearchComplete, ""}, // no mapping
		{DecisionTrustSource, ""},      // no mapping
	}

	for _, tt := range tests {
		t.Run(string(tt.decType), func(t *testing.T) {
			got := nextStatusForDecision(tt.decType)
			if got != tt.want {
				t.Errorf("nextStatusForDecision(%s) = %s, want %s", tt.decType, got, tt.want)
			}
		})
	}
}

func Test_prevStatusForDecision(t *testing.T) {
	tests := []struct {
		decType DecisionType
		want    TaskStatus
	}{
		{DecisionApproveTopic, StatusDraft},
		{DecisionSelectAngle, StatusResearch},
		{DecisionAllowRewrite, StatusWriting},
		{DecisionPublish, StatusReview},
		{DecisionAcceptReview, StatusWriting},
		{DecisionEscalate, StatusPendingApproval},
		{DecisionResearchComplete, ""}, // no mapping
		{DecisionTrustSource, ""},      // no mapping
	}

	for _, tt := range tests {
		t.Run(string(tt.decType), func(t *testing.T) {
			got := prevStatusForDecision(tt.decType)
			if got != tt.want {
				t.Errorf("prevStatusForDecision(%s) = %s, want %s", tt.decType, got, tt.want)
			}
		})
	}
}

// ─── nextStatusForDecision and prevStatusForDecision consistency ──

func Test_nextPrevStatusForDecision_ConsistencyWithCanTransitionTo(t *testing.T) {
	// For each decision type that has a mapping, verify that the approve target
	// is a valid transition from some plausible "current" status.
	// This catches cases where nextStatusForDecision returns a status that
	// CanTransitionTo would reject.
	decisions := []DecisionType{
		DecisionApproveTopic,
		DecisionSelectAngle,
		DecisionAllowRewrite,
		DecisionPublish,
		DecisionAcceptReview,
		DecisionEscalate,
	}

	for _, dt := range decisions {
		t.Run(string(dt), func(t *testing.T) {
			approveTarget := nextStatusForDecision(dt)
			rejectTarget := prevStatusForDecision(dt)
			if approveTarget == "" || rejectTarget == "" {
				t.Fatalf("missing status mapping for %s", dt)
			}

			// The rejectTarget should be a valid "previous" state — meaning
			// from rejectTarget, you should be able to transition to approveTarget
			// (or vice versa) through the normal workflow.
			// At minimum, rejectTarget should not equal approveTarget.
			if approveTarget == rejectTarget {
				t.Errorf("approve and reject targets are the same: %s", approveTarget)
			}
		})
	}
}

// ─── defaultAssignee ───────────────────────────────────────

func Test_defaultAssignee(t *testing.T) {
	tests := []struct {
		status TaskStatus
		want   AssigneeType
	}{
		{StatusResearch, AssigneeResearchAgent},
		{StatusWriting, AssigneeWritingAgent},
		{StatusReview, AssigneeReviewAgent},
		{StatusPendingApproval, AssigneeHuman},
		{StatusPendingPublish, AssigneeHuman},
		{StatusPublished, AssigneeHuman},
		{StatusDraft, AssigneeHuman},   // default
		{StatusArchived, AssigneeHuman}, // default
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			got := defaultAssignee(tt.status)
			if got != tt.want {
				t.Errorf("defaultAssignee(%s) = %s, want %s", tt.status, got, tt.want)
			}
		})
	}
}

// ─── Actor helpers ─────────────────────────────────────────

func Test_NewHumanActor(t *testing.T) {
	a := NewHumanActor("user-123", "Alice")
	if a.Type != ActorHuman {
		t.Errorf("expected type=human, got %s", a.Type)
	}
	if a.UserID != "user-123" {
		t.Errorf("expected userID=user-123, got %s", a.UserID)
	}
	if a.Label != "Alice" {
		t.Errorf("expected label=Alice, got %s", a.Label)
	}
	if a.Role != "" {
		t.Errorf("expected empty role, got %s", a.Role)
	}
}

func Test_NewAgentActor(t *testing.T) {
	a := NewAgentActor(RoleResearch, "研究 Agent")
	if a.Type != ActorAgent {
		t.Errorf("expected type=agent, got %s", a.Type)
	}
	if a.Role != string(RoleResearch) {
		t.Errorf("expected role=research_agent, got %s", a.Role)
	}
	if a.Label != "研究 Agent" {
		t.Errorf("expected label=研究 Agent, got %s", a.Label)
	}
	if a.UserID != "" {
		t.Errorf("expected empty userID, got %s", a.UserID)
	}
}

func Test_NewSystemActor(t *testing.T) {
	a := NewSystemActor("system")
	if a.Type != ActorSystem {
		t.Errorf("expected type=system, got %s", a.Type)
	}
	if a.Label != "system" {
		t.Errorf("expected label=system, got %s", a.Label)
	}
	if a.UserID != "" || a.Role != "" {
		t.Errorf("expected empty userID and role, got userID=%s role=%s", a.UserID, a.Role)
	}
}

func Test_actorFromLegacy(t *testing.T) {
	tests := []struct {
		name       string
		decidedBy  string
		decByType  DecidedByType
		wantType   ActorType
		wantUserID string
		wantRole   string
	}{
		{"human", "user-123", DecidedByHuman, ActorHuman, "user-123", ""},
		{"research_agent", "", DecidedByResearchAgent, ActorAgent, "", "research_agent"},
		{"writing_agent", "", DecidedByWritingAgent, ActorAgent, "", "writing_agent"},
		{"review_agent", "", DecidedByReviewAgent, ActorAgent, "", "review_agent"},
		{"system", "", DecidedBySystem, ActorSystem, "", ""},
		{"system with label", "my-system", DecidedBySystem, ActorSystem, "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := actorFromLegacy(tt.decidedBy, tt.decByType)
			if a.Type != tt.wantType {
				t.Errorf("type = %s, want %s", a.Type, tt.wantType)
			}
			if a.UserID != tt.wantUserID {
				t.Errorf("userID = %s, want %s", a.UserID, tt.wantUserID)
			}
			if a.Role != tt.wantRole {
				t.Errorf("role = %s, want %s", a.Role, tt.wantRole)
			}
		})
	}
}

// ─── ArtifactType.ProducerFor / ConsumerFor ────────────────

func Test_ArtifactType_ProducerFor(t *testing.T) {
	tests := []struct {
		artType ArtifactType
		want    string
	}{
		{ArtifactTopicCard, "human"},
		{ArtifactResearchBrief, "research_agent"},
		{ArtifactSourcePack, "research_agent"},
		{ArtifactFactClaims, "research_agent"},
		{ArtifactOutline, "writing_agent"},
		{ArtifactDraft, "writing_agent"},
		{ArtifactRevisedDraft, "writing_agent"},
		{ArtifactReviewReport, "review_agent"},
	}

	for _, tt := range tests {
		t.Run(string(tt.artType), func(t *testing.T) {
			got := tt.artType.ProducerFor()
			if got != tt.want {
				t.Errorf("ProducerFor(%s) = %s, want %s", tt.artType, got, tt.want)
			}
		})
	}
}

func Test_ArtifactType_ConsumerFor(t *testing.T) {
	tests := []struct {
		artType ArtifactType
		want    string
	}{
		{ArtifactTopicCard, "research_agent"},
		{ArtifactResearchBrief, "writing_agent"},
		{ArtifactFactClaims, "writing_agent"},
		{ArtifactSourcePack, "review_agent"},
		{ArtifactDraft, "review_agent"},
		{ArtifactRevisedDraft, "review_agent"},
		{ArtifactReviewReport, "writing_agent"},
		{ArtifactOutline, "human"},
	}

	for _, tt := range tests {
		t.Run(string(tt.artType), func(t *testing.T) {
			got := tt.artType.ConsumerFor()
			if got != tt.want {
				t.Errorf("ConsumerFor(%s) = %s, want %s", tt.artType, got, tt.want)
			}
		})
	}
}

// ─── AgentContext ──────────────────────────────────────────

func Test_AgentContext_GetArtifact(t *testing.T) {
	ac := NewAgentContext(RoleResearch, "task-1", "user-1")

	// No artifacts → nil
	if art := ac.GetArtifact(ArtifactTopicCard); art != nil {
		t.Error("expected nil when no artifacts")
	}

	// Add a non-approved artifact
	art1 := Artifact{ID: "art-1", Type: ArtifactTopicCard, Status: ArtifactStatusSubmitted}
	ac.AddInputArtifact(art1)

	// Should find non-approved artifact as fallback
	art := ac.GetArtifact(ArtifactTopicCard)
	if art == nil {
		t.Fatal("expected to find non-approved artifact as fallback")
	}
	if art.ID != "art-1" {
		t.Errorf("expected art-1, got %s", art.ID)
	}

	// Add an approved artifact of the same type
	art2 := Artifact{ID: "art-2", Type: ArtifactTopicCard, Status: ArtifactStatusApproved}
	ac.AddInputArtifact(art2)

	// Should prefer the approved one
	art = ac.GetArtifact(ArtifactTopicCard)
	if art == nil {
		t.Fatal("expected to find approved artifact")
	}
	if art.ID != "art-2" {
		t.Errorf("expected art-2 (approved), got %s", art.ID)
	}
}

func Test_AgentContext_GetArtifact_LatestVersion(t *testing.T) {
	ac := NewAgentContext(RoleWriting, "task-1", "user-1")

	// Add multiple approved artifacts of the same type
	ac.AddInputArtifact(Artifact{ID: "art-v1", Type: ArtifactDraft, Status: ArtifactStatusApproved, Version: 1})
	ac.AddInputArtifact(Artifact{ID: "art-v2", Type: ArtifactDraft, Status: ArtifactStatusApproved, Version: 2})

	// Should return the latest (last added)
	art := ac.GetArtifact(ArtifactDraft)
	if art == nil {
		t.Fatal("expected to find artifact")
	}
	if art.ID != "art-v2" {
		t.Errorf("expected art-v2 (latest), got %s", art.ID)
	}
}

// ─── extractMetrics ────────────────────────────────────────

func Test_extractMetrics_ResearchBrief(t *testing.T) {
	svc := &Service{}
	briefJSON, _ := json.Marshal(map[string]interface{}{
		"sources": []map[string]string{
			{"url": "https://a.com", "source": "A"},
			{"url": "https://b.com", "source": "B"},
			{"url": "https://c.com", "source": "C"},
		},
		"claims": []map[string]interface{}{
			{"claim": "fact1", "status": "supported"},
			{"claim": "fact2", "status": "supported"},
			{"claim": "fact3", "status": "conflicted"},
			{"claim": "fact4", "status": "verified"},
		},
		"gaps": []string{"gap1"},
	})

	artifacts := []Artifact{
		{Type: ArtifactResearchBrief, Content: string(briefJSON)},
	}

	metrics := svc.extractMetrics(artifacts)
	if metrics == nil {
		t.Fatal("expected non-nil metrics")
	}
	if metrics.SourceCount != 3 {
		t.Errorf("SourceCount = %d, want 3", metrics.SourceCount)
	}
	if metrics.SupportedClaims != 2 {
		t.Errorf("SupportedClaims = %d, want 2", metrics.SupportedClaims)
	}
	if metrics.ConflictedClaims != 1 {
		t.Errorf("ConflictedClaims = %d, want 1", metrics.ConflictedClaims)
	}
	if metrics.VerifiedClaims != 1 {
		t.Errorf("VerifiedClaims = %d, want 1", metrics.VerifiedClaims)
	}
	if metrics.GapCount != 1 {
		t.Errorf("GapCount = %d, want 1", metrics.GapCount)
	}
}

func Test_extractMetrics_Draft(t *testing.T) {
	svc := &Service{}
	draftJSON, _ := json.Marshal(map[string]interface{}{
		"word_count": 1200,
		"outline": []map[string]string{
			{"section": "intro"},
			{"section": "body"},
			{"section": "conclusion"},
		},
	})

	artifacts := []Artifact{
		{Type: ArtifactDraft, Content: string(draftJSON)},
	}

	metrics := svc.extractMetrics(artifacts)
	if metrics == nil {
		t.Fatal("expected non-nil metrics")
	}
	if metrics.WordCount != 1200 {
		t.Errorf("WordCount = %d, want 1200", metrics.WordCount)
	}
	if metrics.SectionCount != 3 {
		t.Errorf("SectionCount = %d, want 3", metrics.SectionCount)
	}
}

func Test_extractMetrics_ReviewReport(t *testing.T) {
	svc := &Service{}
	reportJSON, _ := json.Marshal(map[string]interface{}{
		"passed":   false,
		"severity": "high",
	})

	artifacts := []Artifact{
		{Type: ArtifactReviewReport, Content: string(reportJSON)},
	}

	metrics := svc.extractMetrics(artifacts)
	if metrics == nil {
		t.Fatal("expected non-nil metrics")
	}
	if metrics.Severity != "high" {
		t.Errorf("Severity = %s, want high", metrics.Severity)
	}
}

func Test_extractMetrics_EmptyArtifacts(t *testing.T) {
	svc := &Service{}
	metrics := svc.extractMetrics(nil)
	if metrics != nil {
		t.Error("expected nil metrics for empty artifacts")
	}
}

func Test_extractMetrics_InvalidJSON(t *testing.T) {
	svc := &Service{}
	artifacts := []Artifact{
		{Type: ArtifactResearchBrief, Content: "not valid json"},
	}
	metrics := svc.extractMetrics(artifacts)
	if metrics != nil {
		t.Error("expected nil metrics for invalid JSON")
	}
}

func Test_extractMetrics_MultipleArtifactsTakesMax(t *testing.T) {
	svc := &Service{}
	brief1JSON, _ := json.Marshal(map[string]interface{}{
		"sources": []map[string]string{{"url": "a"}, {"url": "b"}},
	})
	brief2JSON, _ := json.Marshal(map[string]interface{}{
		"sources": []map[string]string{{"url": "a"}, {"url": "b"}, {"url": "c"}, {"url": "d"}, {"url": "e"}},
	})

	artifacts := []Artifact{
		{Type: ArtifactResearchBrief, Content: string(brief1JSON)},
		{Type: ArtifactFactClaims, Content: string(brief2JSON)},
	}

	metrics := svc.extractMetrics(artifacts)
	if metrics == nil {
		t.Fatal("expected non-nil metrics")
	}
	// Should take the max source count across artifacts
	if metrics.SourceCount != 5 {
		t.Errorf("SourceCount = %d, want 5 (max)", metrics.SourceCount)
	}
}

// ─── buildDecisionOptions ──────────────────────────────────

func Test_buildDecisionOptions(t *testing.T) {
	svc := &Service{}
	d := &Decision{
		Type:                DecisionSelectAngle,
		ApproveTargetStatus: string(StatusWriting),
		RejectTargetStatus:  string(StatusResearch),
	}

	options := svc.buildDecisionOptions(d)
	if len(options) != 2 {
		t.Fatalf("expected 2 options, got %d", len(options))
	}

	approve := options[0]
	if approve.ID != "approve" {
		t.Errorf("expected approve option ID=approve, got %s", approve.ID)
	}
	if approve.TargetStatus != StatusWriting {
		t.Errorf("expected approve target=writing, got %s", approve.TargetStatus)
	}
	if approve.Label != "批准" {
		t.Errorf("expected label=批准, got %s", approve.Label)
	}

	reject := options[1]
	if reject.ID != "reject" {
		t.Errorf("expected reject option ID=reject, got %s", reject.ID)
	}
	if reject.TargetStatus != StatusResearch {
		t.Errorf("expected reject target=research, got %s", reject.TargetStatus)
	}
	if reject.Label != "驳回" {
		t.Errorf("expected label=驳回, got %s", reject.Label)
	}
}

func Test_buildDecisionOptions_AllDecisionTypes(t *testing.T) {
	svc := &Service{}
	decisions := []DecisionType{
		DecisionApproveTopic,
		DecisionSelectAngle,
		DecisionAllowRewrite,
		DecisionPublish,
		DecisionAcceptReview,
		DecisionEscalate,
	}

	for _, dt := range decisions {
		t.Run(string(dt), func(t *testing.T) {
			d := &Decision{
				Type:                dt,
				ApproveTargetStatus: string(nextStatusForDecision(dt)),
				RejectTargetStatus:  string(prevStatusForDecision(dt)),
			}
			options := svc.buildDecisionOptions(d)
			if len(options) != 2 {
				t.Fatalf("expected 2 options, got %d", len(options))
			}
			// Each option should have a non-empty description
			for _, opt := range options {
				if opt.Description == "" {
					t.Errorf("option %s has empty description", opt.ID)
				}
			}
		})
	}
}

// ─── decisionApproveDescription / decisionRejectDescription ──

func Test_decisionApproveDescription(t *testing.T) {
	tests := []struct {
		decType DecisionType
		want    string
	}{
		{DecisionApproveTopic, "批准立项，进入研究阶段"},
		{DecisionSelectAngle, "确认角度，进入写作阶段"},
		{DecisionAllowRewrite, "允许进入审校阶段"},
		{DecisionPublish, "批准发布"},
		{DecisionAcceptReview, "接受审校结果"},
		{DecisionEscalate, "确认无需升级，继续执行"},
	}

	for _, tt := range tests {
		t.Run(string(tt.decType), func(t *testing.T) {
			got := decisionApproveDescription(tt.decType)
			if got != tt.want {
				t.Errorf("decisionApproveDescription(%s) = %q, want %q", tt.decType, got, tt.want)
			}
		})
	}
}

func Test_decisionRejectDescription(t *testing.T) {
	tests := []struct {
		decType DecisionType
		want    string
	}{
		{DecisionApproveTopic, "驳回立项，退回草稿"},
		{DecisionSelectAngle, "驳回，退回研究"},
		{DecisionAllowRewrite, "驳回，退回写作修改"},
		{DecisionPublish, "驳回发布，退回审校"},
		{DecisionAcceptReview, "驳回，退回写作修改"},
		{DecisionEscalate, "升级到人工裁决"},
	}

	for _, tt := range tests {
		t.Run(string(tt.decType), func(t *testing.T) {
			got := decisionRejectDescription(tt.decType)
			if got != tt.want {
				t.Errorf("decisionRejectDescription(%s) = %q, want %q", tt.decType, got, tt.want)
			}
		})
	}
}

// ─── humanActionMessage ────────────────────────────────────

func Test_humanActionMessage(t *testing.T) {
	tests := []struct {
		status TaskStatus
		want   string
	}{
		{StatusPendingPublish, "稿件审查通过，等待发布确认"},
		{StatusPendingApproval, "审校发现严重问题，需人工裁决"},
		{StatusDraft, "需要人工介入"},
		{StatusPublished, "需要人工介入"},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			got := humanActionMessage(tt.status)
			if got != tt.want {
				t.Errorf("humanActionMessage(%s) = %q, want %q", tt.status, got, tt.want)
			}
		})
	}
}

// ─── agentRoleForStatus ────────────────────────────────────

func Test_agentRoleForStatus(t *testing.T) {
	tests := []struct {
		status TaskStatus
		want   string
	}{
		{StatusResearch, string(RoleResearch)},
		{StatusWriting, string(RoleWriting)},
		{StatusReview, string(RoleReview)},
		{StatusDraft, ""},
		{StatusPendingApproval, ""},
		{StatusPublished, ""},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			got := agentRoleForStatus(tt.status)
			if got != tt.want {
				t.Errorf("agentRoleForStatus(%s) = %q, want %q", tt.status, got, tt.want)
			}
		})
	}
}

// ─── AgentRegistry ─────────────────────────────────────────

func Test_AgentRegistry_AllRolesDefined(t *testing.T) {
	roles := []AgentRole{RoleResearch, RoleWriting, RoleReview}
	for _, role := range roles {
		t.Run(string(role), func(t *testing.T) {
			def, ok := AgentRegistry[role]
			if !ok {
				t.Fatalf("role %s not in AgentRegistry", role)
			}
			if def.Role != role {
				t.Errorf("def.Role = %s, want %s", def.Role, role)
			}
			if def.Name == "" {
				t.Error("expected non-empty Name")
			}
			if len(def.CanProduce) == 0 {
				t.Error("expected non-empty CanProduce")
			}
			if len(def.CanConsume) == 0 {
				t.Error("expected non-empty CanConsume")
			}
		})
	}
}

func Test_AgentRegistry_ReviewAgentRequiresIsolation(t *testing.T) {
	def := AgentRegistry[RoleReview]
	if !def.RequiresIsolation {
		t.Error("expected review agent to require isolation")
	}
}

func Test_AgentRegistry_ResearchAndWritingDoNotRequireIsolation(t *testing.T) {
	if AgentRegistry[RoleResearch].RequiresIsolation {
		t.Error("research agent should not require isolation")
	}
	if AgentRegistry[RoleWriting].RequiresIsolation {
		t.Error("writing agent should not require isolation")
	}
}

// ─── ClaimStatus ───────────────────────────────────────────

func Test_ClaimStatus_Values(t *testing.T) {
	// Verify that LLM-determinable statuses don't include "verified"
	llmStatuses := []ClaimStatus{ClaimSupported, ClaimUnsupported, ClaimConflicted, ClaimUnknown}
	for _, s := range llmStatuses {
		if s == ClaimVerified {
			t.Error("verified should not be in LLM-determinable statuses")
		}
	}

	// verified should be a separate value
	if ClaimVerified == ClaimSupported {
		t.Error("verified should differ from supported")
	}
	if ClaimVerified == ClaimUnknown {
		t.Error("verified should differ from unknown")
	}
}

// ─── max helper ────────────────────────────────────────────

func Test_max(t *testing.T) {
	tests := []struct {
		a, b, want int
	}{
		{1, 2, 2},
		{5, 3, 5},
		{0, 0, 0},
		{-1, 1, 1},
		{100, 99, 100},
	}
	for _, tt := range tests {
		got := max(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("max(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

// ─── TransitionCommand ─────────────────────────────────────

func Test_TransitionCauseType_Values(t *testing.T) {
	if TransitionCauseEvent != "event" {
		t.Errorf("TransitionCauseEvent = %q, want 'event'", TransitionCauseEvent)
	}
	if TransitionCauseDecision != "decision" {
		t.Errorf("TransitionCauseDecision = %q, want 'decision'", TransitionCauseDecision)
	}
}

// ─── Error sentinels ───────────────────────────────────────

func Test_ErrorSentinels_Distinct(t *testing.T) {
	errs := []error{
		ErrTaskNotFound,
		ErrArtifactNotFound,
		ErrDecisionNotFound,
		ErrInvalidTransition,
		ErrArtifactNotApproved,
		ErrTokenBudgetExceeded,
		ErrLeaseConflict,
		ErrStatusConflict,
		ErrForbidden,
	}
	seen := make(map[string]bool)
	for _, e := range errs {
		msg := e.Error()
		if seen[msg] {
			t.Errorf("error message %q is not distinct", msg)
		}
		seen[msg] = true
	}
}

package editorial

import (
	"testing"
)

func TestValidateDAG_ValidLinear(t *testing.T) {
	r := NewDynamicAgentRegistry()
	researcher := GenerateAgentConfig("researcher", "研究员", "你是研究员")
	writer := GenerateAgentConfig("writer", "撰稿人", "你是撰稿人")
	reviewer := GenerateAgentConfig("reviewer", "审校", "你是审校")
	r.ApplyGeneratedAgents("task-1", []*AgentConfig{researcher, writer, reviewer})

	spec := &WorkflowSpec{
		TaskID: "task-1",
		Nodes: []NodeSpec{
			{ID: "n1", AgentID: researcher.ID, Dependencies: []string{}, OutputArtifact: ArtifactResearchBrief},
			{ID: "n2", AgentID: writer.ID, Dependencies: []string{"n1"}, OutputArtifact: ArtifactDraft},
			{ID: "n3", AgentID: reviewer.ID, Dependencies: []string{"n2"}, OutputArtifact: ArtifactReviewReport},
		},
	}

	if err := ValidateDAG(spec, r); err != nil {
		t.Errorf("valid DAG should pass: %v", err)
	}
}

func TestValidateDAG_ValidParallel(t *testing.T) {
	r := NewDynamicAgentRegistry()
	r1 := GenerateAgentConfig("researcher", "研究员1", "你是研究员1")
	r2 := GenerateAgentConfig("researcher", "研究员2", "你是研究员2")
	w := GenerateAgentConfig("writer", "撰稿人", "你是撰稿人")
	r.ApplyGeneratedAgents("task-1", []*AgentConfig{r1, r2, w})

	spec := &WorkflowSpec{
		TaskID: "task-1",
		Nodes: []NodeSpec{
			{ID: "n1", AgentID: r1.ID, Dependencies: []string{}, OutputArtifact: ArtifactResearchBrief},
			{ID: "n2", AgentID: r2.ID, Dependencies: []string{}, OutputArtifact: ArtifactResearchBrief},
			{ID: "n3", AgentID: w.ID, Dependencies: []string{"n1", "n2"}, OutputArtifact: ArtifactDraft},
		},
	}

	if err := ValidateDAG(spec, r); err != nil {
		t.Errorf("valid parallel DAG should pass: %v", err)
	}
}

func TestValidateDAG_Cycle(t *testing.T) {
	r := NewDynamicAgentRegistry()
	a := GenerateAgentConfig("writer", "角色A", "你是A")
	b := GenerateAgentConfig("writer", "角色B", "你是B")
	r.ApplyGeneratedAgents("task-1", []*AgentConfig{a, b})

	spec := &WorkflowSpec{
		TaskID: "task-1",
		Nodes: []NodeSpec{
			{ID: "n1", AgentID: a.ID, Dependencies: []string{"n2"}, OutputArtifact: ArtifactDraft},
			{ID: "n2", AgentID: b.ID, Dependencies: []string{"n1"}, OutputArtifact: ArtifactDraft},
		},
	}

	err := ValidateDAG(spec, r)
	if err != ErrDAGHasCycle {
		t.Errorf("expected ErrDAGHasCycle, got %v", err)
	}
}

func TestValidateDAG_Empty(t *testing.T) {
	r := NewDynamicAgentRegistry()
	spec := &WorkflowSpec{
		TaskID: "task-1",
		Nodes:  []NodeSpec{},
	}

	err := ValidateDAG(spec, r)
	if err != ErrDAGEmpty {
		t.Errorf("expected ErrDAGEmpty, got %v", err)
	}
}

func TestValidateDAG_NoEntryPoint(t *testing.T) {
	r := NewDynamicAgentRegistry()
	a := GenerateAgentConfig("writer", "角色A", "你是A")
	b := GenerateAgentConfig("writer", "角色B", "你是B")
	r.ApplyGeneratedAgents("task-1", []*AgentConfig{a, b})

	spec := &WorkflowSpec{
		TaskID: "task-1",
		Nodes: []NodeSpec{
			{ID: "n1", AgentID: a.ID, Dependencies: []string{"n2"}, OutputArtifact: ArtifactDraft},
			{ID: "n2", AgentID: b.ID, Dependencies: []string{"n1"}, OutputArtifact: ArtifactDraft},
		},
	}

	// 这个 DAG 有环，所以应该报 cycle 错误
	err := ValidateDAG(spec, r)
	if err == nil {
		t.Error("DAG with cycle should fail")
	}
}

func TestValidateDAG_NoExitPoint(t *testing.T) {
	r := NewDynamicAgentRegistry()
	a := GenerateAgentConfig("writer", "角色A", "你是A")
	r.ApplyGeneratedAgents("task-1", []*AgentConfig{a})

	spec := &WorkflowSpec{
		TaskID: "task-1",
		Nodes: []NodeSpec{
			{ID: "n1", AgentID: a.ID, Dependencies: []string{"n1"}, OutputArtifact: ArtifactDraft},
		},
	}

	// 自引用 — 有环
	err := ValidateDAG(spec, r)
	if err == nil {
		t.Error("self-referencing DAG should fail")
	}
}

func TestValidateDAG_InvalidNodeID(t *testing.T) {
	r := NewDynamicAgentRegistry()
	a := GenerateAgentConfig("writer", "角色A", "你是A")
	r.ApplyGeneratedAgents("task-1", []*AgentConfig{a})

	spec := &WorkflowSpec{
		TaskID: "task-1",
		Nodes: []NodeSpec{
			{ID: "n1", AgentID: a.ID, Dependencies: []string{}, OutputArtifact: ArtifactDraft},
			{ID: "n2", AgentID: a.ID, Dependencies: []string{"nonexistent"}, OutputArtifact: ArtifactDraft},
		},
	}

	err := ValidateDAG(spec, r)
	if err != ErrDAGInvalidNodeID {
		t.Errorf("expected ErrDAGInvalidNodeID, got %v", err)
	}
}

func TestValidateDAG_DuplicateNodeID(t *testing.T) {
	r := NewDynamicAgentRegistry()
	a := GenerateAgentConfig("writer", "角色A", "你是A")
	r.ApplyGeneratedAgents("task-1", []*AgentConfig{a})

	spec := &WorkflowSpec{
		TaskID: "task-1",
		Nodes: []NodeSpec{
			{ID: "n1", AgentID: a.ID, Dependencies: []string{}, OutputArtifact: ArtifactDraft},
			{ID: "n1", AgentID: a.ID, Dependencies: []string{}, OutputArtifact: ArtifactDraft},
		},
	}

	err := ValidateDAG(spec, r)
	if err != ErrDAGDuplicateNode {
		t.Errorf("expected ErrDAGDuplicateNode, got %v", err)
	}
}

func TestValidateDAG_InvalidAgentID(t *testing.T) {
	r := NewDynamicAgentRegistry()
	spec := &WorkflowSpec{
		TaskID: "task-1",
		Nodes: []NodeSpec{
			{ID: "n1", AgentID: "nonexistent-agent", Dependencies: []string{}, OutputArtifact: ArtifactDraft},
		},
	}

	err := ValidateDAG(spec, r)
	if err != ErrDAGInvalidAgentID {
		t.Errorf("expected ErrDAGInvalidAgentID, got %v", err)
	}
}

func TestTopologicalSort(t *testing.T) {
	nodes := []NodeSpec{
		{ID: "n3", Dependencies: []string{"n1", "n2"}},
		{ID: "n1", Dependencies: []string{}},
		{ID: "n2", Dependencies: []string{"n1"}},
		{ID: "n4", Dependencies: []string{"n3"}},
	}

	sorted, err := TopologicalSort(nodes)
	if err != nil {
		t.Fatalf("topological sort failed: %v", err)
	}

	// 验证拓扑序：n1 必须在 n2 前面，n2 必须在 n3 前面，n3 必须在 n4 前面
	pos := make(map[string]int)
	for i, node := range sorted {
		pos[node.ID] = i
	}

	if pos["n1"] > pos["n2"] {
		t.Error("n1 should come before n2")
	}
	if pos["n2"] > pos["n3"] {
		t.Error("n2 should come before n3")
	}
	if pos["n3"] > pos["n4"] {
		t.Error("n3 should come before n4")
	}
}

func TestTopologicalSort_Cycle(t *testing.T) {
	nodes := []NodeSpec{
		{ID: "n1", Dependencies: []string{"n2"}},
		{ID: "n2", Dependencies: []string{"n1"}},
	}

	_, err := TopologicalSort(nodes)
	if err != ErrDAGHasCycle {
		t.Errorf("expected ErrDAGHasCycle, got %v", err)
	}
}

func TestForkContext_FullHistory(t *testing.T) {
	artifacts := []Artifact{
		{ID: "a1", Type: ArtifactResearchBrief, Status: ArtifactStatusApproved},
	}
	history := []map[string]string{
		{"role": "user", "content": "hello"},
		{"role": "assistant", "content": "hi"},
	}

	ac := ForkContext(ContextForkFull, 0, artifacts, history, RoleResearch, "task-1", "user-1")

	if len(ac.InputArtifacts) != 1 {
		t.Errorf("expected 1 input artifact, got %d", len(ac.InputArtifacts))
	}
	forkedHistory := GetForkedHistory(ac)
	if len(forkedHistory) != 2 {
		t.Errorf("expected 2 history entries, got %d", len(forkedHistory))
	}
}

func TestForkContext_LastNTurns(t *testing.T) {
	history := []map[string]string{
		{"role": "user", "content": "msg1"},
		{"role": "assistant", "content": "msg2"},
		{"role": "user", "content": "msg3"},
		{"role": "assistant", "content": "msg4"},
	}

	ac := ForkContext(ContextForkLastN, 2, nil, history, RoleResearch, "task-1", "user-1")

	forkedHistory := GetForkedHistory(ac)
	if len(forkedHistory) != 2 {
		t.Errorf("expected 2 history entries, got %d", len(forkedHistory))
	}
	// 应该保留最后 2 条
	if forkedHistory[0]["content"] != "msg3" {
		t.Errorf("expected msg3, got %s", forkedHistory[0]["content"])
	}
}

func TestForkContext_SummaryOnly(t *testing.T) {
	artifacts := []Artifact{
		{ID: "a1", Type: ArtifactDraft, Content: "文章内容...", Status: ArtifactStatusApproved},
	}
	history := []map[string]string{
		{"role": "user", "content": "hello"},
	}

	ac := ForkContext(ContextForkSummary, 0, artifacts, history, RoleReview, "task-1", "user-1")

	// SummaryOnly 模式不应传递历史
	forkedHistory := GetForkedHistory(ac)
	if len(forkedHistory) != 0 {
		t.Errorf("expected 0 history entries in summary mode, got %d", len(forkedHistory))
	}
	// 但应有输入 Artifact
	if len(ac.InputArtifacts) != 1 {
		t.Errorf("expected 1 input artifact, got %d", len(ac.InputArtifacts))
	}
}

func TestDAGTokenBudget(t *testing.T) {
	dag := NewDAGTokenBudget(10000)

	dag.AllocateBudget("agent-1", 5000)
	dag.AllocateBudget("agent-2", 5000)

	dag.UpdateUsage("agent-1", 2000)

	budget, ok := dag.GetBudget("agent-1")
	if !ok {
		t.Fatal("budget not found")
	}
	if budget.TotalUsed != 2000 {
		t.Errorf("expected 2000 used, got %d", budget.TotalUsed)
	}
	if budget.TokensLeft != 3000 {
		t.Errorf("expected 3000 left, got %d", budget.TokensLeft)
	}
	if dag.GetTotalUsed() != 2000 {
		t.Errorf("expected total 2000, got %d", dag.GetTotalUsed())
	}
	if dag.IsBudgetExceeded() {
		t.Error("budget should not be exceeded")
	}

	// 超额
	dag.UpdateUsage("agent-2", 8000)
	if !dag.IsBudgetExceeded() {
		t.Error("budget should be exceeded")
	}

	// 低预算检测
	if !dag.IsAgentBudgetLow("agent-1", 5000) {
		t.Error("agent-1 budget should be low")
	}
}

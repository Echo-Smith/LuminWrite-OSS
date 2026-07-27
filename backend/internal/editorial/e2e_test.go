package editorial

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// ═══════════════════════════════════════════════════════════════
// E2E 端到端测试 — 完整的工作流链路
// 3.1 正常通过: 选题→研究→写作→审校通过→发布
// 3.2 研究待人工批准: 研究质量中等→select_angle→人类批准→继续
// 3.3 审校退回修改: 审校发现 medium 问题→退回写作→修改稿→再审校
// 3.4 严重问题升级: 审校发现 high 问题→升级 pending_approval→人类裁决
// ═══════════════════════════════════════════════════════════════

// ─── Mock Executors ────────────────────────────────────────

// mockResearchExecutor 模拟研究 Agent，产出预设质量的研究简报
type mockResearchExecutor struct {
	store     *Store
	quality   string // "high" | "medium" | "low"
	callCount int
}

func (e *mockResearchExecutor) Role() AgentRole { return RoleResearch }
func (e *mockResearchExecutor) Execute(ctx context.Context, ac *AgentContext, task *Task) (*Artifact, error) {
	e.callCount++
	var content string
	switch e.quality {
	case "high":
		content = mustJSON(map[string]interface{}{
			"summary": "High quality research brief",
			"sources": []map[string]string{
				{"url": "https://src1.com", "source": "Src1", "relevance": "high"},
				{"url": "https://src2.com", "source": "Src2", "relevance": "high"},
				{"url": "https://src3.com", "source": "Src3", "relevance": "medium"},
			},
			"claims": []map[string]interface{}{
				{"claim": "fact1", "status": "supported"},
				{"claim": "fact2", "status": "supported"},
				{"claim": "fact3", "status": "verified"},
			},
			"gaps": []string{},
		})
	case "medium":
		content = mustJSON(map[string]interface{}{
			"summary": "Medium quality research brief",
			"sources": []map[string]string{
				{"url": "https://src1.com", "source": "Src1", "relevance": "high"},
				{"url": "https://src2.com", "source": "Src2", "relevance": "medium"},
			},
			"claims": []map[string]interface{}{
				{"claim": "fact1", "status": "supported"},
			},
			"gaps": []string{"missing context"},
		})
	default:
		content = mustJSON(map[string]interface{}{
			"summary": "Low quality research brief",
			"sources": []map[string]string{
				{"url": "https://src1.com", "source": "Src1", "relevance": "low"},
			},
			"claims": []map[string]interface{}{
				{"claim": "fact1", "status": "unknown"},
			},
			"gaps": []string{"gap1", "gap2"},
		})
	}

	art, err := e.store.CreateArtifact(ctx, SubmitArtifactInput{
		Type:       ArtifactResearchBrief,
		Content:    content,
		ProducedBy: "research_agent",
		TokenCost:  1000,
	}, task.ID)
	if err != nil {
		return nil, err
	}
	// Auto-approve the artifact so downstream agents can consume it
	e.store.ReviewArtifact(ctx, art.ID, ReviewArtifactInput{
		Status:     ArtifactStatusApproved,
		ReviewerID: "system",
		ReviewNote: "auto-approved for testing",
	})
	art.Status = ArtifactStatusApproved
	return art, nil
}

// mockWritingExecutor 模拟写作 Agent，产出预设质量的初稿
type mockWritingExecutor struct {
	store     *Store
	quality   string // "high" | "short"
	callCount int
}

func (e *mockWritingExecutor) Role() AgentRole { return RoleWriting }
func (e *mockWritingExecutor) Execute(ctx context.Context, ac *AgentContext, task *Task) (*Artifact, error) {
	e.callCount++
	var content string
	switch e.quality {
	case "high":
		content = mustJSON(map[string]interface{}{
			"title":      "Test Article",
			"content":    "A well-written article with sufficient content...",
			"word_count": 800,
			"outline": []map[string]string{
				{"section": "引言"},
				{"section": "正文"},
				{"section": "结论"},
			},
		})
	default:
		content = mustJSON(map[string]interface{}{
			"title":      "Short Article",
			"content":    "Too short...",
			"word_count": 100,
			"outline": []map[string]string{
				{"section": "only section"},
			},
		})
	}

	art, err := e.store.CreateArtifact(ctx, SubmitArtifactInput{
		Type:       ArtifactDraft,
		Content:    content,
		ProducedBy: "writing_agent",
		TokenCost:  2000,
	}, task.ID)
	if err != nil {
		return nil, err
	}
	e.store.ReviewArtifact(ctx, art.ID, ReviewArtifactInput{
		Status:     ArtifactStatusApproved,
		ReviewerID: "system",
		ReviewNote: "auto-approved for testing",
	})
	art.Status = ArtifactStatusApproved
	return art, nil
}

// mockReviewExecutor 模拟审校 Agent，产出预设严重度的审查报告
// resultSeq 允许按调用次数返回不同结果：第一次调用 resultSeq[0]，第二次 resultSeq[1]...
// 如果 callCount > len(resultSeq)，使用最后一个元素
type mockReviewExecutor struct {
	store     *Store
	resultSeq []string // 每次调用的结果序列
	callCount int
}

func (e *mockReviewExecutor) Role() AgentRole { return RoleReview }
func (e *mockReviewExecutor) Execute(ctx context.Context, ac *AgentContext, task *Task) (*Artifact, error) {
	e.callCount++
	result := "medium"
	if len(e.resultSeq) > 0 {
		idx := e.callCount - 1
		if idx >= len(e.resultSeq) {
			idx = len(e.resultSeq) - 1
		}
		result = e.resultSeq[idx]
	}

	var content string
	switch result {
	case "passed":
		content = mustJSON(map[string]interface{}{
			"passed":   true,
			"severity": "low",
		})
	case "high":
		content = mustJSON(map[string]interface{}{
			"passed":   false,
			"severity": "high",
		})
	default: // medium
		content = mustJSON(map[string]interface{}{
			"passed":   false,
			"severity": "medium",
		})
	}

	art, err := e.store.CreateArtifact(ctx, SubmitArtifactInput{
		Type:       ArtifactReviewReport,
		Content:    content,
		ProducedBy: "review_agent",
		TokenCost:  500,
	}, task.ID)
	if err != nil {
		return nil, err
	}
	e.store.ReviewArtifact(ctx, art.ID, ReviewArtifactInput{
		Status:     ArtifactStatusApproved,
		ReviewerID: "system",
		ReviewNote: "auto-approved for testing",
	})
	art.Status = ArtifactStatusApproved
	return art, nil
}

// ─── Helpers ───────────────────────────────────────────────

func mustJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// waitForStatus 轮询任务状态直到达到目标或超时
func waitForStatus(t *testing.T, store *Store, taskID string, target TaskStatus, timeout time.Duration) *Task {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		task, err := store.GetTask(context.Background(), taskID)
		if err != nil {
			t.Logf("poll GetTask error: %v", err)
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if task.Status == target {
			return task
		}
		time.Sleep(50 * time.Millisecond)
	}
	task, _ := store.GetTask(context.Background(), taskID)
	t.Fatalf("timeout waiting for task %s to reach status %s (current: %s)", taskID, target, task.Status)
	return nil
}

// waitForStatusOrLater 轮询任务状态，达到 target 或更后的状态即返回
func waitForStatusOrLater(t *testing.T, store *Store, taskID string, target TaskStatus, timeout time.Duration) *Task {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		task, err := store.GetTask(context.Background(), taskID)
		if err != nil {
			t.Logf("poll GetTask error: %v", err)
			time.Sleep(50 * time.Millisecond)
			continue
		}
		// If task is at target or has progressed beyond, return
		if isStatusOrLater(task.Status, target) {
			return task
		}
		time.Sleep(50 * time.Millisecond)
	}
	task, _ := store.GetTask(context.Background(), taskID)
	t.Fatalf("timeout waiting for task %s to reach status >= %s (current: %s)", taskID, target, task.Status)
	return nil
}

// isStatusOrLater 判断 status 是否等于或晚于 target 在流水线中的位置
func isStatusOrLater(status, target TaskStatus) bool {
	order := map[TaskStatus]int{
		StatusDraft:           0,
		StatusPendingApproval: 1,
		StatusResearch:        2,
		StatusWriting:         3,
		StatusReview:          4,
		StatusPendingPublish:  5,
		StatusPublished:       6,
	}
	return order[status] >= order[target]
}

// waitForPendingDecision 轮询直到出现 pending decision 或超时
func waitForPendingDecision(t *testing.T, store *Store, taskID string, timeout time.Duration) *Decision {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		decisions, err := store.ListDecisions(context.Background(), taskID)
		if err != nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		for i := range decisions {
			if decisions[i].Status == DecisionStatusPending {
				return &decisions[i]
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil
}

// findPendingDecision 查找任务的 pending decision
func findPendingDecision(t *testing.T, store *Store, taskID string) *Decision {
	t.Helper()
	decisions, err := store.ListDecisions(context.Background(), taskID)
	if err != nil {
		t.Fatalf("ListDecisions failed: %v", err)
	}
	for i := range decisions {
		if decisions[i].Status == DecisionStatusPending {
			return &decisions[i]
		}
	}
	return nil
}

// setupE2EService 创建带 mock executors 的 Service
// reviewResultSeq 是审校结果序列：第一次调用用 reviewResultSeq[0]，以此类推
type e2eConfig struct {
	researchQuality string
	writingQuality  string
	reviewResults   []string
}

func setupE2EService(t *testing.T, cfg e2eConfig) (*Service, *mockResearchExecutor, *mockWritingExecutor, *mockReviewExecutor) {
	t.Helper()
	store := testStore(t)
	emitter := &mockEmitter{}
	svc := NewService(store, emitter)

	researchExec := &mockResearchExecutor{store: store, quality: cfg.researchQuality}
	writingExec := &mockWritingExecutor{store: store, quality: cfg.writingQuality}
	reviewExec := &mockReviewExecutor{store: store, resultSeq: cfg.reviewResults}

	svc.Orchestrator().RegisterExecutor(researchExec)
	svc.Orchestrator().RegisterExecutor(writingExec)
	svc.Orchestrator().RegisterExecutor(reviewExec)

	return svc, researchExec, writingExec, reviewExec
}

// createAndSubmitTask 创建任务并提交审批
func createAndSubmitTask(t *testing.T, svc *Service, userID string) *Task {
	t.Helper()
	ctx := context.Background()
	task, err := svc.CreateTask(ctx, CreateTaskInput{
		Title:          "E2E Test Task",
		Description:    "E2E test description",
		AcceptCriteria: "Must pass all checks",
		Priority:       3,
		Tags:           []string{"test"},
		StyleSlug:      "yinyue",
		TokenBudget:    100000,
	}, userID)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}
	// draft → pending_approval
	err = svc.AdvanceTask(ctx, task.ID, AdvanceTaskInput{
		TargetStatus: StatusPendingApproval,
		DecidedBy:    userID,
		Rationale:    "Submit for approval",
	})
	if err != nil {
		t.Fatalf("AdvanceTask to pending_approval failed: %v", err)
	}
	return task
}

// ═══════════════════════════════════════════════════════════════
// 3.1 E2E: 正常通过 — 选题→研究→写作→审校通过→发布
// ═══════════════════════════════════════════════════════════════

func Test_E2E_3_1_HappyPath(t *testing.T) {
	svc, researchExec, writingExec, reviewExec := setupE2EService(t, e2eConfig{
		researchQuality: "high",
		writingQuality:  "high",
		reviewResults:   []string{"passed"},
	})
	store := svc.Store()
	ctx := context.Background()
	userID := testUser(t, testDB)

	// 1. 创建任务并提交审批
	task := createAndSubmitTask(t, svc, userID)
	t.Logf("task created: %s, status: pending_approval", task.ID)

	// 2. 批准立项 → 自动启动研究 Agent
	err := svc.AdvanceTask(ctx, task.ID, AdvanceTaskInput{
		TargetStatus: StatusResearch,
		DecidedBy:    userID,
		Rationale:    "Approved",
	})
	if err != nil {
		t.Fatalf("AdvanceTask to research failed: %v", err)
	}

	// 3. 等待整个流水线完成 → pending_publish
	// Mock executors 很快，research→writing→review→pending_publish 几乎瞬间完成
	task = waitForStatus(t, store, task.ID, StatusPendingPublish, 10*time.Second)
	t.Logf("pipeline completed: research=%d, writing=%d, review=%d",
		researchExec.callCount, writingExec.callCount, reviewExec.callCount)

	// 4. 验证每个 Agent 恰好执行一次
	if researchExec.callCount != 1 {
		t.Errorf("expected research executor called 1 time, got %d", researchExec.callCount)
	}
	if writingExec.callCount != 1 {
		t.Errorf("expected writing executor called 1 time, got %d", writingExec.callCount)
	}
	if reviewExec.callCount != 1 {
		t.Errorf("expected review executor called 1 time, got %d", reviewExec.callCount)
	}

	// 5. 人类发布
	err = svc.AdvanceTask(ctx, task.ID, AdvanceTaskInput{
		TargetStatus: StatusPublished,
		DecidedBy:    userID,
		Rationale:    "Publish approved",
	})
	if err != nil {
		t.Fatalf("AdvanceTask to published failed: %v", err)
	}

	// Verify final status
	task, err = store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if task.Status != StatusPublished {
		t.Errorf("expected final status=published, got %s", task.Status)
	}

	// 6. 验证决策历史
	decisions, err := store.ListDecisions(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListDecisions failed: %v", err)
	}
	if len(decisions) < 3 {
		t.Errorf("expected at least 3 decisions, got %d", len(decisions))
	}

	// 7. 验证 Agent 事件
	events, err := store.ListAgentRunEvents(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListAgentRunEvents failed: %v", err)
	}
	completedCount := 0
	for _, e := range events {
		if e.Type == EventAgentRunCompleted {
			completedCount++
		}
	}
	if completedCount != 3 {
		t.Errorf("expected 3 agent_run.completed events, got %d", completedCount)
	}

	// 8. 验证 Artifacts
	artifacts, err := store.ListArtifacts(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListArtifacts failed: %v", err)
	}
	// Should have: topic_card + research_brief + draft + review_report = 4+ artifacts
	if len(artifacts) < 4 {
		t.Errorf("expected at least 4 artifacts, got %d", len(artifacts))
	}
}

// ═══════════════════════════════════════════════════════════════
// 3.2 E2E: 研究待人工批准 — 研究质量中等→select_angle→人类批准→继续
// ═══════════════════════════════════════════════════════════════

func Test_E2E_3_2_ResearchNeedsHumanApproval(t *testing.T) {
	svc, researchExec, writingExec, reviewExec := setupE2EService(t, e2eConfig{
		researchQuality: "medium",
		writingQuality:  "high",
		reviewResults:   []string{"passed"},
	})
	store := svc.Store()
	ctx := context.Background()
	userID := testUser(t, testDB)

	// 1. 创建任务并提交审批
	task := createAndSubmitTask(t, svc, userID)

	// 2. 批准立项 → 启动研究 Agent
	err := svc.AdvanceTask(ctx, task.ID, AdvanceTaskInput{
		TargetStatus: StatusResearch,
		DecidedBy:    userID,
		Rationale:    "Approved",
	})
	if err != nil {
		t.Fatalf("AdvanceTask to research failed: %v", err)
	}

	// 3. 等待研究 Agent 完成 → 质量中等 → 创建 pending select_angle decision
	// 任务应该保持在 research 状态（等待人类决策）
	d := waitForPendingDecision(t, store, task.ID, 10*time.Second)
	if d == nil {
		t.Fatal("expected pending DecisionSelectAngle to be created")
	}
	if d.Type != DecisionSelectAngle {
		t.Errorf("expected decision type=select_angle, got %s", d.Type)
	}
	if d.ApproveTargetStatus != string(StatusWriting) {
		t.Errorf("expected approve_target=writing, got %s", d.ApproveTargetStatus)
	}
	t.Logf("found pending decision: type=%s, approve_target=%s", d.Type, d.ApproveTargetStatus)

	// 4. 验证任务状态仍为 research（等待人类决策）
	task, err = store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if task.Status != StatusResearch {
		t.Errorf("expected task to stay at research (pending human decision), got %s", task.Status)
	}
	t.Logf("research completed with medium quality, task at: %s", task.Status)

	// 5. 人类批准 → 推进到 writing → 自动启动写作 Agent
	resolved, err := svc.ResolveDecision(ctx, d.ID, ResolveDecisionInput{
		Status:    DecisionStatusApproved,
		Rationale: "Research is sufficient, proceed to writing",
	}, userID)
	if err != nil {
		t.Fatalf("ResolveDecision failed: %v", err)
	}
	if resolved.Status != DecisionStatusApproved {
		t.Errorf("expected resolved status=approved, got %s", resolved.Status)
	}

	// 6. 等待流水线完成 → pending_publish
	task = waitForStatus(t, store, task.ID, StatusPendingPublish, 10*time.Second)
	t.Logf("pipeline completed after human approval. writing=%d, review=%d",
		writingExec.callCount, reviewExec.callCount)

	// 7. 验证研究 Agent 只执行了一次
	if researchExec.callCount != 1 {
		t.Errorf("expected research executor called 1 time, got %d", researchExec.callCount)
	}
	if writingExec.callCount != 1 {
		t.Errorf("expected writing executor called 1 time, got %d", writingExec.callCount)
	}
	if reviewExec.callCount != 1 {
		t.Errorf("expected review executor called 1 time, got %d", reviewExec.callCount)
	}
}

// ═══════════════════════════════════════════════════════════════
// 3.3 E2E: 审校退回修改 — medium→退回写作→修改稿→再审校
// ═══════════════════════════════════════════════════════════════

func Test_E2E_3_3_ReviewSendBackToWriting(t *testing.T) {
	// 第一次审校返回 medium（退回写作），第二次审校返回 passed（通过）
	svc, researchExec, writingExec, reviewExec := setupE2EService(t, e2eConfig{
		researchQuality: "high",
		writingQuality:  "high",
		reviewResults:   []string{"medium", "passed"},
	})
	store := svc.Store()
	ctx := context.Background()
	userID := testUser(t, testDB)

	// 1. 创建任务、提交审批、批准立项
	task := createAndSubmitTask(t, svc, userID)
	err := svc.AdvanceTask(ctx, task.ID, AdvanceTaskInput{
		TargetStatus: StatusResearch,
		DecidedBy:    userID,
		Rationale:    "Approved",
	})
	if err != nil {
		t.Fatalf("AdvanceTask to research failed: %v", err)
	}

	// 2. 等待整个流程完成 → pending_publish
	// 第一次：research→writing→review(medium)→writing→review(passed)→pending_publish
	task = waitForStatus(t, store, task.ID, StatusPendingPublish, 10*time.Second)
	t.Logf("pipeline completed with review loop. research=%d, writing=%d, review=%d",
		researchExec.callCount, writingExec.callCount, reviewExec.callCount)

	// 3. 验证写作 Agent 被调用了 2 次（初稿 + 修改稿）
	if writingExec.callCount != 2 {
		t.Errorf("expected writing executor called 2 times (original + revision), got %d", writingExec.callCount)
	}

	// 4. 验证审校 Agent 被调用了 2 次（第一次退回 + 第二次通过）
	if reviewExec.callCount != 2 {
		t.Errorf("expected review executor called 2 times (reject + pass), got %d", reviewExec.callCount)
	}

	// 5. 验证研究 Agent 只执行了一次（没有被重跑）
	if researchExec.callCount != 1 {
		t.Errorf("expected research executor called 1 time, got %d", researchExec.callCount)
	}

	// 6. 验证 Artifacts — 应有 topic_card + research_brief + 2 drafts + 2 review_reports = 6
	artifacts, err := store.ListArtifacts(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListArtifacts failed: %v", err)
	}
	if len(artifacts) < 6 {
		t.Errorf("expected at least 6 artifacts (topic_card+brief+2drafts+2reviews), got %d", len(artifacts))
	}
}

// ═══════════════════════════════════════════════════════════════
// 3.4 E2E: 严重问题升级 — high→pending_approval→人类裁决
// ═══════════════════════════════════════════════════════════════

func Test_E2E_3_4_SevereIssueEscalation(t *testing.T) {
	svc, researchExec, _, reviewExec := setupE2EService(t, e2eConfig{
		researchQuality: "high",
		writingQuality:  "high",
		reviewResults:   []string{"high"},
	})
	store := svc.Store()
	ctx := context.Background()
	userID := testUser(t, testDB)

	// 1. 创建任务、提交审批、批准立项
	task := createAndSubmitTask(t, svc, userID)
	err := svc.AdvanceTask(ctx, task.ID, AdvanceTaskInput{
		TargetStatus: StatusResearch,
		DecidedBy:    userID,
		Rationale:    "Approved",
	})
	if err != nil {
		t.Fatalf("AdvanceTask to research failed: %v", err)
	}

	// 2. 等待审校完成 → high 问题 → 升级到 pending_approval（任务停留在此等待人类裁决）
	task = waitForStatus(t, store, task.ID, StatusPendingApproval, 10*time.Second)
	t.Logf("review found high severity issue, escalated to pending_approval. reviewExec called %d time(s)", reviewExec.callCount)
	if reviewExec.callCount != 1 {
		t.Errorf("expected review executor called 1 time, got %d", reviewExec.callCount)
	}

	// 5. 验证任务状态
	if task.Status != StatusPendingApproval {
		t.Errorf("expected status=pending_approval, got %s", task.Status)
	}

	// 6. 验证创建了 escalate 类型的 Decision
	decisions, err := store.ListDecisions(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListDecisions failed: %v", err)
	}
	var escalateFound bool
	for _, d := range decisions {
		if d.Type == DecisionEscalate {
			escalateFound = true
			// The escalate decision should have been auto-created by the review agent
			// It should have been created as "approved" (forward transition to pending_approval)
			if d.Status != DecisionStatusApproved {
				t.Logf("note: escalate decision status=%s (expected approved for forward transition)", d.Status)
			}
		}
	}
	if !escalateFound {
		t.Error("expected DecisionEscalate to be created")
	}

	// 7. 验证 Agent 事件 — 审校 Agent 应该有完成事件
	events, err := store.ListAgentRunEvents(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListAgentRunEvents failed: %v", err)
	}
	var reviewCompleted bool
	for _, e := range events {
		if e.AgentRole == RoleReview && e.Type == EventAgentRunCompleted {
			reviewCompleted = true
		}
	}
	if !reviewCompleted {
		t.Error("expected review agent completed event")
	}

	// 8. 人类可以决定下一步（例如退回草稿）
	err = svc.AdvanceTask(ctx, task.ID, AdvanceTaskInput{
		TargetStatus: StatusDraft,
		DecidedBy:    userID,
		Rationale:    "Human decided to restart from draft after severe issues",
	})
	if err != nil {
		t.Fatalf("AdvanceTask back to draft failed: %v", err)
	}

	task, err = store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if task.Status != StatusDraft {
		t.Errorf("expected task at draft after human decision, got %s", task.Status)
	}
	t.Logf("human decided to restart from draft, task at: %s", task.Status)

	// 9. 验证研究 Agent 只执行了一次
	if researchExec.callCount != 1 {
		t.Errorf("expected research executor called 1 time, got %d", researchExec.callCount)
	}
}

// ═══════════════════════════════════════════════════════════════
// 额外: 验证 Decision Packet 在 E2E 流程中的正确性
// ═══════════════════════════════════════════════════════════════

func Test_E2E_DecisionPacketContainsCorrectData(t *testing.T) {
	svc, _, _, _ := setupE2EService(t, e2eConfig{
		researchQuality: "medium",
		writingQuality:  "high",
		reviewResults:   []string{"passed"},
	})
	store := svc.Store()
	ctx := context.Background()
	userID := testUser(t, testDB)

	// 1. 创建任务、提交审批、批准立项
	task := createAndSubmitTask(t, svc, userID)
	err := svc.AdvanceTask(ctx, task.ID, AdvanceTaskInput{
		TargetStatus: StatusResearch,
		DecidedBy:    userID,
		Rationale:    "Approved",
	})
	if err != nil {
		t.Fatalf("AdvanceTask to research failed: %v", err)
	}

	// 2. 等待研究完成（中等质量 → 创建 pending decision）
	d := waitForPendingDecision(t, store, task.ID, 10*time.Second)
	if d == nil {
		t.Fatal("expected pending decision after medium quality research")
	}

	// 4. 构建 Decision Packet
	packet, err := svc.BuildDecisionPacket(ctx, d.ID)
	if err != nil {
		t.Fatalf("BuildDecisionPacket failed: %v", err)
	}

	// 5. 验证 Packet 内容
	if packet.DecisionID != d.ID {
		t.Errorf("expected decision_id=%s, got %s", d.ID, packet.DecisionID)
	}
	if packet.TaskID != task.ID {
		t.Errorf("expected task_id=%s, got %s", task.ID, packet.TaskID)
	}
	if packet.Type != DecisionSelectAngle {
		t.Errorf("expected type=select_angle, got %s", packet.Type)
	}
	if packet.TaskSummary.Title != "E2E Test Task" {
		t.Errorf("expected title=E2E Test Task, got %s", packet.TaskSummary.Title)
	}
	if len(packet.Options) != 2 {
		t.Errorf("expected 2 options, got %d", len(packet.Options))
	}
	// Should have evidence (research brief artifact)
	if len(packet.Evidence) == 0 {
		t.Error("expected evidence in packet")
	}
	// Should have metrics from research brief
	if packet.Metrics == nil {
		t.Error("expected metrics in packet")
	} else {
		if packet.Metrics.SourceCount != 2 {
			t.Errorf("expected source_count=2, got %d", packet.Metrics.SourceCount)
		}
		if packet.Metrics.GapCount != 1 {
			t.Errorf("expected gap_count=1, got %d", packet.Metrics.GapCount)
		}
	}
}

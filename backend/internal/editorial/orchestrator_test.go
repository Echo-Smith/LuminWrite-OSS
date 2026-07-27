package editorial

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// ═══════════════════════════════════════════════════════════════
// Orchestrator 集成测试 — 需要 TEST_DATABASE_URL
// 覆盖 routeAfterResearch, routeAfterWriting, transitionAfterEvent,
// handleReviewResult 的路由决策逻辑
// ═══════════════════════════════════════════════════════════════

// mockEmitter 收集事件用于断言
type mockEmitter struct {
	events []OrchestratorEvent
}

func (m *mockEmitter) Emit(evt OrchestratorEvent) {
	m.events = append(m.events, evt)
}

func (m *mockEmitter) hasEvent(evtType string) bool {
	for _, e := range m.events {
		if e.Type == evtType {
			return true
		}
	}
	return false
}

// noOpExecutor 是一个不执行任何操作的 Agent 执行器
type noOpExecutor struct {
	role    AgentRole
	artifact *Artifact
	err     error
}

func (e *noOpExecutor) Role() AgentRole { return e.role }
func (e *noOpExecutor) Execute(ctx context.Context, ac *AgentContext, task *Task) (*Artifact, error) {
	if e.err != nil {
		return nil, e.err
	}
	return e.artifact, nil
}

// helper: 创建任务并推进到指定状态
func setupTaskAtStatus(t *testing.T, store *Store, userID string, status TaskStatus) *Task {
	t.Helper()
	task, err := store.CreateTask(context.Background(), CreateTaskInput{
		Title:       "Orchestrator Test Task",
		Description: "Test description",
		StyleSlug:   "yinyue",
		TokenBudget: 100000,
	}, userID)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	// Manually set status for test setup
	assignee := defaultAssignee(status)
	_, err = store.db.ExecContext(context.Background(), `
		UPDATE editorial_tasks SET status = $2, assignee_type = $3 WHERE id = $1
	`, task.ID, status, assignee)
	if err != nil {
		t.Fatalf("failed to set task status to %s: %v", status, err)
	}
	task.Status = status
	task.AssigneeType = assignee
	return task
}

// ─── routeAfterResearch ────────────────────────────────────

// Test_RouteAfterResearch_HighQualityAutoAdvance:
// 研究质量充分 (>=3 sources, 0 gaps) → 自动推进到 writing
func Test_RouteAfterResearch_HighQualityAutoAdvance(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	userID := testUser(t, testDB)

	emitter := &mockEmitter{}
	orch := NewOrchestrator(store, emitter)

	task := setupTaskAtStatus(t, store, userID, StatusResearch)

	// 创建高质量研究简报
	briefJSON, _ := json.Marshal(map[string]interface{}{
		"summary": "High quality research",
		"sources": []map[string]string{
			{"url": "https://a.com", "source": "A", "relevance": "high"},
			{"url": "https://b.com", "source": "B", "relevance": "high"},
			{"url": "https://c.com", "source": "C", "relevance": "medium"},
		},
		"claims": []map[string]interface{}{
			{"claim": "fact1", "status": "supported"},
			{"claim": "fact2", "status": "supported"},
			{"claim": "fact3", "status": "verified", "verified": true},
		},
		"gaps": []string{},
	})

	artifact := &Artifact{
		ID:      "art-research-1",
		TaskID:  task.ID,
		Type:    ArtifactResearchBrief,
		Content: string(briefJSON),
		Status:  ArtifactStatusSubmitted,
	}

	// 记录一个 Event（模拟 Agent 完成事件）
	event, err := store.RecordAgentRunEvent(ctx, AgentRunEvent{
		TaskID:    task.ID,
		Type:      EventAgentRunCompleted,
		AgentRole: RoleResearch,
		Status:    "completed",
	})
	if err != nil {
		t.Fatalf("RecordAgentRunEvent failed: %v", err)
	}

	// 执行路由
	orch.routeAfterResearch(ctx, task, artifact, event)

	// 验证任务推进到 writing
	updatedTask, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if updatedTask.Status != StatusWriting {
		t.Errorf("expected status=writing, got %s", updatedTask.Status)
	}

	// 验证发射了 status_changed 事件
	if !emitter.hasEvent("task.status_changed") {
		t.Error("expected task.status_changed event")
	}
}

// Test_RouteAfterResearch_MediumQualityCreatesPendingDecision:
// 研究质量尚可 (2 sources, 1 gap) → 创建 pending decision 等待人类确认
func Test_RouteAfterResearch_MediumQualityCreatesPendingDecision(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	userID := testUser(t, testDB)

	emitter := &mockEmitter{}
	orch := NewOrchestrator(store, emitter)

	task := setupTaskAtStatus(t, store, userID, StatusResearch)

	briefJSON, _ := json.Marshal(map[string]interface{}{
		"summary": "Medium quality research",
		"sources": []map[string]string{
			{"url": "https://a.com", "source": "A", "relevance": "high"},
			{"url": "https://b.com", "source": "B", "relevance": "medium"},
		},
		"claims": []map[string]interface{}{
			{"claim": "fact1", "status": "supported"},
		},
		"gaps": []string{"missing context"},
	})

	artifact := &Artifact{
		ID:      "art-research-2",
		TaskID:  task.ID,
		Type:    ArtifactResearchBrief,
		Content: string(briefJSON),
		Status:  ArtifactStatusSubmitted,
	}

	event, err := store.RecordAgentRunEvent(ctx, AgentRunEvent{
		TaskID:    task.ID,
		Type:      EventAgentRunCompleted,
		AgentRole: RoleResearch,
		Status:    "completed",
	})
	if err != nil {
		t.Fatalf("RecordAgentRunEvent failed: %v", err)
	}

	// 执行路由
	orch.routeAfterResearch(ctx, task, artifact, event)

	// 验证创建了 pending decision
	decisions, err := store.ListDecisions(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListDecisions failed: %v", err)
	}
	var pendingFound bool
	for _, d := range decisions {
		if d.Status == DecisionStatusPending && d.Type == DecisionSelectAngle {
			pendingFound = true
			if d.ApproveTargetStatus != string(StatusWriting) {
				t.Errorf("expected approve_target=writing, got %s", d.ApproveTargetStatus)
			}
			if d.RejectTargetStatus != string(StatusResearch) {
				t.Errorf("expected reject_target=research, got %s", d.RejectTargetStatus)
			}
		}
	}
	if !pendingFound {
		t.Error("expected pending DecisionSelectAngle to be created")
	}

	// 验证任务状态没变（仍在 research）
	updatedTask, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if updatedTask.Status != StatusResearch {
		t.Errorf("expected task to stay at research, got %s", updatedTask.Status)
	}

	// 验证发射了 decision.required 事件
	if !emitter.hasEvent("decision.required") {
		t.Error("expected decision.required event")
	}
}

// Test_RouteAfterResearch_LowQualityRetries:
// 研究质量不足 (<2 sources) → 重跑研究 Agent
func Test_RouteAfterResearch_LowQualityRetries(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	userID := testUser(t, testDB)

	emitter := &mockEmitter{}
	orch := NewOrchestrator(store, emitter)
	// Register a no-op executor so the retry doesn't fail
	orch.RegisterExecutor(&noOpExecutor{role: RoleResearch})

	task := setupTaskAtStatus(t, store, userID, StatusResearch)

	briefJSON, _ := json.Marshal(map[string]interface{}{
		"summary": "Low quality research",
		"sources": []map[string]string{
			{"url": "https://a.com", "source": "A", "relevance": "low"},
		},
		"claims": []map[string]interface{}{
			{"claim": "fact1", "status": "unknown"},
		},
		"gaps": []string{"gap1", "gap2", "gap3"},
	})

	artifact := &Artifact{
		ID:      "art-research-3",
		TaskID:  task.ID,
		Type:    ArtifactResearchBrief,
		Content: string(briefJSON),
		Status:  ArtifactStatusSubmitted,
	}

	event, err := store.RecordAgentRunEvent(ctx, AgentRunEvent{
		TaskID:    task.ID,
		Type:      EventAgentRunCompleted,
		AgentRole: RoleResearch,
		Status:    "completed",
	})
	if err != nil {
		t.Fatalf("RecordAgentRunEvent failed: %v", err)
	}

	// 执行路由 — should retry (call runResearchAgent internally)
	// The noOpExecutor will be called, acquire a lease, and complete async.
	// We can't easily test the async result, but we can verify no crash.
	orch.routeAfterResearch(ctx, task, artifact, event)

	// Give the goroutine a moment to run
	time.Sleep(100 * time.Millisecond)

	// 验证没有创建 pending decision (low quality retries, doesn't ask human)
	decisions, _ := store.ListDecisions(ctx, task.ID)
	for _, d := range decisions {
		if d.Status == DecisionStatusPending {
			t.Error("should not create pending decision for low quality (should retry)")
		}
	}
}

// ─── routeAfterWriting ─────────────────────────────────────

// Test_RouteAfterWriting_HighQualityAutoAdvance:
// 初稿质量充分 (>=500 words, >=2 sections) → 自动推进到 review
func Test_RouteAfterWriting_HighQualityAutoAdvance(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	userID := testUser(t, testDB)

	emitter := &mockEmitter{}
	orch := NewOrchestrator(store, emitter)

	task := setupTaskAtStatus(t, store, userID, StatusWriting)

	draftJSON, _ := json.Marshal(map[string]interface{}{
		"title":      "Test Article",
		"content":    "Long content here...",
		"word_count": 800,
		"outline": []map[string]string{
			{"section": "引言"},
			{"section": "正文"},
			{"section": "结论"},
		},
	})

	artifact := &Artifact{
		ID:      "art-draft-1",
		TaskID:  task.ID,
		Type:    ArtifactDraft,
		Content: string(draftJSON),
		Status:  ArtifactStatusSubmitted,
	}

	event, err := store.RecordAgentRunEvent(ctx, AgentRunEvent{
		TaskID:    task.ID,
		Type:      EventAgentRunCompleted,
		AgentRole: RoleWriting,
		Status:    "completed",
	})
	if err != nil {
		t.Fatalf("RecordAgentRunEvent failed: %v", err)
	}

	orch.routeAfterWriting(ctx, task, artifact, event)

	// 验证推进到 review
	updatedTask, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if updatedTask.Status != StatusReview {
		t.Errorf("expected status=review, got %s", updatedTask.Status)
	}
}

// Test_RouteAfterWriting_ShortDraftCreatesPendingDecision:
// 初稿偏短 (word_count > 0 but < 500) → 创建 pending decision
func Test_RouteAfterWriting_ShortDraftCreatesPendingDecision(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	userID := testUser(t, testDB)

	emitter := &mockEmitter{}
	orch := NewOrchestrator(store, emitter)

	task := setupTaskAtStatus(t, store, userID, StatusWriting)

	draftJSON, _ := json.Marshal(map[string]interface{}{
		"title":      "Short Article",
		"content":    "Short...",
		"word_count": 200,
		"outline": []map[string]string{
			{"section": "only one section"},
		},
	})

	artifact := &Artifact{
		ID:      "art-draft-2",
		TaskID:  task.ID,
		Type:    ArtifactDraft,
		Content: string(draftJSON),
		Status:  ArtifactStatusSubmitted,
	}

	event, err := store.RecordAgentRunEvent(ctx, AgentRunEvent{
		TaskID:    task.ID,
		Type:      EventAgentRunCompleted,
		AgentRole: RoleWriting,
		Status:    "completed",
	})
	if err != nil {
		t.Fatalf("RecordAgentRunEvent failed: %v", err)
	}

	orch.routeAfterWriting(ctx, task, artifact, event)

	// 验证创建了 pending decision
	decisions, err := store.ListDecisions(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListDecisions failed: %v", err)
	}
	var pendingFound bool
	for _, d := range decisions {
		if d.Status == DecisionStatusPending && d.Type == DecisionAllowRewrite {
			pendingFound = true
		}
	}
	if !pendingFound {
		t.Error("expected pending DecisionAllowRewrite to be created")
	}

	// 验证任务状态没变
	updatedTask, _ := store.GetTask(ctx, task.ID)
	if updatedTask.Status != StatusWriting {
		t.Errorf("expected task to stay at writing, got %s", updatedTask.Status)
	}
}

// ─── transitionAfterEvent ──────────────────────────────────

// Test_TransitionAfterEvent_ValidTransition:
// Event 驱动的合法状态转换 (research → writing)
func Test_TransitionAfterEvent_ValidTransition(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	userID := testUser(t, testDB)

	emitter := &mockEmitter{}
	orch := NewOrchestrator(store, emitter)

	// Register a no-op writing executor so transitionAfterEvent doesn't fail
	orch.RegisterExecutor(&noOpExecutor{role: RoleWriting})

	task := setupTaskAtStatus(t, store, userID, StatusResearch)

	// Record an event
	event, err := store.RecordAgentRunEvent(ctx, AgentRunEvent{
		TaskID:    task.ID,
		Type:      EventAgentRunCompleted,
		AgentRole: RoleResearch,
		Status:    "completed",
	})
	if err != nil {
		t.Fatalf("RecordAgentRunEvent failed: %v", err)
	}

	// Transition research → writing via event
	err = orch.transitionAfterEvent(ctx, task, event.ID, StatusWriting, "research complete")
	if err != nil {
		t.Fatalf("transitionAfterEvent failed: %v", err)
	}

	// Verify task is now at writing
	updatedTask, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if updatedTask.Status != StatusWriting {
		t.Errorf("expected status=writing, got %s", updatedTask.Status)
	}
}

// Test_TransitionAfterEvent_InvalidTransition:
// Event 驱动的非法状态转换应返回错误
func Test_TransitionAfterEvent_InvalidTransition(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	userID := testUser(t, testDB)

	emitter := &mockEmitter{}
	orch := NewOrchestrator(store, emitter)

	task := setupTaskAtStatus(t, store, userID, StatusDraft)

	event, err := store.RecordAgentRunEvent(ctx, AgentRunEvent{
		TaskID:    task.ID,
		Type:      EventAgentRunCompleted,
		AgentRole: RoleResearch,
		Status:    "completed",
	})
	if err != nil {
		t.Fatalf("RecordAgentRunEvent failed: %v", err)
	}

	// draft → writing is not a valid transition
	err = orch.transitionAfterEvent(ctx, task, event.ID, StatusWriting, "invalid")
	if err == nil {
		t.Error("expected error for invalid transition draft→writing")
	}
}

// Test_TransitionAfterEvent_StatusConflict:
// 如果任务状态已被其他操作改变，TransitionTask 的乐观锁应拒绝
func Test_TransitionAfterEvent_StatusConflict(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	userID := testUser(t, testDB)

	emitter := &mockEmitter{}
	orch := NewOrchestrator(store, emitter)

	task := setupTaskAtStatus(t, store, userID, StatusResearch)

	event, err := store.RecordAgentRunEvent(ctx, AgentRunEvent{
		TaskID:    task.ID,
		Type:      EventAgentRunCompleted,
		AgentRole: RoleResearch,
		Status:    "completed",
	})
	if err != nil {
		t.Fatalf("RecordAgentRunEvent failed: %v", err)
	}

	// Simulate another process changing the status before transitionAfterEvent runs
	_, err = store.db.ExecContext(ctx, `
		UPDATE editorial_tasks SET status = 'pending_approval' WHERE id = $1
	`, task.ID)
	if err != nil {
		t.Fatalf("failed to change task status: %v", err)
	}

	// Now transitionAfterEvent should fail because task.Status (research) doesn't match DB (pending_approval)
	err = orch.transitionAfterEvent(ctx, task, event.ID, StatusWriting, "should conflict")
	if err == nil {
		t.Error("expected status conflict error")
	}
}

// ─── handleReviewResult ────────────────────────────────────

// Test_HandleReviewResult_Passed:
// 审校通过 (passed=true) → 推进到 pending_publish
func Test_HandleReviewResult_Passed(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	userID := testUser(t, testDB)

	emitter := &mockEmitter{}
	orch := NewOrchestrator(store, emitter)

	task := setupTaskAtStatus(t, store, userID, StatusReview)

	reportJSON, _ := json.Marshal(map[string]interface{}{
		"passed":   true,
		"severity": "low",
	})

	artifact := &Artifact{
		ID:      "art-review-1",
		TaskID:  task.ID,
		Type:    ArtifactReviewReport,
		Content: string(reportJSON),
		Status:  ArtifactStatusSubmitted,
	}

	event, err := store.RecordAgentRunEvent(ctx, AgentRunEvent{
		TaskID:    task.ID,
		Type:      EventAgentRunCompleted,
		AgentRole: RoleReview,
		Status:    "completed",
	})
	if err != nil {
		t.Fatalf("RecordAgentRunEvent failed: %v", err)
	}

	orch.handleReviewResult(ctx, task, artifact, event)

	updatedTask, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if updatedTask.Status != StatusPendingPublish {
		t.Errorf("expected status=pending_publish, got %s", updatedTask.Status)
	}
}

// Test_HandleReviewResult_MediumSeverity_BackToWriting:
// 审校发现 medium 问题 → 退回写作
func Test_HandleReviewResult_MediumSeverity_BackToWriting(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	userID := testUser(t, testDB)

	emitter := &mockEmitter{}
	orch := NewOrchestrator(store, emitter)

	task := setupTaskAtStatus(t, store, userID, StatusReview)

	reportJSON, _ := json.Marshal(map[string]interface{}{
		"passed":   false,
		"severity": "medium",
	})

	artifact := &Artifact{
		ID:      "art-review-2",
		TaskID:  task.ID,
		Type:    ArtifactReviewReport,
		Content: string(reportJSON),
		Status:  ArtifactStatusSubmitted,
	}

	event, err := store.RecordAgentRunEvent(ctx, AgentRunEvent{
		TaskID:    task.ID,
		Type:      EventAgentRunCompleted,
		AgentRole: RoleReview,
		Status:    "completed",
	})
	if err != nil {
		t.Fatalf("RecordAgentRunEvent failed: %v", err)
	}

	orch.handleReviewResult(ctx, task, artifact, event)

	updatedTask, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if updatedTask.Status != StatusWriting {
		t.Errorf("expected status=writing (sent back), got %s", updatedTask.Status)
	}
}

// Test_HandleReviewResult_HighSeverity_Escalate:
// 审校发现 high 问题 → 升级到 pending_approval
func Test_HandleReviewResult_HighSeverity_Escalate(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	userID := testUser(t, testDB)

	emitter := &mockEmitter{}
	orch := NewOrchestrator(store, emitter)

	task := setupTaskAtStatus(t, store, userID, StatusReview)

	reportJSON, _ := json.Marshal(map[string]interface{}{
		"passed":   false,
		"severity": "high",
	})

	artifact := &Artifact{
		ID:      "art-review-3",
		TaskID:  task.ID,
		Type:    ArtifactReviewReport,
		Content: string(reportJSON),
		Status:  ArtifactStatusSubmitted,
	}

	event, err := store.RecordAgentRunEvent(ctx, AgentRunEvent{
		TaskID:    task.ID,
		Type:      EventAgentRunCompleted,
		AgentRole: RoleReview,
		Status:    "completed",
	})
	if err != nil {
		t.Fatalf("RecordAgentRunEvent failed: %v", err)
	}

	orch.handleReviewResult(ctx, task, artifact, event)

	updatedTask, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if updatedTask.Status != StatusPendingApproval {
		t.Errorf("expected status=pending_approval (escalated), got %s", updatedTask.Status)
	}
}

// Test_HandleReviewResult_InvalidJSON_BackToWriting:
// 审校报告解析失败 → 退回写作
func Test_HandleReviewResult_InvalidJSON_BackToWriting(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	userID := testUser(t, testDB)

	emitter := &mockEmitter{}
	orch := NewOrchestrator(store, emitter)

	task := setupTaskAtStatus(t, store, userID, StatusReview)

	artifact := &Artifact{
		ID:      "art-review-bad",
		TaskID:  task.ID,
		Type:    ArtifactReviewReport,
		Content: "this is not valid json",
		Status:  ArtifactStatusSubmitted,
	}

	event, err := store.RecordAgentRunEvent(ctx, AgentRunEvent{
		TaskID:    task.ID,
		Type:      EventAgentRunCompleted,
		AgentRole: RoleReview,
		Status:    "completed",
	})
	if err != nil {
		t.Fatalf("RecordAgentRunEvent failed: %v", err)
	}

	orch.handleReviewResult(ctx, task, artifact, event)

	updatedTask, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if updatedTask.Status != StatusWriting {
		t.Errorf("expected status=writing (fallback for bad JSON), got %s", updatedTask.Status)
	}
}

// ─── ResolveDecisionTx integration with routing ───────────

// Test_ResolveDecision_SelectAngleApproved_AdvancesToWriting:
// 完整的 select_angle 决策流程：创建 pending → 人类批准 → 任务推进到 writing
func Test_ResolveDecision_SelectAngleApproved_AdvancesToWriting(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	userID := testUser(t, testDB)

	task := setupTaskAtStatus(t, store, userID, StatusResearch)

	// 创建 pending select_angle decision
	d, err := store.CreateDecision(ctx, CreateDecisionInput{
		Type:                DecisionSelectAngle,
		Actor:               NewSystemActor("system"),
		Status:              DecisionStatusPending,
		Rationale:           "Research has 2 sources and 1 gap, need human confirmation",
		ApproveTargetStatus: StatusWriting,
		RejectTargetStatus:  StatusResearch,
	}, task.ID)
	if err != nil {
		t.Fatalf("CreateDecision failed: %v", err)
	}

	// 人类批准
	resolved, nextStatus, err := store.ResolveDecisionTx(ctx, ResolveDecisionTxParams{
		DecisionID: d.ID,
		Status:     DecisionStatusApproved,
		Rationale:  "Looks good, proceed to writing",
		DecidedBy:  userID,
	})
	if err != nil {
		t.Fatalf("ResolveDecisionTx failed: %v", err)
	}
	if resolved.Status != DecisionStatusApproved {
		t.Errorf("expected resolved status=approved, got %s", resolved.Status)
	}
	if nextStatus != StatusWriting {
		t.Errorf("expected nextStatus=writing, got %s", nextStatus)
	}

	// 验证任务已推进
	updatedTask, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if updatedTask.Status != StatusWriting {
		t.Errorf("expected task status=writing, got %s", updatedTask.Status)
	}
}

// Test_ResolveDecision_SelectAngleRejected_StaysAtResearch:
// 人类驳回 select_angle → 任务退回 research（保持原状态重跑）
func Test_ResolveDecision_SelectAngleRejected_StaysAtResearch(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	userID := testUser(t, testDB)

	task := setupTaskAtStatus(t, store, userID, StatusResearch)

	d, err := store.CreateDecision(ctx, CreateDecisionInput{
		Type:                DecisionSelectAngle,
		Actor:               NewSystemActor("system"),
		Status:              DecisionStatusPending,
		Rationale:           "Need human confirmation",
		ApproveTargetStatus: StatusWriting,
		RejectTargetStatus:  StatusResearch, // Reject stays at research (retry)
	}, task.ID)
	if err != nil {
		t.Fatalf("CreateDecision failed: %v", err)
	}

	resolved, nextStatus, err := store.ResolveDecisionTx(ctx, ResolveDecisionTxParams{
		DecisionID: d.ID,
		Status:     DecisionStatusRejected,
		Rationale:  "Need more research",
		DecidedBy:  userID,
	})
	if err != nil {
		t.Fatalf("ResolveDecisionTx failed: %v", err)
	}
	if resolved.Status != DecisionStatusRejected {
		t.Errorf("expected rejected, got %s", resolved.Status)
	}
	if nextStatus != StatusResearch {
		t.Errorf("expected nextStatus=research, got %s", nextStatus)
	}

	// Task should still be at research (same status, no transition)
	updatedTask, _ := store.GetTask(ctx, task.ID)
	if updatedTask.Status != StatusResearch {
		t.Errorf("expected task at research, got %s", updatedTask.Status)
	}
}

// Test_ResolveDecision_AlreadyResolved_ReturnsError:
// 不能对已处理的 decision 再次 resolve
func Test_ResolveDecision_AlreadyResolved_ReturnsError(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	userID := testUser(t, testDB)

	task := setupTaskAtStatus(t, store, userID, StatusResearch)

	d, err := store.CreateDecision(ctx, CreateDecisionInput{
		Type:                DecisionSelectAngle,
		Actor:               NewSystemActor("system"),
		Status:              DecisionStatusPending,
		Rationale:           "test",
		ApproveTargetStatus: StatusWriting,
		RejectTargetStatus:  StatusResearch,
	}, task.ID)
	if err != nil {
		t.Fatalf("CreateDecision failed: %v", err)
	}

	// First resolve — should succeed
	_, _, err = store.ResolveDecisionTx(ctx, ResolveDecisionTxParams{
		DecisionID: d.ID,
		Status:     DecisionStatusApproved,
		Rationale:  "ok",
		DecidedBy:  userID,
	})
	if err != nil {
		t.Fatalf("first ResolveDecisionTx failed: %v", err)
	}

	// Second resolve — should fail (decision no longer pending)
	_, _, err = store.ResolveDecisionTx(ctx, ResolveDecisionTxParams{
		DecisionID: d.ID,
		Status:     DecisionStatusRejected,
		Rationale:  "try again",
		DecidedBy:  userID,
	})
	if err == nil {
		t.Error("expected error when resolving already-resolved decision")
	}
}

// ─── Agent Run Events ─────────────────────────────────────

// Test_RecordAndListAgentRunEvents:
// 记录 Agent 事件并按时间顺序列出
func Test_RecordAndListAgentRunEvents(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	userID := testUser(t, testDB)

	task := setupTaskAtStatus(t, store, userID, StatusResearch)

	// Record multiple events
	event1, err := store.RecordAgentRunEvent(ctx, AgentRunEvent{
		TaskID:    task.ID,
		Type:      EventAgentRunCompleted,
		AgentRole: RoleResearch,
		Status:    "completed",
	})
	if err != nil {
		t.Fatalf("RecordAgentRunEvent 1 failed: %v", err)
	}

	event2, err := store.RecordAgentRunEvent(ctx, AgentRunEvent{
		TaskID:    task.ID,
		Type:      EventAgentRunCompleted,
		AgentRole: RoleWriting,
		Status:    "completed",
	})
	if err != nil {
		t.Fatalf("RecordAgentRunEvent 2 failed: %v", err)
	}

	// List events
	events, err := store.ListAgentRunEvents(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListAgentRunEvents failed: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	// Verify ordering (oldest first)
	if events[0].ID != event1.ID {
		t.Errorf("expected first event = %s, got %s", event1.ID, events[0].ID)
	}
	if events[1].ID != event2.ID {
		t.Errorf("expected second event = %s, got %s", event2.ID, events[1].ID)
	}

	// Verify fields
	if events[1].AgentRole != RoleWriting {
		t.Errorf("expected agent_role=writing_agent, got %s", events[1].AgentRole)
	}
}

// ─── Lease lifecycle ───────────────────────────────────────

// Test_LeaseLifecycle_AcquireCheckRelease:
// 完整的 lease 生命周期
func Test_LeaseLifecycle_AcquireCheckRelease(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	userID := testUser(t, testDB)

	task := setupTaskAtStatus(t, store, userID, StatusResearch)

	// Initially no active lease
	has, err := store.HasActiveLease(ctx, task.ID, RoleResearch)
	if err != nil {
		t.Fatalf("HasActiveLease failed: %v", err)
	}
	if has {
		t.Error("expected no active lease initially")
	}

	// Acquire lease
	err = store.AcquireLease(ctx, task.ID, RoleResearch, 10*time.Minute)
	if err != nil {
		t.Fatalf("AcquireLease failed: %v", err)
	}

	// Now has active lease
	has, _ = store.HasActiveLease(ctx, task.ID, RoleResearch)
	if !has {
		t.Error("expected active lease after acquire")
	}

	// Cannot acquire again
	err = store.AcquireLease(ctx, task.ID, RoleResearch, 10*time.Minute)
	if err != ErrLeaseConflict {
		t.Errorf("expected ErrLeaseConflict, got %v", err)
	}

	// Different role can acquire
	err = store.AcquireLease(ctx, task.ID, RoleWriting, 10*time.Minute)
	if err != nil {
		t.Errorf("AcquireLease for different role failed: %v", err)
	}

	// Release research lease
	err = store.ReleaseLease(ctx, task.ID, RoleResearch, "completed")
	if err != nil {
		t.Fatalf("ReleaseLease failed: %v", err)
	}

	// Research lease no longer active
	has, _ = store.HasActiveLease(ctx, task.ID, RoleResearch)
	if has {
		t.Error("expected no active research lease after release")
	}

	// Writing lease still active
	has, _ = store.HasActiveLease(ctx, task.ID, RoleWriting)
	if !has {
		t.Error("expected writing lease still active")
	}
}

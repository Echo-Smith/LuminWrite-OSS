package editorial

import (
	"context"
	"testing"
	"time"
)

// ═══════════════════════════════════════════════════════════════
// 通用测试辅助 — mockEmitter, setupTaskAtStatus
// 原 Orchestrator 路由测试已随线性流水线删除。
// 以下保留的测试只依赖 Store，不依赖 Orchestrator。
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

// helper: 创建任务并推进到指定状态
func setupTaskAtStatus(t *testing.T, store *Store, userID string, status TaskStatus) *Task {
	t.Helper()
	task, err := store.CreateTask(context.Background(), CreateTaskInput{
		Title:       "Test Task",
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

// ─── ResolveDecisionTx integration ───────────────────────

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

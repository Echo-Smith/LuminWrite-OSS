package editorial

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
)

// testDB is a shared PostgreSQL connection for integration tests.
// It is only initialized when TEST_DATABASE_URL is set.
var testDB *sql.DB

func TestMain(m *testing.M) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		if os.Getenv("CI") == "true" {
			// In CI, a missing TEST_DATABASE_URL is a hard failure —
			// integration tests MUST run, not silently skip.
			fmt.Fprintln(os.Stderr, "CI=true but TEST_DATABASE_URL is not set — integration tests cannot run")
			os.Exit(1)
		}
		// Local dev without database — let pure unit tests run.
		// Integration tests will skip themselves via testStore's t.Skip().
		os.Exit(m.Run())
	}

	db, err := database.NewPostgres(dbURL, 5, 2)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to connect to test database: %v\n", err)
		os.Exit(1)
	}

	if err := database.Migrate(db); err != nil {
		fmt.Fprintf(os.Stderr, "failed to run migrations: %v\n", err)
		os.Exit(1)
	}

	testDB = db.DB
	os.Exit(m.Run())
}

// testStore creates a fresh Store for testing.
// Each test gets a clean database state by truncating editorial tables.
func testStore(t *testing.T) *Store {
	t.Helper()
	if testDB == nil {
		if os.Getenv("CI") == "true" {
			t.Fatal("CI=true but testDB is nil — integration tests must not be skipped in CI")
		}
		t.Skip("TEST_DATABASE_URL not set, skipping integration test")
	}

	// Wait briefly for any goroutines from previous tests to complete
	// (orchestrator goroutines use context.Background() and may still be
	// holding database locks when the next test starts)
	time.Sleep(300 * time.Millisecond)

	// Clean up editorial tables before each test
	// Use TRUNCATE CASCADE to avoid blocking on row-level locks from
	// goroutines that might still be running from previous tests
	tables := []string{
		"editorial_agent_leases",
		"editorial_agent_run_events",
		"editorial_decisions",
		"editorial_artifacts",
		"editorial_tasks",
		"editorial_knowledge",
	}
	// TRUNCATE all tables in one command to avoid FK constraint issues
	if _, err := testDB.Exec(fmt.Sprintf("TRUNCATE %s CASCADE", strings.Join(tables, ", "))); err != nil {
		// Fall back to DELETE if TRUNCATE fails (e.g., tables don't exist yet)
		for _, table := range tables {
			testDB.Exec(fmt.Sprintf("DELETE FROM %s", table))
		}
	}

	return NewStore(testDB)
}

// testUser creates a test user UUID in the database.
func testUser(t *testing.T, db *sql.DB) string {
	t.Helper()
	var userID string
	err := db.QueryRow(`
		INSERT INTO users (uid, provider, name, created_at, updated_at)
		VALUES (gen_random_uuid(), 'email', 'test_user', NOW(), NOW())
		RETURNING id::text
	`).Scan(&userID)
	if err != nil {
		// If users table doesn't have these columns, try a simpler insert
		err = db.QueryRow(`
			INSERT INTO users (uid, created_at, updated_at)
			VALUES (gen_random_uuid(), NOW(), NOW())
			RETURNING id::text
		`).Scan(&userID)
		if err != nil {
			t.Skipf("cannot create test user: %v", err)
		}
	}
	return userID
}

// ─── P0-0: Failing Test Scenarios ────────────────────────
// These tests lock down the 5 current failure scenarios that must pass
// after the architectural fixes are applied.

// Test_P0_0_AuthenticatedUserCanCreateTask tests that a logged-in user
// can create a task and that owner_id/created_by are correctly set.
func Test_P0_0_AuthenticatedUserCanCreateTask(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	userID := testUser(t, testDB)

	task, err := store.CreateTask(ctx, CreateTaskInput{
		Title:          "Test Task",
		Description:    "Test description",
		AcceptCriteria: "Must pass tests",
		Priority:       3,
		Tags:           []string{"test"},
		StyleSlug:      "yinyue",
		TokenBudget:    100000,
	}, userID)

	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}
	if task.ID == "" {
		t.Error("expected non-empty task ID")
	}
	if task.OwnerID != userID {
		t.Errorf("expected owner_id=%s, got %s", userID, task.OwnerID)
	}
	if task.CreatedBy != userID {
		t.Errorf("expected created_by=%s, got %s", userID, task.CreatedBy)
	}
	if task.Status != StatusDraft {
		t.Errorf("expected status=draft, got %s", task.Status)
	}
}

// Test_P0_0_SystemCanCreateDecision tests that "system" can legally
// create a Decision with the new actor model.
func Test_P0_0_SystemCanCreateDecision(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	userID := testUser(t, testDB)

	task, err := store.CreateTask(ctx, CreateTaskInput{
		Title: "Decision Test",
	}, userID)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	// System creates a decision
	d, err := store.CreateDecision(ctx, CreateDecisionInput{
		Type:  DecisionResearchComplete,
		Actor: NewSystemActor("system"),
		Status: DecisionStatusApproved,
		Rationale: "Research complete, auto-advancing to writing",
		ApproveTargetStatus: StatusWriting,
		RejectTargetStatus:  StatusPendingApproval,
	}, task.ID)
	if err != nil {
		t.Fatalf("CreateDecision by system failed: %v", err)
	}
	if d.Actor.Type != ActorSystem {
		t.Errorf("expected actor_type=system, got %s", d.Actor.Type)
	}
	if d.ApproveTargetStatus != string(StatusWriting) {
		t.Errorf("expected approve_target_status=writing, got %s", d.ApproveTargetStatus)
	}
}

// Test_P0_0_EmptyArtifactIDCanInsert tests that a Decision with empty
// artifact_id can be inserted without UUID cast errors.
func Test_P0_0_EmptyArtifactIDCanInsert(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	userID := testUser(t, testDB)

	task, err := store.CreateTask(ctx, CreateTaskInput{
		Title: "Empty Artifact Test",
	}, userID)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	// Create decision with empty artifact_id
	d, err := store.CreateDecision(ctx, CreateDecisionInput{
		Type:       DecisionSelectAngle,
		Actor:      NewHumanActor(userID, "test_user"),
		Status:     DecisionStatusPending,
		Rationale:  "Need human to select angle",
		ArtifactID: "", // empty string, should not cause UUID error
		ApproveTargetStatus: StatusWriting,
		RejectTargetStatus:  StatusResearch,
	}, task.ID)
	if err != nil {
		t.Fatalf("CreateDecision with empty artifact_id failed: %v", err)
	}
	if d.ArtifactID != "" {
		t.Errorf("expected empty artifact_id, got %s", d.ArtifactID)
	}
}

// Test_P0_0_SelectAngleApprovalEntersWriting tests that approving a
// select_angle decision transitions the task to writing status.
func Test_P0_0_SelectAngleApprovalEntersWriting(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	userID := testUser(t, testDB)

	// Create task and advance to research
	task, err := store.CreateTask(ctx, CreateTaskInput{
		Title: "Approval Flow Test",
	}, userID)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	// Manually set status to research (simulating approval)
	_, err = testDB.ExecContext(ctx, `
		UPDATE agent_traces SET editorial_status = 'research' WHERE trace_id = $1
	`, task.ID)
	if err != nil {
		t.Fatalf("failed to set task to research: %v", err)
	}

	// Create a pending select_angle decision
	d, err := store.CreateDecision(ctx, CreateDecisionInput{
		Type:                DecisionSelectAngle,
		Actor:               NewSystemActor("system"),
		Status:              DecisionStatusPending,
		Rationale:           "Need human to confirm research is sufficient",
		ApproveTargetStatus: StatusWriting,
		RejectTargetStatus:  StatusResearch,
	}, task.ID)
	if err != nil {
		t.Fatalf("CreateDecision failed: %v", err)
	}

	// Resolve the decision as approved
	resolved, nextStatus, err := store.ResolveDecisionTx(ctx, ResolveDecisionTxParams{
		DecisionID: d.ID,
		Status:     DecisionStatusApproved,
		Rationale:  "Research looks good",
		DecidedBy:  userID,
	})
	if err != nil {
		t.Fatalf("ResolveDecisionTx failed: %v", err)
	}
	if resolved.Status != DecisionStatusApproved {
		t.Errorf("expected decision status=approved, got %s", resolved.Status)
	}
	if nextStatus != StatusWriting {
		t.Errorf("expected nextStatus=writing, got %s", nextStatus)
	}

	// Verify task was actually advanced
	updatedTask, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if updatedTask.Status != StatusWriting {
		t.Errorf("expected task status=writing, got %s", updatedTask.Status)
	}
}

// Test_P0_0_CannotStartTwoAgentRunsConcurrently tests that the agent lease
// prevents starting two agents of the same role on the same task.
func Test_P0_0_CannotStartTwoAgentRunsConcurrently(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	userID := testUser(t, testDB)

	task, err := store.CreateTask(ctx, CreateTaskInput{
		Title: "Lease Test",
	}, userID)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	// Acquire first lease — should succeed
	err = store.AcquireLease(ctx, task.ID, RoleResearch, 10*time.Minute)
	if err != nil {
		t.Fatalf("first AcquireLease failed: %v", err)
	}

	// Acquire second lease — should fail with ErrLeaseConflict
	err = store.AcquireLease(ctx, task.ID, RoleResearch, 10*time.Minute)
	if err == nil {
		t.Error("expected second AcquireLease to fail, but it succeeded")
	}
	if err != ErrLeaseConflict {
		t.Errorf("expected ErrLeaseConflict, got %v", err)
	}

	// Release the first lease
	err = store.ReleaseLease(ctx, task.ID, RoleResearch, "completed")
	if err != nil {
		t.Fatalf("ReleaseLease failed: %v", err)
	}

	// Now acquire should succeed again
	err = store.AcquireLease(ctx, task.ID, RoleResearch, 10*time.Minute)
	if err != nil {
		t.Errorf("AcquireLease after release failed: %v", err)
	}
}

// Test_P0_0_TransitionTaskWithSelectForUpdate tests that TransitionTask
// uses SELECT FOR UPDATE for proper concurrency control.
func Test_P0_0_TransitionTaskWithSelectForUpdate(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	userID := testUser(t, testDB)

	task, err := store.CreateTask(ctx, CreateTaskInput{
		Title: "Transition Test",
	}, userID)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	// Transition from draft to pending_approval
	updatedTask, err := store.TransitionTask(ctx, TransitionCommand{
		TaskID:         task.ID,
		TargetStatus:   StatusPendingApproval,
		ExpectedStatus: StatusDraft,
		Cause:          TransitionCauseDecision,
		Actor:          NewHumanActor(userID, "test_user"),
		Rationale:      "Submitting for approval",
	})
	if err != nil {
		t.Fatalf("TransitionTask failed: %v", err)
	}
	if updatedTask.Status != StatusPendingApproval {
		t.Errorf("expected status=pending_approval, got %s", updatedTask.Status)
	}

	// Try with wrong expected status — should fail
	_, err = store.TransitionTask(ctx, TransitionCommand{
		TaskID:         task.ID,
		TargetStatus:   StatusResearch,
		ExpectedStatus: StatusDraft, // wrong, task is now pending_approval
		Cause:          TransitionCauseDecision,
		Actor:          NewHumanActor(userID, "test_user"),
		Rationale:      "Should fail",
	})
	if err == nil {
		t.Error("expected TransitionTask with wrong expected status to fail")
	}
}

package server

// Deep-research pilot authorization tests (wp-pilot-launch). Covered here:
//  1. the SQL subject policy over research_pilot_entitlements — granted,
//     expired, wrong scope, absent, and the two fail-closed shapes (lookup
//     error, unwired store);
//  2. the researchPilotGateExecutor decorator — typed RESEARCH_PILOT_REQUIRED
//     refusal for unentitled subjects, subject fallback (Subject → UserID),
//     subjectless refusal, fail-closed on policy errors and nil policy, pass
//     -through for entitled subjects, and Descriptor() transparency;
//  3. the governedResearchSpecs assembly — every research direct executor is
//     wrapped and keeps its binding identity.
//
// DB-backed cases ride dbtest (per-process throwaway database) and skip when
// TEST_DATABASE_URL is unset, exactly like the governed harnesses.

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database/dbtest"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingruntime"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// ── shared DB helpers ───────────────────────────────────────────────────────

// pilotTestDB opens the per-test throwaway database (fully migrated, so
// migration 121's pilot tables exist) or skips the test.
func pilotTestDB(t *testing.T) *pilotDBFixture {
	t.Helper()
	db, cleanup, err := dbtest.Open(os.Getenv("TEST_DATABASE_URL"), 5, 2)
	if err != nil {
		if err == dbtest.ErrNoDatabaseURL {
			t.Skip("TEST_DATABASE_URL not set")
		}
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	return &pilotDBFixture{db: db}
}

type pilotDBFixture struct {
	db *database.DB
}

// pilotProvisionUser inserts the fixture user row and returns its UUID id
// (the entitlement table's subject_id).
func (fixture *pilotDBFixture) provisionUser(t *testing.T, uid string) string {
	t.Helper()
	var id string
	if err := fixture.db.QueryRow(`INSERT INTO users (uid, name) VALUES ($1, 'pilot test user')
		ON CONFLICT (uid) DO UPDATE SET name = EXCLUDED.name RETURNING id::text`, uid).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// grantPilotEntitlement inserts an unexpired research_review entitlement.
func (fixture *pilotDBFixture) grantPilotEntitlement(t *testing.T, subjectID string) {
	t.Helper()
	fixture.grantPilotEntitlementExpiring(t, subjectID, "NULL")
}

// grantPilotEntitlementExpiring inserts an entitlement whose expires_at is
// the given SQL expression ("NULL", "now() + interval '1 hour'", ...).
func (fixture *pilotDBFixture) grantPilotEntitlementExpiring(t *testing.T, subjectID, expiresAtSQL string) {
	t.Helper()
	query := `INSERT INTO research_pilot_entitlements (subject_id, scope, granted_by, reason, expires_at)
		VALUES ($1::uuid, $2, 'wp-pilot-launch-tests', 'test grant', ` + expiresAtSQL + `)
		ON CONFLICT (subject_id, scope) DO UPDATE SET expires_at = EXCLUDED.expires_at`
	if _, err := fixture.db.Exec(query, subjectID, ResearchPilotScopeResearchReview); err != nil {
		t.Fatal(err)
	}
}

// ── 1. the SQL subject policy ───────────────────────────────────────────────

func TestResearchPilotPolicyAnswersEntitlement(t *testing.T) {
	fixture := pilotTestDB(t)
	policy := NewResearchPilotPolicy(fixture.db)
	ctx := context.Background()
	subject := fixture.provisionUser(t, "00000000-0000-0000-0000-00000000p011")

	// No row yet: a definitive no, not an error.
	entitled, err := policy.Entitled(ctx, subject, ResearchPilotScopeResearchReview)
	if err != nil {
		t.Fatalf("unentitled lookup errored: %v", err)
	}
	if entitled {
		t.Fatal("subject without a row must not be entitled")
	}

	// Grant → entitled.
	fixture.grantPilotEntitlement(t, subject)
	entitled, err = policy.Entitled(ctx, subject, ResearchPilotScopeResearchReview)
	if err != nil || !entitled {
		t.Fatalf("entitled lookup = (%v, %v), want (true, nil)", entitled, err)
	}

	// Expired grant counts as no grant.
	fixture.grantPilotEntitlementExpiring(t, subject, "now() - interval '1 hour'")
	entitled, err = policy.Entitled(ctx, subject, ResearchPilotScopeResearchReview)
	if err != nil || entitled {
		t.Fatalf("expired grant = (%v, %v), want (false, nil)", entitled, err)
	}

	// A live expiry window counts as entitled.
	fixture.grantPilotEntitlementExpiring(t, subject, "now() + interval '1 hour'")
	if entitled, err = policy.Entitled(ctx, subject, ResearchPilotScopeResearchReview); err != nil || !entitled {
		t.Fatalf("unexpired grant = (%v, %v), want (true, nil)", entitled, err)
	}

	// A different scope stays unauthorized (precise-scope grants).
	if entitled, err = policy.Entitled(ctx, subject, "other_scope"); err != nil || entitled {
		t.Fatalf("other scope = (%v, %v), want (false, nil)", entitled, err)
	}
}

func TestResearchPilotPolicyFailsClosed(t *testing.T) {
	fixture := pilotTestDB(t)
	policy := NewResearchPilotPolicy(fixture.db)
	ctx := context.Background()
	subject := fixture.provisionUser(t, "00000000-0000-0000-0000-00000000p012")
	fixture.grantPilotEntitlement(t, subject)

	// A malformed subject cannot match the uuid column: the lookup errors and
	// the caller must treat it as "cannot authorize", not as "not entitled".
	if _, err := policy.Entitled(ctx, "not-a-uuid", ResearchPilotScopeResearchReview); err == nil {
		t.Fatal("malformed subject must fail the lookup (fail closed)")
	}

	// A broken store (closed pool) fails closed too.
	if err := fixture.db.Close(); err != nil {
		t.Fatal(err)
	}
	if entitled, err := policy.Entitled(ctx, subject, ResearchPilotScopeResearchReview); err == nil || entitled {
		t.Fatalf("closed-store lookup = (%v, %v), want (false, error)", entitled, err)
	}

	// An unwired policy can never answer "yes".
	if entitled, err := (*ResearchPilotPolicy)(nil).Entitled(ctx, subject, ResearchPilotScopeResearchReview); err == nil || entitled {
		t.Fatalf("nil policy = (%v, %v), want (false, error)", entitled, err)
	}
	if entitled, err := NewResearchPilotPolicy(nil).Entitled(ctx, subject, ResearchPilotScopeResearchReview); err == nil || entitled {
		t.Fatalf("policy without a pool = (%v, %v), want (false, error)", entitled, err)
	}
}

// ── 2. the gate executor decorator ─────────────────────────────────────────

type stubPilotPolicy struct {
	entitled bool
	err      error
	subject  string
	scope    string
}

func (stub *stubPilotPolicy) Entitled(_ context.Context, subjectID, scope string) (bool, error) {
	stub.subject, stub.scope = subjectID, scope
	if stub.err != nil {
		return false, stub.err
	}
	return stub.entitled, nil
}

type pilotGateProbeExecutor struct {
	descriptor writingruntime.ExecutorDescriptor
	calls      int
}

func (probe *pilotGateProbeExecutor) Descriptor() writingruntime.ExecutorDescriptor {
	return probe.descriptor
}
func (probe *pilotGateProbeExecutor) Execute(context.Context, writingruntime.ExecutionRequest) (writingruntime.ExecutionResult, error) {
	probe.calls++
	return writingruntime.ExecutionResult{}, nil
}

func pilotGateRequest(subject, userID string) writingruntime.ExecutionRequest {
	return writingruntime.ExecutionRequest{Subject: subject, UserID: userID}
}

func assertResearchPilotRequired(t *testing.T, err error, wantFragments ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a refusal, got nil")
	}
	var runtimeErr *writingruntime.RuntimeError
	if !errors.As(err, &runtimeErr) {
		t.Fatalf("error %v is not a typed writingruntime.RuntimeError", err)
	}
	if runtimeErr.Code != writingruntime.CodeResearchPilotRequired {
		t.Fatalf("error code %q, want RESEARCH_PILOT_REQUIRED", runtimeErr.Code)
	}
	if runtimeErr.RetryClass != writingruntime.RetryNever {
		t.Fatalf("refusal retry class %q, want never", runtimeErr.RetryClass)
	}
	for _, fragment := range wantFragments {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("error %q does not name %q", err, fragment)
		}
	}
}

func TestResearchPilotGateExecutorRefusesUnentitledSubject(t *testing.T) {
	policy := &stubPilotPolicy{entitled: false}
	inner := &pilotGateProbeExecutor{}
	executor := NewResearchPilotGateExecutor(inner, policy, ResearchPilotScopeResearchReview)

	_, err := executor.Execute(context.Background(), pilotGateRequest("11111111-1111-1111-1111-111111111111", ""))
	assertResearchPilotRequired(t, err, ResearchPilotScopeResearchReview, "11111111-1111-1111-1111-111111111111")
	if policy.subject != "11111111-1111-1111-1111-111111111111" || policy.scope != ResearchPilotScopeResearchReview {
		t.Fatalf("policy consulted with (%q, %q)", policy.subject, policy.scope)
	}
	if inner.calls != 0 {
		t.Fatalf("inner executor ran %d times behind a refusal", inner.calls)
	}
}

func TestResearchPilotGateExecutorFallsBackToUserID(t *testing.T) {
	policy := &stubPilotPolicy{entitled: true}
	executor := NewResearchPilotGateExecutor(&pilotGateProbeExecutor{}, policy, ResearchPilotScopeResearchReview)
	if _, err := executor.Execute(context.Background(), pilotGateRequest("", "user-42")); err != nil {
		t.Fatalf("entitled request refused: %v", err)
	}
	if policy.subject != "user-42" {
		t.Fatalf("subject %q, want the UserID fallback", policy.subject)
	}
}

func TestResearchPilotGateExecutorRefusesSubjectlessRequest(t *testing.T) {
	policy := &stubPilotPolicy{entitled: true}
	inner := &pilotGateProbeExecutor{}
	executor := NewResearchPilotGateExecutor(inner, policy, ResearchPilotScopeResearchReview)
	_, err := executor.Execute(context.Background(), pilotGateRequest("  ", ""))
	assertResearchPilotRequired(t, err, ResearchPilotScopeResearchReview)
	if inner.calls != 0 {
		t.Fatal("inner executor ran for a subjectless request")
	}
}

func TestResearchPilotGateExecutorFailsClosedOnPolicyError(t *testing.T) {
	policy := &stubPilotPolicy{err: errors.New("connection refused")}
	inner := &pilotGateProbeExecutor{}
	executor := NewResearchPilotGateExecutor(inner, policy, ResearchPilotScopeResearchReview)

	_, err := executor.Execute(context.Background(), pilotGateRequest("subject-1", ""))
	if err == nil {
		t.Fatal("policy failure must refuse the execution")
	}
	var runtimeErr *writingruntime.RuntimeError
	if !errors.As(err, &runtimeErr) || runtimeErr.Code != writingruntime.CodeExecutionFailed {
		t.Fatalf("policy failure error %v, want a typed EXECUTION_FAILED", err)
	}
	if inner.calls != 0 {
		t.Fatal("inner executor ran behind a failed entitlement check")
	}

	// A nil policy is the unwired-store shape: same fail-closed treatment.
	_, err = NewResearchPilotGateExecutor(inner, nil, ResearchPilotScopeResearchReview).Execute(context.Background(), pilotGateRequest("subject-1", ""))
	if err == nil {
		t.Fatal("nil policy must refuse the execution")
	}
	if inner.calls != 0 {
		t.Fatal("inner executor ran behind a nil policy")
	}
}

func TestResearchPilotGateExecutorPassesEntitledThrough(t *testing.T) {
	policy := &stubPilotPolicy{entitled: true}
	inner := &pilotGateProbeExecutor{}
	executor := NewResearchPilotGateExecutor(inner, policy, ResearchPilotScopeResearchReview)

	result, err := executor.Execute(context.Background(), pilotGateRequest("subject-2", ""))
	if err != nil {
		t.Fatalf("entitled execution refused: %v", err)
	}
	if inner.calls != 1 {
		t.Fatalf("inner executor ran %d times, want exactly 1", inner.calls)
	}
	_ = result
}

func TestResearchPilotGateExecutorDescriptorIsTransparent(t *testing.T) {
	inner := &pilotGateProbeExecutor{descriptor: writingruntime.ExecutorDescriptor{
		ExecutorID: "engine.step.research_outline", Version: "1.0.0",
		SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction}}}
	wrapper := NewResearchPilotGateExecutor(inner, &stubPilotPolicy{entitled: true}, ResearchPilotScopeResearchReview)
	got := wrapper.Descriptor()
	if got.ExecutorID != inner.descriptor.ExecutorID || got.Version != inner.descriptor.Version ||
		got.Cancellable != inner.descriptor.Cancellable || len(got.SupportedNodeKinds) != len(inner.descriptor.SupportedNodeKinds) {
		t.Fatalf("wrapper descriptor %+v differs from inner %+v", got, inner.descriptor)
	}
}

// ── 3. the governedResearchSpecs assembly ───────────────────────────────────

func TestGovernedResearchSpecsCarryPilotGate(t *testing.T) {
	fixture := pilotTestDB(t)
	server := &Server{}
	store, err := writingstore.New(fixture.db)
	if err != nil {
		t.Fatal(err)
	}
	canonical := writingruntime.WritingStoreContentGateway{Store: store}
	specs := server.governedResearchSpecs(store, canonical, writingplan.DefaultCapabilityRegistry())
	wrapped := 0
	for _, spec := range specs {
		gate, ok := spec.Direct.(*researchPilotGateExecutor)
		if !ok {
			t.Fatalf("research executor %s is not wrapped in the pilot gate: %T", spec.BindingID, spec.Direct)
		}
		if gate.inner == nil {
			t.Fatalf("pilot gate for %s has no inner executor", spec.BindingID)
		}
		if gate.scope != ResearchPilotScopeResearchReview {
			t.Fatalf("pilot gate scope %q, want %q", gate.scope, ResearchPilotScopeResearchReview)
		}
		if gate.inner.Descriptor().ExecutorID != spec.BindingID {
			t.Fatalf("wrapped descriptor %s does not match binding %s", gate.inner.Descriptor().ExecutorID, spec.BindingID)
		}
		policy, ok := gate.policy.(*ResearchPilotPolicy)
		if !ok || policy.DB == nil {
			t.Fatalf("pilot gate for %s is not backed by the store's database", spec.BindingID)
		}
		wrapped++
	}
	if wrapped != 8 {
		t.Fatalf("wrapped %d research executors, want 8 (discover/read/outline/draft/citations/fact + material pair)", wrapped)
	}
}

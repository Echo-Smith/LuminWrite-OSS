package writingstore

import (
	"context"
	"errors"
	"testing"
	"time"
)

// T10 store tests (specs/research-review/ar012-sidecar.md): the durable job
// ledger behind the AR-012 candidate evaluation endpoint — replay identity,
// lease fencing, honest terminal states, and the A18 isolation invariant.

func newArReviewFixture(t *testing.T, name string) (*Store, context.Context) {
	t.Helper()
	if integrationDB == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	if _, err := integrationDB.ExecContext(context.Background(),
		`TRUNCATE writing_ar_review_jobs`); err != nil {
		t.Fatalf("reset ar review jobs: %v", err)
	}
	store, err := New(integrationDB)
	if err != nil {
		t.Fatal(err)
	}
	return store, context.Background()
}

func arJob(name, owner, runID, inputTag string) ArReviewJob {
	return ArReviewJob{
		OwnerUserID:        owner,
		RunID:              runID,
		SurrogateProjectID: "lb-" + inputTag + "0000000000000000000000000000",
		IdempotencyKey:     "ar012-" + inputTag,
		CentralQuestion:    "测试问题",
		ContractHash:       testHash("contract"),
		EvidencePackHash:   testHash("pack"),
		ApprovedOutlineHash: testHash("outline"),
		GeneratorVersion:   "ar012-review@test",
		InputHash:          testHash("input-" + inputTag),
	}
}

func TestArReviewJobCreateAndReplay(t *testing.T) {
	store, ctx := newArReviewFixture(t, "replay")
	job, err := store.CreateArReviewJob(ctx, arJob("a", "user_a", "run_a", "aaaa"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if job.Status != ArReviewJobPending || job.ID == "" {
		t.Fatalf("unexpected fresh job: %+v", job)
	}
	replay, err := store.CreateArReviewJob(ctx, arJob("b", "user_a", "run_a", "aaaa"))
	if !errors.Is(err, ErrArReviewJobReplayed) {
		t.Fatalf("identical input must replay, got %v", err)
	}
	if replay.ID != job.ID {
		t.Fatalf("replay must return the original job: %s vs %s", replay.ID, job.ID)
	}
	// A different frozen input set gets its own job.
	other, err := store.CreateArReviewJob(ctx, arJob("c", "user_a", "run_a", "bbbb"))
	if err != nil {
		t.Fatalf("distinct input must create a new job: %v", err)
	}
	if other.ID == job.ID {
		t.Fatal("distinct inputs must not collide")
	}
	// Same input, different owner: separate identity.
	if _, err := store.CreateArReviewJob(ctx, arJob("d", "user_b", "run_a", "aaaa")); err != nil {
		t.Fatalf("other owner must get a fresh job: %v", err)
	}
}

func TestArReviewJobLeaseFencing(t *testing.T) {
	store, ctx := newArReviewFixture(t, "lease")
	now := time.Now().UTC()
	if _, err := store.CreateArReviewJob(ctx, arJob("a", "user_a", "run_a", "aaaa")); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.CreateArReviewJob(ctx, arJob("b", "user_a", "run_b", "bbbb")); err != nil {
		t.Fatalf("create second: %v", err)
	}
	first, ok, err := store.ClaimPendingArReviewJob(ctx, "worker-1", time.Minute, now)
	if err != nil || !ok {
		t.Fatalf("first claim: %v (claimed %v)", err, ok)
	}
	second, ok, err := store.ClaimPendingArReviewJob(ctx, "worker-2", time.Minute, now)
	if err != nil || !ok {
		t.Fatalf("second claim: %v (claimed %v)", err, ok)
	}
	if first.ID == second.ID {
		t.Fatal("two workers claimed the same job")
	}
	// Queue is empty now.
	if _, ok, _ := store.ClaimPendingArReviewJob(ctx, "worker-3", time.Minute, now); ok {
		t.Fatal("empty queue must not claim")
	}
	// An expired lease is reclaimable; a freshly-flipped outcome_unknown is
	// deferred until its reconciliation cutoff passes.
	if err := store.FinishArReviewJob(ctx, second.ID, ArReviewJobOutcomeUnknwn, "unknown", "", nil, nil, nil, "reviewrun_x"); err != nil {
		t.Fatalf("finish unknown: %v", err)
	}
	// Lease expiry is test-clock driven: at now+2min the first job's
	// (now+1min) lease is expired, while the unknown job's cutoff (= now)
	// still excludes its just-written updated_at.
	stuck, err := store.StuckArReviewJobs(ctx, now.Add(2*time.Minute), now, 10)
	if err != nil {
		t.Fatalf("stuck scan: %v", err)
	}
	ids := map[string]bool{}
	for _, job := range stuck {
		ids[job.ID] = true
	}
	if !ids[first.ID] || ids[second.ID] {
		t.Fatalf("stuck scan must see only the expired lease: %v", ids)
	}
	// Once the reconciliation cutoff passes the finish time, the unknown job
	// becomes due for its read-only reconcile pass.
	time.Sleep(20 * time.Millisecond)
	stuck, err = store.StuckArReviewJobs(ctx, now.Add(2*time.Minute), time.Now(), 10)
	if err != nil {
		t.Fatalf("stuck scan 2: %v", err)
	}
	if len(stuck) != 2 {
		t.Fatalf("both jobs must be due after the cutoff: %d", len(stuck))
	}
}

func TestArReviewJobTerminalStates(t *testing.T) {
	store, ctx := newArReviewFixture(t, "terminal")
	now := time.Now().UTC()
	job, err := store.CreateArReviewJob(ctx, arJob("a", "user_a", "run_a", "aaaa"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.RequestArReviewJobCancel(ctx, "user_a", job.ID); err != nil {
		t.Fatalf("cancel pending: %v", err)
	}
	cancelled, err := store.GetArReviewJob(ctx, "user_a", job.ID)
	if err != nil || cancelled.Status != ArReviewJobCancelled {
		t.Fatalf("pending cancel must complete: %+v %v", cancelled, err)
	}
	// Terminal rows are not claimable and refuse a second finish.
	if _, ok, _ := store.ClaimPendingArReviewJob(ctx, "w", time.Minute, now); ok {
		t.Fatal("cancelled job must not be claimable")
	}
	if err := store.FinishArReviewJob(ctx, job.ID, ArReviewJobCompleted, "", "", nil, nil, nil, ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("finish of a terminal row must refuse as conflict, got %v", err)
	}

	// Owner-first scoping on reads.
	if _, err := store.GetArReviewJob(ctx, "user_b", job.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign owner must not read the job")
	}
}

func TestArReviewJobA18Isolation(t *testing.T) {
	store, ctx := newArReviewFixture(t, "a18")
	now := time.Now().UTC()
	job, err := store.CreateArReviewJob(ctx, arJob("a", "user_a", "run_a18", "aaaa"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, ok, err := store.ClaimPendingArReviewJob(ctx, "w", time.Minute, now); err != nil || !ok {
		t.Fatalf("claim: %v (claimed %v)", err, ok)
	}
	refs := []ArReviewArtifactRef{
		{Kind: "manuscript", ContentHash: testHash("manuscript"), MediaType: "text/markdown", Size: 10, SidecarPath: "05_writing/MANUSCRIPT_REVIEW_AGENT.md"},
		{Kind: "metrics", ContentHash: testHash("metrics"), MediaType: "application/json", Size: 12, SidecarPath: "host://metrics"},
	}
	if err := store.FinishArReviewJob(ctx, job.ID, ArReviewJobCompleted, "", "",
		refs, map[string]any{"measured": true, "input_tokens": 12}, []string{"warn"}, "reviewrun_x"); err != nil {
		t.Fatalf("finish: %v", err)
	}
	stored, err := store.GetArReviewJob(ctx, "user_a", job.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if stored.Status != ArReviewJobCompleted || stored.RemoteRunID != "reviewrun_x" {
		t.Fatalf("terminal state wrong: %+v", stored)
	}
	if len(stored.ArtifactRefs) != 2 || stored.Usage["measured"] != true {
		t.Fatalf("refs/usage not persisted: %+v", stored)
	}
	// A18: candidate import must leave the run's artifact ledger and every
	// document untouched — the job row is the only reference holder.
	runArtifacts, err := store.ListRunArtifacts(ctx, "run_a18")
	if err != nil {
		t.Fatalf("list run artifacts: %v", err)
	}
	if len(runArtifacts) != 0 {
		t.Fatalf("A18 violation: candidate import created %d run artifacts", len(runArtifacts))
	}
	// Cancel-request wins over a completed import.
	running, err := store.CreateArReviewJob(ctx, arJob("b", "user_b", "run_b", "bbbb"))
	if err != nil {
		t.Fatalf("create running job: %v", err)
	}
	if _, ok, _ := store.ClaimPendingArReviewJob(ctx, "w", time.Minute, now); !ok {
		t.Fatal("claim running job")
	}
	if _, err := store.RequestArReviewJobCancel(ctx, "user_b", running.ID); err != nil {
		t.Fatalf("flag running job: %v", err)
	}
	if err := store.FinishArReviewJob(ctx, running.ID, ArReviewJobCompleted, "", "",
		refs, nil, nil, "reviewrun_y"); err != nil {
		t.Fatalf("finish flagged: %v", err)
	}
	flagged, err := store.GetArReviewJob(ctx, "user_b", running.ID)
	if err != nil {
		t.Fatalf("reload flagged: %v", err)
	}
	if flagged.Status != ArReviewJobCancelled || len(flagged.ArtifactRefs) != 0 {
		t.Fatalf("cancel must win over import: %+v", flagged)
	}
}

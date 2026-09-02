package writingstore

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/projectmemory"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
)

var integrationDB *database.DB

func TestMain(m *testing.M) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		if os.Getenv("CI") == "true" {
			fmt.Fprintln(os.Stderr, "CI=true but TEST_DATABASE_URL is not set")
			os.Exit(1)
		}
		os.Exit(m.Run())
	}
	db, err := database.NewPostgres(databaseURL, 5, 2)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect writingstore test database: %v\n", err)
		os.Exit(1)
	}
	if err := database.Migrate(db); err != nil {
		fmt.Fprintf(os.Stderr, "migrate writingstore test database: %v\n", err)
		_ = db.Close()
		os.Exit(1)
	}
	integrationDB = db
	code := m.Run()
	_ = db.Close()
	os.Exit(code)
}

// TestMaterialSnapshotRepositoryOnRealDatabase exercises the run-level
// material snapshot against PostgreSQL: load-miss, first-writer-wins,
// round-trip, concurrent capture, and no partial state on failure.
func TestMaterialSnapshotRepositoryOnRealDatabase(t *testing.T) {
	store, fixture := newIntegrationFixture(t, true)
	ctx := context.Background()
	manifestA := []MaterialSnapshotArtifact{{ArtifactID: "art_contract", Version: 1, ArtifactType: "contract",
		ContentHash: testHash("matsnap-contract"), MediaType: "application/json", ContentRef: "db://writing_contracts/1"},
		{ArtifactID: "art_materials", Version: 1, ArtifactType: "materials", ContentHash: testHash("matsnap-materials"),
			MediaType: "application/json", ContentRef: "memory://materials"}}
	if _, err := store.LoadInitialMaterialSnapshot(ctx, fixture.runID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("fresh run load err=%v", err)
	}
	saved, created, err := store.SaveInitialMaterialSnapshot(ctx, fixture.runID, manifestA)
	if err != nil || !created || len(saved.Artifacts) != 2 {
		t.Fatalf("save=%#v created=%v err=%v", saved, created, err)
	}
	manifestB := []MaterialSnapshotArtifact{{ArtifactID: "art_other", Version: 1, ArtifactType: "contract",
		ContentHash: testHash("matsnap-other"), MediaType: "application/json", ContentRef: "memory://other"}}
	replay, created, err := store.SaveInitialMaterialSnapshot(ctx, fixture.runID, manifestB)
	if err != nil || created {
		t.Fatalf("second save must be first-writer-wins: created=%v err=%v", created, err)
	}
	if replay.Artifacts[0].ArtifactID != "art_contract" {
		t.Fatalf("replay returned a different snapshot: %#v", replay)
	}
	loaded, err := store.LoadInitialMaterialSnapshot(ctx, fixture.runID)
	if err != nil || len(loaded.Artifacts) != 2 || loaded.Artifacts[0].ContentHash != testHash("matsnap-contract") {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}

	// Concurrent first capture on a fresh run: exactly one writer wins and
	// every loser observes the winning manifest.
	concurrentRun := RunRecord{RunID: fixture.runID + "_concurrent", DocumentID: fixture.documentID,
		ContractID: fixture.contract.ContractID, ContractVersion: fixture.contract.Version,
		ContractHash: fixture.contract.ContractHash, Status: "planned", ApprovalMode: writingkernel.ApprovalModeAuto,
		RequestedAssurance: writingkernel.AssuranceLevelStandard,
		Budget:             writingplan.PlanBudget{MaxCostUSD: 10, MaxDurationMS: 10000, MaxConcurrency: 1, MaxNodes: 2, MaxItems: 1},
		Permissions:        []writingplan.Permission{"model.invoke"}, Trace: testTrace()}
	if err := store.CreateRun(ctx, concurrentRun); err != nil {
		t.Fatal(err)
	}
	const racers = 8
	createdCount := make(chan bool, racers)
	results := make(chan MaterialSnapshotRecord, racers)
	for index := 0; index < racers; index++ {
		artifact := MaterialSnapshotArtifact{ArtifactID: fmt.Sprintf("art_cand_%d", index), Version: 1,
			ArtifactType: "contract", ContentHash: testHash(fmt.Sprintf("matsnap-cand-%d", index)),
			MediaType: "application/json", ContentRef: fmt.Sprintf("memory://cand-%d", index)}
		go func(candidate MaterialSnapshotArtifact) {
			record, created, err := store.SaveInitialMaterialSnapshot(ctx, concurrentRun.RunID, []MaterialSnapshotArtifact{candidate})
			if err == nil {
				createdCount <- created
				results <- record
			} else {
				createdCount <- false
				results <- MaterialSnapshotRecord{}
			}
		}(artifact)
	}
	winners := 0
	for index := 0; index < racers; index++ {
		if <-createdCount {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("%d concurrent writers claimed the snapshot", winners)
	}
	winner := <-results
	for index := 1; index < racers; index++ {
		loser := <-results
		if len(loser.Artifacts) > 0 && loser.Artifacts[0].ArtifactID != winner.Artifacts[0].ArtifactID {
			t.Fatalf("loser observed a different snapshot: %#v vs %#v", loser, winner)
		}
	}
	loaded, err = store.LoadInitialMaterialSnapshot(ctx, concurrentRun.RunID)
	if err != nil || loaded.Artifacts[0].ArtifactID != winner.Artifacts[0].ArtifactID {
		t.Fatalf("persisted winner=%#v loaded=%#v err=%v", winner, loaded, err)
	}

	// A save for a nonexistent run fails and leaves no partial snapshot.
	if _, _, err := store.SaveInitialMaterialSnapshot(ctx, "run_missing_snapshot", manifestA); err == nil {
		t.Fatal("save for a missing run must fail")
	}
	if _, err := store.LoadInitialMaterialSnapshot(ctx, "run_missing_snapshot"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed save left partial state: err=%v", err)
	}
}

// TestSeedRuntimeEvidenceForDowngradeCheck leaves one governed runtime
// evidence row behind so migration downgrade guards can be exercised against
// real data. Run it alone: any later fixture call truncates the table.
func TestSeedRuntimeEvidenceForDowngradeCheck(t *testing.T) {
	store, fixture := newIntegrationFixture(t, true)
	ctx := context.Background()
	if _, _, err := store.StartNodeAttempt(ctx, fixture.nodeAttempt(), testTrace()); err != nil {
		t.Fatal(err)
	}
	idempotencyKey, err := NodeAttemptKey(fixture.runID, fixture.nodeID, 1)
	if err != nil {
		t.Fatal(err)
	}
	event := RunEvent{RunID: fixture.runID, EventType: "runtime.route_decided", NodeID: fixture.nodeID,
		Attempt: 1, IdempotencyKey: idempotencyKey,
		EntityKind: "rollout_evidence", EntityID: "evt_downgrade_probe",
		Payload: map[string]any{"probe": true}, Trace: testTrace()}
	appendTestEvent(t, store, event)
}

func TestNodeAttemptKeyIsExactAndDeterministic(t *testing.T) {
	key, err := NodeAttemptKey("run_test", "node_draft", 2)
	if err != nil {
		t.Fatal(err)
	}
	if key != "run_test:node_draft:2" {
		t.Fatalf("unexpected key %q", key)
	}
	for _, test := range []struct {
		runID  string
		nodeID string
		try    int
	}{
		{"run_test", "", 1},
		{"run_test", "node:bad", 1},
		{"run_test", "node_ok", 0},
		{"bad", "node_ok", 1},
	} {
		if _, err := NodeAttemptKey(test.runID, test.nodeID, test.try); !errors.Is(err, ErrInvalidRecord) {
			t.Fatalf("NodeAttemptKey(%q, %q, %d) error = %v", test.runID, test.nodeID, test.try, err)
		}
	}
}

func TestQualityPreflightRejectsBlockersAndUnpersistedVerification(t *testing.T) {
	base := QualityReportRecord{
		ReportID: "qr_test", ReportVersion: 1, RunID: "run_test",
		PlanID: "plan_test", PlanVersion: 1, DocumentID: "doc_test",
		CandidateVersionID: "ver_test", ContentHash: testHash("quality"),
		RequestedAssurance: writingkernel.AssuranceLevelStandard,
		AchievedAssurance:  writingkernel.AssuranceLevelStandard,
		AssuranceSatisfied: true, VersionConsistent: true,
		Payload: map[string]any{}, SnapshotID: "snap_test", SnapshotVersion: 1,
		SnapshotPersisted: true, Trace: testTrace(),
	}
	blocked := base
	blocked.QualityState = QualityAcceptedDraft
	blocked.BlockerCount = 1
	if err := validateQualityReport(blocked); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("BLOCKER must prevent acceptance, got %v", err)
	}

	unpersisted := base
	unpersisted.QualityState = QualityVerifiedDeliverable
	unpersisted.ValidatedVersionID = unpersisted.CandidateVersionID
	unpersisted.CommittedVersionID = unpersisted.CandidateVersionID
	unpersisted.RequiredValidatorsSatisfied = true
	unpersisted.SnapshotPersisted = false
	if err := validateQualityReport(unpersisted); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("unpersisted snapshot must prevent verification, got %v", err)
	}
}

func TestQualityPreflightAllowsAcceptedDraftBelowRequestedAssurance(t *testing.T) {
	report := QualityReportRecord{
		ReportID: "qr_accepted_lower_assurance", ReportVersion: 1, RunID: "run_test",
		PlanID: "plan_test", PlanVersion: 1, DocumentID: "doc_test", CandidateVersionID: "ver_test",
		ContentHash: testHash("accepted-lower-assurance"), RequestedAssurance: writingkernel.AssuranceLevelStrict,
		AchievedAssurance: writingkernel.AssuranceLevelSourced, AssuranceSatisfied: false,
		QualityState: QualityAcceptedDraft, VersionConsistent: true, Payload: map[string]any{},
		SnapshotID: "snap_test", SnapshotVersion: 1, SnapshotPersisted: true, Trace: testTrace(),
	}
	if err := validateQualityReport(report); err != nil {
		t.Fatalf("Accepted Draft should preserve an assurance shortfall without claiming Verified: %v", err)
	}
}

func TestContractIsImmutableAndReplaySafe(t *testing.T) {
	store, fixture := newIntegrationFixture(t, false)
	ctx := context.Background()
	record := ContractRecord{DocumentID: fixture.documentID, Contract: fixture.contract, Trace: testTrace()}
	if err := store.PutContract(ctx, record); err != nil {
		t.Fatalf("identical contract replay failed: %v", err)
	}
	changed := fixture.contract
	changed.Intent.Purpose = "a different immutable purpose"
	changed = resealContract(t, changed)
	record.Contract = changed
	if err := store.PutContract(ctx, record); !errors.Is(err, ErrImmutableConflict) {
		t.Fatalf("changed immutable contract error = %v", err)
	}
}

func TestDocumentCommitUsesOptimisticBaseLock(t *testing.T) {
	store, fixture := newIntegrationFixture(t, false)
	ctx := context.Background()
	first := testDocumentVersion(t, fixture.documentID, "ver_first", nil, "first")
	if _, err := store.CommitDocumentVersion(ctx, CommitDocumentVersionParams{
		Version: first, ContractID: fixture.contract.ContractID,
		ContractVersion: fixture.contract.Version, Trace: testTrace(),
	}); err != nil {
		t.Fatal(err)
	}
	stale := testDocumentVersion(t, fixture.documentID, "ver_stale", nil, "stale")
	_, err := store.CommitDocumentVersion(ctx, CommitDocumentVersionParams{
		Version: stale, ExpectedBaseVersionID: "", ContractID: fixture.contract.ContractID,
		ContractVersion: fixture.contract.Version, Trace: testTrace(),
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale base error = %v", err)
	}
}

func TestNodeAttemptReplayAndConflict(t *testing.T) {
	store, fixture := newIntegrationFixture(t, true)
	ctx := context.Background()
	attempt := fixture.nodeAttempt()
	var created bool
	if err := store.InTransaction(ctx, func(tx *Tx) error {
		_, createdNow, err := tx.EnsureNodeAttempt(ctx, attempt)
		created = createdNow
		return err
	}); err != nil || !created {
		t.Fatalf("first attempt created=%v error=%v", created, err)
	}
	if err := store.InTransaction(ctx, func(tx *Tx) error {
		_, createdNow, err := tx.EnsureNodeAttempt(ctx, attempt)
		if createdNow {
			t.Fatal("idempotent replay created a second attempt")
		}
		return err
	}); err != nil {
		t.Fatalf("idempotent attempt replay: %v", err)
	}
	attempt.InputHash = testHash("changed input")
	err := store.InTransaction(ctx, func(tx *Tx) error {
		_, _, err := tx.EnsureNodeAttempt(ctx, attempt)
		return err
	})
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed attempt replay error = %v", err)
	}
}

func TestRunEventsAreAtomicMonotonicAndIdempotent(t *testing.T) {
	store, fixture := newIntegrationFixture(t, true)
	ctx := context.Background()
	attempt := fixture.nodeAttempt()
	if err := store.InTransaction(ctx, func(tx *Tx) error {
		_, _, err := tx.EnsureNodeAttempt(ctx, attempt)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	key, _ := NodeAttemptKey(fixture.runID, fixture.nodeID, 1)
	nodeEvent := RunEvent{RunID: fixture.runID, EventType: "node.started",
		NodeID: fixture.nodeID, Attempt: 1, IdempotencyKey: key,
		EntityKind: "node", EntityID: fixture.nodeID, Payload: map[string]any{"phase": "draft"}, Trace: testTrace()}
	first := appendTestEvent(t, store, nodeEvent)
	replayed := appendTestEvent(t, store, nodeEvent)
	if first.Sequence != 1 || replayed.Sequence != 1 || replayed.EventID != first.EventID {
		t.Fatalf("node event replay allocated another sequence: first=%#v replay=%#v", first, replayed)
	}
	second := appendTestEvent(t, store, RunEvent{RunID: fixture.runID, EventType: "run.started",
		EntityKind: "run", EntityID: fixture.runID, Payload: map[string]any{}, Trace: testTrace()})
	if second.Sequence != 2 {
		t.Fatalf("second event sequence = %d", second.Sequence)
	}
	var projected, ledger int64
	if err := integrationDB.QueryRow(`SELECT last_event_sequence FROM writing_runs WHERE run_id=$1`, fixture.runID).Scan(&projected); err != nil {
		t.Fatal(err)
	}
	if err := integrationDB.QueryRow(`SELECT MAX(sequence) FROM writing_run_events WHERE run_id=$1`, fixture.runID).Scan(&ledger); err != nil {
		t.Fatal(err)
	}
	if projected != 2 || ledger != projected {
		t.Fatalf("run projection=%d ledger=%d", projected, ledger)
	}
}

func TestRunTransitionAtomicallyAuditsProjectionAndStaleCommands(t *testing.T) {
	store, fixture := newIntegrationFixture(t, true)
	ctx := context.Background()
	command := RunTransitionCommand{RunID: fixture.runID,
		IdempotencyKey: fixture.runID + ":transition:pause", ExpectedFrom: "running",
		RequestedTo: "pausing", RuleAccepted: true, Cause: "test_pause",
		Summary: "pause", Trace: testTrace()}
	first, err := store.RecordRunTransition(ctx, command)
	if err != nil || !first.Accepted || first.EffectiveState != "pausing" {
		t.Fatalf("first transition=%#v err=%v", first, err)
	}
	replayed, err := store.RecordRunTransition(ctx, command)
	if err != nil || !replayed.Replayed || replayed.Event.Sequence != first.Event.Sequence {
		t.Fatalf("replay=%#v err=%v", replayed, err)
	}
	stale, err := store.RecordRunTransition(ctx, RunTransitionCommand{RunID: fixture.runID,
		IdempotencyKey: fixture.runID + ":transition:stale", ExpectedFrom: "running",
		RequestedTo: "failed", RuleAccepted: true, Cause: "stale", Summary: "stale",
		Trace: testTrace()})
	if err != nil || stale.Accepted || stale.ActualFrom != "pausing" || stale.EffectiveState != "pausing" {
		t.Fatalf("stale transition=%#v err=%v", stale, err)
	}
	var status string
	if err := integrationDB.QueryRowContext(ctx, `SELECT status FROM writing_runs WHERE run_id=$1`, fixture.runID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "pausing" {
		t.Fatalf("status=%s", status)
	}
}

func TestRuntimeEvidenceIsAppendOnlyAndBoundToNodeAttempt(t *testing.T) {
	store, fixture := newIntegrationFixture(t, true)
	ctx := context.Background()
	if _, dispatch, err := store.StartNodeAttempt(ctx, fixture.nodeAttempt(), testTrace()); err != nil || !dispatch {
		t.Fatalf("start attempt dispatch=%v error=%v", dispatch, err)
	}
	record := RuntimeEvidenceRecord{EvidenceID: "evt_rollout_route", RunID: fixture.runID,
		NodeID: fixture.nodeID, Attempt: 1, Kind: "route_decision",
		Payload:    map[string]any{"mode": "shadow", "lane": "baseline", "policy_hash": testHash("policy")},
		OccurredAt: time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)}
	if err := store.RecordRuntimeEvidence(ctx, record); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordRuntimeEvidence(ctx, record); err != nil {
		t.Fatalf("identical evidence replay failed: %v", err)
	}
	var count int
	var eventType, entityKind string
	if err := integrationDB.QueryRowContext(ctx, `
		SELECT COUNT(*), MIN(event_type), MIN(entity_kind)
		FROM writing_run_events WHERE run_id=$1 AND event_id=$2
	`, fixture.runID, record.EvidenceID).Scan(&count, &eventType, &entityKind); err != nil {
		t.Fatal(err)
	}
	if count != 1 || eventType != "runtime.route_decided" || entityKind != "rollout_evidence" {
		t.Fatalf("count=%d type=%s entity=%s", count, eventType, entityKind)
	}
}

func TestTask13ShadowContentAndPromotionRecordsAreDurable(t *testing.T) {
	store, fixture := newIntegrationFixture(t, true)
	ctx := context.Background()
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	body := []byte("isolated shadow body")
	bodyHash := fmt.Sprintf("sha256:%x", sha256.Sum256(body))
	policyHash := testHash("task13-policy")
	key := strings.TrimPrefix(policyHash, "sha256:") + "/" + fixture.runID + "-node_draft-1-draft/" + strings.TrimPrefix(bodyHash, "sha256:")
	record := ShadowContentRecord{ContentKey: key, PolicyHash: policyHash, RunID: fixture.runID,
		MediaType: "text/markdown", ContentHash: bodyHash, Body: body,
		StoredAt: now, ExpiresAt: now.Add(24 * time.Hour)}
	if err := store.PutShadowContent(ctx, record); err != nil {
		t.Fatal(err)
	}
	if err := store.PutShadowContent(ctx, record); err != nil {
		t.Fatalf("idempotent replay: %v", err)
	}
	reopened, err := New(integrationDB)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := reopened.GetShadowContent(ctx, key)
	if err != nil || string(loaded.Body) != string(body) || loaded.ContentHash != record.ContentHash {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
	if removed, err := reopened.DeleteShadowContentPrefix(ctx, strings.TrimPrefix(policyHash, "sha256:")+"/"+fixture.runID+"-"); err != nil || removed != 1 {
		t.Fatalf("removed=%d err=%v", removed, err)
	}

	if _, _, err := store.StartNodeAttempt(ctx, fixture.nodeAttempt(), testTrace()); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 3; index++ {
		evidence := RuntimeEvidenceRecord{EvidenceID: StableID("evt_", "task13", fmt.Sprint(index)),
			RunID: fixture.runID, NodeID: fixture.nodeID, Attempt: 1, Kind: "shadow_comparison",
			Payload:    map[string]any{"kind": "shadow_comparison", "policy_hash": policyHash, "status": "different", "error_code": ""},
			OccurredAt: now.Add(time.Duration(index) * time.Minute)}
		if err := store.RecordRuntimeEvidence(ctx, evidence); err != nil {
			t.Fatal(err)
		}
	}
	health, err := reopened.RolloutEvidenceHealth(ctx, policyHash, now.Add(-time.Hour))
	if err != nil || health.ComparisonRecords != 3 || health.FailedRecords != 0 || health.LastRecordedAt.IsZero() {
		t.Fatalf("health=%#v err=%v", health, err)
	}
	approval := RolloutApprovalRecord{ApprovalID: "approval_task13", PolicyHash: policyHash, PolicyVersion: 2,
		ActivationKey: "change-task13", TargetMode: "allowlist", ApprovedBy: "operator_test", Reason: "integration test",
		EvidenceHealth: health, EvidenceCutoff: health.Cutoff, EvidenceLastRecordedAt: health.LastRecordedAt,
		CreatedAt: now.Add(4 * time.Minute), ExpiresAt: now.Add(24 * time.Hour)}
	if err := store.RecordRolloutApproval(ctx, approval); err != nil {
		t.Fatal(err)
	}
	persisted, err := reopened.LatestRolloutApproval(ctx, policyHash, 2, "change-task13")
	if err != nil || persisted.ApprovalID != approval.ApprovalID || persisted.EvidenceHealth.ComparisonRecords != 3 {
		t.Fatalf("approval=%#v err=%v", persisted, err)
	}
	refreshed := approval
	refreshed.ApprovalID = "approval_task13_refresh"
	refreshed.CreatedAt = approval.CreatedAt.Add(time.Minute)
	refreshed.ExpiresAt = approval.ExpiresAt.Add(time.Minute)
	if err := store.RecordRolloutApproval(ctx, refreshed); err != nil {
		t.Fatalf("append refreshed approval: %v", err)
	}
	persisted, err = reopened.LatestRolloutApproval(ctx, policyHash, 2, "change-task13")
	if err != nil || persisted.ApprovalID != refreshed.ApprovalID {
		t.Fatalf("latest refreshed approval=%#v err=%v", persisted, err)
	}
	if _, err := integrationDB.ExecContext(ctx, `UPDATE writing_rollout_approvals SET reason='mutated' WHERE approval_id=$1`, refreshed.ApprovalID); err == nil {
		t.Fatal("append-only approval accepted an update")
	}

	percentageHealth := health
	percentageHealth.PolicyHash = testHash("task13-percentage-policy")
	percentageApproval := RolloutApprovalRecord{ApprovalID: "approval_task13_percentage", PolicyHash: percentageHealth.PolicyHash,
		PolicyVersion: 2, ActivationKey: "change-task13", TargetMode: "percentage", ApprovedBy: "operator_test",
		Reason: "integration test percentage", EvidenceHealth: percentageHealth, EvidenceCutoff: percentageHealth.Cutoff,
		EvidenceLastRecordedAt: percentageHealth.LastRecordedAt, CreatedAt: now.Add(6 * time.Minute), ExpiresAt: now.Add(24 * time.Hour)}
	if err := store.RecordRolloutApproval(ctx, percentageApproval); err != nil {
		t.Fatalf("record percentage approval: %v", err)
	}
	ladder, err := reopened.LatestRolloutApprovalByActivationKey(ctx, "change-task13", "allowlist")
	if err != nil || ladder.TargetMode != "allowlist" || ladder.ApprovalID != refreshed.ApprovalID {
		t.Fatalf("allowlist ladder=%#v err=%v", ladder, err)
	}
	latestPercentage, err := reopened.LatestRolloutApprovalByActivationKey(ctx, "change-task13", "percentage")
	if err != nil || latestPercentage.ApprovalID != percentageApproval.ApprovalID {
		t.Fatalf("percentage ladder=%#v err=%v", latestPercentage, err)
	}
	if _, err := reopened.LatestRolloutApprovalByActivationKey(ctx, "change-absent", "allowlist"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing ladder err=%v", err)
	}

	// Enabled approvals certify the percentage stage's evidence health, so the
	// certified health carries the percentage policy hash rather than the
	// enabled policy's own hash; the store must accept that pairing and reject
	// the mismatched pairing for the lower rungs.
	enabledHealth := percentageHealth
	enabledApproval := RolloutApprovalRecord{ApprovalID: "approval_task13_enabled", PolicyHash: testHash("task13-enabled-policy"),
		PolicyVersion: 3, ActivationKey: "change-task13", TargetMode: "enabled", ApprovedBy: "operator_test",
		Reason: "integration test enabled", EvidenceHealth: enabledHealth, EvidenceCutoff: enabledHealth.Cutoff,
		EvidenceLastRecordedAt: enabledHealth.LastRecordedAt, CreatedAt: now.Add(7 * time.Minute), ExpiresAt: now.Add(24 * time.Hour)}
	if err := store.RecordRolloutApproval(ctx, enabledApproval); err != nil {
		t.Fatalf("record enabled approval: %v", err)
	}
	latestEnabled, err := reopened.LatestRolloutApprovalByActivationKey(ctx, "change-task13", "enabled")
	if err != nil || latestEnabled.ApprovalID != enabledApproval.ApprovalID {
		t.Fatalf("enabled ladder=%#v err=%v", latestEnabled, err)
	}
	misbound := percentageApproval
	misbound.ApprovalID = "approval_task13_misbound"
	misbound.PolicyHash = testHash("task13-unrelated-policy")
	misbound.CreatedAt = now.Add(8 * time.Minute)
	misbound.ExpiresAt = misbound.ExpiresAt.Add(time.Minute)
	if err := store.RecordRolloutApproval(ctx, misbound); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("misbound percentage approval err=%v", err)
	}
}

func TestNodeAttemptLifecycleCommitsArtifactAndUsageAtomically(t *testing.T) {
	store, fixture := newIntegrationFixture(t, true)
	ctx := context.Background()
	attempt, dispatch, err := store.StartNodeAttempt(ctx, fixture.nodeAttempt(), testTrace())
	if err != nil || !dispatch || attempt.Status != "running" {
		t.Fatalf("start=%#v dispatch=%v err=%v", attempt, dispatch, err)
	}
	artifact := fixture.artifact()
	if err := store.CompleteNodeAttempt(ctx, AttemptCompletion{RunID: fixture.runID,
		NodeID: fixture.nodeID, Attempt: 1, Status: "succeeded", Artifacts: []ArtifactRecord{artifact},
		CostUSD: .5, InputTokens: 10, OutputTokens: 20, DurationMS: 30,
		Trace: testTrace(), CompletedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	attempts, err := store.ListRunAttempts(ctx, fixture.runID)
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := store.ListRunArtifacts(ctx, fixture.runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 || attempts[0].Status != "succeeded" || attempts[0].ActualCostUSD != .5 || len(artifacts) != 1 || artifacts[0].ArtifactID != artifact.ArtifactID {
		t.Fatalf("attempts=%#v artifacts=%#v", attempts, artifacts)
	}
}

func TestArtifactImmutableReplay(t *testing.T) {
	store, fixture := newIntegrationFixture(t, true)
	ctx := context.Background()
	attempt := fixture.nodeAttempt()
	if err := store.InTransaction(ctx, func(tx *Tx) error {
		if _, _, err := tx.EnsureNodeAttempt(ctx, attempt); err != nil {
			return err
		}
		return tx.PutArtifact(ctx, fixture.artifact())
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.InTransaction(ctx, func(tx *Tx) error { return tx.PutArtifact(ctx, fixture.artifact()) }); err != nil {
		t.Fatalf("identical artifact replay failed: %v", err)
	}
	changed := fixture.artifact()
	changed.ContentRef = "object://changed-with-same-hash"
	err := store.InTransaction(ctx, func(tx *Tx) error { return tx.PutArtifact(ctx, changed) })
	if !errors.Is(err, ErrImmutableConflict) {
		t.Fatalf("changed artifact replay error = %v", err)
	}
}

func TestCheckpointAtomicallyPromotesAcceptedDraft(t *testing.T) {
	store, fixture := newIntegrationFixture(t, true)
	ctx := context.Background()
	attempt := fixture.nodeAttempt()
	artifact := fixture.artifact()
	if err := store.InTransaction(ctx, func(tx *Tx) error {
		if _, _, err := tx.EnsureNodeAttempt(ctx, attempt); err != nil {
			return err
		}
		return tx.PutArtifact(ctx, artifact)
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	report := QualityReportRecord{
		ReportID: "qr_checkpoint", ReportVersion: 1, RunID: fixture.runID,
		PlanID: fixture.planID, PlanVersion: 1, DocumentID: fixture.documentID,
		CandidateVersionID: fixture.version.VersionID, ContentHash: fixture.version.ContentHash,
		RequestedAssurance: writingkernel.AssuranceLevelStandard,
		AchievedAssurance:  writingkernel.AssuranceLevelStandard,
		AssuranceSatisfied: true, QualityState: QualityAcceptedDraft,
		VersionConsistent: true, RequiredValidatorsSatisfied: true,
		Payload: map[string]any{"result": "accepted"}, SnapshotID: "snap_checkpoint",
		SnapshotVersion: 1, Trace: testTrace(), CreatedAt: now,
	}
	bundle := CheckpointBundle{
		Snapshot: SnapshotRecord{
			SnapshotID: "snap_checkpoint", SnapshotVersion: 1, RunID: fixture.runID,
			CheckpointID: "accepted-1", PlanID: fixture.planID, PlanVersion: 1,
			ContractID: fixture.contract.ContractID, ContractVersion: fixture.contract.Version,
			ContractHash: fixture.contract.ContractHash, DocumentID: fixture.documentID,
			BaseVersionID: fixture.version.VersionID, CandidateVersionID: fixture.version.VersionID,
			QualityReportID: report.ReportID, QualityReportVersion: report.ReportVersion,
			ContentHash: testHash("snapshot"), Status: "persisted", Complete: true,
			Manifest:   map[string]any{"document_version": fixture.version.VersionID},
			StorageRef: "object://snapshots/accepted-1", Trace: testTrace(),
			CreatedAt: now, PersistedAt: now,
		},
		QualityReport: &report,
		DocumentPromotion: &DocumentPromotion{DocumentID: fixture.documentID,
			VersionID: fixture.version.VersionID, QualityState: QualityAcceptedDraft, AcceptedAt: now},
		Artifacts:         []ArtifactPromotion{{ArtifactID: artifact.ArtifactID, Version: artifact.Version, ContentHash: artifact.ContentHash}},
		AchievedAssurance: writingkernel.AssuranceLevelStandard,
	}
	committed, err := store.CommitCheckpoint(ctx, bundle)
	if err != nil {
		t.Fatalf("commit checkpoint: %v", err)
	}
	if committed.LedgerSequence != 1 {
		t.Fatalf("snapshot ledger sequence = %d", committed.LedgerSequence)
	}
	replayed, err := store.CommitCheckpoint(ctx, bundle)
	if err != nil || replayed.LedgerSequence != committed.LedgerSequence {
		t.Fatalf("checkpoint replay = %#v, %v", replayed, err)
	}
	var qualityState, artifactStatus, snapshotID string
	if err := integrationDB.QueryRow(`
		SELECT quality_state, snapshot_manifest_id FROM writing_document_versions
		WHERE document_id=$1 AND version_id=$2
	`, fixture.documentID, fixture.version.VersionID).Scan(&qualityState, &snapshotID); err != nil {
		t.Fatal(err)
	}
	if err := integrationDB.QueryRow(`
		SELECT status FROM writing_artifacts WHERE artifact_id=$1 AND version=$2
	`, artifact.ArtifactID, artifact.Version).Scan(&artifactStatus); err != nil {
		t.Fatal(err)
	}
	if qualityState != QualityAcceptedDraft || snapshotID != bundle.Snapshot.SnapshotID || artifactStatus != "committed" {
		t.Fatalf("checkpoint projection state=%s snapshot=%s artifact=%s", qualityState, snapshotID, artifactStatus)
	}
}

type integrationFixture struct {
	documentID string
	runID      string
	planID     string
	nodeID     string
	contract   writingkernel.WritingContract
	version    writingkernel.DocumentVersion
}

func newIntegrationFixture(t *testing.T, complete bool) (*Store, integrationFixture) {
	t.Helper()
	if integrationDB == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	if _, err := integrationDB.ExecContext(ctx, `TRUNCATE writing_rollout_approvals, writing_documents CASCADE`); err != nil {
		t.Fatalf("reset writing tables: %v", err)
	}
	var userID string
	uid := StableID("writingstore_", strings.ToLower(t.Name()), fmt.Sprint(time.Now().UnixNano()))
	if err := integrationDB.QueryRowContext(ctx, `
		INSERT INTO users (uid, name) VALUES ($1, 'writingstore test') RETURNING id::text
	`, uid).Scan(&userID); err != nil {
		t.Fatalf("create fixture user: %v", err)
	}
	store, err := New(integrationDB)
	if err != nil {
		t.Fatal(err)
	}
	fixture := integrationFixture{documentID: "doc_store", runID: "run_store", nodeID: "node_draft"}
	if err := store.CreateDocument(ctx, DocumentRecord{DocumentID: fixture.documentID,
		OwnerUserID: userID, Title: "Store test", Actor: testTrace().Actor}); err != nil {
		t.Fatal(err)
	}
	fixture.contract = testContract(t)
	if err := store.PutContract(ctx, ContractRecord{DocumentID: fixture.documentID,
		Contract: fixture.contract, Trace: testTrace()}); err != nil {
		t.Fatal(err)
	}
	if !complete {
		return store, fixture
	}
	fixture.version = testDocumentVersion(t, fixture.documentID, "ver_store", nil, "governed draft")
	if _, err := store.CommitDocumentVersion(ctx, CommitDocumentVersionParams{
		Version: fixture.version, ContractID: fixture.contract.ContractID,
		ContractVersion: fixture.contract.Version, Trace: testTrace(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateRun(ctx, RunRecord{RunID: fixture.runID, DocumentID: fixture.documentID,
		ContractID: fixture.contract.ContractID, ContractVersion: fixture.contract.Version,
		ContractHash: fixture.contract.ContractHash, BaseVersionID: fixture.version.VersionID,
		Status: "planned", ApprovalMode: writingkernel.ApprovalModeAuto,
		RequestedAssurance: writingkernel.AssuranceLevelStandard,
		Budget:             writingplan.PlanBudget{MaxCostUSD: 10, MaxDurationMS: 10000, MaxConcurrency: 1, MaxNodes: 2, MaxItems: 1},
		Permissions:        []writingplan.Permission{"model.invoke"}, Trace: testTrace()}); err != nil {
		t.Fatal(err)
	}
	envelope := testPlanEnvelope(t, fixture.contract)
	fixture.planID = envelope.ExecutablePlan.PlanID
	if err := store.PutPlan(ctx, PlanRecord{RunID: fixture.runID, PlanVersion: 1,
		Envelope: envelope, Budget: writingplan.PlanBudget{MaxCostUSD: 10, MaxDurationMS: 10000, MaxConcurrency: 1, MaxNodes: 2, MaxItems: 1},
		Permissions: []writingplan.Permission{"model.invoke"}, Trace: testTrace()}); err != nil {
		t.Fatal(err)
	}
	if err := store.InTransaction(ctx, func(tx *Tx) error {
		return tx.ActivatePlan(ctx, fixture.runID, fixture.planID, 1, "running")
	}); err != nil {
		t.Fatal(err)
	}
	return store, fixture
}

func (f integrationFixture) nodeAttempt() NodeAttempt {
	return NodeAttempt{RunID: f.runID, PlanID: f.planID, PlanVersion: 1,
		NodeID: f.nodeID, Attempt: 1, NodeKind: writingplan.NodeAction,
		CapabilityID: "core.writing.draft", CapabilityVersion: "1.0.0",
		ExecutorID: "executor.test", FailurePath: writingplan.FailureFail,
		Bounds:    writingplan.Bounds{MaxAttempts: 1, MaxConcurrency: 1, MaxItems: 1, MaxCostUSD: 1, TimeoutMS: 1000},
		InputHash: testHash("input"), InputArtifactIDs: []string{},
	}
}

func (f integrationFixture) artifact() ArtifactRecord {
	return ArtifactRecord{ArtifactID: "art_draft", Version: 1, RunID: f.runID,
		PlanID: f.planID, PlanVersion: 1, NodeID: f.nodeID, Attempt: 1,
		OutputKey: "draft", ArtifactType: "full_draft", Status: "validated",
		ContentHash: testHash("artifact"), MediaType: "text/markdown",
		ContentRef: "object://drafts/1", Parents: []ArtifactRef{},
		Producer: "core.writing.draft", CapabilityVersion: "1.0.0",
		InputHashes: []string{testHash("input")}, Trace: testTrace(),
	}
}

func appendTestEvent(t *testing.T, store *Store, event RunEvent) RunEvent {
	t.Helper()
	var appended RunEvent
	if err := store.InTransaction(context.Background(), func(tx *Tx) error {
		var err error
		appended, err = tx.AppendRunEvent(context.Background(), event)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return appended
}

func testContract(t *testing.T) writingkernel.WritingContract {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join("..", "..", "..", "specs", "lcp", "v1", "fixtures", "writing-contract.valid.json"))
	if err != nil {
		t.Fatal(err)
	}
	contract, err := writingkernel.DecodeWritingContractStrict(payload)
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

func resealContract(t *testing.T, contract writingkernel.WritingContract) writingkernel.WritingContract {
	t.Helper()
	for index := range contract.SourceAttributions {
		hash, err := contract.FieldValueHash(contract.SourceAttributions[index].FieldPath)
		if err != nil {
			t.Fatal(err)
		}
		contract.SourceAttributions[index].ValueHash = hash
	}
	sealed, err := contract.WithComputedHash()
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

func testDocumentVersion(t *testing.T, documentID, versionID string, base *string, text string) writingkernel.DocumentVersion {
	t.Helper()
	origin := writingkernel.Origin{Kind: writingkernel.OriginSystem, Ref: "writingstore/test"}
	textNode := &writingkernel.DocumentNode{
		BlockID: "blk_text", Type: writingkernel.NodeTypeText, Text: text,
		Attrs: map[string]any{}, Children: []*writingkernel.DocumentNode{}, Origin: origin,
	}
	paragraph := &writingkernel.DocumentNode{
		BlockID: "blk_paragraph", Type: writingkernel.NodeTypeParagraph,
		Attrs: map[string]any{}, Children: []*writingkernel.DocumentNode{textNode}, Origin: origin,
	}
	section := &writingkernel.DocumentNode{
		BlockID: "blk_section", Type: writingkernel.NodeTypeSection,
		Attrs: map[string]any{"level": 1}, Children: []*writingkernel.DocumentNode{paragraph}, Origin: origin,
	}
	document := writingkernel.DocumentVersion{
		SchemaVersion: writingkernel.SchemaVersionV1, DocumentID: documentID,
		VersionID: versionID, BaseVersionID: base,
		Root: &writingkernel.DocumentNode{BlockID: "blk_root", Type: writingkernel.NodeTypeDocument,
			Attrs: map[string]any{}, Children: []*writingkernel.DocumentNode{section}, Origin: origin},
	}
	sealed, err := document.WithComputedHashes()
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

func testPlanEnvelope(t *testing.T, contract writingkernel.WritingContract) writingplan.WritingPlanEnvelope {
	t.Helper()
	now := time.Now().UTC()
	intent, err := (writingplan.IntentPlan{IntentPlanID: "iplan_store",
		ContractRef: writingplan.ObjectRef{ID: contract.ContractID, Version: contract.Version, Hash: contract.ContractHash},
		Summary:     "produce a governed draft", CreatedBy: writingplan.ActorSystem, CreatedAt: now,
		ProposedSteps: []writingplan.ProposedStep{{StepID: "draft", Objective: "draft the document",
			CapabilityHint: "core.writing.draft", DependsOn: []string{}}},
	}).WithComputedHash()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := (writingplan.ExecutablePlan{PlanID: "plan_store",
		IntentPlanRef: writingplan.ObjectRef{ID: intent.IntentPlanID, Version: 1, Hash: intent.IntentPlanHash},
		TrustLevel:    writingplan.TrustT3, Status: writingplan.PlanValidated, RootNodeID: "node_draft",
		Nodes: []writingplan.PlanNode{{NodeID: "node_draft", Kind: writingplan.NodeAction,
			Capability: "core.writing.draft", CapabilityVersion: "1.0.0", DependsOn: []string{},
			InputArtifactTypes:  []writingplan.ArtifactType{"contract"},
			OutputArtifactTypes: []writingplan.ArtifactType{"full_draft"},
			Bounds:              writingplan.Bounds{MaxAttempts: 1, MaxConcurrency: 1, MaxItems: 1, MaxCostUSD: 1, TimeoutMS: 1000},
			FailurePath:         writingplan.FailureFail}},
		StaticValidation: writingplan.StaticValidation{Valid: true, CheckedAt: now, Errors: []string{},
			CapabilityRegistryVersion: "registry-test", BudgetValid: true, PermissionsValid: true,
			ArtifactFlowValid: true, FailurePathsValid: true},
	}).WithComputedHash()
	if err != nil {
		t.Fatal(err)
	}
	decision := writingplan.StrategyDecision{DecisionID: "decision_store", IntentPlanRef: plan.IntentPlanRef,
		Candidates: []writingplan.StrategyCandidate{{PlanHash: plan.PlanHash, TrustLevel: plan.TrustLevel,
			EstimatedCostUSD: 1, EstimatedDurationMS: 1000, EstimatedConfidence: .8}},
		SelectedPlanHash: plan.PlanHash, SelectionSource: writingplan.SelectionSystem,
		RequestedOrchestration: writingkernel.OrchestrationModeAuto,
		EffectiveOrchestration: writingkernel.OrchestrationModeFast,
		ReasonCode:             "test", Summary: "test plan", Confidence: .8,
		DegradationConditions: []string{}, CreatedAt: now,
	}
	envelope := writingplan.WritingPlanEnvelope{SchemaVersion: writingplan.SchemaVersion,
		IntentPlan: intent, ExecutablePlan: plan, StrategyDecision: decision}
	if err := envelope.Validate(); err != nil {
		t.Fatal(err)
	}
	return envelope
}

func testTrace() TraceContext {
	return TraceContext{Provenance: map[string]any{"source": "writingstore/test"},
		SourceRefs: []string{}, Actor: Actor{Type: ActorSystem, ID: "writingstore-test"}}
}

func testHash(seed string) string {
	return StableID("sha256:", seed) + strings.Repeat("0", 32)
}

func TestProjectMemoryCandidateLifecycleRequiresUserCommit(t *testing.T) {
	if integrationDB == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	// Truncate project scopes first: writing_documents references
	// writing_projects, so a later CASCADE would drop the fixture document.
	ctx := context.Background()
	if _, err := integrationDB.ExecContext(ctx, `TRUNCATE project_facts, project_memory_candidates, writing_projects CASCADE`); err != nil {
		t.Fatalf("reset project tables: %v", err)
	}
	store, fixture := newIntegrationFixture(t, false)
	var userID string
	if err := integrationDB.QueryRowContext(ctx, `SELECT owner_user_id::text FROM writing_documents WHERE document_id=$1`, fixture.documentID).Scan(&userID); err != nil {
		t.Fatalf("load fixture user: %v", err)
	}
	user := Actor{Type: ActorUser, ID: userID}
	projectID := "prj_store"
	if err := store.CreateProject(ctx, ProjectRecord{ProjectID: projectID, OwnerUserID: userID, Title: "V2.9 M1", Actor: user}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetProject(ctx, projectID); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDocumentProject(ctx, fixture.documentID, projectID); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDocumentProject(ctx, fixture.documentID, "prj_absent"); err == nil {
		t.Fatal("missing project accepted by FK")
	}

	asOf := time.Now().UTC().Add(-2 * time.Hour)
	candidate := func(id, batchID, subject, predicate, object string) projectmemory.Candidate {
		return projectmemory.Candidate{CandidateID: id, BatchID: batchID, ProjectID: projectID,
			Subject: subject, Predicate: predicate, Object: object, AsOf: asOf,
			SourceRefs: []string{"doc_store"}, SubmittedByType: string(ActorModel)}
	}
	mustCommit := func(id, factID string) projectmemory.Fact {
		t.Helper()
		fact, committed, err := store.CommitMemoryCandidate(ctx, id, user, factID)
		if err != nil || !committed {
			t.Fatalf("commit %s fact=%#v committed=%v err=%v", id, fact, committed, err)
		}
		return fact
	}

	// A batch containing one invalid candidate fails whole: no partial lanes.
	invalid := []projectmemory.Candidate{candidate("cand_store_1", "bat_store", "林然", "location", "旧书店"),
		{CandidateID: "cand_store_bad", BatchID: "bat_store", ProjectID: projectID,
			Subject: "林然", Predicate: "location", Object: "无处", AsOf: asOf, SubmittedByType: string(ActorModel)}}
	if err := store.StageMemoryCandidates(ctx, invalid); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("invalid batch err=%v", err)
	}
	if staged, err := store.ListStagedMemoryCandidates(ctx, projectID, ""); err != nil || len(staged) != 0 {
		t.Fatalf("staged after failed batch=%d err=%v", len(staged), err)
	}

	batch := []projectmemory.Candidate{candidate("cand_store_1", "bat_store", "林然", "location", "旧书店"),
		candidate("cand_store_rel", "bat_store", "林然", "relationship", "陈默"),
		candidate("cand_store_ext", "bat_store", "林然", "x-mood", "沉静")}
	if err := store.StageMemoryCandidates(ctx, batch); err != nil {
		t.Fatal(err)
	}
	staged, err := store.ListStagedMemoryCandidates(ctx, projectID, "bat_store")
	if err != nil || len(staged) != 3 {
		t.Fatalf("staged=%d err=%v", len(staged), err)
	}
	var extended *projectmemory.Candidate
	for i := range staged {
		if staged[i].CandidateID == "cand_store_ext" {
			extended = &staged[i]
		}
	}
	if extended == nil || len(extended.Warnings) != 1 || extended.Warnings[0] != "extended_predicate:x-mood" || !extended.ExtendedPredicate {
		t.Fatalf("extension candidate=%#v", extended)
	}

	// HITL gate: only a user actor may turn candidates into canon.
	if _, _, err := store.CommitMemoryCandidate(ctx, "cand_store_1", Actor{Type: ActorModel, ID: "extractor"}, "fact_store_1"); err == nil || !strings.Contains(err.Error(), "user actor") {
		t.Fatalf("model commit err=%v", err)
	}
	fact := mustCommit("cand_store_1", "fact_store_1")
	if fact.Predicate != "location" || fact.Subject != "林然" || fact.ValidTo != nil || fact.ContentHash == "" {
		t.Fatalf("active fact=%#v", fact)
	}

	// Replaying the same triple through a new candidate is idempotent: the
	// existing fact comes back and the replayed candidate closes.
	if err := store.StageMemoryCandidates(ctx, []projectmemory.Candidate{candidate("cand_store_replay", "bat_store_replay", "林然", "location", "旧书店")}); err != nil {
		t.Fatal(err)
	}
	fact, committed, err := store.CommitMemoryCandidate(ctx, "cand_store_replay", user, "fact_store_replay")
	if err != nil || committed || fact.FactID != "fact_store_1" {
		t.Fatalf("replay fact=%#v committed=%v err=%v", fact, committed, err)
	}

	// A state change supersedes the previous location. A state change predating
	// the active fact is rejected: the closed interval would violate
	// valid_to > valid_from.
	relocation := candidate("cand_store_move", "bat_store_move", "林然", "location", "咖啡馆")
	relocation.AsOf = asOf.Add(time.Hour)
	if err := store.StageMemoryCandidates(ctx, []projectmemory.Candidate{relocation}); err != nil {
		t.Fatal(err)
	}
	mustCommit("cand_store_move", "fact_store_move")
	old, err := store.GetFact(ctx, "fact_store_1")
	if err != nil || old.ValidTo == nil || old.SupersededBy != "fact_store_move" {
		t.Fatalf("superseded old=%#v err=%v", old, err)
	}
	if err := store.StageMemoryCandidates(ctx, []projectmemory.Candidate{candidate("cand_store_back", "bat_store_back", "林然", "location", "车站")}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CommitMemoryCandidate(ctx, "cand_store_back", user, "fact_store_back"); !errors.Is(err, ErrConflict) {
		t.Fatalf("backdated commit err=%v", err)
	}

	// Relationship facts fold their endpoint pair: committing the reverse
	// direction is the idempotent path, while a different pair coexists.
	mustCommit("cand_store_rel", "fact_store_rel")
	if err := store.StageMemoryCandidates(ctx, []projectmemory.Candidate{candidate("cand_store_rel_reverse", "bat_store_rel_reverse", "陈默", "relationship", "林然")}); err != nil {
		t.Fatal(err)
	}
	fact, committed, err = store.CommitMemoryCandidate(ctx, "cand_store_rel_reverse", user, "fact_store_rel_reverse")
	if err != nil || committed || fact.FactID != "fact_store_rel" {
		t.Fatalf("reverse rel fact=%#v committed=%v err=%v", fact, committed, err)
	}
	if err := store.StageMemoryCandidates(ctx, []projectmemory.Candidate{candidate("cand_store_rel2", "bat_store_rel2", "陈默", "relationship", "老周")}); err != nil {
		t.Fatal(err)
	}
	mustCommit("cand_store_rel2", "fact_store_rel2")

	active, err := store.ListActiveFacts(ctx, projectID, "")
	if err != nil || len(active) != 3 {
		t.Fatalf("active=%d err=%v", len(active), err)
	}
	bySubject, err := store.ListActiveFacts(ctx, projectID, "  林然 ")
	if err != nil || len(bySubject) != 2 {
		t.Fatalf("bySubject=%d err=%v", len(bySubject), err)
	}
	if bySubject, err = store.ListActiveFacts(ctx, projectID, "陈默"); err != nil || len(bySubject) != 1 {
		t.Fatalf("bySubject=%d err=%v", len(bySubject), err)
	}

	// Fact content is immutable at the database level; only the interval
	// columns may move.
	if _, err := integrationDB.ExecContext(ctx, `UPDATE project_facts SET subject='篡改' WHERE fact_id=$1`, "fact_store_1"); err == nil {
		t.Fatal("immutable fact accepted a content update")
	}
	// Supersede is HITL-only and requires a successor that exists.
	if err := store.SupersedeFact(ctx, "fact_store_move", "fact_absent", time.Now().UTC(), Actor{Type: ActorModel, ID: "worker"}); err == nil || !strings.Contains(err.Error(), "user actor") {
		t.Fatalf("model supersede err=%v", err)
	}
	if err := store.SupersedeFact(ctx, "fact_store_move", "fact_absent", time.Now().UTC(), user); err == nil {
		t.Fatal("absent successor accepted")
	}
	if err := store.RejectMemoryCandidate(ctx, "cand_store_ext", user); err != nil {
		t.Fatal(err)
	}
	if err := store.RejectMemoryCandidate(ctx, "cand_store_back", user); err != nil {
		t.Fatal(err)
	}
	if staged, err := store.ListStagedMemoryCandidates(ctx, projectID, ""); err != nil || len(staged) != 0 {
		t.Fatalf("staged after reject=%d err=%v", len(staged), err)
	}
}

func TestProjectMemoryClaimCorroborationAndPromotion(t *testing.T) {
	if integrationDB == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	if _, err := integrationDB.ExecContext(ctx, `TRUNCATE project_facts, project_memory_candidates, project_claim_evidence, project_claims, project_entities, writing_projects CASCADE`); err != nil {
		t.Fatalf("reset project tables: %v", err)
	}
	store, fixture := newIntegrationFixture(t, false)
	var userID string
	if err := integrationDB.QueryRowContext(ctx, `SELECT owner_user_id::text FROM writing_documents WHERE document_id=$1`, fixture.documentID).Scan(&userID); err != nil {
		t.Fatalf("load fixture user: %v", err)
	}
	user := Actor{Type: ActorUser, ID: userID}
	projectID := "prj_claim"
	if err := store.CreateProject(ctx, ProjectRecord{ProjectID: projectID, OwnerUserID: userID, Title: "V2.9 M2", Actor: user}); err != nil {
		t.Fatal(err)
	}

	claim := projectmemory.Claim{ClaimID: "claim_store_1", BatchID: "bat_claim", ProjectID: projectID,
		Subject: "LuminBuddy", Predicate: "state", Object: "开源", AsOf: time.Now().UTC().Add(-time.Hour),
		SourceRunID: "run_store", RaisedByType: string(ActorModel)}
	if err := store.StageMemoryClaims(ctx, []projectmemory.Claim{claim}); err != nil {
		t.Fatal(err)
	}

	// Duplicate staging of the same open triple is rejected: corroboration
	// accumulates on the existing claim, never fragments.
	duplicate := claim
	duplicate.ClaimID = "claim_store_dup"
	if err := store.StageMemoryClaims(ctx, []projectmemory.Claim{duplicate}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate claim err=%v", err)
	}

	// The raising run is already one citation; a model may corroborate.
	supported, err := store.CorroborateMemoryClaim(ctx, "claim_store_1", "evd_store_2", "run_store_2", nil, Actor{Type: ActorModel, ID: "researcher"})
	if err != nil || supported.Status != "supported" {
		t.Fatalf("supported=%#v err=%v", supported, err)
	}
	// Re-recording the same citation is idempotent and does not flip again.
	replayed, err := store.CorroborateMemoryClaim(ctx, "claim_store_1", "evd_store_2_replay", "run_store_2", nil, Actor{Type: ActorModel, ID: "researcher"})
	if err != nil || replayed.Status != "supported" {
		t.Fatalf("replay=%#v err=%v", replayed, err)
	}

	// Promotion is HITL-only, and support never auto-promotes.
	if _, _, err := store.CommitMemoryClaim(ctx, "claim_store_1", Actor{Type: ActorModel, ID: "writer"}, "fact_claim_1"); err == nil || !strings.Contains(err.Error(), "user actor") {
		t.Fatalf("model promotion err=%v", err)
	}
	fact, promoted, err := store.CommitMemoryClaim(ctx, "claim_store_1", user, "fact_claim_1")
	if err != nil || !promoted || fact.Subject != "luminbuddy" || fact.Predicate != "state" {
		t.Fatalf("fact=%#v promoted=%v err=%v", fact, promoted, err)
	}
	claims, err := store.ListMemoryClaims(ctx, projectID, "promoted")
	if err != nil || len(claims) != 1 || claims[0].PromotedFactID != fact.FactID {
		t.Fatalf("promoted claims=%#v err=%v", claims, err)
	}
	// The promoted claim no longer accepts evidence or re-promotion.
	if _, err := store.CorroborateMemoryClaim(ctx, "claim_store_1", "evd_store_3", "run_store_3", nil, Actor{Type: ActorModel, ID: "researcher"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("evidence on promoted claim err=%v", err)
	}
	if _, _, err := store.CommitMemoryClaim(ctx, "claim_store_1", user, "fact_claim_replay"); !errors.Is(err, ErrConflict) {
		t.Fatalf("re-promotion err=%v", err)
	}

	// A second claim stays open, is rejected by the user, then vanishes from
	// the rejectable pool.
	if err := store.StageMemoryClaims(ctx, []projectmemory.Claim{{
		ClaimID: "claim_store_2", BatchID: "bat_claim_2", ProjectID: projectID,
		Subject: "LuminBuddy", Predicate: "state", Object: "闭源", AsOf: time.Now().UTC(),
		SourceRefs: []string{"doc_store"}, RaisedByType: string(ActorModel)}}); err != nil {
		t.Fatal(err)
	}
	if err := store.RejectMemoryClaim(ctx, "claim_store_2", Actor{Type: ActorModel, ID: "writer"}); err == nil || !strings.Contains(err.Error(), "user actor") {
		t.Fatalf("model rejection err=%v", err)
	}
	if err := store.RejectMemoryClaim(ctx, "claim_store_2", user); err != nil {
		t.Fatal(err)
	}
	if err := store.RejectMemoryClaim(ctx, "claim_store_2", user); err == nil {
		t.Fatal("double rejection accepted")
	}
}

func TestProjectMemoryEntityCandidatePool(t *testing.T) {
	if integrationDB == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	if _, err := integrationDB.ExecContext(ctx, `TRUNCATE project_facts, project_memory_candidates, project_claim_evidence, project_claims, project_entities, writing_projects CASCADE`); err != nil {
		t.Fatalf("reset project tables: %v", err)
	}
	store, fixture := newIntegrationFixture(t, false)
	var userID string
	if err := integrationDB.QueryRowContext(ctx, `SELECT owner_user_id::text FROM writing_documents WHERE document_id=$1`, fixture.documentID).Scan(&userID); err != nil {
		t.Fatalf("load fixture user: %v", err)
	}
	user := Actor{Type: ActorUser, ID: userID}
	projectID := "prj_entity"
	if err := store.CreateProject(ctx, ProjectRecord{ProjectID: projectID, OwnerUserID: userID, Title: "V2.9 M2 entities", Actor: user}); err != nil {
		t.Fatal(err)
	}

	// A model stages a candidate birth certificate with case-variant aliases.
	entity := &projectmemory.Entity{EntityID: "ent_store_1", ProjectID: projectID, EntityKind: "organization",
		CanonicalName: "Acme Labs", Aliases: []string{"ACME", "acme"}, SourceRunID: "run_store", RaisedByType: string(ActorModel)}
	if err := store.StageMemoryEntity(ctx, entity); err != nil {
		t.Fatal(err)
	}
	if len(entity.Aliases) != 1 || entity.Aliases[0] != "ACME" || entity.Status != "candidate" {
		t.Fatalf("normalized entity=%#v", entity)
	}

	// A second live identity with the same (project, kind, name) collides.
	twin := &projectmemory.Entity{EntityID: "ent_store_twin", ProjectID: projectID, EntityKind: "organization",
		CanonicalName: "Acme Labs", SourceRunID: "run_store", RaisedByType: string(ActorModel)}
	if err := store.StageMemoryEntity(ctx, twin); err == nil {
		t.Fatal("live identity collision accepted")
	}

	// Promotion is HITL-only; then the name is freed by archiving.
	if err := store.PromoteMemoryEntity(ctx, "ent_store_1", Actor{Type: ActorModel, ID: "writer"}); err == nil || !strings.Contains(err.Error(), "user actor") {
		t.Fatalf("model promotion err=%v", err)
	}
	if err := store.PromoteMemoryEntity(ctx, "ent_store_1", user); err != nil {
		t.Fatal(err)
	}
	promoted, err := store.ListMemoryEntities(ctx, projectID, "organization", "promoted")
	if err != nil || len(promoted) != 1 || promoted[0].EntityID != "ent_store_1" {
		t.Fatalf("promoted=%#v err=%v", promoted, err)
	}
	if err := store.ArchiveMemoryEntity(ctx, "ent_store_1", user); err != nil {
		t.Fatal(err)
	}
	// The archived identity can be re-registered.
	if err := store.StageMemoryEntity(ctx, twin); err != nil {
		t.Fatalf("re-register after archive: %v", err)
	}
	entities, err := store.ListMemoryEntities(ctx, projectID, "", "")
	if err != nil || len(entities) != 2 {
		t.Fatalf("entities=%d err=%v", len(entities), err)
	}
	// Entity identity columns are immutable at the database level.
	if _, err := integrationDB.ExecContext(ctx, `UPDATE project_entities SET canonical_name='篡改' WHERE entity_id=$1`, "ent_store_1"); err == nil {
		t.Fatal("immutable entity accepted an identity update")
	}
	if err := store.ArchiveMemoryEntity(ctx, "ent_absent", user); err == nil {
		t.Fatal("absent entity archived")
	}
}

func TestProjectMemoryCuratedStateLifecycle(t *testing.T) {
	if integrationDB == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	if _, err := integrationDB.ExecContext(ctx, `TRUNCATE project_facts, project_memory_candidates, project_claim_evidence, project_claims, project_entities, project_terminology, project_decisions, project_open_questions, project_threads, writing_projects CASCADE`); err != nil {
		t.Fatalf("reset project tables: %v", err)
	}
	store, fixture := newIntegrationFixture(t, false)
	var userID string
	if err := integrationDB.QueryRowContext(ctx, `SELECT owner_user_id::text FROM writing_documents WHERE document_id=$1`, fixture.documentID).Scan(&userID); err != nil {
		t.Fatalf("load fixture user: %v", err)
	}
	user := Actor{Type: ActorUser, ID: userID}
	projectID := "prj_curated"
	if err := store.CreateProject(ctx, ProjectRecord{ProjectID: projectID, OwnerUserID: userID, Title: "V2.9 M2.5", Actor: user}); err != nil {
		t.Fatal(err)
	}
	model := Actor{Type: ActorModel, ID: "curator"}

	// Terminology: stage -> model promotion refused -> user promotion ->
	// duplicate live term rejected by the partial unique index -> archive
	// frees the term for re-registration.
	entry := &projectmemory.Terminology{TerminologyID: "term_store_1", ProjectID: projectID,
		Term: "生成式检索", Definition: "由模型直接生成检索结果的方法", Aliases: []string{"GSR"},
		Forbidden: []string{"AI搜索"}, SourceRunID: "run_store", RaisedByType: string(ActorModel)}
	if err := store.StageTerminology(ctx, entry); err != nil {
		t.Fatal(err)
	}
	if entry.Status != "candidate" {
		t.Fatalf("status=%q", entry.Status)
	}
	if err := store.PromoteTerminology(ctx, "term_store_1", model); err == nil || !strings.Contains(err.Error(), "user actor") {
		t.Fatalf("model promotion err=%v", err)
	}
	if err := store.PromoteTerminology(ctx, "term_store_1", user); err != nil {
		t.Fatal(err)
	}
	// The (project, term) identity collides at stage time while an entry is
	// live — same rule as the entity pool; archiving frees the term.
	twin := &projectmemory.Terminology{TerminologyID: "term_store_twin", ProjectID: projectID,
		Term: "生成式检索", SourceRunID: "run_store", RaisedByType: string(ActorModel)}
	if err := store.StageTerminology(ctx, twin); err == nil {
		t.Fatal("duplicate live term staged")
	}
	if err := store.ArchiveTerminology(ctx, "term_store_1", user); err != nil {
		t.Fatal(err)
	}
	if err := store.StageTerminology(ctx, twin); err != nil {
		t.Fatalf("stage after archive: %v", err)
	}
	if err := store.PromoteTerminology(ctx, "term_store_twin", user); err != nil {
		t.Fatalf("promote after archive: %v", err)
	}
	glossary, err := store.ListTerminology(ctx, projectID, "active")
	if err != nil || len(glossary) != 1 || glossary[0].TerminologyID != "term_store_twin" {
		t.Fatalf("glossary=%#v err=%v", glossary, err)
	}
	if _, err := integrationDB.ExecContext(ctx, `UPDATE project_terminology SET term='篡改' WHERE terminology_id=$1`, "term_store_twin"); err == nil {
		t.Fatal("immutable terminology accepted a content update")
	}

	// Decisions: two candidates, promotion order decides the supersede chain.
	first := &projectmemory.Decision{DecisionID: "dec_store_1", ProjectID: projectID,
		Statement: "全文统一用“模型”指代底层引擎", Rationale: "与产品文案一致", SourceRunID: "run_store", RaisedByType: string(ActorModel)}
	second := &projectmemory.Decision{DecisionID: "dec_store_2", ProjectID: projectID,
		Statement: "全文统一用“引擎”指代底层引擎", Rationale: "更中性", Supersedes: "dec_store_1",
		SourceRefs: []string{"doc_store"}, RaisedByType: string(ActorModel)}
	if err := store.StageDecision(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := store.StageDecision(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := store.PromoteDecision(ctx, "dec_store_2", user); err == nil {
		t.Fatal("superseding an inactive decision accepted")
	}
	if err := store.PromoteDecision(ctx, "dec_store_1", user); err != nil {
		t.Fatal(err)
	}
	if err := store.PromoteDecision(ctx, "dec_store_2", model); err == nil || !strings.Contains(err.Error(), "user actor") {
		t.Fatalf("model decision promotion err=%v", err)
	}
	if err := store.PromoteDecision(ctx, "dec_store_2", user); err != nil {
		t.Fatal(err)
	}
	decisions, err := store.ListDecisions(ctx, projectID, "superseded")
	if err != nil || len(decisions) != 1 || decisions[0].DecisionID != "dec_store_1" {
		t.Fatalf("superseded=%#v err=%v", decisions, err)
	}

	// Open questions: raise by model, answer/drop by user only.
	question := &projectmemory.OpenQuestion{QuestionID: "qu_store_1", ProjectID: projectID,
		Question: "数据口径以哪家年报为准？", Context: "第三章引用营收数据", SourceRunID: "run_store", RaisedByType: string(ActorModel)}
	if err := store.RaiseOpenQuestion(ctx, question); err != nil {
		t.Fatal(err)
	}
	if err := store.AnswerOpenQuestion(ctx, "qu_store_1", "以 2025 年度报告为准", "", model); err == nil || !strings.Contains(err.Error(), "user actor") {
		t.Fatalf("model answer err=%v", err)
	}
	if err := store.AnswerOpenQuestion(ctx, "qu_store_1", "", "", user); err == nil {
		t.Fatal("empty answer accepted")
	}
	if err := store.AnswerOpenQuestion(ctx, "qu_store_1", "以 2025 年度报告为准", "", user); err != nil {
		t.Fatal(err)
	}
	if err := store.DropOpenQuestion(ctx, "qu_store_1", user); err == nil {
		t.Fatal("answering twice via drop accepted")
	}
	if err := store.RaiseOpenQuestion(ctx, &projectmemory.OpenQuestion{QuestionID: "qu_store_2", ProjectID: projectID,
		Question: "是否引用竞品定价？", SourceRefs: []string{"doc_store"}, RaisedByType: string(ActorModel)}); err != nil {
		t.Fatal(err)
	}
	if err := store.DropOpenQuestion(ctx, "qu_store_2", user); err != nil {
		t.Fatal(err)
	}
	openQuestions, err := store.ListOpenQuestions(ctx, projectID, "open")
	if err != nil || len(openQuestions) != 0 {
		t.Fatalf("open questions=%d err=%v", len(openQuestions), err)
	}

	// Threads: resident thread survives, resolution links a fact.
	fact, _, err := store.CommitMemoryCandidate(ctx, func() string {
		if err := store.StageMemoryCandidates(ctx, []projectmemory.Candidate{{
			CandidateID: "cand_thread", BatchID: "bat_thread", ProjectID: projectID,
			Subject: "LuminBuddy", Predicate: "state", Object: "开源", AsOf: time.Now().UTC().Add(-time.Hour),
			SourceRefs: []string{"doc_store"}, SubmittedByType: string(ActorModel)}}); err != nil {
			t.Fatal(err)
		}
		return "cand_thread"
	}(), user, "fact_thread")
	if err != nil {
		t.Fatal(err)
	}
	thread := &projectmemory.Thread{ThreadID: "thr_store_1", ProjectID: projectID,
		Label: "论点链：检索优于重排", Summary: "贯穿全文的核心论证", Resident: true,
		SourceRunID: "run_store", RaisedByType: string(ActorModel)}
	if err := store.StageThread(ctx, thread); err != nil {
		t.Fatal(err)
	}
	if err := store.ResolveThread(ctx, "thr_store_1", "", model); err == nil || !strings.Contains(err.Error(), "user actor") {
		t.Fatalf("model resolution err=%v", err)
	}
	if err := store.PromoteThread(ctx, "thr_store_1", user); err != nil {
		t.Fatal(err)
	}
	if err := store.ResolveThread(ctx, "thr_store_1", fact.FactID, user); err != nil {
		t.Fatal(err)
	}
	threads, err := store.ListThreads(ctx, projectID, "resolved")
	if err != nil || len(threads) != 1 || threads[0].ResolvedFactID != fact.FactID {
		t.Fatalf("resolved threads=%#v err=%v", threads, err)
	}
	// Fact content is immutable; only interval columns move (M1 rule holds
	// for curated references too).
	if _, err := integrationDB.ExecContext(ctx, `UPDATE project_threads SET label='篡改' WHERE thread_id=$1`, "thr_store_1"); err == nil {
		t.Fatal("immutable thread accepted a content update")
	}
}

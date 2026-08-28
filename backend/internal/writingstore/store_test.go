package writingstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
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
	if _, err := integrationDB.ExecContext(ctx, `TRUNCATE writing_documents CASCADE`); err != nil {
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

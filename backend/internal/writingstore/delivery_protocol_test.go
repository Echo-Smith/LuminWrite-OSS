package writingstore

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
)

// TestDeliveryCommitProtocol exercises the M1.0 delivery commit protocol the
// governed runtime will drive from its quality node: a delivery snapshot that
// binds the draft's candidate version to an accepted quality report, promoted
// in one transaction, with the document quality gate satisfied and every
// cross-reference the finalize gate will later require. Idempotent replay and
// gate violations must fail closed without partial state.
func TestDeliveryCommitProtocol(t *testing.T) {
	if integrationDB == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	store, fixture := newIntegrationFixture(t, true)
	ctx := context.Background()
	candidateVersionID := fixture.version.VersionID // committed by the fixture as candidate_draft
	trace := TraceContext{Actor: Actor{Type: ActorPolicy, ID: "delivery_protocol"},
		Provenance: map[string]any{}, SourceRefs: []string{}}

	acceptedReport := QualityReportRecord{
		ReportID: StableID("qr_", fixture.runID, "delivery"), ReportVersion: 1,
		RunID: fixture.runID, PlanID: fixture.planID, PlanVersion: 1,
		DocumentID: fixture.documentID, CandidateVersionID: candidateVersionID,
		ContentHash:        artifactTestHash([]byte("quality report payload")),
		RequestedAssurance: writingkernel.AssuranceLevelStandard,
		AchievedAssurance:  writingkernel.AssuranceLevelStandard,
		AssuranceSatisfied: true,
		QualityState:       QualityAcceptedDraft,
		VersionConsistent:  true,
		BlockerCount:       0, ErrorCount: 0, OpenErrorCount: 0, WaivedErrorCount: 0, WarningCount: 1,
		Payload:    map[string]any{"scenario": "m1_0_protocol"},
		SnapshotID: StableID("snap_", fixture.runID, "delivery"), SnapshotVersion: 1,
		Trace: trace,
	}
	bundle := CheckpointBundle{
		Snapshot: SnapshotRecord{
			SnapshotID: acceptedReport.SnapshotID, SnapshotVersion: 1, RunID: fixture.runID,
			CheckpointID: StableID("chk_", fixture.runID, "delivery"), PlanID: fixture.planID,
			PlanVersion: 1, ContractID: fixture.contract.ContractID, ContractVersion: fixture.contract.Version,
			ContractHash: fixture.contract.ContractHash, DocumentID: fixture.documentID,
			CandidateVersionID: candidateVersionID, QualityReportID: acceptedReport.ReportID,
			QualityReportVersion: acceptedReport.ReportVersion, ContentHash: artifactTestHash([]byte("delivery manifest")),
			Status: "persisted", Complete: true,
			Manifest:   map[string]any{"delivery": true},
			StorageRef: "db://writing_snapshots/" + acceptedReport.SnapshotID,
			Trace:      trace, CreatedAt: time.Now().UTC(), PersistedAt: time.Now().UTC(),
		},
		QualityReport: &acceptedReport,
		DocumentPromotion: &DocumentPromotion{DocumentID: fixture.documentID, VersionID: candidateVersionID,
			QualityState: QualityAcceptedDraft, AcceptedAt: time.Now().UTC()},
		AchievedAssurance: writingkernel.AssuranceLevelStandard,
	}
	if _, err := store.CommitCheckpoint(ctx, bundle); err != nil {
		t.Fatalf("delivery commit: %v", err)
	}

	// Snapshot carries the candidate-version + quality-report binding.
	var snapCandidate, snapReportID string
	var snapReportVersion int
	if err := integrationDB.QueryRowContext(ctx, `
		SELECT candidate_version_id, quality_report_id, quality_report_version
		FROM writing_snapshots WHERE snapshot_id=$1 AND snapshot_version=1
	`, acceptedReport.SnapshotID).Scan(&snapCandidate, &snapReportID, &snapReportVersion); err != nil {
		t.Fatal(err)
	}
	if snapCandidate != candidateVersionID || snapReportID != acceptedReport.ReportID || snapReportVersion != 1 {
		t.Fatalf("snapshot bindings candidate=%q report=%s/%d", snapCandidate, snapReportID, snapReportVersion)
	}

	// Quality report row exists with the candidate-version linkage the
	// finalize gate cross-references.
	var reportCandidate, reportState string
	var reportSnapshotID string
	if err := integrationDB.QueryRowContext(ctx, `
		SELECT candidate_version_id, quality_state, snapshot_manifest_id
		FROM writing_quality_reports WHERE report_id=$1 AND report_version=1
	`, acceptedReport.ReportID).Scan(&reportCandidate, &reportState, &reportSnapshotID); err != nil {
		t.Fatal(err)
	}
	if reportCandidate != candidateVersionID || reportState != QualityAcceptedDraft || reportSnapshotID != acceptedReport.SnapshotID {
		t.Fatalf("quality report linkage candidate=%q state=%s snapshot=%q", reportCandidate, reportState, reportSnapshotID)
	}

	// Document promotion: the candidate version advanced to accepted with the
	// quality gate's back-filled references.
	var qualityState, qualityReportID, snapshotManifestID string
	if err := integrationDB.QueryRowContext(ctx, `
		SELECT quality_state, quality_report_id, snapshot_manifest_id
		FROM writing_document_versions WHERE document_id=$1 AND version_id=$2
	`, fixture.documentID, candidateVersionID).Scan(&qualityState, &qualityReportID, &snapshotManifestID); err != nil {
		t.Fatal(err)
	}
	if qualityState != QualityAcceptedDraft || qualityReportID != acceptedReport.ReportID || snapshotManifestID != acceptedReport.SnapshotID {
		t.Fatalf("promotion state=%s report=%q snapshot=%q", qualityState, qualityReportID, snapshotManifestID)
	}

	// Idempotent replay: same bundle commits cleanly, no duplicate rows.
	if _, err := store.CommitCheckpoint(ctx, bundle); err != nil {
		t.Fatalf("replay errored: %v", err)
	}
	var reportCount int
	if err := integrationDB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM writing_quality_reports WHERE report_id=$1
	`, acceptedReport.ReportID).Scan(&reportCount); err != nil || reportCount != 1 {
		t.Fatalf("replay duplicated report rows=%d err=%v", reportCount, err)
	}

	// Gate violation: a blocker finding prevents accepted promotion. The
	// violating delivery uses its own snapshot id (an independent commit
	// attempt), and the whole transaction must roll back — no snapshot row,
	// no report row.
	blocked := acceptedReport
	blocked.ReportID = StableID("qr_", fixture.runID, "blocked")
	blocked.SnapshotID = StableID("snap_", fixture.runID, "blocked")
	blocked.BlockerCount = 1
	blockedBundle := bundle
	blockedBundle.QualityReport = &blocked
	blockedBundle.DocumentPromotion = &DocumentPromotion{DocumentID: fixture.documentID,
		VersionID: candidateVersionID, QualityState: QualityAcceptedDraft, AcceptedAt: time.Now().UTC()}
	blockedBundle.Snapshot.SnapshotID = blocked.SnapshotID
	blockedBundle.Snapshot.CheckpointID = StableID("chk_", fixture.runID, "blocked")
	blockedBundle.Snapshot.QualityReportID = blocked.ReportID
	blockedBundle.Snapshot.QualityReportVersion = blocked.ReportVersion
	if _, err := store.CommitCheckpoint(ctx, blockedBundle); err == nil ||
		!strings.Contains(err.Error(), "BLOCKER or open ERROR prevents accepted draft") {
		t.Fatalf("blocker finding must fail closed: %v", err)
	}
	var leftovers int
	if err := integrationDB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM writing_snapshots WHERE snapshot_id=$1
	`, blocked.SnapshotID).Scan(&leftovers); err != nil || leftovers != 0 {
		t.Fatalf("failed delivery commit left snapshot rows=%d err=%v", leftovers, err)
	}

	// Binding violation: report and snapshot pointing at different candidates
	// must be rejected inside the transaction (fresh snapshot id so the
	// binding check, not snapshot replay, is what fires).
	misbound := acceptedReport
	misbound.ReportID = StableID("qr_", fixture.runID, "misbound")
	misbound.CandidateVersionID = "ver_some_other_version"
	misboundBundle := bundle
	misboundBundle.QualityReport = &misbound
	misboundBundle.Snapshot.SnapshotID = StableID("snap_", fixture.runID, "misbound")
	misboundBundle.Snapshot.CheckpointID = StableID("chk_", fixture.runID, "misbound")
	if _, err := store.CommitCheckpoint(ctx, misboundBundle); err == nil ||
		!strings.Contains(err.Error(), "bindings differ") {
		t.Fatalf("misbound report/snapshot accepted: %v", err)
	}
}

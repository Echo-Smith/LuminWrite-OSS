package writingstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
)

type SnapshotRecord struct {
	SnapshotID           string
	SnapshotVersion      int
	RunID                string
	CheckpointID         string
	LedgerSequence       int64
	PlanID               string
	PlanVersion          int
	ContractID           string
	ContractVersion      int
	ContractHash         string
	DocumentID           string
	BaseVersionID        string
	CandidateVersionID   string
	QualityReportID      string
	QualityReportVersion int
	ContentHash          string
	Status               string
	Complete             bool
	Manifest             map[string]any
	StorageRef           string
	Trace                TraceContext
	CreatedAt            time.Time
	PersistedAt          time.Time
}

type DocumentPromotion struct {
	DocumentID   string
	VersionID    string
	QualityState string
	AcceptedAt   time.Time
	VerifiedAt   time.Time
}

type ArtifactPromotion struct {
	ArtifactID  string
	Version     int
	ContentHash string
}

type CheckpointBundle struct {
	Snapshot          SnapshotRecord
	QualityReport     *QualityReportRecord
	DocumentPromotion *DocumentPromotion
	Artifacts         []ArtifactPromotion
	AchievedAssurance writingkernel.AssuranceLevel
}

func (s *Store) CommitCheckpoint(ctx context.Context, bundle CheckpointBundle) (SnapshotRecord, error) {
	var committed SnapshotRecord
	err := s.InTransaction(ctx, func(tx *Tx) error {
		var err error
		committed, err = tx.CommitCheckpoint(ctx, bundle)
		return err
	})
	return committed, err
}

func (tx *Tx) CommitCheckpoint(ctx context.Context, bundle CheckpointBundle) (SnapshotRecord, error) {
	snapshot := bundle.Snapshot
	if err := validateID(snapshot.SnapshotID, "snap_", "snapshot_id"); err != nil {
		return SnapshotRecord{}, err
	}
	if snapshot.SnapshotVersion < 1 || snapshot.PlanVersion < 1 || snapshot.ContractVersion < 1 {
		return SnapshotRecord{}, fmt.Errorf("%w: snapshot object versions must be positive", ErrInvalidRecord)
	}
	if err := validateHash(snapshot.ContractHash, "snapshot contract_hash"); err != nil {
		return SnapshotRecord{}, err
	}
	if err := validateHash(snapshot.ContentHash, "snapshot content_hash"); err != nil {
		return SnapshotRecord{}, err
	}
	if snapshot.Status != "persisted" || !snapshot.Complete || snapshot.PersistedAt.IsZero() {
		return SnapshotRecord{}, fmt.Errorf("%w: checkpoint commit requires a complete persisted snapshot", ErrInvalidRecord)
	}
	if snapshot.Manifest == nil || snapshot.StorageRef == "" || snapshot.CheckpointID == "" {
		return SnapshotRecord{}, fmt.Errorf("%w: snapshot manifest, storage ref, and checkpoint are required", ErrInvalidRecord)
	}
	if err := snapshot.Trace.validate(); err != nil {
		return SnapshotRecord{}, err
	}

	var existingRunID, existingCheckpointID, existingHash string
	var existingSequence int64
	err := tx.tx.QueryRowContext(ctx, `
		SELECT run_id, checkpoint_id, content_hash, ledger_sequence FROM writing_snapshots
		WHERE snapshot_id=$1 AND snapshot_version=$2
	`, snapshot.SnapshotID, snapshot.SnapshotVersion).Scan(
		&existingRunID, &existingCheckpointID, &existingHash, &existingSequence)
	if err == nil {
		if existingRunID != snapshot.RunID || existingCheckpointID != snapshot.CheckpointID || existingHash != snapshot.ContentHash {
			return SnapshotRecord{}, fmt.Errorf("%w: snapshot %s version %d has different content", ErrImmutableConflict, snapshot.SnapshotID, snapshot.SnapshotVersion)
		}
		if err := tx.verifyCommittedCheckpoint(ctx, bundle); err != nil {
			return SnapshotRecord{}, err
		}
		snapshot.LedgerSequence = existingSequence
		return snapshot, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return SnapshotRecord{}, fmt.Errorf("check existing snapshot: %w", err)
	}

	if bundle.QualityReport != nil {
		report := *bundle.QualityReport
		if report.RunID != snapshot.RunID || report.SnapshotID != snapshot.SnapshotID ||
			report.SnapshotVersion != snapshot.SnapshotVersion || report.DocumentID != snapshot.DocumentID ||
			report.CandidateVersionID != snapshot.CandidateVersionID {
			return SnapshotRecord{}, fmt.Errorf("%w: quality report and snapshot bindings differ", ErrInvalidRecord)
		}
		if snapshot.QualityReportID != report.ReportID || snapshot.QualityReportVersion != report.ReportVersion {
			return SnapshotRecord{}, fmt.Errorf("%w: snapshot does not reference supplied quality report", ErrInvalidRecord)
		}
		report.SnapshotPersisted = true
		if err := tx.PutQualityReport(ctx, report); err != nil {
			return SnapshotRecord{}, err
		}
	} else if snapshot.QualityReportID != "" || snapshot.QualityReportVersion != 0 {
		return SnapshotRecord{}, fmt.Errorf("%w: snapshot quality reference has no supplied report", ErrInvalidRecord)
	}

	event, err := tx.AppendRunEvent(ctx, RunEvent{
		RunID:      snapshot.RunID,
		EventType:  "snapshot.created",
		EntityKind: "snapshot",
		EntityID:   snapshot.SnapshotID,
		Payload: map[string]any{
			"snapshot_version": snapshot.SnapshotVersion,
			"checkpoint_id":    snapshot.CheckpointID,
			"content_hash":     snapshot.ContentHash,
		},
		Trace: snapshot.Trace,
	})
	if err != nil {
		return SnapshotRecord{}, err
	}
	snapshot.LedgerSequence = event.Sequence
	manifest, err := marshalJSON(snapshot.Manifest, "snapshot manifest")
	if err != nil {
		return SnapshotRecord{}, err
	}
	provenance, sources, err := snapshot.Trace.values()
	if err != nil {
		return SnapshotRecord{}, err
	}
	createdAt := snapshot.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	_, err = tx.tx.ExecContext(ctx, `
		INSERT INTO writing_snapshots (
			snapshot_id, snapshot_version, run_id, checkpoint_id, ledger_sequence,
			plan_id, plan_version, contract_id, contract_version, contract_hash,
			document_id, base_version_id, candidate_version_id, quality_report_id,
			quality_report_version, content_hash, snapshot_status, complete,
			manifest_payload, storage_ref, provenance, source_refs,
			created_by_type, created_by_id, created_at, persisted_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,
		          $17,$18,$19,$20,$21,$22,$23,$24,$25,$26)
	`, snapshot.SnapshotID, snapshot.SnapshotVersion, snapshot.RunID,
		snapshot.CheckpointID, snapshot.LedgerSequence, snapshot.PlanID,
		snapshot.PlanVersion, snapshot.ContractID, snapshot.ContractVersion,
		snapshot.ContractHash, snapshot.DocumentID, nullString(snapshot.BaseVersionID),
		nullString(snapshot.CandidateVersionID), nullString(snapshot.QualityReportID),
		nullVersion(snapshot.QualityReportVersion), snapshot.ContentHash, snapshot.Status,
		snapshot.Complete, manifest, snapshot.StorageRef, provenance, sources,
		string(snapshot.Trace.Actor.Type), nullString(snapshot.Trace.Actor.ID),
		createdAt, snapshot.PersistedAt)
	if err != nil {
		return SnapshotRecord{}, fmt.Errorf("insert writing snapshot: %w", err)
	}

	if bundle.DocumentPromotion != nil {
		promotion := bundle.DocumentPromotion
		if promotion.DocumentID != snapshot.DocumentID || promotion.VersionID != snapshot.CandidateVersionID ||
			(promotion.QualityState != QualityAcceptedDraft && promotion.QualityState != QualityVerifiedDeliverable) || bundle.QualityReport == nil {
			return SnapshotRecord{}, fmt.Errorf("%w: invalid document promotion binding", ErrInvalidRecord)
		}
		if promotion.AcceptedAt.IsZero() {
			return SnapshotRecord{}, fmt.Errorf("%w: accepted document promotion requires accepted_at", ErrInvalidRecord)
		}
		if promotion.QualityState == QualityVerifiedDeliverable && promotion.VerifiedAt.IsZero() {
			return SnapshotRecord{}, fmt.Errorf("%w: verified document promotion requires verified_at", ErrInvalidRecord)
		}
		result, err := tx.tx.ExecContext(ctx, `
			UPDATE writing_document_versions
			SET quality_state=$1, quality_report_id=$2, quality_report_version=$3,
			    snapshot_manifest_id=$4, snapshot_version=$5,
			    accepted_at=$6, verified_at=$7
			WHERE document_id=$8 AND version_id=$9
		`, promotion.QualityState, snapshot.QualityReportID, snapshot.QualityReportVersion,
			snapshot.SnapshotID, snapshot.SnapshotVersion, nullTime(promotion.AcceptedAt),
			nullTime(promotion.VerifiedAt), promotion.DocumentID, promotion.VersionID)
		if err != nil {
			return SnapshotRecord{}, fmt.Errorf("promote document version: %w", err)
		}
		updated, err := result.RowsAffected()
		if err != nil || updated != 1 {
			return SnapshotRecord{}, fmt.Errorf("%w: document promotion target missing", ErrConflict)
		}
	}

	for _, artifact := range bundle.Artifacts {
		result, err := tx.tx.ExecContext(ctx, `
			UPDATE writing_artifacts
			SET status='committed', snapshot_manifest_id=$1, snapshot_version=$2,
			    quality_report_id=$3, quality_report_version=$4, committed_at=$5
			WHERE run_id=$6 AND artifact_id=$7 AND version=$8
			  AND content_hash=$9 AND status <> 'superseded'
		`, snapshot.SnapshotID, snapshot.SnapshotVersion,
			nullString(snapshot.QualityReportID), nullVersion(snapshot.QualityReportVersion),
			snapshot.PersistedAt, snapshot.RunID, artifact.ArtifactID, artifact.Version, artifact.ContentHash)
		if err != nil {
			return SnapshotRecord{}, fmt.Errorf("commit artifact %s: %w", artifact.ArtifactID, err)
		}
		updated, err := result.RowsAffected()
		if err != nil || updated != 1 {
			return SnapshotRecord{}, fmt.Errorf("%w: artifact %s changed before checkpoint", ErrConflict, artifact.ArtifactID)
		}
	}

	result, err := tx.tx.ExecContext(ctx, `
		UPDATE writing_runs
		SET last_snapshot_id=$1, last_snapshot_version=$2,
		    achieved_assurance=COALESCE($3, achieved_assurance), updated_at=NOW()
		WHERE run_id=$4
	`, snapshot.SnapshotID, snapshot.SnapshotVersion,
		nullString(string(bundle.AchievedAssurance)), snapshot.RunID)
	if err != nil {
		return SnapshotRecord{}, fmt.Errorf("advance run snapshot: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return SnapshotRecord{}, ErrNotFound
	}
	return snapshot, nil
}

func (tx *Tx) verifyCommittedCheckpoint(ctx context.Context, bundle CheckpointBundle) error {
	snapshot := bundle.Snapshot
	if bundle.QualityReport != nil {
		var reportHash string
		err := tx.tx.QueryRowContext(ctx, `
			SELECT content_hash FROM writing_quality_reports
			WHERE run_id=$1 AND report_id=$2 AND report_version=$3
		`, snapshot.RunID, bundle.QualityReport.ReportID, bundle.QualityReport.ReportVersion).Scan(&reportHash)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: snapshot exists without its quality report", ErrImmutableConflict)
		}
		if err != nil {
			return fmt.Errorf("verify checkpoint quality report: %w", err)
		}
		if reportHash != bundle.QualityReport.ContentHash {
			return fmt.Errorf("%w: checkpoint quality report has different content", ErrImmutableConflict)
		}
	}
	if bundle.DocumentPromotion != nil {
		var state string
		var snapshotID sql.NullString
		var snapshotVersion sql.NullInt64
		err := tx.tx.QueryRowContext(ctx, `
			SELECT quality_state, snapshot_manifest_id, snapshot_version
			FROM writing_document_versions WHERE document_id=$1 AND version_id=$2
		`, bundle.DocumentPromotion.DocumentID, bundle.DocumentPromotion.VersionID).Scan(&state, &snapshotID, &snapshotVersion)
		if err != nil {
			return fmt.Errorf("verify checkpoint document promotion: %w", err)
		}
		if state != bundle.DocumentPromotion.QualityState || snapshotID.String != snapshot.SnapshotID || snapshotVersion.Int64 != int64(snapshot.SnapshotVersion) {
			return fmt.Errorf("%w: snapshot exists without matching document promotion", ErrImmutableConflict)
		}
	}
	for _, artifact := range bundle.Artifacts {
		var status string
		var snapshotID sql.NullString
		var snapshotVersion sql.NullInt64
		err := tx.tx.QueryRowContext(ctx, `
			SELECT status, snapshot_manifest_id, snapshot_version
			FROM writing_artifacts
			WHERE run_id=$1 AND artifact_id=$2 AND version=$3 AND content_hash=$4
		`, snapshot.RunID, artifact.ArtifactID, artifact.Version, artifact.ContentHash).Scan(&status, &snapshotID, &snapshotVersion)
		if err != nil {
			return fmt.Errorf("verify checkpoint artifact %s: %w", artifact.ArtifactID, err)
		}
		if status != "committed" || snapshotID.String != snapshot.SnapshotID || snapshotVersion.Int64 != int64(snapshot.SnapshotVersion) {
			return fmt.Errorf("%w: snapshot exists without matching artifact commit", ErrImmutableConflict)
		}
	}
	return nil
}

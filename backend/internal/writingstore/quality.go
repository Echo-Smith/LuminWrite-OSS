package writingstore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
)

const (
	QualityAcceptedDraft       = "accepted_draft"
	QualityVerifiedDeliverable = "verified_deliverable"
)

type QualityReportRecord struct {
	ReportID                    string
	ReportVersion               int
	RunID                       string
	PlanID                      string
	PlanVersion                 int
	DocumentID                  string
	CandidateVersionID          string
	ValidatedVersionID          string
	CommittedVersionID          string
	ContentHash                 string
	RequestedAssurance          writingkernel.AssuranceLevel
	AchievedAssurance           writingkernel.AssuranceLevel
	AssuranceSatisfied          bool
	QualityState                string
	VersionConsistent           bool
	RequiredValidatorsSatisfied bool
	BlockerCount                int
	ErrorCount                  int
	OpenErrorCount              int
	WaivedErrorCount            int
	WarningCount                int
	Payload                     map[string]any
	SnapshotID                  string
	SnapshotVersion             int
	SnapshotPersisted           bool
	Trace                       TraceContext
	CreatedAt                   time.Time
}

func (tx *Tx) PutQualityReport(ctx context.Context, report QualityReportRecord) error {
	if err := validateQualityReport(report); err != nil {
		return err
	}
	provenance, sources, err := report.Trace.values()
	if err != nil {
		return err
	}
	payload, err := marshalJSON(report.Payload, "quality report")
	if err != nil {
		return err
	}
	createdAt := report.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	result, err := tx.tx.ExecContext(ctx, `
		INSERT INTO writing_quality_reports (
			report_id, report_version, run_id, plan_id, plan_version,
			document_id, candidate_version_id, validated_version_id,
			committed_version_id, content_hash, requested_assurance,
			achieved_assurance, assurance_satisfied, quality_state,
			version_consistent, required_validators_satisfied, blocker_count,
			error_count, open_error_count, waived_error_count, warning_count,
			report_payload, snapshot_manifest_id, snapshot_version,
			snapshot_persisted, provenance, source_refs, created_by_type,
			created_by_id, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,
		          $16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30)
		ON CONFLICT (report_id, report_version) DO NOTHING
	`, report.ReportID, report.ReportVersion, report.RunID, report.PlanID,
		report.PlanVersion, report.DocumentID, report.CandidateVersionID,
		nullString(report.ValidatedVersionID), nullString(report.CommittedVersionID),
		report.ContentHash, string(report.RequestedAssurance), string(report.AchievedAssurance),
		report.AssuranceSatisfied, report.QualityState, report.VersionConsistent,
		report.RequiredValidatorsSatisfied, report.BlockerCount, report.ErrorCount,
		report.OpenErrorCount, report.WaivedErrorCount, report.WarningCount, payload,
		nullString(report.SnapshotID), nullVersion(report.SnapshotVersion),
		report.SnapshotPersisted, provenance, sources, string(report.Trace.Actor.Type),
		nullString(report.Trace.Actor.ID), createdAt)
	if err != nil {
		return fmt.Errorf("insert quality report: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect quality report insert: %w", err)
	}
	if inserted == 1 {
		return nil
	}
	var existingRunID, existingHash string
	var immutablePayloadMatches bool
	if err := tx.tx.QueryRowContext(ctx, `
		SELECT run_id, content_hash,
		       plan_id=$3 AND plan_version=$4 AND document_id=$5
		         AND candidate_version_id=$6
		         AND COALESCE(validated_version_id, '')=$7
		         AND COALESCE(committed_version_id, '')=$8
		         AND requested_assurance=$9 AND achieved_assurance=$10
		         AND assurance_satisfied=$11 AND quality_state=$12
		         AND version_consistent=$13 AND required_validators_satisfied=$14
		         AND blocker_count=$15 AND error_count=$16 AND open_error_count=$17
		         AND waived_error_count=$18 AND warning_count=$19
		         AND report_payload=$20::jsonb
		         AND COALESCE(snapshot_manifest_id, '')=$21
		         AND COALESCE(snapshot_version, 0)=$22
		         AND provenance=$23::jsonb AND source_refs=$24::jsonb
		         AND created_by_type=$25 AND COALESCE(created_by_id, '')=$26
		FROM writing_quality_reports
		WHERE report_id=$1 AND report_version=$2
	`, report.ReportID, report.ReportVersion, report.PlanID, report.PlanVersion,
		report.DocumentID, report.CandidateVersionID, report.ValidatedVersionID,
		report.CommittedVersionID, string(report.RequestedAssurance), string(report.AchievedAssurance),
		report.AssuranceSatisfied, report.QualityState, report.VersionConsistent,
		report.RequiredValidatorsSatisfied, report.BlockerCount, report.ErrorCount,
		report.OpenErrorCount, report.WaivedErrorCount, report.WarningCount,
		string(payload), report.SnapshotID, report.SnapshotVersion,
		string(provenance), string(sources), string(report.Trace.Actor.Type), report.Trace.Actor.ID).Scan(
		&existingRunID, &existingHash, &immutablePayloadMatches); err != nil {
		return fmt.Errorf("load conflicting quality report: %w", err)
	}
	if existingRunID == report.RunID && existingHash == report.ContentHash && immutablePayloadMatches {
		return nil
	}
	return fmt.Errorf("%w: quality report %s version %d has different content", ErrImmutableConflict, report.ReportID, report.ReportVersion)
}

func validateQualityReport(report QualityReportRecord) error {
	if err := validateID(report.ReportID, "qr_", "report_id"); err != nil {
		return err
	}
	if report.ReportVersion < 1 || report.PlanVersion < 1 {
		return fmt.Errorf("%w: quality report and plan versions must be positive", ErrInvalidRecord)
	}
	if err := validateHash(report.ContentHash, "quality content_hash"); err != nil {
		return err
	}
	if !report.RequestedAssurance.Valid() || !report.AchievedAssurance.Valid() {
		return fmt.Errorf("%w: invalid quality assurance level", ErrInvalidRecord)
	}
	if report.Payload == nil {
		return fmt.Errorf("%w: quality report payload is required", ErrInvalidRecord)
	}
	if report.BlockerCount < 0 || report.ErrorCount < 0 || report.OpenErrorCount < 0 || report.WaivedErrorCount < 0 || report.WarningCount < 0 || report.OpenErrorCount+report.WaivedErrorCount > report.ErrorCount {
		return fmt.Errorf("%w: quality finding counts cannot be negative", ErrInvalidRecord)
	}
	switch report.QualityState {
	case QualityCandidateDraft:
	case QualityAcceptedDraft:
		if report.BlockerCount > 0 || report.OpenErrorCount > 0 || !report.VersionConsistent ||
			!report.AssuranceSatisfied || assuranceRank(report.AchievedAssurance) < assuranceRank(report.RequestedAssurance) {
			return fmt.Errorf("%w: BLOCKER or open ERROR prevents accepted draft", ErrInvalidRecord)
		}
	case QualityVerifiedDeliverable:
		if report.BlockerCount > 0 || report.OpenErrorCount > 0 || report.WaivedErrorCount > 0 ||
			!report.VersionConsistent || !report.RequiredValidatorsSatisfied || !report.AssuranceSatisfied ||
			assuranceRank(report.AchievedAssurance) < assuranceRank(report.RequestedAssurance) ||
			report.ValidatedVersionID == "" || report.ValidatedVersionID != report.CommittedVersionID ||
			report.CandidateVersionID != report.CommittedVersionID {
			return fmt.Errorf("%w: verified deliverable does not satisfy quality gates", ErrInvalidRecord)
		}
	default:
		return fmt.Errorf("%w: invalid quality state %q", ErrInvalidRecord, report.QualityState)
	}
	if report.QualityState != QualityCandidateDraft && (report.SnapshotID == "" || report.SnapshotVersion < 1 || !report.SnapshotPersisted) {
		return fmt.Errorf("%w: accepted or verified quality requires snapshot", ErrInvalidRecord)
	}
	if strings.TrimSpace(report.CandidateVersionID) == "" {
		return fmt.Errorf("%w: candidate_version_id is required", ErrInvalidRecord)
	}
	return report.Trace.validate()
}

func assuranceRank(level writingkernel.AssuranceLevel) int {
	switch level {
	case writingkernel.AssuranceLevelFlexible:
		return 1
	case writingkernel.AssuranceLevelStandard:
		return 2
	case writingkernel.AssuranceLevelSourced:
		return 3
	case writingkernel.AssuranceLevelStrict:
		return 4
	default:
		return 0
	}
}

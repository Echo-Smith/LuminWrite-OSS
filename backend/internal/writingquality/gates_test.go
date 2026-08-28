package writingquality

import (
	"errors"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
)

func TestCandidateDraftMayContainOpenBlocker(t *testing.T) {
	report := testReport(writingkernel.QualityStateCandidateDraft)
	addContractValidator(&report)
	report.Findings = []writingkernel.QualityFinding{testFinding(writingkernel.FindingSeverityBlocker, writingkernel.FindingStatusOpen)}
	decision := EvaluateGate(report, report.QualityState)
	if !decision.Allowed || decision.BlockerCount != 1 {
		t.Fatalf("candidate decision = %#v", decision)
	}
}

func TestBlockerCannotBeWaivedAndPreventsPromotion(t *testing.T) {
	for _, state := range []writingkernel.QualityState{writingkernel.QualityStateCandidateDraft, writingkernel.QualityStateAcceptedDraft, writingkernel.QualityStateVerifiedDeliverable} {
		report := testReport(state)
		addContractValidator(&report)
		finding := testFinding(writingkernel.FindingSeverityBlocker, writingkernel.FindingStatusWaived)
		decisionID := "decision_bad"
		finding.WaiverDecisionID = &decisionID
		report.Findings = []writingkernel.QualityFinding{finding}
		report.DecisionRecords = []writingkernel.DecisionRecord{testDecision(decisionID, writingkernel.DecisionTypeErrorWaiver)}
		if EvaluateGate(report, state).Allowed {
			t.Fatalf("BLOCKER waiver was allowed for %s", state)
		}
	}
}

func TestErrorWaiverAllowsAcceptedButNeverVerified(t *testing.T) {
	decisionID := "decision_error"
	accepted := testReport(writingkernel.QualityStateAcceptedDraft)
	addContractValidator(&accepted)
	finding := testFinding(writingkernel.FindingSeverityError, writingkernel.FindingStatusWaived)
	finding.WaiverDecisionID = &decisionID
	accepted.Findings = []writingkernel.QualityFinding{finding}
	accepted.DecisionRecords = append(accepted.DecisionRecords, testDecision(decisionID, writingkernel.DecisionTypeErrorWaiver))
	if decision := EvaluateGate(accepted, accepted.QualityState); !decision.Allowed {
		t.Fatalf("accepted error waiver denied: %#v", decision)
	}

	verified := accepted
	verified.QualityState = writingkernel.QualityStateVerifiedDeliverable
	if EvaluateGate(verified, verified.QualityState).Allowed {
		t.Fatal("Verified Deliverable accepted an ERROR waiver")
	}
}

func TestAssuranceShortfallBlocksVerifiedButNotAccepted(t *testing.T) {
	accepted := testReport(writingkernel.QualityStateAcceptedDraft)
	accepted.RequestedAssurance = writingkernel.AssuranceLevelStrict
	accepted.AchievedAssurance = writingkernel.AssuranceLevelSourced
	accepted.AssuranceSatisfied = false
	accepted.Validators = append(accepted.Validators, writingkernel.ValidatorResult{ValidatorID: ValidatorEvidence, Version: "1.0.0", Required: true, Status: writingkernel.ValidatorStatusUnavailable, Equivalence: writingkernel.ValidatorEquivalenceNone})
	accepted.Degradations = append(accepted.Degradations, writingkernel.QualityDegradation{ValidatorID: ValidatorEvidence, Equivalence: "non_equivalent", FromAssurance: writingkernel.AssuranceLevelStrict, ToAssurance: writingkernel.AssuranceLevelSourced, ReasonCode: "provider_unavailable"})
	if !EvaluateGate(accepted, accepted.QualityState).Allowed {
		t.Fatal("assurance shortfall should remain visible without blocking Accepted Draft")
	}
	verified := accepted
	verified.QualityState = writingkernel.QualityStateVerifiedDeliverable
	if EvaluateGate(verified, verified.QualityState).Allowed {
		t.Fatal("assurance shortfall allowed Verified Deliverable")
	}
}

func TestAssuranceShortfallRequiresRecordedNonEquivalentDegradation(t *testing.T) {
	report := testReport(writingkernel.QualityStateAcceptedDraft)
	report.RequestedAssurance = writingkernel.AssuranceLevelStrict
	report.AchievedAssurance = writingkernel.AssuranceLevelSourced
	report.AssuranceSatisfied = false
	if EvaluateGate(report, report.QualityState).Allowed {
		t.Fatal("unexplained assurance shortfall was allowed")
	}
}

func TestValidatedVersionMustMatchCommittedVersion(t *testing.T) {
	report := testReport(writingkernel.QualityStateVerifiedDeliverable)
	other := "ver_other"
	report.DocumentVersions.ValidatedVersionID = &other
	report.DocumentVersions.VersionConsistent = false
	if EvaluateGate(report, report.QualityState).Allowed {
		t.Fatal("version mismatch allowed verification")
	}
}

func TestAcceptedRequiresRecordedAcceptanceAndSnapshot(t *testing.T) {
	report := testReport(writingkernel.QualityStateAcceptedDraft)
	report.DecisionRecords = nil
	if EvaluateGate(report, report.QualityState).Allowed {
		t.Fatal("accepted draft without an acceptance decision was allowed")
	}
	report = testReport(writingkernel.QualityStateAcceptedDraft)
	report.SnapshotPersisted = false
	if EvaluateGate(report, report.QualityState).Allowed {
		t.Fatal("accepted draft without a complete snapshot was allowed")
	}
}

func TestFinalizePersistsExactGateTrace(t *testing.T) {
	report := testReport(writingkernel.QualityStateVerifiedDeliverable)
	report.QualityState = ""
	report.AssuranceSatisfied = false
	report.DocumentVersions.VersionConsistent = false
	final, err := FinalizeReport(report, writingkernel.QualityStateVerifiedDeliverable)
	if err != nil {
		t.Fatal(err)
	}
	if len(final.GateChecks) == 0 || !final.AssuranceSatisfied || !final.DocumentVersions.VersionConsistent {
		t.Fatalf("finalized report = %#v", final)
	}
	if err := ValidateReport(final); err != nil {
		t.Fatalf("finalized report is invalid: %v", err)
	}
}

func TestValidateReportReturnsStableSentinel(t *testing.T) {
	report := testReport(writingkernel.QualityStateVerifiedDeliverable)
	report.Validators[0].Status = writingkernel.ValidatorStatusUnavailable
	if err := ValidateReport(report); !errors.Is(err, ErrInvalidReport) {
		t.Fatalf("error = %v", err)
	}
}

func TestUserAndAuditViewsAreReadOnlyProjections(t *testing.T) {
	report := testReport(writingkernel.QualityStateAcceptedDraft)
	addContractValidator(&report)
	report.Findings = []writingkernel.QualityFinding{testFinding(writingkernel.FindingSeverityWarning, writingkernel.FindingStatusOpen)}
	report.GateChecks = []writingkernel.QualityGateCheck{{Code: "PERSISTED_DECISION", Passed: true, Message: "stored once"}}
	summary := ProjectUserSummary(report, 1)
	audit := ProjectAuditReport(report)

	report.QualityState = writingkernel.QualityStateCandidateDraft
	report.Findings[0].Code = "MUTATED"
	report.GateChecks[0].Code = "MUTATED"
	if summary.QualityState != writingkernel.QualityStateAcceptedDraft || summary.KeyFindings[0].Code != "CONTRACT_TEST" {
		t.Fatalf("summary was recomputed or aliased: %#v", summary)
	}
	if audit.Report.QualityState != writingkernel.QualityStateAcceptedDraft || audit.Report.Findings[0].Code != "CONTRACT_TEST" || audit.Report.GateChecks[0].Code != "PERSISTED_DECISION" {
		t.Fatalf("audit projection was recomputed or aliased: %#v", audit)
	}
}

func testReport(state writingkernel.QualityState) writingkernel.QualityReport {
	version := "ver_candidate"
	snapshot := "snap_quality"
	report := writingkernel.QualityReport{
		SchemaVersion: writingkernel.SchemaVersionV1, ReportID: "qr_test", RunID: "run_test",
		DocumentVersions:   writingkernel.QualityDocumentVersions{CandidateVersionID: version},
		RequestedAssurance: writingkernel.AssuranceLevelStandard,
		AchievedAssurance:  writingkernel.AssuranceLevelStandard, AssuranceSatisfied: true,
		QualityState: state,
		Validators:   []writingkernel.ValidatorResult{{ValidatorID: "core.ast.integrity", Version: "1.0.0", Required: true, Status: writingkernel.ValidatorStatusPassed, Equivalence: writingkernel.ValidatorEquivalencePrimary}},
		Degradations: []writingkernel.QualityDegradation{}, Findings: []writingkernel.QualityFinding{},
		DecisionRecords: []writingkernel.DecisionRecord{}, SnapshotManifestID: &snapshot,
		SnapshotPersisted: true, CreatedAt: time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC),
	}
	if state == writingkernel.QualityStateCandidateDraft {
		report.DocumentVersions.VersionConsistent = false
		report.SnapshotManifestID = nil
		report.SnapshotPersisted = false
		return report
	}
	report.DocumentVersions.ValidatedVersionID = &version
	report.DocumentVersions.CommittedVersionID = &version
	report.DocumentVersions.VersionConsistent = true
	if state == writingkernel.QualityStateAcceptedDraft {
		report.DecisionRecords = append(report.DecisionRecords, testDecision("decision_accept", writingkernel.DecisionTypeAccept))
	}
	return report
}

func testFinding(severity writingkernel.FindingSeverity, status writingkernel.FindingStatus) writingkernel.QualityFinding {
	return writingkernel.QualityFinding{
		FindingID: "finding_test", Severity: severity, Category: writingkernel.FindingCategoryContract,
		Code: "CONTRACT_TEST", Message: "contract validation finding", ValidatorID: "core.contract.consistency",
		ValidatorStatus: writingkernel.ValidatorStatusFailed, RuleVersion: "1.0.0", Explanation: "test explanation",
		FixScope: "document", Status: status,
	}
}

func testDecision(id string, kind writingkernel.DecisionType) writingkernel.DecisionRecord {
	return writingkernel.DecisionRecord{DecisionID: id, DecisionType: kind, ReasonCode: "editorial_choice", Summary: "recorded choice", DecidedBy: writingkernel.DecisionActorUser, DecidedAt: time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)}
}

func addContractValidator(report *writingkernel.QualityReport) {
	report.Validators = append(report.Validators, writingkernel.ValidatorResult{ValidatorID: "core.contract.consistency", Version: "1.0.0", Required: false, Status: writingkernel.ValidatorStatusFailed, Equivalence: writingkernel.ValidatorEquivalencePrimary})
}

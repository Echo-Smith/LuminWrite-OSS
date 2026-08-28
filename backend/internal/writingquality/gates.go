package writingquality

import (
	"fmt"
	"strings"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
)

func EvaluateGate(report writingkernel.QualityReport, target writingkernel.QualityState) GateDecision {
	decision := GateDecision{Target: target, Allowed: true, RequiredValidatorsMet: true, Checks: []writingkernel.QualityGateCheck{}}
	add := func(code string, passed bool, message string) {
		decision.Checks = append(decision.Checks, writingkernel.QualityGateCheck{Code: code, Passed: passed, Message: message})
		if !passed {
			decision.Allowed = false
		}
	}

	reportValid := validateReportShape(report, &decision) == nil
	add("REPORT_VALID", reportValid, "quality report structure and references are valid")
	if !target.Valid() || report.QualityState != target {
		add("TARGET_STATE_MATCH", false, "report quality_state must match the requested gate")
	}
	if !reportValid || !target.Valid() {
		return decision
	}

	if target == writingkernel.QualityStateCandidateDraft {
		return decision
	}

	add("NO_BLOCKER", decision.BlockerCount == 0, "all BLOCKER findings must be resolved")
	add("NO_OPEN_ERROR", decision.OpenErrorCount == 0, "all ERROR findings must be resolved or explicitly waived")
	add("VERSION_CONSISTENT", report.DocumentVersions.VersionConsistent, "candidate, validation, and commit references must describe one version")
	add("SNAPSHOT_PERSISTED", report.SnapshotPersisted && report.SnapshotManifestID != nil && strings.TrimSpace(*report.SnapshotManifestID) != "", "a complete quality snapshot must be persisted")

	if target == writingkernel.QualityStateAcceptedDraft {
		add("ACCEPTANCE_RECORDED", hasAcceptanceDecision(report.DecisionRecords), "Accepted Draft requires a user or policy acceptance decision")
		return decision
	}

	add("NO_ERROR_WAIVER", decision.WaivedErrorCount == 0, "Verified Deliverable cannot contain waived ERROR findings")
	add("REQUIRED_VALIDATORS_PASSED", len(report.Validators) > 0 && decision.RequiredValidatorsMet, "every required validator must pass")
	add("ASSURANCE_SATISFIED", report.AssuranceSatisfied, "achieved assurance must meet the contract request")
	versions := report.DocumentVersions
	verifiedVersionMatch := versions.ValidatedVersionID != nil && versions.CommittedVersionID != nil &&
		*versions.ValidatedVersionID == versions.CandidateVersionID && *versions.CommittedVersionID == versions.CandidateVersionID
	add("VERIFIED_VERSION_MATCH", verifiedVersionMatch, "the validated, committed, and candidate version IDs must be identical")
	return decision
}

// FinalizeReport computes only canonical derived fields and persists the exact
// gate trace later used by both projections.
func FinalizeReport(report writingkernel.QualityReport, target writingkernel.QualityState) (writingkernel.QualityReport, error) {
	report.QualityState = target
	report.AssuranceSatisfied = assuranceRank(report.AchievedAssurance) >= assuranceRank(report.RequestedAssurance)
	report.DocumentVersions.VersionConsistent = versionsConsistent(report.DocumentVersions)
	decision := EvaluateGate(report, target)
	report.GateChecks = cloneGateChecks(decision.Checks)
	if !decision.Allowed {
		return report, fmt.Errorf("%w: %s", ErrGateDenied, failedGateCodes(decision.Checks))
	}
	return report, nil
}

func ValidateReport(report writingkernel.QualityReport) error {
	decision := EvaluateGate(report, report.QualityState)
	if !decision.Allowed {
		return fmt.Errorf("%w: %s", ErrInvalidReport, failedGateCodes(decision.Checks))
	}
	return nil
}

func validateReportShape(report writingkernel.QualityReport, decision *GateDecision) error {
	if report.SchemaVersion != writingkernel.SchemaVersionV1 || !canonicalID(report.ReportID, "qr_") || !canonicalID(report.RunID, "run_") || report.CreatedAt.IsZero() {
		return ErrInvalidReport
	}
	if !report.RequestedAssurance.Valid() || !report.AchievedAssurance.Valid() || !report.QualityState.Valid() {
		return ErrInvalidReport
	}
	if report.Validators == nil || report.Degradations == nil || report.Findings == nil || report.DecisionRecords == nil {
		return ErrInvalidReport
	}
	if !canonicalID(report.DocumentVersions.CandidateVersionID, "ver_") ||
		(report.DocumentVersions.ValidatedVersionID != nil && !canonicalID(*report.DocumentVersions.ValidatedVersionID, "ver_")) ||
		(report.DocumentVersions.CommittedVersionID != nil && !canonicalID(*report.DocumentVersions.CommittedVersionID, "ver_")) {
		return ErrInvalidReport
	}
	if report.AssuranceSatisfied != (assuranceRank(report.AchievedAssurance) >= assuranceRank(report.RequestedAssurance)) ||
		report.DocumentVersions.VersionConsistent != versionsConsistent(report.DocumentVersions) {
		return ErrInvalidReport
	}

	decisions := make(map[string]writingkernel.DecisionRecord, len(report.DecisionRecords))
	for _, record := range report.DecisionRecords {
		if !canonicalID(record.DecisionID, "decision_") || !record.DecisionType.Valid() || !record.DecidedBy.Valid() ||
			strings.TrimSpace(record.ReasonCode) == "" || strings.TrimSpace(record.Summary) == "" || record.DecidedAt.IsZero() {
			return ErrInvalidReport
		}
		if _, exists := decisions[record.DecisionID]; exists {
			return ErrInvalidReport
		}
		decisions[record.DecisionID] = record
	}

	validatorIDs := make(map[string]struct{}, len(report.Validators))
	for _, result := range report.Validators {
		if strings.TrimSpace(result.ValidatorID) == "" || strings.TrimSpace(result.Version) == "" || !result.Status.Valid() || !result.Equivalence.Valid() {
			return ErrInvalidReport
		}
		if _, exists := validatorIDs[result.ValidatorID]; exists {
			return ErrInvalidReport
		}
		validatorIDs[result.ValidatorID] = struct{}{}
		if result.Required && result.Status != writingkernel.ValidatorStatusPassed {
			decision.RequiredValidatorsMet = false
		}
	}

	findingIDs := make(map[string]struct{}, len(report.Findings))
	for _, finding := range report.Findings {
		if strings.TrimSpace(finding.FindingID) == "" || strings.TrimSpace(finding.Code) == "" || strings.TrimSpace(finding.Message) == "" ||
			strings.TrimSpace(finding.ValidatorID) == "" || strings.TrimSpace(finding.RuleVersion) == "" || strings.TrimSpace(finding.Explanation) == "" ||
			strings.TrimSpace(finding.FixScope) == "" || !finding.Severity.Valid() || !finding.Category.Valid() || !finding.Status.Valid() || !finding.ValidatorStatus.Valid() {
			return ErrInvalidReport
		}
		if _, exists := findingIDs[finding.FindingID]; exists {
			return ErrInvalidReport
		}
		if _, exists := validatorIDs[finding.ValidatorID]; !exists {
			return ErrInvalidReport
		}
		if finding.Location != nil && strings.TrimSpace(finding.Location.BlockID) == "" && strings.TrimSpace(finding.Location.ClaimID) == "" && strings.TrimSpace(finding.Location.SourceRef) == "" {
			return ErrInvalidReport
		}
		findingIDs[finding.FindingID] = struct{}{}
		if finding.Severity == writingkernel.FindingSeverityBlocker {
			if finding.Status == writingkernel.FindingStatusWaived || finding.WaiverDecisionID != nil {
				return ErrInvalidReport
			}
			if finding.Status == writingkernel.FindingStatusOpen {
				decision.BlockerCount++
			}
		}
		if finding.Severity == writingkernel.FindingSeverityError {
			switch finding.Status {
			case writingkernel.FindingStatusOpen:
				decision.OpenErrorCount++
			case writingkernel.FindingStatusWaived:
				decision.WaivedErrorCount++
			}
		}
		if finding.Status == writingkernel.FindingStatusWaived {
			if finding.WaiverDecisionID == nil {
				return ErrInvalidReport
			}
			record, ok := decisions[*finding.WaiverDecisionID]
			if !ok || (finding.Severity == writingkernel.FindingSeverityError && record.DecisionType != writingkernel.DecisionTypeErrorWaiver) {
				return ErrInvalidReport
			}
		} else if finding.WaiverDecisionID != nil {
			return ErrInvalidReport
		}
	}

	hasNonEquivalentDegradation := false
	for _, degradation := range report.Degradations {
		if strings.TrimSpace(degradation.ValidatorID) == "" || strings.TrimSpace(degradation.ReasonCode) == "" ||
			!degradation.FromAssurance.Valid() || !degradation.ToAssurance.Valid() {
			return ErrInvalidReport
		}
		if _, exists := validatorIDs[degradation.ValidatorID]; !exists {
			return ErrInvalidReport
		}
		switch degradation.Equivalence {
		case "equivalent":
			if degradation.FromAssurance != degradation.ToAssurance {
				return ErrInvalidReport
			}
		case "non_equivalent":
			hasNonEquivalentDegradation = true
			if assuranceRank(degradation.ToAssurance) >= assuranceRank(degradation.FromAssurance) || assuranceRank(report.AchievedAssurance) > assuranceRank(degradation.ToAssurance) {
				return ErrInvalidReport
			}
		default:
			return ErrInvalidReport
		}
	}
	if assuranceRank(report.AchievedAssurance) < assuranceRank(report.RequestedAssurance) && !hasNonEquivalentDegradation {
		return ErrInvalidReport
	}
	return nil
}

func canonicalID(value, prefix string) bool {
	if !strings.HasPrefix(value, prefix) || len(value) == len(prefix) {
		return false
	}
	for _, r := range value[len(prefix):] {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func versionsConsistent(versions writingkernel.QualityDocumentVersions) bool {
	if versions.CommittedVersionID == nil || strings.TrimSpace(*versions.CommittedVersionID) == "" || *versions.CommittedVersionID != versions.CandidateVersionID {
		return false
	}
	return versions.ValidatedVersionID == nil || *versions.ValidatedVersionID == *versions.CommittedVersionID
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

func lowerAssurance(level writingkernel.AssuranceLevel) writingkernel.AssuranceLevel {
	switch level {
	case writingkernel.AssuranceLevelStrict:
		return writingkernel.AssuranceLevelSourced
	case writingkernel.AssuranceLevelSourced:
		return writingkernel.AssuranceLevelStandard
	case writingkernel.AssuranceLevelStandard:
		return writingkernel.AssuranceLevelFlexible
	default:
		return writingkernel.AssuranceLevelFlexible
	}
}

func hasAcceptanceDecision(records []writingkernel.DecisionRecord) bool {
	for _, record := range records {
		if record.DecisionType == writingkernel.DecisionTypeAccept && (record.DecidedBy == writingkernel.DecisionActorUser || record.DecidedBy == writingkernel.DecisionActorPolicy) {
			return true
		}
	}
	return false
}

func failedGateCodes(checks []writingkernel.QualityGateCheck) string {
	failed := make([]string, 0)
	for _, check := range checks {
		if !check.Passed {
			failed = append(failed, check.Code)
		}
	}
	return strings.Join(failed, ",")
}

func cloneGateChecks(checks []writingkernel.QualityGateCheck) []writingkernel.QualityGateCheck {
	return append([]writingkernel.QualityGateCheck(nil), checks...)
}

// ProjectUserSummary is intentionally a projection: it copies the persisted
// state and assurance conclusion and never calls EvaluateGate.
func ProjectUserSummary(report writingkernel.QualityReport, findingLimit int) UserQualitySummary {
	if findingLimit < 0 || findingLimit > len(report.Findings) {
		findingLimit = len(report.Findings)
	}
	return UserQualitySummary{
		QualityState: report.QualityState, RequestedAssurance: report.RequestedAssurance,
		AchievedAssurance: report.AchievedAssurance, AssuranceSatisfied: report.AssuranceSatisfied,
		KeyFindings: cloneFindings(report.Findings[:findingLimit]),
	}
}

// ProjectAuditReport returns the full persisted quality evidence. It does not
// infer a new state, validator result, or assurance level.
func ProjectAuditReport(report writingkernel.QualityReport) AuditQualityReport {
	return AuditQualityReport{Report: cloneReport(report)}
}

func cloneReport(report writingkernel.QualityReport) writingkernel.QualityReport {
	copy := report
	if report.DocumentVersions.ValidatedVersionID != nil {
		value := *report.DocumentVersions.ValidatedVersionID
		copy.DocumentVersions.ValidatedVersionID = &value
	}
	if report.DocumentVersions.CommittedVersionID != nil {
		value := *report.DocumentVersions.CommittedVersionID
		copy.DocumentVersions.CommittedVersionID = &value
	}
	if report.SnapshotManifestID != nil {
		value := *report.SnapshotManifestID
		copy.SnapshotManifestID = &value
	}
	copy.Validators = append([]writingkernel.ValidatorResult(nil), report.Validators...)
	copy.Degradations = append([]writingkernel.QualityDegradation(nil), report.Degradations...)
	copy.Findings = cloneFindings(report.Findings)
	copy.DecisionRecords = append([]writingkernel.DecisionRecord(nil), report.DecisionRecords...)
	copy.GateChecks = cloneGateChecks(report.GateChecks)
	return copy
}

func cloneFindings(findings []writingkernel.QualityFinding) []writingkernel.QualityFinding {
	copy := append([]writingkernel.QualityFinding(nil), findings...)
	for i := range copy {
		if findings[i].WaiverDecisionID != nil {
			value := *findings[i].WaiverDecisionID
			copy[i].WaiverDecisionID = &value
		}
		if findings[i].Location != nil {
			location := *findings[i].Location
			copy[i].Location = &location
		}
	}
	return copy
}

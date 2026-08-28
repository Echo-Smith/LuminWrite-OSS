package writingkernel

import "time"

// QualityState is a delivery-governance state, not an artifact type.
type QualityState string

const (
	QualityStateCandidateDraft      QualityState = "candidate_draft"
	QualityStateAcceptedDraft       QualityState = "accepted_draft"
	QualityStateVerifiedDeliverable QualityState = "verified_deliverable"
)

func (v QualityState) Valid() bool {
	switch v {
	case QualityStateCandidateDraft, QualityStateAcceptedDraft, QualityStateVerifiedDeliverable:
		return true
	default:
		return false
	}
}

type FindingSeverity string

const (
	FindingSeverityBlocker FindingSeverity = "BLOCKER"
	FindingSeverityError   FindingSeverity = "ERROR"
	FindingSeverityWarning FindingSeverity = "WARNING"
)

func (v FindingSeverity) Valid() bool {
	switch v {
	case FindingSeverityBlocker, FindingSeverityError, FindingSeverityWarning:
		return true
	default:
		return false
	}
}

type FindingStatus string

const (
	FindingStatusOpen     FindingStatus = "open"
	FindingStatusResolved FindingStatus = "resolved"
	FindingStatusWaived   FindingStatus = "waived"
)

func (v FindingStatus) Valid() bool {
	switch v {
	case FindingStatusOpen, FindingStatusResolved, FindingStatusWaived:
		return true
	default:
		return false
	}
}

type FindingCategory string

const (
	FindingCategoryStructure            FindingCategory = "structure"
	FindingCategoryContract             FindingCategory = "contract"
	FindingCategoryEvidence             FindingCategory = "evidence"
	FindingCategorySemanticPreservation FindingCategory = "semantic_preservation"
	FindingCategoryVoice                FindingCategory = "voice"
	FindingCategoryReadability          FindingCategory = "readability"
	FindingCategoryDelivery             FindingCategory = "delivery"
	FindingCategorySnapshot             FindingCategory = "snapshot"
)

func (v FindingCategory) Valid() bool {
	switch v {
	case FindingCategoryStructure, FindingCategoryContract, FindingCategoryEvidence,
		FindingCategorySemanticPreservation, FindingCategoryVoice,
		FindingCategoryReadability, FindingCategoryDelivery, FindingCategorySnapshot:
		return true
	default:
		return false
	}
}

type ValidatorStatus string

const (
	ValidatorStatusPassed      ValidatorStatus = "passed"
	ValidatorStatusFailed      ValidatorStatus = "failed"
	ValidatorStatusUnavailable ValidatorStatus = "unavailable"
	ValidatorStatusSkipped     ValidatorStatus = "skipped"
)

func (v ValidatorStatus) Valid() bool {
	switch v {
	case ValidatorStatusPassed, ValidatorStatusFailed, ValidatorStatusUnavailable, ValidatorStatusSkipped:
		return true
	default:
		return false
	}
}

type ValidatorEquivalence string

const (
	ValidatorEquivalencePrimary               ValidatorEquivalence = "primary"
	ValidatorEquivalenceEquivalentFallback    ValidatorEquivalence = "equivalent_fallback"
	ValidatorEquivalenceNonEquivalentFallback ValidatorEquivalence = "non_equivalent_fallback"
	ValidatorEquivalenceNone                  ValidatorEquivalence = "none"
)

func (v ValidatorEquivalence) Valid() bool {
	switch v {
	case ValidatorEquivalencePrimary, ValidatorEquivalenceEquivalentFallback,
		ValidatorEquivalenceNonEquivalentFallback, ValidatorEquivalenceNone:
		return true
	default:
		return false
	}
}

type DecisionType string

const (
	DecisionTypeAccept      DecisionType = "accept"
	DecisionTypeErrorWaiver DecisionType = "error_waiver"
	DecisionTypeDegradation DecisionType = "degradation"
	DecisionTypeRepair      DecisionType = "repair"
	DecisionTypeReject      DecisionType = "reject"
)

func (v DecisionType) Valid() bool {
	switch v {
	case DecisionTypeAccept, DecisionTypeErrorWaiver, DecisionTypeDegradation,
		DecisionTypeRepair, DecisionTypeReject:
		return true
	default:
		return false
	}
}

type DecisionActor string

const (
	DecisionActorUser      DecisionActor = "user"
	DecisionActorPolicy    DecisionActor = "policy"
	DecisionActorValidator DecisionActor = "validator"
)

func (v DecisionActor) Valid() bool {
	switch v {
	case DecisionActorUser, DecisionActorPolicy, DecisionActorValidator:
		return true
	default:
		return false
	}
}

type QualityDocumentVersions struct {
	CandidateVersionID string  `json:"candidate_version_id"`
	ValidatedVersionID *string `json:"validated_version_id"`
	CommittedVersionID *string `json:"committed_version_id"`
	VersionConsistent  bool    `json:"version_consistent"`
}

type ValidatorResult struct {
	ValidatorID string               `json:"validator_id"`
	Version     string               `json:"version"`
	Required    bool                 `json:"required"`
	Status      ValidatorStatus      `json:"status"`
	Equivalence ValidatorEquivalence `json:"equivalence"`
}

type QualityDegradation struct {
	ValidatorID   string         `json:"validator_id"`
	Equivalence   string         `json:"equivalence"`
	FromAssurance AssuranceLevel `json:"from_assurance"`
	ToAssurance   AssuranceLevel `json:"to_assurance"`
	ReasonCode    string         `json:"reason_code"`
}

// FindingLocation supports document, claim, and source-level repair without
// forcing report-wide findings (for example snapshot failures) into a fake block.
type FindingLocation struct {
	BlockID   string `json:"block_id,omitempty"`
	ClaimID   string `json:"claim_id,omitempty"`
	SourceRef string `json:"source_ref,omitempty"`
}

type QualityFinding struct {
	FindingID        string           `json:"finding_id"`
	Severity         FindingSeverity  `json:"severity"`
	Category         FindingCategory  `json:"category"`
	Code             string           `json:"code"`
	Message          string           `json:"message"`
	ValidatorID      string           `json:"validator_id"`
	ValidatorStatus  ValidatorStatus  `json:"validator_status,omitempty"`
	RuleVersion      string           `json:"rule_version,omitempty"`
	Explanation      string           `json:"explanation,omitempty"`
	FixScope         string           `json:"fix_scope,omitempty"`
	Location         *FindingLocation `json:"location,omitempty"`
	Status           FindingStatus    `json:"status"`
	WaiverDecisionID *string          `json:"waiver_decision_id"`
}

type DecisionRecord struct {
	DecisionID   string        `json:"decision_id"`
	DecisionType DecisionType  `json:"decision_type"`
	ReasonCode   string        `json:"reason_code"`
	Summary      string        `json:"summary"`
	DecidedBy    DecisionActor `json:"decided_by"`
	DecidedAt    time.Time     `json:"decided_at"`
}

// QualityGateCheck is persisted with the report so user and audit projections
// show the same decision rather than independently recalculating it.
type QualityGateCheck struct {
	Code    string `json:"code"`
	Passed  bool   `json:"passed"`
	Message string `json:"message"`
}

type QualityReport struct {
	SchemaVersion      string                  `json:"schema_version"`
	ReportID           string                  `json:"report_id"`
	RunID              string                  `json:"run_id"`
	DocumentVersions   QualityDocumentVersions `json:"document_versions"`
	RequestedAssurance AssuranceLevel          `json:"requested_assurance"`
	AchievedAssurance  AssuranceLevel          `json:"achieved_assurance"`
	AssuranceSatisfied bool                    `json:"assurance_satisfied"`
	QualityState       QualityState            `json:"quality_state"`
	Validators         []ValidatorResult       `json:"validators"`
	Degradations       []QualityDegradation    `json:"degradations"`
	Findings           []QualityFinding        `json:"findings"`
	DecisionRecords    []DecisionRecord        `json:"decision_records"`
	GateChecks         []QualityGateCheck      `json:"gate_checks,omitempty"`
	SnapshotManifestID *string                 `json:"snapshot_manifest_id"`
	SnapshotPersisted  bool                    `json:"snapshot_persisted"`
	CreatedAt          time.Time               `json:"created_at"`
}

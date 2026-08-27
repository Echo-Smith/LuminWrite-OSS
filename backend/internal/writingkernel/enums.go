package writingkernel

// ContractStatus describes the immutable lifecycle of a WritingContract version.
type ContractStatus string

const (
	ContractStatusDraft      ContractStatus = "draft"
	ContractStatusConfirmed  ContractStatus = "confirmed"
	ContractStatusSuperseded ContractStatus = "superseded"
)

func (v ContractStatus) Valid() bool {
	switch v {
	case ContractStatusDraft, ContractStatusConfirmed, ContractStatusSuperseded:
		return true
	default:
		return false
	}
}

type TaskMode string

const (
	TaskModeAuto    TaskMode = "auto"
	TaskModeWriting TaskMode = "writing"
	TaskModeGuided  TaskMode = "guided"
	TaskModePolish  TaskMode = "polish"
)

func (v TaskMode) Valid() bool {
	switch v {
	case TaskModeAuto, TaskModeWriting, TaskModeGuided, TaskModePolish:
		return true
	default:
		return false
	}
}

type OrchestrationMode string

const (
	OrchestrationModeAuto           OrchestrationMode = "auto"
	OrchestrationModeFast           OrchestrationMode = "fast"
	OrchestrationModeOutlineFirst   OrchestrationMode = "outline_first"
	OrchestrationModeSourced        OrchestrationMode = "sourced"
	OrchestrationModeStrictResearch OrchestrationMode = "strict_research"
)

func (v OrchestrationMode) Valid() bool {
	switch v {
	case OrchestrationModeAuto, OrchestrationModeFast, OrchestrationModeOutlineFirst, OrchestrationModeSourced, OrchestrationModeStrictResearch:
		return true
	default:
		return false
	}
}

type AssuranceLevel string

const (
	AssuranceLevelFlexible AssuranceLevel = "flexible"
	AssuranceLevelStandard AssuranceLevel = "standard"
	AssuranceLevelSourced  AssuranceLevel = "sourced"
	AssuranceLevelStrict   AssuranceLevel = "strict"
)

func (v AssuranceLevel) Valid() bool {
	switch v {
	case AssuranceLevelFlexible, AssuranceLevelStandard, AssuranceLevelSourced, AssuranceLevelStrict:
		return true
	default:
		return false
	}
}

type ApprovalMode string

const (
	ApprovalModeConditional ApprovalMode = "conditional"
	ApprovalModeAlways      ApprovalMode = "always"
	ApprovalModeAuto        ApprovalMode = "auto"
)

func (v ApprovalMode) Valid() bool {
	switch v {
	case ApprovalModeConditional, ApprovalModeAlways, ApprovalModeAuto:
		return true
	default:
		return false
	}
}

type Operation string

const (
	OperationCreate     Operation = "create"
	OperationSynthesize Operation = "synthesize"
	OperationRewrite    Operation = "rewrite"
	OperationPolish     Operation = "polish"
)

func (v Operation) Valid() bool {
	switch v {
	case OperationCreate, OperationSynthesize, OperationRewrite, OperationPolish:
		return true
	default:
		return false
	}
}

type UserMaterialPriority string

const (
	UserMaterialPriorityHighest   UserMaterialPriority = "highest"
	UserMaterialPriorityPreferred UserMaterialPriority = "preferred"
	UserMaterialPriorityBalanced  UserMaterialPriority = "balanced"
)

func (v UserMaterialPriority) Valid() bool {
	switch v {
	case UserMaterialPriorityHighest, UserMaterialPriorityPreferred, UserMaterialPriorityBalanced:
		return true
	default:
		return false
	}
}

type ConflictHandling string

const (
	ConflictHandlingAskUser            ConflictHandling = "ask_user"
	ConflictHandlingPreferUserMaterial ConflictHandling = "prefer_user_material"
	ConflictHandlingRecordAndContinue  ConflictHandling = "record_and_continue"
)

func (v ConflictHandling) Valid() bool {
	switch v {
	case ConflictHandlingAskUser, ConflictHandlingPreferUserMaterial, ConflictHandlingRecordAndContinue:
		return true
	default:
		return false
	}
}

type EvidenceLevel string

const (
	EvidenceLevelFlexible EvidenceLevel = "flexible"
	EvidenceLevelStandard EvidenceLevel = "standard"
	EvidenceLevelSourced  EvidenceLevel = "sourced"
	EvidenceLevelStrict   EvidenceLevel = "strict"
)

func (v EvidenceLevel) Valid() bool {
	switch v {
	case EvidenceLevelFlexible, EvidenceLevelStandard, EvidenceLevelSourced, EvidenceLevelStrict:
		return true
	default:
		return false
	}
}

type UnsupportedClaimsPolicy string

const (
	UnsupportedClaimsProhibit UnsupportedClaimsPolicy = "prohibit"
	UnsupportedClaimsFlag     UnsupportedClaimsPolicy = "flag"
	UnsupportedClaimsAllow    UnsupportedClaimsPolicy = "allow"
)

func (v UnsupportedClaimsPolicy) Valid() bool {
	switch v {
	case UnsupportedClaimsProhibit, UnsupportedClaimsFlag, UnsupportedClaimsAllow:
		return true
	default:
		return false
	}
}

type DeliveryFormat string

const (
	DeliveryFormatMarkdown DeliveryFormat = "markdown"
	DeliveryFormatHTML     DeliveryFormat = "html"
	DeliveryFormatDOCX     DeliveryFormat = "docx"
	DeliveryFormatPDF      DeliveryFormat = "pdf"
)

func (v DeliveryFormat) Valid() bool {
	switch v {
	case DeliveryFormatMarkdown, DeliveryFormatHTML, DeliveryFormatDOCX, DeliveryFormatPDF:
		return true
	default:
		return false
	}
}

type AttributionSource string

const (
	AttributionSourceUser            AttributionSource = "user"
	AttributionSourceSystemInference AttributionSource = "system_inference"
	AttributionSourcePlatformDefault AttributionSource = "platform_default"
)

func (v AttributionSource) Valid() bool {
	switch v {
	case AttributionSourceUser, AttributionSourceSystemInference, AttributionSourcePlatformDefault:
		return true
	default:
		return false
	}
}

type InferenceStatus string

const (
	InferenceStatusPendingClarification InferenceStatus = "pending_clarification"
	InferenceStatusClarified            InferenceStatus = "clarified"
	InferenceStatusAccepted             InferenceStatus = "accepted"
	InferenceStatusRejected             InferenceStatus = "rejected"
)

func (v InferenceStatus) Valid() bool {
	switch v {
	case InferenceStatusPendingClarification, InferenceStatusClarified, InferenceStatusAccepted, InferenceStatusRejected:
		return true
	default:
		return false
	}
}

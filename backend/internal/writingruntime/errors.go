package writingruntime

import (
	"errors"
	"fmt"
)

type ErrorCode string

const (
	CodeExecutorNotRegistered          ErrorCode = "EXECUTOR_NOT_REGISTERED"
	CodeExecutorContractMismatch       ErrorCode = "EXECUTOR_CONTRACT_MISMATCH"
	CodeExecutorOutputInvalid          ErrorCode = "EXECUTOR_OUTPUT_INVALID"
	CodeExecutorUsageUnmeasured        ErrorCode = "EXECUTOR_USAGE_UNMEASURED"
	CodeExecutorCancelUnsupported      ErrorCode = "EXECUTOR_CANCEL_UNSUPPORTED"
	CodeExecutorTrafficDisabled        ErrorCode = "EXECUTOR_TRAFFIC_DISABLED"
	CodeLegacyWriteViolation           ErrorCode = "LEGACY_WRITE_VIOLATION"
	CodeMaterialAccessDenied           ErrorCode = "MATERIAL_ACCESS_DENIED"
	CodeMaterialIntegrityFailed        ErrorCode = "MATERIAL_INTEGRITY_FAILED"
	CodeSourceSnapshotFailed           ErrorCode = "SOURCE_SNAPSHOT_FAILED"
	CodeSourceConflictRequiresDecision ErrorCode = "SOURCE_CONFLICT_REQUIRES_DECISION"
	CodeArtifactCommitFailed           ErrorCode = "ARTIFACT_COMMIT_FAILED"
	CodeRolloutPolicyInvalid           ErrorCode = "ROLLOUT_POLICY_INVALID"
	CodeRolloutEvidenceFailed          ErrorCode = "ROLLOUT_EVIDENCE_FAILED"
	CodeRolloutPromotionDenied         ErrorCode = "ROLLOUT_PROMOTION_DENIED"
	CodeContextRequiredMissing         ErrorCode = "CONTEXT_REQUIRED_MISSING"
	CodeExecutionFailed                ErrorCode = "EXECUTION_FAILED"

	// CodeResearchPilotRequired marks a research execution refused by the
	// deep-research pilot's per-subject entitlement gate (wp-pilot-launch):
	// the subject holds no unexpired research_pilot_entitlements row for the
	// requested scope. Fail-node, never a retry — re-executing cannot grant
	// an entitlement.
	CodeResearchPilotRequired ErrorCode = "RESEARCH_PILOT_REQUIRED"
)

var (
	// ErrContextRequiredMissing marks a node whose capability enforces its
	// context contract and whose envelope lacks a required block. It is
	// fail-node (never a retry): the data gap does not heal by re-running.
	ErrContextRequiredMissing = errors.New("writingruntime: required context blocks are missing")
)

type RetryClass string

const (
	RetryNever      RetryClass = "never"
	RetrySafe       RetryClass = "safe"
	RetryAfterHuman RetryClass = "after_human"
)

type RuntimeError struct {
	Code       ErrorCode
	RetryClass RetryClass
	Message    string
	Cause      error
}

func (err *RuntimeError) Error() string {
	if err == nil {
		return ""
	}
	if err.Message != "" {
		return fmt.Sprintf("%s: %s", err.Code, err.Message)
	}
	return string(err.Code)
}

func (err *RuntimeError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Cause
}

func runtimeError(code ErrorCode, retry RetryClass, message string, cause error) error {
	return &RuntimeError{Code: code, RetryClass: retry, Message: message, Cause: cause}
}

func ErrorCodeOf(err error) ErrorCode {
	if err == nil {
		return ""
	}
	var governed *RuntimeError
	if errors.As(err, &governed) {
		return governed.Code
	}
	switch {
	case errors.Is(err, ErrExecutorNotFound):
		return CodeExecutorNotRegistered
	case errors.Is(err, ErrExecutorTrafficDisabled):
		return CodeExecutorTrafficDisabled
	case errors.Is(err, ErrExecutorMismatch), errors.Is(err, ErrInvalidExecutionRequest):
		return CodeExecutorContractMismatch
	case errors.Is(err, ErrInvalidExecutionResult), errors.Is(err, ErrLegacyOutputMissing):
		return CodeExecutorOutputInvalid
	case errors.Is(err, ErrLegacyUsageMissing):
		return CodeExecutorUsageUnmeasured
	case errors.Is(err, ErrLegacyContentIntegrity):
		return CodeMaterialIntegrityFailed
	case errors.Is(err, ErrLegacyEmitterUnsafe):
		return CodeLegacyWriteViolation
	case errors.Is(err, ErrShadowContentLeak):
		return CodeArtifactCommitFailed
	default:
		return CodeExecutionFailed
	}
}

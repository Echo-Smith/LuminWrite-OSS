package server

// Deep-research pilot authorization infrastructure (wp-pilot-launch).
//
// 深度研究试点期按 subject 精确授权：research_pilot_entitlements 表是试点
// 范围的审批记录（migration 121），draft 端点与治理运行时的 research direct
// executor 双层都通过同一 subject policy 查询它。查询失败一律 fail closed：
// 授权面宁可拒绝一次合法请求，也不放行一次未授权执行。
//
// During the deep-research pilot, access is granted per subject: the
// research_pilot_entitlements table (migration 121) holds the pilot's
// approval records, and both the draft endpoint and the governed runtime's
// research direct executors consult it through this same subject policy.
// Lookup failures fail closed: the authorization surface would rather refuse
// one legitimate request than let one unauthorized execution through.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingruntime"
)

// ResearchPilotScopeResearchReview is the pilot scope the deep-research flow
// (draft endpoint + research executors) is gated under.
const ResearchPilotScopeResearchReview = "research_review"

var (
	// errResearchPilotRequired maps to 403 RESEARCH_PILOT_REQUIRED: the
	// subject is known and authenticated but holds no unexpired pilot
	// entitlement for the scope (checked after document authorization, so it
	// applies to replays too — an entitled document is not an entitled user).
	errResearchPilotRequired = errors.New("writing api: subject is not entitled to the research pilot")
	// errResearchPilotUnavailable maps to 503 RESEARCH_UNAVAILABLE: the
	// entitlement lookup itself failed (store down, bad subject shape), so
	// the gate cannot answer and refuses honestly instead of guessing.
	errResearchPilotUnavailable = errors.New("writing api: research pilot entitlement check failed")
)

// ResearchPilotAuthorizer answers one (subject, scope) entitlement question.
// An interface so the gate executor is unit-testable without a database.
type ResearchPilotAuthorizer interface {
	Entitled(ctx context.Context, subjectID, scope string) (bool, error)
}

// ResearchPilotPolicy is the SQL-backed subject policy over
// research_pilot_entitlements. A subject is entitled when an unexpired row
// exists for (subject_id, scope); expires_at IS NULL means "no expiry".
type ResearchPilotPolicy struct {
	DB *database.DB
}

// NewResearchPilotPolicy builds the policy over the given pool.
func NewResearchPilotPolicy(db *database.DB) *ResearchPilotPolicy {
	return &ResearchPilotPolicy{DB: db}
}

// Entitled implements ResearchPilotAuthorizer. Errors are returned, never
// collapsed into (false, nil) — the caller decides the fail-closed behavior,
// and this contract keeps "no row" distinguishable from "cannot ask".
func (policy *ResearchPilotPolicy) Entitled(ctx context.Context, subjectID, scope string) (bool, error) {
	if policy == nil || policy.DB == nil || policy.DB.DB == nil {
		return false, errors.New("research pilot: entitlement store is not wired")
	}
	if strings.TrimSpace(subjectID) == "" || strings.TrimSpace(scope) == "" {
		return false, errors.New("research pilot: subject and scope are required")
	}
	var entitled int
	err := policy.DB.QueryRowContext(ctx, `
		SELECT 1 FROM research_pilot_entitlements
		WHERE subject_id = $1::uuid AND scope = $2
		  AND (expires_at IS NULL OR expires_at > now())
		LIMIT 1
	`, subjectID, scope).Scan(&entitled)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("research pilot: entitlement lookup: %w", err)
	}
	return true, nil
}

// requireResearchPilot is the persistentWritingAPI-side gate for the draft
// endpoint. Fail-closed in both directions: a nil policy (no store wired)
// and a failed lookup both refuse with errResearchPilotUnavailable, and a
// definitive "no" refuses with errResearchPilotRequired.
func (service *persistentWritingAPI) requireResearchPilot(ctx context.Context, subject, scope string) error {
	if service.researchPilot == nil {
		return errResearchPilotUnavailable
	}
	entitled, err := service.researchPilot.Entitled(ctx, subject, scope)
	if err != nil {
		return fmt.Errorf("%w: %v", errResearchPilotUnavailable, err)
	}
	if !entitled {
		return fmt.Errorf("%w: scope %s subject %s", errResearchPilotRequired, scope, subject)
	}
	return nil
}

// NewResearchPilotGateExecutor wraps a research direct executor in the
// pilot's per-subject gate. Descriptor() is transparent (the inner
// executor's identity is preserved, so governed_runtime.go's
// binding-id consistency check sees exactly the id the spec was built
// with); Execute() resolves the subject (request.Subject, falling back to
// request.UserID) and consults the policy before the inner executor ever runs.
func NewResearchPilotGateExecutor(inner writingruntime.Executor, policy ResearchPilotAuthorizer, scope string) writingruntime.Executor {
	return &researchPilotGateExecutor{inner: inner, policy: policy, scope: scope}
}

type researchPilotGateExecutor struct {
	inner  writingruntime.Executor
	policy ResearchPilotAuthorizer
	scope  string
}

// Descriptor delegates to the inner executor: the pilot gate is an
// authorization concern, never an identity change.
func (executor *researchPilotGateExecutor) Descriptor() writingruntime.ExecutorDescriptor {
	return executor.inner.Descriptor()
}

// Execute refuses unauthorized subjects with a typed
// RESEARCH_PILOT_REQUIRED error (the node failure path records the code) and
// fail-closed on policy errors. The inner executor only runs for entitled
// subjects.
func (executor *researchPilotGateExecutor) Execute(ctx context.Context, request writingruntime.ExecutionRequest) (writingruntime.ExecutionResult, error) {
	subject := strings.TrimSpace(request.Subject)
	if subject == "" {
		subject = strings.TrimSpace(request.UserID)
	}
	if subject == "" {
		return writingruntime.ExecutionResult{}, &writingruntime.RuntimeError{
			Code: writingruntime.CodeResearchPilotRequired, RetryClass: writingruntime.RetryNever,
			Message: fmt.Sprintf("research pilot gate (%s) refuses an execution request that carries no subject", executor.scope),
		}
	}
	if executor.policy == nil {
		return writingruntime.ExecutionResult{}, &writingruntime.RuntimeError{
			Code: writingruntime.CodeExecutionFailed, RetryClass: writingruntime.RetryNever,
			Message: fmt.Sprintf("research pilot policy is not wired (scope %s, subject %s)", executor.scope, subject),
		}
	}
	entitled, err := executor.policy.Entitled(ctx, subject, executor.scope)
	if err != nil {
		return writingruntime.ExecutionResult{}, &writingruntime.RuntimeError{
			Code: writingruntime.CodeExecutionFailed, RetryClass: writingruntime.RetryNever,
			Message: fmt.Sprintf("research pilot entitlement check failed (scope %s, subject %s)", executor.scope, subject),
			Cause:   err,
		}
	}
	if !entitled {
		return writingruntime.ExecutionResult{}, &writingruntime.RuntimeError{
			Code: writingruntime.CodeResearchPilotRequired, RetryClass: writingruntime.RetryNever,
			Message: fmt.Sprintf("subject %s is not entitled to the %s research pilot", subject, executor.scope),
		}
	}
	return executor.inner.Execute(ctx, request)
}

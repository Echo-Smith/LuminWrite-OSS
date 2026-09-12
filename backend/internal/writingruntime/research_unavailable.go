package writingruntime

import (
	"context"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
)

// UnavailableResearchExecutor is the honest stand-in for a research executor
// whose upstream dependency (scholar worker, model client) is not configured
// in this deployment. Design.md §7: worker/模型不可用 → SERVICE_UNAVAILABLE,
// 研究路径暂停，普通路径继续可用. Registering the capability keeps plan
// compilation honest (the template is complete) while dispatch pauses with a
// typed error instead of pretending to run.
type UnavailableResearchExecutor struct {
	descriptor ExecutorDescriptor
	reason     string
}

// NewUnavailableResearchExecutor builds the pause-on-dispatch executor.
func NewUnavailableResearchExecutor(executorID, reason string) *UnavailableResearchExecutor {
	return &UnavailableResearchExecutor{
		descriptor: ExecutorDescriptor{ExecutorID: executorID, Version: "1",
			SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction, writingplan.NodeValidate},
			Cancellable:        false},
		reason: reason,
	}
}

func (executor *UnavailableResearchExecutor) Descriptor() ExecutorDescriptor {
	return executor.descriptor
}

// Execute always fails with RESEARCH_UNAVAILABLE: the run pauses through the
// node's pause failure path and nothing upstream is called.
func (executor *UnavailableResearchExecutor) Execute(context.Context, ExecutionRequest) (ExecutionResult, error) {
	return ExecutionResult{}, runtimeError(CodeResearchUnavailable, RetryAfterHuman,
		"research dependency unavailable: "+executor.reason, ErrResearchGeneratorUnavailable)
}

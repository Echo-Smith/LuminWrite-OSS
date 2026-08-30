package writingruntime

import (
	"context"
	"errors"
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
)

type registryExecutor struct{ descriptor ExecutorDescriptor }

func (executor registryExecutor) Descriptor() ExecutorDescriptor { return executor.descriptor }
func (registryExecutor) Execute(context.Context, ExecutionRequest) (ExecutionResult, error) {
	return ExecutionResult{}, nil
}

func TestExecutorRegistryRejectsDuplicateAndMismatchedBindings(t *testing.T) {
	registry := NewExecutorRegistry()
	executor := registryExecutor{descriptor: ExecutorDescriptor{ExecutorID: "engine.step.write", Version: "adapter-1", SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction}}}
	if err := registry.Register(executor); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(executor); !errors.Is(err, ErrExecutorAlreadyExists) {
		t.Fatalf("expected duplicate error, got %v", err)
	}
	manifest := writingplan.CapabilityManifest{ID: "core.draft.generate", Executor: "engine.step.write", Version: "1.0.0"}
	node := writingplan.PlanNode{NodeID: "node_write", Kind: writingplan.NodeAction, Capability: manifest.ID, CapabilityVersion: manifest.Version}
	if _, err := registry.Resolve(manifest, node); err != nil {
		t.Fatal(err)
	}
	node.Kind = writingplan.NodeValidate
	if _, err := registry.Resolve(manifest, node); !errors.Is(err, ErrExecutorMismatch) {
		t.Fatalf("expected mismatch, got %v", err)
	}
}

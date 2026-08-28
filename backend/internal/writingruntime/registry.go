package writingruntime

import (
	"errors"
	"fmt"
	"sync"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
)

var (
	ErrExecutorNotFound      = errors.New("writingruntime: executor not found")
	ErrExecutorAlreadyExists = errors.New("writingruntime: executor already registered")
	ErrExecutorMismatch      = errors.New("writingruntime: executor binding mismatch")
)

// ExecutorRegistry is the runtime dispatch table. Capability metadata remains
// authoritative in writingplan; this registry only owns live typed executors.
type ExecutorRegistry struct {
	mu        sync.RWMutex
	executors map[string]Executor
}

func NewExecutorRegistry() *ExecutorRegistry {
	return &ExecutorRegistry{executors: make(map[string]Executor)}
}

func (registry *ExecutorRegistry) Register(executor Executor) error {
	if registry == nil || executor == nil {
		return fmt.Errorf("%w: executor is required", ErrInvalidExecutorDescriptor)
	}
	descriptor := executor.Descriptor()
	if err := descriptor.Validate(); err != nil {
		return err
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, exists := registry.executors[descriptor.ExecutorID]; exists {
		return fmt.Errorf("%w: %s", ErrExecutorAlreadyExists, descriptor.ExecutorID)
	}
	registry.executors[descriptor.ExecutorID] = executor
	return nil
}

func (registry *ExecutorRegistry) Resolve(manifest writingplan.CapabilityManifest, node writingplan.PlanNode) (Executor, error) {
	if registry == nil {
		return nil, ErrExecutorNotFound
	}
	registry.mu.RLock()
	executor, exists := registry.executors[manifest.Executor]
	registry.mu.RUnlock()
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrExecutorNotFound, manifest.Executor)
	}
	descriptor := executor.Descriptor()
	if manifest.ID != node.Capability || manifest.Version != node.CapabilityVersion || descriptor.ExecutorID != manifest.Executor || !supportsNodeKind(descriptor, node.Kind) {
		return nil, fmt.Errorf("%w: capability %s node %s", ErrExecutorMismatch, manifest.ID, node.NodeID)
	}
	if descriptor.Cancellable {
		if _, ok := executor.(CancellableExecutor); !ok {
			return nil, fmt.Errorf("%w: %s declares cancellation without Cancel", ErrExecutorMismatch, descriptor.ExecutorID)
		}
	}
	return executor, nil
}

func supportsNodeKind(descriptor ExecutorDescriptor, kind writingplan.NodeKind) bool {
	for _, supported := range descriptor.SupportedNodeKinds {
		if supported == kind {
			return true
		}
	}
	return false
}

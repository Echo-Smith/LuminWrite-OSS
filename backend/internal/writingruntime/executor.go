package writingruntime

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

var (
	ErrInvalidExecutionRequest   = errors.New("writingruntime: invalid execution request")
	ErrInvalidExecutionResult    = errors.New("writingruntime: invalid execution result")
	ErrInvalidExecutorDescriptor = errors.New("writingruntime: invalid executor descriptor")
)

var (
	executionHashPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	outputKeyPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

type ExecutorDescriptor struct {
	ExecutorID         string
	Version            string
	SupportedNodeKinds []writingplan.NodeKind
	Cancellable        bool
}

type ExecutionIdentity struct {
	RunID          string                `json:"run_id"`
	PlanID         string                `json:"plan_id"`
	PlanVersion    int                   `json:"plan_version"`
	NodeID         string                `json:"node_id"`
	Attempt        int                   `json:"attempt"`
	IdempotencyKey string                `json:"idempotency_key"`
	ContractRef    writingplan.ObjectRef `json:"contract_ref"`
	// Subject is the rollout audience this execution belongs to (user or
	// tenant). Empty means the run id is the routing subject.
	Subject string `json:"subject,omitempty"`
}

func (identity ExecutionIdentity) Validate() error {
	if !hasIDPrefix(identity.RunID, "run_") || !hasIDPrefix(identity.PlanID, "plan_") || identity.PlanVersion < 1 || !hasIDPrefix(identity.NodeID, "node_") {
		return runtimeError(CodeExecutorContractMismatch, RetryNever, "invalid execution identity", ErrInvalidExecutionRequest)
	}
	expected, err := writingstore.NodeAttemptKey(identity.RunID, identity.NodeID, identity.Attempt)
	if err != nil || identity.IdempotencyKey != expected || !hasIDPrefix(identity.ContractRef.ID, "ctr_") || identity.ContractRef.Version < 1 || !executionHashPattern.MatchString(identity.ContractRef.Hash) {
		return runtimeError(CodeExecutorContractMismatch, RetryNever, "execution identity is not canonically bound", ErrInvalidExecutionRequest)
	}
	return nil
}

func (descriptor ExecutorDescriptor) Validate() error {
	if strings.TrimSpace(descriptor.ExecutorID) == "" || strings.TrimSpace(descriptor.Version) == "" || len(descriptor.SupportedNodeKinds) == 0 {
		return fmt.Errorf("%w: executor id, version, and node kinds are required", ErrInvalidExecutorDescriptor)
	}
	seen := make(map[writingplan.NodeKind]struct{}, len(descriptor.SupportedNodeKinds))
	for _, kind := range descriptor.SupportedNodeKinds {
		if !validExecutionNodeKind(kind) {
			return fmt.Errorf("%w: unsupported node kind %q", ErrInvalidExecutorDescriptor, kind)
		}
		if _, duplicate := seen[kind]; duplicate {
			return fmt.Errorf("%w: duplicate node kind %q", ErrInvalidExecutorDescriptor, kind)
		}
		seen[kind] = struct{}{}
	}
	return nil
}

// Executor is deliberately unable to mutate documents, quality state, or run
// state. It receives immutable references and returns provisional artifact
// drafts; the orchestrator and writingstore own all authoritative commits.
type Executor interface {
	Descriptor() ExecutorDescriptor
	Execute(context.Context, ExecutionRequest) (ExecutionResult, error)
}

type AdapterFamily string

const (
	AdapterFamilyEngine    AdapterFamily = "engine"
	AdapterFamilyEditorial AdapterFamily = "editorial"
	AdapterFamilyHarness   AdapterFamily = "harness"
)

type AdapterTrafficMode string

const (
	AdapterTrafficOffline AdapterTrafficMode = "offline_only"
	AdapterTrafficEnabled AdapterTrafficMode = "enabled"
)

type AuthorityScope struct {
	DocumentWrite bool `json:"document_write"`
	QualityWrite  bool `json:"quality_write"`
	RunWrite      bool `json:"run_write"`
	ArtifactWrite bool `json:"artifact_write"`
}

type AdapterPolicy struct {
	Family      AdapterFamily      `json:"family"`
	TrafficMode AdapterTrafficMode `json:"traffic_mode"`
	Authority   AuthorityScope     `json:"authority"`
}

func OfflineAdapterPolicy(family AdapterFamily) AdapterPolicy {
	return AdapterPolicy{Family: family, TrafficMode: AdapterTrafficOffline}
}

func (policy AdapterPolicy) Validate() error {
	if policy.Family != AdapterFamilyEngine && policy.Family != AdapterFamilyEditorial && policy.Family != AdapterFamilyHarness {
		return runtimeError(CodeExecutorContractMismatch, RetryNever, "unknown adapter family", ErrExecutorMismatch)
	}
	if policy.TrafficMode != AdapterTrafficOffline && policy.TrafficMode != AdapterTrafficEnabled {
		return runtimeError(CodeExecutorContractMismatch, RetryNever, "invalid adapter traffic mode", ErrExecutorMismatch)
	}
	if policy.Authority.DocumentWrite || policy.Authority.QualityWrite || policy.Authority.RunWrite || policy.Authority.ArtifactWrite {
		return runtimeError(CodeLegacyWriteViolation, RetryNever, "executor adapter requested authoritative writes", nil)
	}
	return nil
}

type ExecutorAdapter interface {
	Executor
	AdapterPolicy() AdapterPolicy
	Prepare(context.Context, ExecutionRequest) (ExecutionRequest, error)
	NormalizeResult(ExecutionRequest, ExecutionResult) (ExecutionResult, error)
}

type CancellableExecutor interface {
	Executor
	Cancel(context.Context, ExecutionHandle) error
}

type ExecutionHandle struct {
	RunID          string
	NodeID         string
	Attempt        int
	IdempotencyKey string
}

type InputArtifact struct {
	ArtifactID   string
	Version      int
	ArtifactType writingplan.ArtifactType
	ContentHash  string
	MediaType    string
	ContentRef   string
}

type ExecutionRequest struct {
	RunID          string
	PlanID         string
	PlanVersion    int
	NodeID         string
	Attempt        int
	IdempotencyKey string
	ContractRef    writingplan.ObjectRef
	Node           writingplan.PlanNode
	Inputs         []InputArtifact
	Permissions    []writingplan.Permission
	// Subject carries the rollout audience (user/tenant) for allowlist and
	// percentage routing. Empty falls back to the run id.
	Subject string
}

func (request ExecutionRequest) Validate() error {
	if err := request.Identity().Validate(); err != nil {
		return err
	}
	if request.Node.NodeID != request.NodeID || !validExecutionNodeKind(request.Node.Kind) ||
		strings.TrimSpace(request.Node.Capability) == "" || strings.TrimSpace(request.Node.CapabilityVersion) == "" {
		return fmt.Errorf("%w: request does not match a governed plan node", ErrInvalidExecutionRequest)
	}
	if request.Node.Bounds.MaxAttempts < 1 || request.Attempt > request.Node.Bounds.MaxAttempts ||
		request.Node.Bounds.MaxConcurrency < 1 || request.Node.Bounds.MaxItems < 1 ||
		request.Node.Bounds.MaxCostUSD < 0 || request.Node.Bounds.TimeoutMS < 1 ||
		!validExecutionFailurePath(request.Node.FailurePath) {
		return fmt.Errorf("%w: invalid or exceeded node bounds", ErrInvalidExecutionRequest)
	}
	if request.Node.InputArtifactTypes == nil || request.Node.OutputArtifactTypes == nil || request.Inputs == nil || request.Permissions == nil {
		return fmt.Errorf("%w: artifact types, inputs, and permissions must be present", ErrInvalidExecutionRequest)
	}
	availableTypes := make(map[writingplan.ArtifactType]bool, len(request.Inputs))
	seenInputs := make(map[string]struct{}, len(request.Inputs))
	for _, input := range request.Inputs {
		if !hasIDPrefix(input.ArtifactID, "art_") || input.Version < 1 || strings.TrimSpace(string(input.ArtifactType)) == "" ||
			!executionHashPattern.MatchString(input.ContentHash) || !validExecutionMediaType(input.MediaType) || strings.TrimSpace(input.ContentRef) == "" {
			return fmt.Errorf("%w: invalid input artifact", ErrInvalidExecutionRequest)
		}
		identity := artifactIdentity(input.ArtifactID, input.Version)
		if _, duplicate := seenInputs[identity]; duplicate {
			return fmt.Errorf("%w: duplicate input artifact %s", ErrInvalidExecutionRequest, identity)
		}
		seenInputs[identity] = struct{}{}
		availableTypes[input.ArtifactType] = true
	}
	for _, requiredType := range request.Node.InputArtifactTypes {
		if !availableTypes[requiredType] {
			return fmt.Errorf("%w: missing input artifact type %q", ErrInvalidExecutionRequest, requiredType)
		}
	}
	return nil
}

func (request ExecutionRequest) Identity() ExecutionIdentity {
	return ExecutionIdentity{RunID: request.RunID, PlanID: request.PlanID, PlanVersion: request.PlanVersion, NodeID: request.NodeID, Attempt: request.Attempt, IdempotencyKey: request.IdempotencyKey, ContractRef: request.ContractRef, Subject: strings.TrimSpace(request.Subject)}
}

func (request ExecutionRequest) Handle() ExecutionHandle {
	return ExecutionHandle{RunID: request.RunID, NodeID: request.NodeID,
		Attempt: request.Attempt, IdempotencyKey: request.IdempotencyKey}
}

type OutputArtifactDraft struct {
	OutputKey         string
	ArtifactType      writingplan.ArtifactType
	ContentHash       string
	MediaType         string
	ContentRef        string
	Parents           []writingstore.ArtifactRef
	Producer          string
	CapabilityVersion string
	InputHashes       []string
	ModelRef          string
	PromptTemplateRef string
	Provenance        map[string]any
	SourceRefs        []string
}

type ExecutionUsage struct {
	CostUSD      float64 `json:"cost_usd"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	DurationMS   int64   `json:"duration_ms"`
}

type ExecutionResult struct {
	Artifacts   []OutputArtifactDraft
	Usage       ExecutionUsage
	StartedAt   time.Time
	CompletedAt time.Time
}

func (result ExecutionResult) Validate(request ExecutionRequest) error {
	if err := request.Validate(); err != nil {
		return fmt.Errorf("%w: request binding: %v", ErrInvalidExecutionResult, err)
	}
	if result.Artifacts == nil || result.StartedAt.IsZero() || result.CompletedAt.Before(result.StartedAt) {
		return fmt.Errorf("%w: artifacts and execution interval are required", ErrInvalidExecutionResult)
	}
	if result.Usage.CostUSD < 0 || result.Usage.InputTokens < 0 || result.Usage.OutputTokens < 0 || result.Usage.DurationMS < 0 {
		return fmt.Errorf("%w: execution usage cannot be negative", ErrInvalidExecutionResult)
	}
	declaredOutputs := make(map[writingplan.ArtifactType]bool, len(request.Node.OutputArtifactTypes))
	for _, artifactType := range request.Node.OutputArtifactTypes {
		declaredOutputs[artifactType] = true
	}
	producedOutputs := make(map[writingplan.ArtifactType]bool, len(result.Artifacts))
	seenOutputKeys := make(map[string]struct{}, len(result.Artifacts))
	inputs := make(map[string]InputArtifact, len(request.Inputs))
	for _, input := range request.Inputs {
		inputs[artifactIdentity(input.ArtifactID, input.Version)] = input
	}
	for _, artifact := range result.Artifacts {
		if !outputKeyPattern.MatchString(artifact.OutputKey) || !declaredOutputs[artifact.ArtifactType] ||
			!executionHashPattern.MatchString(artifact.ContentHash) || !validExecutionMediaType(artifact.MediaType) ||
			strings.TrimSpace(artifact.ContentRef) == "" {
			return fmt.Errorf("%w: invalid or undeclared output artifact", ErrInvalidExecutionResult)
		}
		if producedOutputs[artifact.ArtifactType] {
			return fmt.Errorf("%w: duplicate output artifact type %q", ErrInvalidExecutionResult, artifact.ArtifactType)
		}
		if _, duplicate := seenOutputKeys[artifact.OutputKey]; duplicate {
			return fmt.Errorf("%w: duplicate output key %q", ErrInvalidExecutionResult, artifact.OutputKey)
		}
		seenOutputKeys[artifact.OutputKey] = struct{}{}
		if artifact.Producer != request.Node.Capability || artifact.CapabilityVersion != request.Node.CapabilityVersion {
			return fmt.Errorf("%w: artifact producer does not match plan capability", ErrInvalidExecutionResult)
		}
		if artifact.Parents == nil || artifact.InputHashes == nil || artifact.Provenance == nil || artifact.SourceRefs == nil {
			return fmt.Errorf("%w: artifact lineage and provenance must be present", ErrInvalidExecutionResult)
		}
		parents := make(map[string]struct{}, len(artifact.Parents))
		lineageHashes := make(map[string]struct{}, len(artifact.Parents))
		for _, parent := range artifact.Parents {
			identity := artifactIdentity(parent.ArtifactID, parent.Version)
			input, exists := inputs[identity]
			if !exists {
				return fmt.Errorf("%w: artifact parent is not an execution input", ErrInvalidExecutionResult)
			}
			parents[identity] = struct{}{}
			lineageHashes[input.ContentHash] = struct{}{}
		}
		if len(parents) != len(inputs) {
			return fmt.Errorf("%w: artifact parents do not cover every execution input", ErrInvalidExecutionResult)
		}
		if len(artifact.InputHashes) != len(lineageHashes) {
			return fmt.Errorf("%w: input hash lineage differs from parent content", ErrInvalidExecutionResult)
		}
		seenInputHashes := make(map[string]struct{}, len(artifact.InputHashes))
		for _, inputHash := range artifact.InputHashes {
			if !executionHashPattern.MatchString(inputHash) {
				return fmt.Errorf("%w: invalid input hash", ErrInvalidExecutionResult)
			}
			if _, duplicate := seenInputHashes[inputHash]; duplicate {
				return fmt.Errorf("%w: duplicate input hash", ErrInvalidExecutionResult)
			}
			seenInputHashes[inputHash] = struct{}{}
			if _, exists := lineageHashes[inputHash]; !exists {
				return fmt.Errorf("%w: input hash has no parent binding", ErrInvalidExecutionResult)
			}
		}
		producedOutputs[artifact.ArtifactType] = true
	}
	for _, requiredType := range request.Node.OutputArtifactTypes {
		if !producedOutputs[requiredType] {
			return fmt.Errorf("%w: missing declared output artifact type %q", ErrInvalidExecutionResult, requiredType)
		}
	}
	return nil
}

func hasIDPrefix(value, prefix string) bool {
	return strings.HasPrefix(value, prefix) && len(value) > len(prefix)
}

func artifactIdentity(id string, version int) string {
	return fmt.Sprintf("%s:%d", id, version)
}

func validExecutionMediaType(mediaType string) bool {
	return mediaType == "application/json" || mediaType == "text/markdown" || mediaType == "text/plain"
}

func validExecutionNodeKind(kind writingplan.NodeKind) bool {
	switch kind {
	case writingplan.NodeSequence, writingplan.NodeParallel, writingplan.NodeMap,
		writingplan.NodeReduce, writingplan.NodeCondition, writingplan.NodeRetry,
		writingplan.NodeRefine, writingplan.NodeHumanGate, writingplan.NodeValidate,
		writingplan.NodeFallback, writingplan.NodeAction:
		return true
	default:
		return false
	}
}

func validExecutionFailurePath(path writingplan.FailurePath) bool {
	return path == writingplan.FailureFail || path == writingplan.FailurePause ||
		path == writingplan.FailureFallback || path == writingplan.FailurePartial
}

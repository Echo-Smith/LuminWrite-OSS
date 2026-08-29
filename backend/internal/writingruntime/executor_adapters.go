package writingruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/agent"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/editorial"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

var (
	ErrLegacyContentIntegrity = errors.New("writingruntime: legacy content integrity failure")
	ErrLegacyUsageMissing     = errors.New("writingruntime: legacy executor usage is not measured")
	ErrLegacyOutputMissing    = errors.New("writingruntime: legacy executor output missing")
	ErrLegacyHarnessUnsafe    = errors.New("writingruntime: Harness.Run cannot be adapted before its session-writing core is extracted")
	ErrLegacyDAGUnsafe        = errors.New("writingruntime: DAGExecutor cannot be adapted before its store and status writes are extracted")
	ErrLegacyEmitterUnsafe    = errors.New("writingruntime: legacy emitters may own sessions, streams, or old event stores")
)

type ContentGateway interface {
	Load(context.Context, InputArtifact) ([]byte, error)
	Stage(context.Context, string, string, []byte) (contentRef, contentHash string, err error)
}

type LegacyNodeInput struct {
	Request  ExecutionRequest
	Payloads map[writingplan.ArtifactType][][]byte
}

type LegacyPayload struct {
	OutputKey, MediaType, ModelRef, PromptTemplateRef string
	ArtifactType                                      writingplan.ArtifactType
	Body                                              []byte
	SourceRefs                                        []string
	Provenance                                        map[string]any
}

type LegacyUsage struct {
	Measured                  bool
	CostUSD                   float64
	InputTokens, OutputTokens int64
}

type LegacyNodeRunner interface {
	Run(context.Context, LegacyNodeInput) ([]LegacyPayload, LegacyUsage, error)
}

type LegacyExecutor struct {
	descriptor                      ExecutorDescriptor
	policy                          AdapterPolicy
	capabilityID, capabilityVersion string
	requiredPermissions             []writingplan.Permission
	content                         ContentGateway
	shadow                          *ShadowContentGateway
	runner                          LegacyNodeRunner
	now                             func() time.Time
}

func NewLegacyExecutor(descriptor ExecutorDescriptor, capabilityID, capabilityVersion string, required []writingplan.Permission, content ContentGateway, runner LegacyNodeRunner) (*LegacyExecutor, error) {
	return NewLegacyExecutorAdapter(adapterFamilyForExecutor(descriptor.ExecutorID), descriptor, capabilityID, capabilityVersion, required, content, runner)
}

func NewLegacyExecutorAdapter(family AdapterFamily, descriptor ExecutorDescriptor, capabilityID, capabilityVersion string, required []writingplan.Permission, content ContentGateway, runner LegacyNodeRunner) (*LegacyExecutor, error) {
	if err := descriptor.Validate(); err != nil {
		return nil, err
	}
	policy := OfflineAdapterPolicy(family)
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	if descriptor.Cancellable {
		return nil, runtimeError(CodeExecutorCancelUnsupported, RetryNever, "legacy adapters remain non-cancellable until every nested tool propagates context", ErrExecutorMismatch)
	}
	if strings.TrimSpace(capabilityID) == "" || strings.TrimSpace(capabilityVersion) == "" || required == nil || content == nil || runner == nil {
		return nil, ErrRuntimeNotReady
	}
	return &LegacyExecutor{descriptor: descriptor, policy: policy, capabilityID: capabilityID, capabilityVersion: capabilityVersion,
		requiredPermissions: append([]writingplan.Permission(nil), required...), content: content, runner: runner,
		now: func() time.Time { return time.Now().UTC() }}, nil
}

func (executor *LegacyExecutor) Descriptor() ExecutorDescriptor { return executor.descriptor }
func (executor *LegacyExecutor) AdapterPolicy() AdapterPolicy   { return executor.policy }

// ShadowGateway reports the shadow content namespace this adapter stages
// into, or nil when the adapter stages through the canonical gateway.
func (executor *LegacyExecutor) ShadowGateway() *ShadowContentGateway { return executor.shadow }

// ShadowIsolatedCandidate proves that a candidate adapter stages content only
// into the shadow namespace. Rollout executors require this proof at
// construction instead of trusting adapter wiring.
type ShadowIsolatedCandidate interface {
	ExecutorAdapter
	ShadowGateway() *ShadowContentGateway
}

// NewShadowIsolatedExecutorAdapter builds a candidate adapter whose staged
// content can never reach the canonical store: every Stage call lands in the
// shadow namespace of one policy version.
func NewShadowIsolatedExecutorAdapter(family AdapterFamily, descriptor ExecutorDescriptor, capabilityID, capabilityVersion string, required []writingplan.Permission, shadowGateway *ShadowContentGateway, runner LegacyNodeRunner) (*LegacyExecutor, error) {
	if shadowGateway == nil {
		return nil, ErrRuntimeNotReady
	}
	adapter, err := NewLegacyExecutorAdapter(family, descriptor, capabilityID, capabilityVersion, required, shadowGateway, runner)
	if err != nil {
		return nil, err
	}
	adapter.shadow = shadowGateway
	return adapter, nil
}

func (executor *LegacyExecutor) Prepare(_ context.Context, request ExecutionRequest) (ExecutionRequest, error) {
	if err := executor.policy.Validate(); err != nil {
		return ExecutionRequest{}, err
	}
	if err := request.Validate(); err != nil {
		return ExecutionRequest{}, err
	}
	return request, nil
}

func (executor *LegacyExecutor) NormalizeResult(request ExecutionRequest, result ExecutionResult) (ExecutionResult, error) {
	if err := result.Validate(request); err != nil {
		return ExecutionResult{}, runtimeError(CodeExecutorOutputInvalid, RetryNever, "legacy output does not satisfy governed result contract", err)
	}
	return result, nil
}

func (executor *LegacyExecutor) Execute(ctx context.Context, request ExecutionRequest) (ExecutionResult, error) {
	prepared, err := executor.Prepare(ctx, request)
	if err != nil {
		return ExecutionResult{}, err
	}
	request = prepared
	if request.Node.Capability != executor.capabilityID || request.Node.CapabilityVersion != executor.capabilityVersion || !supportsNodeKind(executor.descriptor, request.Node.Kind) || !permissionsContain(request.Permissions, executor.requiredPermissions) {
		return ExecutionResult{}, ErrExecutorMismatch
	}
	payloads := make(map[writingplan.ArtifactType][][]byte)
	for _, input := range request.Inputs {
		body, err := executor.content.Load(ctx, input)
		if err != nil {
			return ExecutionResult{}, err
		}
		if contentHash(body) != input.ContentHash {
			return ExecutionResult{}, fmt.Errorf("%w: %s", ErrLegacyContentIntegrity, input.ArtifactID)
		}
		payloads[input.ArtifactType] = append(payloads[input.ArtifactType], append([]byte(nil), body...))
	}
	started := executor.now()
	legacyCtx, cancel := context.WithTimeout(ctx, time.Duration(request.Node.Bounds.TimeoutMS)*time.Millisecond)
	outputs, usage, err := executor.runner.Run(legacyCtx, LegacyNodeInput{Request: request, Payloads: payloads})
	cancel()
	if err != nil {
		return ExecutionResult{}, err
	}
	if !usage.Measured {
		return ExecutionResult{}, ErrLegacyUsageMissing
	}
	parents := make([]writingstore.ArtifactRef, len(request.Inputs))
	inputHashes := make([]string, len(request.Inputs))
	for index, input := range request.Inputs {
		parents[index] = writingstore.ArtifactRef{ArtifactID: input.ArtifactID, Version: input.Version}
		inputHashes[index] = input.ContentHash
	}
	result := ExecutionResult{Artifacts: make([]OutputArtifactDraft, 0, len(outputs)), StartedAt: started,
		Usage: ExecutionUsage{CostUSD: usage.CostUSD, InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens}}
	for _, output := range outputs {
		ref, hash, err := executor.content.Stage(ctx, request.IdempotencyKey+":"+output.OutputKey, output.MediaType, output.Body)
		if err != nil {
			return ExecutionResult{}, err
		}
		if hash != contentHash(output.Body) {
			return ExecutionResult{}, ErrLegacyContentIntegrity
		}
		provenance := output.Provenance
		if provenance == nil {
			provenance = map[string]any{}
		}
		sources := output.SourceRefs
		if sources == nil {
			sources = []string{}
		}
		result.Artifacts = append(result.Artifacts, OutputArtifactDraft{OutputKey: output.OutputKey,
			ArtifactType: output.ArtifactType, ContentHash: hash, MediaType: output.MediaType,
			ContentRef: ref, Parents: append([]writingstore.ArtifactRef(nil), parents...),
			Producer: request.Node.Capability, CapabilityVersion: request.Node.CapabilityVersion,
			InputHashes: append([]string(nil), inputHashes...), ModelRef: output.ModelRef,
			PromptTemplateRef: output.PromptTemplateRef, Provenance: provenance, SourceRefs: sources})
	}
	result.CompletedAt = executor.now()
	result.Usage.DurationMS = result.CompletedAt.Sub(started).Milliseconds()
	return executor.NormalizeResult(request, result)
}

func adapterFamilyForExecutor(executorID string) AdapterFamily {
	switch {
	case strings.HasPrefix(executorID, "editorial."):
		return AdapterFamilyEditorial
	case strings.HasPrefix(executorID, "harness."):
		return AdapterFamilyHarness
	default:
		return AdapterFamilyEngine
	}
}

type EngineStepRunner struct {
	StepFactory func() engine.Step
	Emitter     engine.EventEmitter
	Seed        engine.CompatibilityInput
	Usage       func(*engine.ExecutionContext) (LegacyUsage, error)
}

func NewEngineStepExecutorAdapter(descriptor ExecutorDescriptor, capabilityID, capabilityVersion string, required []writingplan.Permission, content ContentGateway, runner EngineStepRunner) (*LegacyExecutor, error) {
	return NewLegacyExecutorAdapter(AdapterFamilyEngine, descriptor, capabilityID, capabilityVersion, required, content, runner)
}

// sortedPayloadTypes fixes the map iteration order so prompt assembly and
// shadow comparisons stay deterministic across runs.
func sortedPayloadTypes(payloads map[writingplan.ArtifactType][][]byte) []writingplan.ArtifactType {
	types := make([]writingplan.ArtifactType, 0, len(payloads))
	for artifactType := range payloads {
		types = append(types, artifactType)
	}
	sort.Slice(types, func(i, j int) bool { return types[i] < types[j] })
	return types
}

// GovernedStepEmitter is the only emitter engine steps may receive under the
// governed runtime. It is observer-only by construction: no session writes,
// no persistence, no terminal events. Legacy emitters are rejected outright.
type GovernedStepEmitter struct{}

func NewGovernedStepEmitter() *GovernedStepEmitter { return &GovernedStepEmitter{} }

func (*GovernedStepEmitter) StepStart(engine.StepName, int)                  {}
func (*GovernedStepEmitter) StepComplete(engine.StepName, interface{}, int64) {}
func (*GovernedStepEmitter) StreamDelta(string)                              {}
func (*GovernedStepEmitter) StreamReset()                                    {}
func (*GovernedStepEmitter) ReasoningDelta(string)                           {}
func (*GovernedStepEmitter) ArticleTitle(string)                             {}
func (*GovernedStepEmitter) StreamDone(string)                               {}
func (*GovernedStepEmitter) AwaitInput(engine.StepName, interface{}, []string, int, int) {}
func (*GovernedStepEmitter) Paused(engine.StepName, interface{})             {}
func (*GovernedStepEmitter) PausedWithReason(engine.StepName, interface{}, string) {}
func (*GovernedStepEmitter) Resumed(engine.StepName)                         {}
func (*GovernedStepEmitter) Error(string, string, engine.StepName)           {}
func (*GovernedStepEmitter) Completed(string, string, interface{}, interface{}) {}
func (*GovernedStepEmitter) Cancelled()                                      {}
func (*GovernedStepEmitter) Compaction(int, int, string, uint64, string)     {}

func (runner EngineStepRunner) Run(ctx context.Context, input LegacyNodeInput) ([]LegacyPayload, LegacyUsage, error) {
	if runner.StepFactory == nil {
		return nil, LegacyUsage{}, ErrRuntimeNotReady
	}
	var emitter engine.EventEmitter = NewGovernedStepEmitter()
	if runner.Emitter != nil {
		governed, ok := runner.Emitter.(*GovernedStepEmitter)
		if !ok {
			return nil, LegacyUsage{}, runtimeError(CodeLegacyWriteViolation, RetryNever,
				"governed engine steps accept only the observer-only governed emitter", ErrLegacyEmitterUnsafe)
		}
		emitter = governed
	}
	execCtx := engine.NewCompatibilityExecutionContext(runner.Seed)
	execCtx.TraceID = input.Request.IdempotencyKey
	for _, artifactType := range sortedPayloadTypes(input.Payloads) {
		for _, payload := range input.Payloads[artifactType] {
			switch artifactType {
			case "contract":
				execCtx.UserInput = string(payload)
			case "materials":
				execCtx.UserMaterials = append(execCtx.UserMaterials, string(payload))
			case "source_pack":
				var wrapper struct {
					Results []engine.SearchResult `json:"results"`
				}
				if json.Unmarshal(payload, &wrapper) == nil {
					execCtx.SearchResults = append(execCtx.SearchResults, wrapper.Results...)
				}
			case "outline":
				var outline engine.OutlineData
				if err := json.Unmarshal(payload, &outline); err != nil {
					return nil, LegacyUsage{}, err
				}
				execCtx.Outline = &outline
			case "full_draft":
				execCtx.Article = string(payload)
			}
		}
	}
	step := runner.StepFactory()
	if step == nil {
		return nil, LegacyUsage{}, ErrRuntimeNotReady
	}
	if err := step.Execute(ctx, execCtx, emitter); err != nil {
		return nil, LegacyUsage{}, err
	}
	legacy := editorial.CollectLegacyPayloads(execCtx)
	outputs := make([]LegacyPayload, 0, len(input.Request.Node.OutputArtifactTypes))
	for _, required := range input.Request.Node.OutputArtifactTypes {
		for _, payload := range legacy {
			mapped, ok := mapLegacyArtifact(payload.Type)
			if !ok || mapped != required {
				continue
			}
			outputs = append(outputs, LegacyPayload{OutputKey: string(mapped), ArtifactType: mapped,
				MediaType: payload.MediaType, Body: []byte(payload.Content), SourceRefs: payload.SourceRefs,
				Provenance: map[string]any{"adapter": "engine_step", "legacy_producer": payload.ProducedBy}})
			break
		}
	}
	if len(outputs) != len(input.Request.Node.OutputArtifactTypes) {
		return nil, LegacyUsage{}, ErrLegacyOutputMissing
	}
	if runner.Usage == nil {
		return nil, LegacyUsage{}, ErrLegacyUsageMissing
	}
	usage, err := runner.Usage(execCtx)
	if err != nil {
		return nil, LegacyUsage{}, err
	}
	return outputs, usage, nil
}

func mapLegacyArtifact(kind editorial.ArtifactType) (writingplan.ArtifactType, bool) {
	switch kind {
	case editorial.ArtifactSourcePack:
		return "source_pack", true
	case editorial.ArtifactOutline:
		return "outline", true
	case editorial.ArtifactDraft:
		return "full_draft", true
	case editorial.ArtifactReviewReport:
		return "quality_report", true
	default:
		return "", false
	}
}

// NewHarnessExecutorAdapter intentionally fails closed. Harness.Run persists
// conversation/session history and emits terminal status, so wrapping it would
// create a second authority beside the governed runtime.
func NewHarnessExecutorAdapter() (Executor, error) {
	return nil, runtimeError(CodeLegacyWriteViolation, RetryNever, "Harness.Run still owns session and terminal writes", ErrLegacyHarnessUnsafe)
}

// NewEditorialDAGExecutorAdapter intentionally fails closed. The pure
// EditorialRoleNodeRunner is testable offline, but DAGExecutor still owns task,
// artifact, decision, and event persistence.
func NewEditorialDAGExecutorAdapter() (Executor, error) {
	return nil, runtimeError(CodeLegacyWriteViolation, RetryNever, "DAGExecutor still owns authoritative editorial writes", ErrLegacyDAGUnsafe)
}

type EditorialRoleInvoker interface {
	Run(context.Context, editorial.RoleRunConfig) (*editorial.RoleRunResult, error)
}

// EditorialRoleNodeRunner reuses only RoleAgentRunner's returned value. It
// never invokes Research/Writing/ReviewAgentExecutor, DAGExecutor, or Store.
type EditorialRoleNodeRunner struct {
	Invoker EditorialRoleInvoker
	Config  *editorial.AgentConfig
	Seed    engine.CompatibilityInput
	Usage   func(*editorial.RoleRunResult) (LegacyUsage, error)
}

func NewEditorialRoleExecutorAdapter(descriptor ExecutorDescriptor, capabilityID, capabilityVersion string, required []writingplan.Permission, content ContentGateway, runner EditorialRoleNodeRunner) (*LegacyExecutor, error) {
	return NewLegacyExecutorAdapter(AdapterFamilyEditorial, descriptor, capabilityID, capabilityVersion, required, content, runner)
}

func (runner EditorialRoleNodeRunner) Run(ctx context.Context, input LegacyNodeInput) ([]LegacyPayload, LegacyUsage, error) {
	if runner.Invoker == nil || runner.Config == nil || runner.Usage == nil {
		return nil, LegacyUsage{}, ErrRuntimeNotReady
	}
	execCtx := engine.NewCompatibilityExecutionContext(runner.Seed)
	execCtx.TraceID = input.Request.IdempotencyKey
	task := &editorial.Task{ID: input.Request.NodeID, Title: input.Request.Node.Capability,
		Description: execCtx.UserInput, OwnerID: runner.Seed.UserID, TokenBudget: runner.Seed.MaxTokens,
		StyleSlug: runner.Seed.StyleSlug, CreatedBy: "writingruntime"}
	agentContext := editorial.NewAgentContext(editorial.AgentRole(runner.Config.Role), task.ID, task.OwnerID)
	var upstream strings.Builder
	for _, artifactType := range sortedPayloadTypes(input.Payloads) {
		for index, body := range input.Payloads[artifactType] {
			legacyType, ok := governedToEditorialArtifact(artifactType)
			if ok {
				agentContext.AddInputArtifact(editorial.Artifact{ID: fmt.Sprintf("%s-%d", artifactType, index),
					TaskID: task.ID, Type: legacyType, Version: 1, Content: string(body),
					Status: editorial.ArtifactStatusSubmitted, ProducedBy: "writingruntime"})
			}
			upstream.WriteString("\n[" + string(artifactType) + "]\n" + string(body))
		}
	}
	result, err := runner.Invoker.Run(ctx, editorial.RoleRunConfig{AgentConfig: runner.Config,
		Task: task, AgentContext: agentContext, ExecutionContext: execCtx,
		RoleSystemPromptExtra: upstream.String()})
	if err != nil {
		return nil, LegacyUsage{}, err
	}
	usage, err := runner.Usage(result)
	if err != nil {
		return nil, LegacyUsage{}, err
	}
	outputs := make([]LegacyPayload, 0, len(input.Request.Node.OutputArtifactTypes))
	for _, artifactType := range input.Request.Node.OutputArtifactTypes {
		payload, ok := editorialRoleOutput(artifactType, result)
		if !ok {
			return nil, LegacyUsage{}, fmt.Errorf("%w: %s", ErrLegacyOutputMissing, artifactType)
		}
		outputs = append(outputs, payload)
	}
	return outputs, usage, nil
}

func governedToEditorialArtifact(kind writingplan.ArtifactType) (editorial.ArtifactType, bool) {
	switch kind {
	case "source_pack":
		return editorial.ArtifactSourcePack, true
	case "outline":
		return editorial.ArtifactOutline, true
	case "full_draft":
		return editorial.ArtifactDraft, true
	case "quality_report":
		return editorial.ArtifactReviewReport, true
	default:
		return "", false
	}
}

func editorialRoleOutput(kind writingplan.ArtifactType, result *editorial.RoleRunResult) (LegacyPayload, bool) {
	base := LegacyPayload{OutputKey: string(kind), ArtifactType: kind, Provenance: map[string]any{"adapter": "editorial_role"}, SourceRefs: []string{}}
	switch kind {
	case "source_pack":
		body, err := json.Marshal(map[string]any{"results": result.SearchResults, "count": len(result.SearchResults)})
		if err != nil {
			return LegacyPayload{}, false
		}
		base.MediaType = "application/json"
		base.Body = body
		for _, source := range result.SearchResults {
			if source.URL != "" {
				base.SourceRefs = append(base.SourceRefs, source.URL)
			}
		}
	case "brief", "full_draft", "quality_report", "revision_set":
		if strings.TrimSpace(result.Output) == "" {
			return LegacyPayload{}, false
		}
		base.MediaType = "text/markdown"
		base.Body = []byte(result.Output)
	default:
		return LegacyPayload{}, false
	}
	return base, true
}

// HarnessCoreRequest contains only immutable governed inputs. It intentionally
// has no WritingSession, SessionStore, terminal status callback, or canonical
// article handle, so an implementation cannot become a second authority.
type HarnessCoreRequest struct {
	Identity            ExecutionIdentity
	Capability          string
	Artifacts           map[writingplan.ArtifactType][][]byte
	Permissions         []writingplan.Permission
	MaxItems            int
	MaxCostUSD          float64
	TimeoutMS           int64
	OutputArtifactTypes []writingplan.ArtifactType
}

type HarnessCoreResult struct {
	Outputs []LegacyPayload
	Usage   LegacyUsage
}

type HarnessCoreInvoker interface {
	RunCore(context.Context, HarnessCoreRequest) (HarnessCoreResult, error)
}

// HarnessCoreNodeRunner is the governed seam for the Harness tool loop. The
// existing Harness.Run remains unavailable because it loads/saves sessions and
// owns terminal article state.
type HarnessCoreNodeRunner struct {
	Invoker HarnessCoreInvoker
}

func (runner HarnessCoreNodeRunner) Run(ctx context.Context, input LegacyNodeInput) ([]LegacyPayload, LegacyUsage, error) {
	if runner.Invoker == nil {
		return nil, LegacyUsage{}, ErrRuntimeNotReady
	}
	payloads := make(map[writingplan.ArtifactType][][]byte, len(input.Payloads))
	for artifactType, values := range input.Payloads {
		payloads[artifactType] = make([][]byte, len(values))
		for index, value := range values {
			payloads[artifactType][index] = append([]byte(nil), value...)
		}
	}
	result, err := runner.Invoker.RunCore(ctx, HarnessCoreRequest{Identity: input.Request.Identity(),
		Capability: input.Request.Node.Capability, Artifacts: payloads,
		Permissions: append([]writingplan.Permission(nil), input.Request.Permissions...),
		MaxItems:    input.Request.Node.Bounds.MaxItems, MaxCostUSD: input.Request.Node.Bounds.MaxCostUSD,
		TimeoutMS:           input.Request.Node.Bounds.TimeoutMS,
		OutputArtifactTypes: append([]writingplan.ArtifactType(nil), input.Request.Node.OutputArtifactTypes...)})
	if err != nil {
		return nil, LegacyUsage{}, err
	}
	if !result.Usage.Measured {
		return nil, LegacyUsage{}, ErrLegacyUsageMissing
	}
	declared := make(map[writingplan.ArtifactType]bool, len(input.Request.Node.OutputArtifactTypes))
	for _, artifactType := range input.Request.Node.OutputArtifactTypes {
		declared[artifactType] = true
	}
	seen := make(map[writingplan.ArtifactType]bool, len(result.Outputs))
	for index := range result.Outputs {
		output := &result.Outputs[index]
		if !declared[output.ArtifactType] || seen[output.ArtifactType] || len(output.Body) == 0 {
			return nil, LegacyUsage{}, fmt.Errorf("%w: harness core output %q", ErrLegacyOutputMissing, output.ArtifactType)
		}
		seen[output.ArtifactType] = true
		if output.Provenance == nil {
			output.Provenance = map[string]any{}
		}
		output.Provenance["adapter"] = "harness_core"
		if output.SourceRefs == nil {
			output.SourceRefs = []string{}
		}
	}
	for artifactType := range declared {
		if !seen[artifactType] {
			return nil, LegacyUsage{}, fmt.Errorf("%w: harness core missing %q", ErrLegacyOutputMissing, artifactType)
		}
	}
	return result.Outputs, result.Usage, nil
}

func NewHarnessCoreExecutorAdapter(descriptor ExecutorDescriptor, capabilityID, capabilityVersion string, required []writingplan.Permission, content ContentGateway, invoker HarnessCoreInvoker) (*LegacyExecutor, error) {
	return NewLegacyExecutorAdapter(AdapterFamilyHarness, descriptor, capabilityID, capabilityVersion, required, content, HarnessCoreNodeRunner{Invoker: invoker})
}

type AgentHarnessCore interface {
	RunCore(context.Context, *engine.ExecutionContext, *agent.WritingSession) (agent.HarnessCoreOutput, error)
}

// AgentHarnessCoreBridge adapts the real Harness tool loop after RunCore has
// removed session persistence and terminal event side effects.
type AgentHarnessCoreBridge struct {
	Core  AgentHarnessCore
	Seed  engine.CompatibilityInput
	Usage func(agent.HarnessCoreOutput) (LegacyUsage, error)
}

func (bridge AgentHarnessCoreBridge) RunCore(ctx context.Context, request HarnessCoreRequest) (HarnessCoreResult, error) {
	if bridge.Core == nil || bridge.Usage == nil {
		return HarnessCoreResult{}, ErrRuntimeNotReady
	}
	execCtx := engine.NewCompatibilityExecutionContext(bridge.Seed)
	execCtx.TraceID = request.Identity.IdempotencyKey
	session := agent.NewWritingSession("", bridge.Seed.UserID, bridge.Seed.StyleSlug)
	for _, artifactType := range sortedPayloadTypes(request.Artifacts) {
		for _, value := range request.Artifacts[artifactType] {
			switch artifactType {
			case "contract":
				execCtx.UserInput = string(value)
			case "materials":
				execCtx.UserMaterials = append(execCtx.UserMaterials, string(value))
				session.UserMaterials = append(session.UserMaterials, string(value))
			case "outline":
				var outline engine.OutlineData
				if err := json.Unmarshal(value, &outline); err != nil {
					return HarnessCoreResult{}, err
				}
				execCtx.Outline, session.Outline = &outline, &outline
			case "full_draft":
				execCtx.Article, session.CurrentArticle = string(value), string(value)
			case "source_pack":
				var wrapper struct {
					Results []engine.SearchResult `json:"results"`
				}
				if json.Unmarshal(value, &wrapper) == nil {
					execCtx.SearchResults = append(execCtx.SearchResults, wrapper.Results...)
					session.SearchResults = append(session.SearchResults, wrapper.Results...)
				}
			}
		}
	}
	output, err := bridge.Core.RunCore(ctx, execCtx, session)
	if err != nil {
		return HarnessCoreResult{}, err
	}
	usage, err := bridge.Usage(output)
	if err != nil {
		return HarnessCoreResult{}, err
	}
	result := HarnessCoreResult{Usage: usage, Outputs: make([]LegacyPayload, 0, len(request.OutputArtifactTypes))}
	for _, artifactType := range request.OutputArtifactTypes {
		payload := LegacyPayload{OutputKey: string(artifactType), ArtifactType: artifactType,
			Provenance: map[string]any{"adapter": "harness_core", "core": "agent.Harness.RunCore"}, SourceRefs: []string{}}
		switch artifactType {
		case "full_draft":
			if strings.TrimSpace(output.Article) == "" {
				return HarnessCoreResult{}, fmt.Errorf("%w: harness core article", ErrLegacyOutputMissing)
			}
			payload.MediaType, payload.Body = "text/markdown", []byte(output.Article)
		case "source_pack":
			body, marshalErr := json.Marshal(map[string]any{"results": output.SearchResults})
			if marshalErr != nil {
				return HarnessCoreResult{}, marshalErr
			}
			payload.MediaType, payload.Body = "application/json", body
			for _, source := range output.SearchResults {
				if source.URL != "" {
					payload.SourceRefs = append(payload.SourceRefs, source.URL)
				}
			}
		case "quality_report", "review_report":
			if output.ReviewResult == nil {
				return HarnessCoreResult{}, fmt.Errorf("%w: harness core review", ErrLegacyOutputMissing)
			}
			body, marshalErr := json.Marshal(output.ReviewResult)
			if marshalErr != nil {
				return HarnessCoreResult{}, marshalErr
			}
			payload.MediaType, payload.Body = "application/json", body
		default:
			return HarnessCoreResult{}, fmt.Errorf("%w: harness core type %q", ErrLegacyOutputMissing, artifactType)
		}
		result.Outputs = append(result.Outputs, payload)
	}
	return result, nil
}

func NewAgentHarnessCoreExecutorAdapter(descriptor ExecutorDescriptor, capabilityID, capabilityVersion string, required []writingplan.Permission, content ContentGateway, bridge AgentHarnessCoreBridge) (*LegacyExecutor, error) {
	return NewHarnessCoreExecutorAdapter(descriptor, capabilityID, capabilityVersion, required, content, bridge)
}

func contentHash(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

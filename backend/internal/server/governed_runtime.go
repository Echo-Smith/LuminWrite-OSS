package server

// Governed runtime composition root (Task13). This is the single place where
// the governed writing runtime is assembled from production dependencies:
// writingstore persistence, the real Engine/Editorial/Harness runners, the
// canonical content gateway, durable rollout evidence, and the durable
// shadow content sink. Adapters are registered only when their dependencies
// are ready, every candidate stays offline behind a default-off rollout
// policy, and a runtime that cannot be composed reports
// WRITING_RUNTIME_NOT_READY before any run row is created.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/agent"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/editorial"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine/steps"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingruntime"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

const (
	// governedCostPerTokenUSD is the declared per-token cost ceiling used to
	// derive governed execution cost from measured tokens until per-model
	// pricing is wired into the runtime.
	governedCostPerTokenUSD = 2e-6

	governedDraftCapability = "core.draft.generate"

	// Each dispatched run is detached from its creating HTTP request, but it
	// is never unbounded. Node deadlines remain the tighter limit; this ceiling
	// also bounds persistence/recovery work between nodes.
	governedDispatchTimeout = 2 * time.Hour

	// Terminalization uses a fresh bounded context after Execute returns; the
	// execution context may already be cancelled or timed out.
	governedTerminalizeTimeout = 15 * time.Second
)

var errWritingRuntimeNotReady = errors.New("governed writing runtime is not ready")

type GovernedRuntimeDeps struct {
	Store   *writingstore.Store
	DB      *sql.DB
	LLM     *tools.LLMClient
	Search  *tools.SearchClient
	KB      tools.KnowledgeSearcher
	Profile *profile.StyleProfile
	Metrics *MetricsRegistry
	Edition string
}

type GovernedRuntime struct {
	store        *writingstore.Store
	orchestrator *writingruntime.Orchestrator
	registry     *writingruntime.ExecutorRegistry
	evidence     *writingruntime.StoreRolloutEvidence
	shadow       writingruntime.ShadowContentSink
	canonical    *writingruntime.StoreContentGateway
	policies     map[string]*writingruntime.MutableRolloutPolicyProvider
	capabilities *writingplan.CapabilityRegistry
	metrics      *MetricsRegistry

	ready       atomic.Bool
	blockedCode string
	dispatches  atomic.Int64
}

// RecoverPending schedules runs whose durable state proves they were already
// dispatchable before this process started. Approval and pause gates remain
// authoritative because the store query excludes those states.
func (runtime *GovernedRuntime) RecoverPending(ctx context.Context) (int, error) {
	if !runtime.Ready() || runtime.store == nil {
		return 0, errWritingRuntimeNotReady
	}
	runIDs, err := runtime.store.ListRecoverableRunIDs(ctx, 1000)
	if err != nil {
		return 0, err
	}
	dispatched := 0
	for _, runID := range runIDs {
		if err := runtime.Dispatch(runID); err != nil {
			return dispatched, err
		}
		dispatched++
	}
	return dispatched, nil
}

// Ready reports whether governed runs may be created and dispatched.
func (runtime *GovernedRuntime) Ready() bool {
	return runtime != nil && runtime.ready.Load()
}

// BlockedCode returns the stable reason the runtime is not ready.
func (runtime *GovernedRuntime) BlockedCode() string {
	if runtime == nil || runtime.ready.Load() {
		return ""
	}
	return runtime.blockedCode
}

// DispatchCount reports how many governed runs were scheduled on this process.
func (runtime *GovernedRuntime) DispatchCount() int64 {
	if runtime == nil {
		return 0
	}
	return runtime.dispatches.Load()
}

// Dispatch schedules one governed run on a server-owned context. The caller
// is responsible for recording a stable transition rejection when Dispatch
// fails, so no run is ever left in an unexplained planned state.
func (runtime *GovernedRuntime) Dispatch(runID string) error {
	if runtime == nil || !runtime.ready.Load() || runtime.orchestrator == nil {
		return errWritingRuntimeNotReady
	}
	runtime.dispatches.Add(1)
	go func() {
		// The run outlives the HTTP request that created it, while the process
		// ceiling prevents database or recovery work from becoming immortal.
		ctx, cancel := context.WithTimeout(context.Background(), governedDispatchTimeout)
		defer cancel()
		if _, err := runtime.orchestrator.Execute(ctx, runID); err != nil {
			causes := []string{err.Error()}
			for unwrapped := errors.Unwrap(err); unwrapped != nil; unwrapped = errors.Unwrap(unwrapped) {
				causes = append(causes, unwrapped.Error())
			}
			slog.Warn("governed run finished with error", "run_id", runID,
				"error", err, "cause_chain", strings.Join(causes, " ← "))
			if !expectedDispatchOutcome(err) {
				terminalCtx, terminalCancel := context.WithTimeout(context.Background(), governedTerminalizeTimeout)
				terminalErr := runtime.orchestrator.FailDispatch(terminalCtx, runID, writingruntime.ErrorCodeOf(err))
				terminalCancel()
				if terminalErr != nil {
					slog.Error("governed run could not be terminalized after dispatch failure", "run_id", runID,
						"error", terminalErr, "original_error_code", writingruntime.ErrorCodeOf(err))
				}
			}
		}
	}()
	return nil
}

func expectedDispatchOutcome(err error) bool {
	return errors.Is(err, writingruntime.ErrApprovalRequired) || errors.Is(err, writingruntime.ErrRunPaused) ||
		errors.Is(err, writingruntime.ErrRunCancelled) || errors.Is(err, writingruntime.ErrRunReplanning) ||
		errors.Is(err, writingruntime.ErrHumanRecoveryRequired)
}

// Pause/Resume/Cancel adapt the orchestrator to the writingRunController
// interface used by the writing API.
func (runtime *GovernedRuntime) Pause(ctx context.Context, runID, commandID string, actor writingstore.Actor) error {
	if !runtime.Ready() {
		return errWritingRuntimeNotReady
	}
	return runtime.orchestrator.Pause(ctx, runID, commandID, actor)
}

func (runtime *GovernedRuntime) Resume(ctx context.Context, runID, commandID string, actor writingstore.Actor) error {
	if !runtime.Ready() {
		return errWritingRuntimeNotReady
	}
	if _, err := runtime.orchestrator.Resume(ctx, runID, commandID, actor); err != nil {
		return err
	}
	return nil
}

func (runtime *GovernedRuntime) Cancel(ctx context.Context, runID, commandID string, actor writingstore.Actor) error {
	if !runtime.Ready() {
		return errWritingRuntimeNotReady
	}
	return runtime.orchestrator.Cancel(ctx, runID, commandID, actor)
}

// ComposeGovernedRuntime builds the governed runtime. The capability registry
// is the same instance the writing API compiles plans against: registered
// adapters flip their capabilities to available, which is what makes real
// runs dispatchable. Any missing hard dependency keeps the whole runtime
// fail-closed.
func ComposeGovernedRuntime(deps GovernedRuntimeDeps, capabilities *writingplan.CapabilityRegistry) *GovernedRuntime {
	runtime := &GovernedRuntime{blockedCode: "WRITING_RUNTIME_NOT_READY",
		policies: map[string]*writingruntime.MutableRolloutPolicyProvider{}, metrics: deps.Metrics}
	if deps.Store == nil || deps.DB == nil || deps.LLM == nil || capabilities == nil {
		slog.Warn("governed runtime not composed: store, database, model client, or capability registry missing", "edition", deps.Edition)
		return runtime
	}
	evidence, err := writingruntime.NewStoreRolloutEvidence(deps.Store)
	if err != nil {
		slog.Warn("governed runtime not composed: durable evidence unavailable", "error", err)
		return runtime
	}
	shadow, err := writingruntime.NewStoreShadowContent(deps.Store, 0)
	if err != nil {
		slog.Warn("governed runtime not composed: durable shadow sink unavailable", "error", err)
		return runtime
	}
	canonical, err := writingruntime.NewStoreContentGateway(deps.Store)
	if err != nil {
		slog.Warn("governed runtime not composed: canonical content gateway unavailable", "error", err)
		return runtime
	}
	registry := writingruntime.NewExecutorRegistry()
	materialAdapter, err := writingruntime.NewMaterialAdapter(governedMaterialSource{db: deps.DB}, canonical)
	if err != nil {
		slog.Warn("governed runtime not composed: material adapter unavailable", "error", err)
		return runtime
	}
	initial, err := writingruntime.NewCompositeInitialArtifactProvider(
		&governedContractProvider{store: deps.Store, gateway: canonical},
		&governedMaterialInitialProvider{provider: &writingruntime.MaterialArtifactProvider{Adapter: materialAdapter,
			Selection: governedMaterialSelection{store: deps.Store, db: deps.DB}}},
	)
	if err != nil {
		slog.Warn("governed runtime not composed: initial artifact providers unavailable", "error", err)
		return runtime
	}
	orchestrator := &writingruntime.Orchestrator{Store: deps.Store, Capabilities: capabilities,
		Executors: registry, State: writingruntime.NewStateMachine(governedTransitionStore{store: deps.Store}),
		Checkpoints: writingruntime.PersistentCheckpointRepository{Store: deps.Store, Trace: governedRuntimeTrace()},
		Initial:     initial, Materials: deps.Store, Telemetry: governedRuntimeTelemetry(deps.Metrics)}

	register := func(capabilityID string, family writingruntime.AdapterFamily, runner writingruntime.LegacyNodeRunner) error {
		return runtime.registerGovernedCapability(capabilities, registry, canonical, shadow, evidence,
			capabilityID, family, runner, family, runner)
	}
	mustRegister := func(capabilityID string, family writingruntime.AdapterFamily, runner writingruntime.LegacyNodeRunner) bool {
		if err := register(capabilityID, family, runner); err != nil {
			slog.Warn("governed adapter not registered", "capability", capabilityID, "error", err)
			return false
		}
		return true
	}
	// Engine family: real pipeline prefixes, one governed node each. The
	// prefix ends with the node's own artifact producer.
	if deps.Search != nil && deps.Search.HasSources() {
		if !mustRegister("core.retrieval.search", writingruntime.AdapterFamilyEngine,
			engineStepRunner(&governedSequentialStep{name: "governed_search", steps: []engine.Step{
				steps.NewQueryPlanStep(deps.LLM), steps.NewSearchStep(deps.LLM, deps.Search),
			}}, materialAdapter)) {
			return runtime
		}
	}
	if !mustRegister("core.outline.generate", writingruntime.AdapterFamilyEngine,
		engineStepRunner(&governedSequentialStep{name: "governed_outline", steps: []engine.Step{
			&governedConfirmTimeoutStep{timeout: time.Millisecond},
			steps.NewIntentStep(deps.LLM), &governedModeOverrideStep{mode: "guided"},
			steps.NewQueryPlanStep(deps.LLM), steps.NewOutlineStepWithProfile(deps.LLM, deps.Profile),
		}}, materialAdapter)) {
		return runtime
	}
	if !mustRegister("core.validation.quality", writingruntime.AdapterFamilyEngine,
		engineStepRunner(&governedSequentialStep{name: "governed_quality", steps: []engine.Step{
			steps.NewPostReviewStepWithSearchAndJiaozhen(deps.LLM, nil, deps.Profile, deps.Search, nil).RequireSuccess(),
		}}, materialAdapter)) {
		return runtime
	}
	// Engine baseline + Harness shadow candidate for the draft capability:
	// the harness tool loop is the first governed cross-implementation
	// candidate and stays offline until an explicit shadow policy is installed.
	if err := runtime.registerGovernedCapability(capabilities, registry, canonical, shadow, evidence,
		governedDraftCapability, writingruntime.AdapterFamilyEngine,
		engineStepRunner(&governedSequentialStep{name: "governed_draft", steps: []engine.Step{
			steps.NewWriteStepWithKB(deps.LLM, deps.Profile, deps.Search, deps.KB),
		}}, materialAdapter), writingruntime.AdapterFamilyHarness,
		writingruntime.HarnessCoreNodeRunner{Invoker: &writingruntime.AgentHarnessCoreBridge{
			Core:      agent.NewHarness(deps.LLM, deps.Search, deps.KB, deps.Profile, nil, nil),
			Materials: materialAdapter,
			Usage: func(output agent.HarnessCoreOutput) (writingruntime.LegacyUsage, error) {
				return writingruntime.LegacyUsage{Measured: true, InputTokens: int64(output.TotalTokens)}, nil
			},
		}}); err != nil {
		slog.Warn("governed draft rollout not registered", "error", err)
		return runtime
	}
	// Editorial family: the finalize role produces the revision set. It gets
	// its own registered tool registry so governed execution never borrows the
	// legacy DAG's mutable runtime state or dereferences a nil registry.
	editorialTools := editorial.NewEditorialToolRegistry()
	editorial.RegisterBuiltinTools(editorialTools)
	if !mustRegister("core.document.finalize", writingruntime.AdapterFamilyEditorial,
		&writingruntime.EditorialRoleNodeRunner{
			Invoker: editorial.NewRoleAgentRunner(deps.LLM, deps.Search, deps.KB, deps.Profile, nil, editorialTools),
			Config:  &editorial.AgentConfig{ID: "governed-finalize", Name: "Governed Finalizer", Role: string(editorial.RoleWriting)},
			Usage: func(result *editorial.RoleRunResult) (writingruntime.LegacyUsage, error) {
				return writingruntime.LegacyUsage{Measured: true, InputTokens: int64(result.Tokens)}, nil
			},
		}) {
		return runtime
	}

	runtime.orchestrator = orchestrator
	runtime.store = deps.Store
	runtime.registry = registry
	runtime.evidence = evidence
	runtime.shadow = shadow
	runtime.canonical = canonical
	runtime.capabilities = capabilities
	runtime.ready.Store(true)
	slog.Info("governed runtime composed", "edition", deps.Edition,
		"capabilities", len(runtime.policies))
	return runtime
}

// registerGovernedCapability wires one capability into the governed runtime:
// an authoritative baseline adapter (canonical gateway) paired with a
// shadow-isolated candidate behind a default-off rollout policy. The
// executor registry only accepts rollout executors, so offline adapters can
// never bypass the rollout boundary. candidateRunner may equal baselineRunner
// (isolation-equivalent comparisons) or a different real implementation
// (cross-implementation shadow comparisons, e.g. Harness vs Engine draft).
func (runtime *GovernedRuntime) registerGovernedCapability(capabilities *writingplan.CapabilityRegistry,
	registry *writingruntime.ExecutorRegistry, canonical *writingruntime.StoreContentGateway,
	shadow writingruntime.ShadowContentSink, evidence *writingruntime.StoreRolloutEvidence,
	capabilityID string, baselineFamily writingruntime.AdapterFamily, baselineRunner writingruntime.LegacyNodeRunner,
	candidateFamily writingruntime.AdapterFamily, candidateRunner writingruntime.LegacyNodeRunner) error {
	manifest, ok := capabilities.Get(capabilityID)
	if !ok {
		return fmt.Errorf("unknown governed capability %s", capabilityID)
	}
	baseline, err := writingruntime.NewLegacyExecutorAdapter(baselineFamily,
		writingruntime.ExecutorDescriptor{ExecutorID: manifest.Executor, Version: "governed-1",
			SupportedNodeKinds: manifest.SupportedNodeKinds},
		capabilityID, manifest.Version, manifest.Permissions, canonical, baselineRunner)
	if err != nil {
		return err
	}
	candidateID := manifest.Executor + ".shadow_candidate"
	nodePolicy := governedOffPolicy(candidateID, candidateFamily, capabilityID, manifest.Version)
	nodeGateway, err := writingruntime.NewShadowContentGateway(canonical, shadow, nodePolicy)
	if err != nil {
		return err
	}
	candidate, err := writingruntime.NewShadowIsolatedExecutorAdapter(candidateFamily,
		writingruntime.ExecutorDescriptor{ExecutorID: candidateID, Version: "governed-1",
			SupportedNodeKinds: manifest.SupportedNodeKinds},
		capabilityID, manifest.Version, manifest.Permissions, nodeGateway, candidateRunner)
	if err != nil {
		return err
	}
	nodeProvider, err := writingruntime.NewMutableRolloutPolicyProvider(nodePolicy)
	if err != nil {
		return err
	}
	rollout, err := writingruntime.NewShadowRolloutExecutor(baseline, candidate, nodeProvider, evidence,
		governedRuntimeTelemetry(runtime.metrics))
	if err != nil {
		return err
	}
	if err := registry.Register(rollout); err != nil {
		return err
	}
	acceptedInputs := append(append([]writingplan.ArtifactType(nil), manifest.InputTypes...), manifest.OptionalInputTypes...)
	if err := capabilities.RegisterExecutor(writingplan.ExecutorBinding{ID: manifest.Executor,
		AcceptedInputTypes:  acceptedInputs,
		ProducedOutputTypes: append([]writingplan.ArtifactType(nil), manifest.OutputTypes...),
		Dispatch: func(context.Context, writingplan.ExecutionRequest) (writingplan.ExecutionResult, error) {
			return writingplan.ExecutionResult{}, nil
		}}); err != nil {
		return err
	}
	if err := capabilities.Enable(capabilityID); err != nil {
		return err
	}
	runtime.policies[capabilityID] = nodeProvider
	return nil
}

func governedOffPolicy(executorID string, family writingruntime.AdapterFamily, capabilityID, capabilityVersion string) writingruntime.AdapterRolloutPolicy {
	policy := writingruntime.AdapterRolloutPolicy{PolicyVersion: 1, Mode: writingruntime.RolloutOff,
		ExecutorID: executorID, Family: family, CapabilityID: capabilityID, CapabilityVersion: capabilityVersion,
		AllowSubjects: []string{}, Reason: "task13_default_off"}
	policy, _ = policy.WithComputedHash()
	return policy
}

// governedSequentialStep runs a pipeline prefix as one governed node. The
// real pipeline steps have ordering dependencies (IntentStep classifies the
// task, QueryPlanStep produces the WritingTask that OutlineStep renders), so
// capability nodes execute the prefix that ends with their own artifact.
type governedSequentialStep struct {
	name  engine.StepName
	steps []engine.Step
}

func (step *governedSequentialStep) Name() engine.StepName { return step.name }
func (step *governedSequentialStep) CanPause() bool        { return false }
func (step *governedSequentialStep) Execute(ctx context.Context, execCtx *engine.ExecutionContext, emitter engine.EventEmitter) error {
	for _, subStep := range step.steps {
		if skipper, ok := subStep.(engine.Skipper); ok && skipper.ShouldSkip(execCtx) {
			continue
		}
		if err := subStep.Execute(ctx, execCtx, emitter); err != nil {
			return err
		}
	}
	return nil
}

// governedModeOverrideStep switches the compatibility context's mode between
// pipeline prefixes: IntentStep classifies deterministically in "writing"
// mode, while OutlineStep only renders outlines in "guided" mode.
type governedModeOverrideStep struct {
	mode string
}

func (step *governedModeOverrideStep) Name() engine.StepName {
	return engine.StepName("governed_mode_" + step.mode)
}
func (step *governedModeOverrideStep) CanPause() bool { return false }
func (step *governedModeOverrideStep) Execute(_ context.Context, execCtx *engine.ExecutionContext, _ engine.EventEmitter) error {
	execCtx.Mode = step.mode
	return nil
}

// governedConfirmTimeoutStep caps the interactive outline confirmation wait:
// governed batch runs auto-confirm the outline (the no-op emitter never
// delivers confirmations), so the wait must release immediately instead of
// burning the node deadline.
type governedConfirmTimeoutStep struct {
	timeout time.Duration
}

func (step *governedConfirmTimeoutStep) Name() engine.StepName {
	return engine.StepName("governed_confirm_timeout")
}
func (step *governedConfirmTimeoutStep) CanPause() bool { return false }
func (step *governedConfirmTimeoutStep) Execute(_ context.Context, execCtx *engine.ExecutionContext, _ engine.EventEmitter) error {
	execCtx.ConfirmTimeout = step.timeout
	return nil
}

func engineStepRunner(step engine.Step, materials writingruntime.MaterialSnapshotResolver) writingruntime.LegacyNodeRunner {
	return writingruntime.EngineStepRunner{
		// Governed nodes are writing nodes: the compatibility context pins the
		// mode so intent classification never downgrades a contract-driven run
		// into chat.
		Seed:        engine.CompatibilityInput{Mode: "writing"},
		Materials:   materials,
		StepFactory: func() engine.Step { return step },
		Usage: func(execCtx *engine.ExecutionContext) (writingruntime.LegacyUsage, error) {
			if execCtx == nil || execCtx.TotalTokens <= 0 {
				return writingruntime.LegacyUsage{}, writingruntime.ErrLegacyUsageMissing
			}
			return writingruntime.LegacyUsage{Measured: true, InputTokens: int64(execCtx.TotalTokens),
				CostUSD: float64(execCtx.TotalTokens) * governedCostPerTokenUSD}, nil
		}}
}

// ── initial artifact providers ───────────────────────────

// governedContractProvider stages the confirmed contract as the contract
// input artifact. The staged body is the contract's canonical JSON; its
// integrity is independently enforced by the sealed ContractRef in the plan.
type governedContractProvider struct {
	store   *writingstore.Store
	gateway writingruntime.ContentGateway
}

func (provider *governedContractProvider) InitialArtifacts(ctx context.Context, run writingstore.RuntimeRun, _ writingstore.PlanRecord) ([]writingruntime.InputArtifact, error) {
	record, err := provider.store.GetContract(ctx, run.ContractID, run.ContractVersion)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(record.Contract)
	if err != nil {
		return nil, err
	}
	ref, hash, err := provider.gateway.Stage(ctx, "contract:"+run.ContractID+":"+fmt.Sprint(run.ContractVersion), "application/json", body)
	if err != nil {
		return nil, err
	}
	return []writingruntime.InputArtifact{{ArtifactID: writingstore.StableID("art_", run.RunID, "contract"),
		Version: run.ContractVersion, ArtifactType: "contract", ContentHash: hash,
		MediaType: "application/json", ContentRef: ref}}, nil
}

// governedMaterialSelection resolves a run's material descriptors from the
// owning document's material_refs metadata. Empty selections are valid: the
// material provider is skipped entirely for runs without materials.
type governedMaterialSelection struct {
	store *writingstore.Store
	db    *sql.DB
}

func (selection governedMaterialSelection) MaterialsForRun(ctx context.Context, run writingstore.RuntimeRun, _ writingstore.PlanRecord) (writingruntime.MaterialSnapshotRequest, error) {
	document, err := selection.store.GetDocument(ctx, run.DocumentID)
	if err != nil {
		return writingruntime.MaterialSnapshotRequest{}, err
	}
	descriptors, err := governedMaterialDescriptors(ctx, selection.db, document.Metadata, document.OwnerUserID)
	if err != nil {
		return writingruntime.MaterialSnapshotRequest{}, err
	}
	return writingruntime.MaterialSnapshotRequest{RunID: run.RunID, OwnerID: document.OwnerUserID,
		ConflictHandling: "ask_user", Materials: descriptors}, nil
}

// governedMaterialInitialProvider always emits the typed materials manifest.
// An empty selection becomes an immutable empty manifest, which keeps the
// plan's initial artifact contract uniform without inventing source content.
type governedMaterialInitialProvider struct {
	provider *writingruntime.MaterialArtifactProvider
}

func (provider *governedMaterialInitialProvider) InitialArtifacts(ctx context.Context, run writingstore.RuntimeRun, plan writingstore.PlanRecord) ([]writingruntime.InputArtifact, error) {
	return provider.provider.InitialArtifacts(ctx, run, plan)
}

// governedMaterialSource loads user material bodies from the local
// user_materials table (PostgreSQL-backed material storage).
type governedMaterialSource struct {
	db *sql.DB
}

func (source governedMaterialSource) LoadMaterial(ctx context.Context, ownerID string, descriptor writingruntime.MaterialDescriptor) (writingruntime.MaterialContent, error) {
	if source.db == nil {
		return writingruntime.MaterialContent{}, fmt.Errorf("%w: material database unavailable", writingruntime.ErrLegacyContentIntegrity)
	}
	var preview, sourceURL, documentID sql.NullString
	err := source.db.QueryRowContext(ctx, `
		SELECT content_preview, source_url, doc_id::text
		FROM user_materials WHERE id::text=$1 AND user_id=$2 AND status='active'
	`, descriptor.MaterialID, ownerID).Scan(&preview, &sourceURL, &documentID)
	if errors.Is(err, sql.ErrNoRows) {
		return writingruntime.MaterialContent{}, fmt.Errorf("%w: material %s", writingstore.ErrNotFound, descriptor.MaterialID)
	}
	if err != nil {
		return writingruntime.MaterialContent{}, err
	}
	refs := []string{}
	if descriptor.SourceRef != "" {
		refs = append(refs, descriptor.SourceRef)
	}
	if sourceURL.Valid && sourceURL.String != "" {
		refs = append(refs, sourceURL.String)
	}
	body := ""
	if documentID.Valid && documentID.String != "" {
		var chunks sql.NullString
		if err := source.db.QueryRowContext(ctx, `
			SELECT string_agg(kc.content, E'\n\n' ORDER BY kc.chunk_index)
			FROM knowledge_chunks kc
			JOIN knowledge_base kb ON kb.id=kc.doc_id
			WHERE kc.doc_id::text=$1 AND (kb.user_id=$2 OR kc.user_id=$2)
		`, documentID.String, ownerID).Scan(&chunks); err != nil {
			return writingruntime.MaterialContent{}, fmt.Errorf("load material chunks: %w", err)
		}
		body = strings.TrimSpace(chunks.String)
		if body != "" {
			refs = append(refs, "kb://"+documentID.String)
		}
	}
	if body == "" {
		body = strings.TrimSpace(preview.String)
	}
	if body == "" {
		return writingruntime.MaterialContent{}, fmt.Errorf("%w: material %s has no persisted body", writingruntime.ErrLegacyContentIntegrity, descriptor.MaterialID)
	}
	return writingruntime.MaterialContent{Body: []byte(body), SourceRefs: refs}, nil
}

func governedRuntimeTrace() writingstore.TraceContext {
	return writingstore.TraceContext{Provenance: map[string]any{"runtime": "governed", "composition": "task13"},
		SourceRefs: []string{}, Actor: writingstore.Actor{Type: writingstore.ActorPolicy, ID: "server.governed_runtime"}}
}

// governedMaterialDescriptors decodes the material references the frontend
// attached to the document (identities only; bodies are loaded at snapshot
// time through governedMaterialSource).
func governedMaterialDescriptors(ctx context.Context, db *sql.DB, metadata map[string]any, ownerID string) ([]writingruntime.MaterialDescriptor, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: material database unavailable", writingruntime.ErrRuntimeNotReady)
	}
	raw, ok := metadata["material_refs"].([]any)
	if !ok {
		return []writingruntime.MaterialDescriptor{}, nil
	}
	descriptors := make([]writingruntime.MaterialDescriptor, 0, len(raw))
	for _, entry := range raw {
		item, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		materialID, _ := item["material_id"].(string)
		if strings.TrimSpace(materialID) == "" {
			continue
		}
		var title, sourceKind string
		var sourceURL, documentID sql.NullString
		var updatedAt time.Time
		err := db.QueryRowContext(ctx, `
			SELECT title, source_type, source_url, doc_id::text, updated_at
			FROM user_materials WHERE id::text=$1 AND user_id=$2 AND status='active'
		`, materialID, ownerID).Scan(&title, &sourceKind, &sourceURL, &documentID, &updatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: material %s is unavailable to this document owner", writingstore.ErrNotFound, materialID)
		}
		if err != nil {
			return nil, fmt.Errorf("load material descriptor: %w", err)
		}
		sourceRef := materialID
		if sourceURL.Valid && sourceURL.String != "" {
			sourceRef = sourceURL.String
		} else if documentID.Valid && documentID.String != "" {
			sourceRef = "kb://" + documentID.String
		}
		descriptors = append(descriptors, writingruntime.MaterialDescriptor{MaterialID: materialID,
			OwnerID: ownerID, Title: title, SourceKind: writingruntime.MaterialSourceKind(sourceKind),
			SourceRef: sourceRef, MediaType: "text/plain", UpdatedAt: updatedAt.UTC()})
	}
	return descriptors, nil
}

// governedRuntimeTelemetry bridges runtime metrics onto the bounded
// Prometheus series. Label values come from the runtime metric's bounded
// fields only — never identity, URLs, or content.
func governedRuntimeTelemetry(metrics *MetricsRegistry) writingruntime.RuntimeTelemetry {
	return writingruntime.RuntimeTelemetryFunc(func(_ context.Context, metric writingruntime.RuntimeMetric) {
		if metrics == nil {
			return
		}
		family, executor, capability := string(metric.Family), metric.ExecutorID, metric.Capability
		mode, lane, status := string(metric.Mode), string(metric.Lane), metric.Status
		code := string(metric.ErrorCode)
		switch metric.Kind {
		case writingruntime.MetricRouteDecision:
			metrics.GovernedRouteDecisionsTotal.Inc(family, executor, capability, mode, lane, status, metric.Reason, code)
		case writingruntime.MetricExecution:
			metrics.GovernedExecutionsTotal.Inc(family, executor, capability, mode, lane, status, code)
			if metric.InputTokens > 0 {
				metrics.GovernedUsageTokensTotal.Add(metric.InputTokens, family, executor, capability, lane, "input")
			}
			if metric.OutputTokens > 0 {
				metrics.GovernedUsageTokensTotal.Add(metric.OutputTokens, family, executor, capability, lane, "output")
			}
			if metric.CostUSD > 0 {
				metrics.GovernedCostMicroUSDTotal.Add(int64(metric.CostUSD*1e6), family, executor, capability, lane)
			}
			if metric.DurationMS > 0 {
				metrics.GovernedExecutionDuration.Observe(time.Duration(metric.DurationMS)*time.Millisecond, family, executor, capability, lane, status)
			}
		case writingruntime.MetricMaterialIntegrity:
			metrics.GovernedMaterialIntegrityTotal.Inc(string(metric.MaterialSourceKind), status, code)
		case writingruntime.MetricShadowComparison:
			metrics.GovernedShadowComparisonsTotal.Inc(family, executor, capability, status, code)
		case writingruntime.MetricCanonicalCommit:
			metrics.GovernedCommitsTotal.Inc(executor, capability, status, code)
		case writingruntime.MetricAuthorityViolation:
			metrics.GovernedAuthorityViolations.Inc(family, executor, capability, code)
		}
	})
}

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
	orchestrator *writingruntime.Orchestrator
	registry     *writingruntime.ExecutorRegistry
	evidence     *writingruntime.StoreRolloutEvidence
	shadow       writingruntime.ShadowContentSink
	canonical    *writingruntime.StoreContentGateway
	policies     map[string]*writingruntime.MutableRolloutPolicyProvider
	metrics      *MetricsRegistry

	ready       atomic.Bool
	blockedCode string
	dispatches  atomic.Int64
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
		// The run outlives the HTTP request that created it: node timeouts and
		// the plan budget bound execution, not the request lifecycle.
		ctx := context.WithoutCancel(context.Background())
		if _, err := runtime.orchestrator.Execute(ctx, runID); err != nil {
			slog.Warn("governed run finished with error", "run_id", runID, "error", err)
		}
	}()
	return nil
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
	if deps.Store == nil || deps.LLM == nil {
		slog.Warn("governed runtime not composed: store or model client missing", "edition", deps.Edition)
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
			Selection: governedMaterialSelection{store: deps.Store}}},
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
		return runtime.registerBaselineAdapter(capabilities, registry, canonical, capabilityID, family, runner)
	}
	mustRegister := func(capabilityID string, family writingruntime.AdapterFamily, runner writingruntime.LegacyNodeRunner) {
		if err := register(capabilityID, family, runner); err != nil {
			slog.Warn("governed adapter not registered", "capability", capabilityID, "error", err)
		}
	}
	// Engine family: real pipeline steps, one governed node each.
	mustRegister("core.retrieval.search", writingruntime.AdapterFamilyEngine,
		engineStepRunner(steps.NewSearchStep(deps.LLM, deps.Search)))
	mustRegister("core.outline.generate", writingruntime.AdapterFamilyEngine,
		engineStepRunner(steps.NewOutlineStepWithProfile(deps.LLM, deps.Profile)))
	mustRegister("core.validation.quality", writingruntime.AdapterFamilyEngine,
		engineStepRunner(steps.NewPostReviewStepWithSearchAndJiaozhen(deps.LLM, nil, deps.Profile, deps.Search, nil)))
	// Engine baseline + Harness shadow candidate for the draft capability:
	// the harness tool loop is the first governed candidate and stays
	// offline until an explicit shadow policy is installed.
	draftRunner := engineStepRunner(steps.NewWriteStepWithKB(deps.LLM, deps.Profile, deps.Search, deps.KB))
	harnessRunner := writingruntime.HarnessCoreNodeRunner{Invoker: &writingruntime.AgentHarnessCoreBridge{
		Core: agent.NewHarness(deps.LLM, deps.Search, deps.KB, deps.Profile, nil, nil),
		Usage: func(output agent.HarnessCoreOutput) (writingruntime.LegacyUsage, error) {
			return writingruntime.LegacyUsage{Measured: true, InputTokens: int64(output.TotalTokens)}, nil
		},
	}}
	if err := runtime.registerRolloutCapability(capabilities, registry, canonical, shadow, evidence,
		governedDraftCapability, writingruntime.AdapterFamilyEngine, draftRunner,
		writingruntime.AdapterFamilyHarness, harnessRunner); err != nil {
		slog.Warn("governed draft rollout not registered", "error", err)
	}
	// Editorial family: the finalize role produces the revision set.
	mustRegister("core.document.finalize", writingruntime.AdapterFamilyEditorial,
		&writingruntime.EditorialRoleNodeRunner{
			Invoker: editorial.NewRoleAgentRunner(deps.LLM, deps.Search, deps.KB, deps.Profile, nil, nil),
			Config:  &editorial.AgentConfig{ID: "governed-finalize", Name: "Governed Finalizer", Role: string(editorial.RoleWriting)},
			Usage: func(result *editorial.RoleRunResult) (writingruntime.LegacyUsage, error) {
				return writingruntime.LegacyUsage{Measured: true, InputTokens: int64(result.Tokens)}, nil
			},
		})

	runtime.orchestrator = orchestrator
	runtime.registry = registry
	runtime.evidence = evidence
	runtime.shadow = shadow
	runtime.canonical = canonical
	runtime.ready.Store(true)
	slog.Info("governed runtime composed", "edition", deps.Edition,
		"capabilities", len(runtime.policies)+4)
	return runtime
}

func (runtime *GovernedRuntime) registerBaselineAdapter(capabilities *writingplan.CapabilityRegistry,
	registry *writingruntime.ExecutorRegistry, canonical *writingruntime.StoreContentGateway,
	capabilityID string, family writingruntime.AdapterFamily, runner writingruntime.LegacyNodeRunner) error {
	manifest, ok := capabilities.Get(capabilityID)
	if !ok {
		return fmt.Errorf("unknown governed capability %s", capabilityID)
	}
	if err := capabilities.RegisterExecutor(writingplan.ExecutorBinding{ID: manifest.Executor,
		AcceptedInputTypes: append([]writingplan.ArtifactType(nil), manifest.InputTypes...),
		ProducedOutputTypes: append([]writingplan.ArtifactType(nil), manifest.OutputTypes...),
		Dispatch: func(context.Context, writingplan.ExecutionRequest) (writingplan.ExecutionResult, error) {
			return writingplan.ExecutionResult{}, nil
		}}); err != nil {
		return err
	}
	available := manifest
	available.Available = true
	if err := capabilities.Register(available); err != nil {
		return err
	}
	baseline, err := writingruntime.NewLegacyExecutorAdapter(family,
		writingruntime.ExecutorDescriptor{ExecutorID: manifest.Executor, Version: "governed-1",
			SupportedNodeKinds: manifest.SupportedNodeKinds},
		capabilityID, manifest.Version, manifest.Permissions, canonical, runner)
	if err != nil {
		return err
	}
	return registry.Register(baseline)
}

// registerRolloutCapability pairs an authoritative baseline adapter with a
// shadow-isolated candidate behind a default-off rollout policy.
func (runtime *GovernedRuntime) registerRolloutCapability(capabilities *writingplan.CapabilityRegistry,
	registry *writingruntime.ExecutorRegistry, canonical *writingruntime.StoreContentGateway,
	shadow writingruntime.ShadowContentSink, evidence *writingruntime.StoreRolloutEvidence,
	capabilityID string, baselineFamily writingruntime.AdapterFamily, baselineRunner writingruntime.LegacyNodeRunner,
	candidateFamily writingruntime.AdapterFamily, candidateRunner writingruntime.LegacyNodeRunner) error {
	manifest, ok := capabilities.Get(capabilityID)
	if !ok {
		return fmt.Errorf("unknown governed capability %s", capabilityID)
	}
	bindingID := manifest.Executor
	if err := capabilities.RegisterExecutor(writingplan.ExecutorBinding{ID: bindingID,
		AcceptedInputTypes: append([]writingplan.ArtifactType(nil), manifest.InputTypes...),
		ProducedOutputTypes: append([]writingplan.ArtifactType(nil), manifest.OutputTypes...),
		Dispatch: func(context.Context, writingplan.ExecutionRequest) (writingplan.ExecutionResult, error) {
			return writingplan.ExecutionResult{}, nil
		}}); err != nil {
		return err
	}
	available := manifest
	available.Available = true
	if err := capabilities.Register(available); err != nil {
		return err
	}
	baseline, err := writingruntime.NewLegacyExecutorAdapter(baselineFamily,
		writingruntime.ExecutorDescriptor{ExecutorID: bindingID, Version: "governed-1",
			SupportedNodeKinds: manifest.SupportedNodeKinds},
		capabilityID, manifest.Version, manifest.Permissions, canonical, baselineRunner)
	if err != nil {
		return err
	}
	candidateID := bindingID + ".harness_candidate"
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
	rollout, err := writingruntime.NewRolloutExecutor(baseline, candidate, nodeProvider, evidence,
		governedRuntimeTelemetry(runtime.metrics))
	if err != nil {
		return err
	}
	if err := registry.Register(rollout); err != nil {
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

func engineStepRunner(step engine.Step) writingruntime.LegacyNodeRunner {
	return writingruntime.EngineStepRunner{StepFactory: func() engine.Step { return step },
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
}

func (selection governedMaterialSelection) MaterialsForRun(ctx context.Context, run writingstore.RuntimeRun, _ writingstore.PlanRecord) (writingruntime.MaterialSnapshotRequest, error) {
	document, err := selection.store.GetDocument(ctx, run.DocumentID)
	if err != nil {
		return writingruntime.MaterialSnapshotRequest{}, err
	}
	descriptors := governedMaterialDescriptors(document.Metadata, document.OwnerUserID)
	return writingruntime.MaterialSnapshotRequest{RunID: run.RunID, OwnerID: document.OwnerUserID,
		ConflictHandling: "ask_user", Materials: descriptors}, nil
}

// governedMaterialInitialProvider skips material snapshotting for runs that
// selected no materials instead of failing the whole run.
type governedMaterialInitialProvider struct {
	provider *writingruntime.MaterialArtifactProvider
}

func (provider *governedMaterialInitialProvider) InitialArtifacts(ctx context.Context, run writingstore.RuntimeRun, plan writingstore.PlanRecord) ([]writingruntime.InputArtifact, error) {
	request, err := provider.provider.Selection.MaterialsForRun(ctx, run, plan)
	if err != nil {
		return nil, err
	}
	if len(request.Materials) == 0 {
		return nil, nil
	}
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
	var preview, sourceURL sql.NullString
	err := source.db.QueryRowContext(ctx, `
		SELECT content_preview, source_url FROM user_materials WHERE id::text=$1 AND user_id=$2
	`, descriptor.MaterialID, ownerID).Scan(&preview, &sourceURL)
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
	body := strings.TrimSpace(preview.String)
	if body == "" {
		body = descriptor.Title
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
func governedMaterialDescriptors(metadata map[string]any, ownerID string) []writingruntime.MaterialDescriptor {
	raw, ok := metadata["material_refs"].([]any)
	if !ok {
		return []writingruntime.MaterialDescriptor{}
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
		title, _ := item["title"].(string)
		sourceKind, _ := item["source_kind"].(string)
		sourceRef, _ := item["source_ref"].(string)
		if sourceRef == "" {
			sourceRef = materialID
		}
		if sourceKind == "" {
			sourceKind = string(writingruntime.MaterialSourceText)
		}
		descriptors = append(descriptors, writingruntime.MaterialDescriptor{MaterialID: materialID,
			OwnerID: ownerID, Title: title, SourceKind: writingruntime.MaterialSourceKind(sourceKind),
			SourceRef: sourceRef, MediaType: "text/plain", UpdatedAt: time.Now().UTC()})
	}
	return descriptors
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

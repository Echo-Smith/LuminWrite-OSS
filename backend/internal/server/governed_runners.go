// Governed run execution wiring (V3.0 M1.4, docs/24): this file mounts the
// governed writing runtime on the live writing API and supplies the
// per-capability engine-step runners the composition needs. Mounting is
// mode-gated (WRITING_RUNTIME_MODE, default off = zero behavior change);
// mode=shadow wires real engine steps behind the shadow rollout so every
// governed run produces baseline artifacts plus durable shadow evidence.
//
// Trigger discipline: ApproveRun's store transition is the sole authority
// that moves a run to running; the executor trigger fires at most once per
// run per process and executes in the background — an execution failure is
// recorded on the run by the orchestrator itself, never swallowed here.
package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine/steps"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/services"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingruntime"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// governedRunnerFactory builds the engine-step runners for one capability at
// dispatch time. The StepEnv's resolved profile and the server's engine
// clients (LLM/search/sensitive/jiaozhen/KB) decide the concrete step.
type governedRunnerFactory struct {
	server *Server
	// llm resolves the LLM client at dispatch time (the server may rebuild
	// clients after DB-backed configuration loads).
	llm func() *tools.LLMClient
	// kb adapts the local knowledge base; nil disables KB-scoped drafting.
	kb tools.KnowledgeSearcher
}

// newGovernedRunnerFactory captures the server's engine clients lazily so
// runner construction never pins stale clients.
func newGovernedRunnerFactory(server *Server, kb tools.KnowledgeSearcher) *governedRunnerFactory {
	return &governedRunnerFactory{server: server, llm: func() *tools.LLMClient { return server.llm }, kb: kb}
}

// RunnerFor returns the LegacyNodeRunner for one governed capability, or nil
// when the server cannot serve it (the composition then fails closed).
func (factory *governedRunnerFactory) RunnerFor(capability string) writingruntime.LegacyNodeRunner {
	server := factory.server
	switch capability {
	case "core.draft.generate":
		return writingruntime.EngineStepRunner{
			Styles: writingruntime.LoaderStyleResolver{Loader: server.profiles},
			StepFactory: func(env writingruntime.StepEnv) (engine.Step, error) {
				return steps.NewWriteStepWithKB(factory.llm(), env.Profile, server.search, factory.kb), nil
			},
			Usage: engineUsage,
		}
	case "core.validation.quality":
		return writingruntime.EngineStepRunner{
			Styles: writingruntime.LoaderStyleResolver{Loader: server.profiles},
			StepFactory: func(env writingruntime.StepEnv) (engine.Step, error) {
				return server.newGovernedPostReviewStep(factory.llm(), env.Profile), nil
			},
			Usage: engineUsage,
		}
	case "core.validation.evidence", "core.validation.fact":
		// Validators degrade honestly when no LLM is wired (docs/22 D2); a
		// missing factory LLM is exactly that deployment shape.
		return &writingruntime.ValidatorRunner{LLM: factory.llm()}
	case "core.outline.generate":
		return writingruntime.EngineStepRunner{
			Styles: writingruntime.LoaderStyleResolver{Loader: server.profiles},
			StepFactory: func(env writingruntime.StepEnv) (engine.Step, error) {
				return governedOutlineStep{inner: steps.NewOutlineStepWithProfile(factory.llm(), env.Profile)}, nil
			},
			Usage: engineUsage,
		}
	case "core.retrieval.search":
		// The research capability runs the legacy multi-step search chain as
		// one composite governed step (M0b-2b SequentialGroup). Without an
		// embedding client the relevance step degrades to score-only mode,
		// mirroring the legacy pipeline's fallback.
		return writingruntime.EngineStepRunner{
			StepFactory: func(writingruntime.StepEnv) (engine.Step, error) {
				return engine.NewSequentialGroup("governed_research",
					steps.NewQueryPlanStep(factory.llm()),
					steps.NewSearchStep(factory.llm(), server.search),
					steps.NewRelevanceStepWithEmbedding(server.embedding),
					steps.NewCompressStep(factory.llm())), nil
			},
			Usage: engineUsage,
		}
	default:
		return nil
	}
}

// newGovernedPostReviewStep mirrors the legacy newPostReviewStepWithLLM
// assembly (sensitive check + web search + jiaozhen) for governed dispatch.
func (s *Server) newGovernedPostReviewStep(llm *tools.LLMClient, p *profile.StyleProfile) engine.Step {
	if s.sensitiveSvc != nil {
		return steps.NewPostReviewStepWithSearchAndJiaozhen(llm, &sensitiveCheckAdapter{svc: s.sensitiveSvc}, p, s.search, s.jiaozhen)
	}
	return steps.NewPostReviewStepWithSearchAndJiaozhen(llm, nil, p, s.search, s.jiaozhen)
}

// governedOutlineStep adapts the legacy OutlineStep to governed dispatch.
// The legacy step is guided-mode-only (it returns silently otherwise, which
// under the governed runtime reads as a missing output) and it awaits user
// confirmation after generating; governed runs run without an interactive
// confirm channel, so the wrapper sets guided mode and relies on the
// legacy confirm-timeout auto-confirm to accept the first outline.
type governedOutlineStep struct{ inner engine.Step }

func (step governedOutlineStep) Name() engine.StepName { return step.inner.Name() }
func (step governedOutlineStep) CanPause() bool        { return step.inner.CanPause() }

func (step governedOutlineStep) Execute(ctx context.Context, execCtx *engine.ExecutionContext, emitter engine.EventEmitter) error {
	if execCtx.Mode == "" {
		execCtx.Mode = "guided"
	}
	if execCtx.ConfirmTimeout == 0 {
		execCtx.ConfirmTimeout = time.Second
	}
	// The guided outline prompt reads WritingTask unconditionally (legacy
	// runs always had IntentStep ahead of it). Governed runs have no intent
	// step: derive the task from the contract artifact the orchestrator
	// staged as the node's contract input, falling back to the raw input.
	if execCtx.WritingTask == nil {
		task := &engine.WritingTask{Topic: strings.TrimSpace(execCtx.UserInput), WordLimit: 1500}
		if len(task.Topic) > 120 {
			task.Topic = task.Topic[:120]
		}
		if contract, err := writingkernel.DecodeWritingContractStrict([]byte(execCtx.UserInput)); err == nil {
			if topic := strings.TrimSpace(contract.Content.Topic); topic != "" {
				task.Topic = topic
			}
			if max := contract.Delivery.Length.Max; max > 0 {
				task.WordLimit = max
			}
		}
		execCtx.WritingTask = task
	}
	return step.inner.Execute(ctx, execCtx, emitter)
}

// engineUsage projects the ExecutionContext's token counter onto the
// governance usage record. Steps only maintain the aggregate, so the whole
// count rides the output side.
func engineUsage(execCtx *engine.ExecutionContext) (writingruntime.LegacyUsage, error) {
	if execCtx == nil {
		return writingruntime.LegacyUsage{}, nil
	}
	return writingruntime.LegacyUsage{Measured: true, OutputTokens: int64(execCtx.TotalTokens)}, nil
}

// governedCapabilitySpecs returns the wiring specs for the capabilities this
// server can execute.
func (s *Server) governedCapabilitySpecs(store *writingstore.Store, canonical writingruntime.ContentGateway) ([]governedCapabilitySpec, error) {
	factory := newGovernedRunnerFactory(s, s.governedKBSearcher())
	defaults := writingplan.DefaultCapabilityRegistry()
	specs := []governedCapabilitySpec{}
	for _, capability := range []string{"core.draft.generate", "core.outline.generate", "core.retrieval.search", "core.validation.quality", "core.validation.evidence", "core.validation.fact", "core.document.finalize"} {
		manifest, ok := defaults.Get(capability)
		if !ok {
			continue
		}
		runner := factory.RunnerFor(capability)
		if runner == nil {
			// The kernel finalize executor is always constructible; only the
			// factory-served capabilities can be missing.
			if capability != "core.document.finalize" {
				continue
			}
			runner = &writingruntime.FinalizeRunner{Store: store, Content: canonical}
		}
		specs = append(specs, governedCapabilitySpec{
			BindingID: "governed.baseline." + capability, CandidateID: "governed.candidate." + capability,
			CapabilityID: capability, CapabilityVersion: "1.0.0",
			Inputs:      append(append([]writingplan.ArtifactType(nil), manifest.InputTypes...), manifest.OptionalInputTypes...),
			Outputs:     append([]writingplan.ArtifactType(nil), manifest.OutputTypes...),
			Permissions: append([]writingplan.Permission(nil), manifest.Permissions...),
			Runner:      runner,
		})
	}
	return specs, nil
}

// governedKBSearcher adapts the local knowledge base when configured.
func (s *Server) governedKBSearcher() tools.KnowledgeSearcher {
	if s.kbMgr == nil {
		return nil
	}
	return s.kbSearcherAdapter()
}

// kbSearcherAdapter wraps the KB manager in the searcher adapter. Kept as a
// method so the construction site stays swappable (GraphRAG wiring etc.).
func (s *Server) kbSearcherAdapter() tools.KnowledgeSearcher {
	if s.kbSearch == nil {
		s.kbSearch = services.NewKbSearchAdapter(s.kbMgr)
	}
	return s.kbSearch
}

// mountGovernedRuntime assembles and mounts the governed runtime on the live
// writing API when the configured mode asks for it. mode=off leaves the
// writing API byte-identical to pre-V3.0.
func (s *Server) mountGovernedRuntime(store *writingstore.Store) {
	mode, _ := writingruntime.NormalizeRuntimeMode(s.cfg.WritingRuntime.Mode)
	if mode == writingruntime.RuntimeModeOff {
		return
	}
	canonical := writingruntime.WritingStoreContentGateway{Store: store}
	specs, err := s.governedCapabilitySpecs(store, canonical)
	if err != nil {
		// A wiring failure must not silently drop the governed runtime: log
		// loudly and stay unmounted (the API reports runtime unavailable).
		slog.Error("governed runtime: capability wiring failed", "error", err)
		return
	}
	deps := governedRuntimeDependencies{
		canonical:       canonical,
		sink:            s.governedRollout.shadow,
		evidence:        s.governedRollout.evidence,
		transitionStore: writingruntime.WritingStoreTransitionRecorder{Store: store},
		checkpoints: writingruntime.PersistentCheckpointRepository{Store: store,
			Trace: writingstore.TraceContext{Actor: writingstore.Actor{Type: writingstore.ActorSystem, ID: "writingruntime"},
				Provenance: map[string]any{}, SourceRefs: []string{}}},
		initial:   governedInitialProvider{store: store},
		materials: store,
		context:   writingruntime.StoreContextSource{Store: store},
	}
	runtime, err := newGovernedWritingRuntime(store, mode, deps, specs)
	if err != nil {
		slog.Error("governed runtime: composition refused", "mode", string(mode), "error", err)
		return
	}
	if runtime == nil {
		return
	}
	if api, ok := s.writingAPI.(*persistentWritingAPI); ok {
		api.controller = runtime.controller
		api.trigger = &governedRunTrigger{orchestrator: runtime.orchestrator}
		// CompilePlan/CreateRun validate against the same executable
		// registry the orchestrator dispatches through; the declared-only
		// default catalog would fail every plan compile as T4.
		api.capabilities = runtime.capabilities
		s.governedTrigger = api.trigger
	}
	slog.Info("governed writing runtime mounted", "mode", string(mode), "capabilities", len(specs))
}

// governedInitialProvider supplies the contract artifact from the run's
// stored contract so node 1 has its input from the canonical content store.
type governedInitialProvider struct{ store *writingstore.Store }

func (provider governedInitialProvider) InitialArtifacts(ctx context.Context, run writingstore.RuntimeRun, _ writingstore.PlanRecord) ([]writingruntime.InputArtifact, error) {
	contract, err := provider.store.GetContract(ctx, run.ContractID, run.ContractVersion)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(contract.Contract)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	hash := "sha256:" + hex.EncodeToString(sum[:])
	if err := provider.store.PutArtifactContent(ctx, hash, "application/json", body); err != nil {
		return nil, err
	}
	return []writingruntime.InputArtifact{{ArtifactID: "art_" + run.RunID + "_contract", Version: 1,
		ArtifactType: "contract", ContentHash: hash, MediaType: "application/json",
		ContentRef: "artifact://" + hash}}, nil
}

// governedRunTrigger executes approved runs exactly once per run per
// process. The store's approval transition is the authority; this trigger
// only turns the approved state into background execution.
type governedRunTrigger struct {
	orchestrator *writingruntime.Orchestrator

	mu      sync.Mutex
	started map[string]bool
}

// TriggerAfterApproval launches execution for an approved run unless this
// process already started it. Idempotent across handler retries and safe
// under concurrency.
func (trigger *governedRunTrigger) TriggerAfterApproval(runID string) {
	if trigger == nil || trigger.orchestrator == nil {
		return
	}
	trigger.mu.Lock()
	if trigger.started == nil {
		trigger.started = map[string]bool{}
	}
	if trigger.started[runID] {
		trigger.mu.Unlock()
		return
	}
	trigger.started[runID] = true
	trigger.mu.Unlock()
	go func() {
		outcome, err := trigger.orchestrator.Execute(context.Background(), runID)
		if err != nil {
			// The orchestrator has already recorded the failure on the run
			// (state transition + attempt ledger); this log is for operators.
			slog.Error("governed run execution failed", "run_id", runID,
				"state", string(outcome.State), "error_type", fmt.Sprintf("%T", err), "error", err)
		}
	}()
}

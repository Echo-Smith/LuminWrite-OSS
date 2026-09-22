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
	"os"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/scholar"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine/steps"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/services"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingtransport"
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
			Styles: governedStyleResolver{server: server},
			StepFactory: func(env writingruntime.StepEnv) (engine.Step, error) {
				return steps.NewWriteStepWithKB(factory.llm(), env.Profile, server.search, factory.kb), nil
			},
			Usage: engineUsage,
		}
	case "core.validation.quality":
		// T07 research quality gate: the mechanical half consumes the
		// citations validator's evidence_report blocker mapping and fails
		// closed on blockers before the inner post-review runs. Legacy
		// evidence reports (core.validation.evidence/fact) carry no research
		// blockers and pass through unchanged.
		return ResearchQualityGateAdapter{
			Inner: writingruntime.EngineStepRunner{
				Styles: governedStyleResolver{server: server},
				StepFactory: func(env writingruntime.StepEnv) (engine.Step, error) {
					return server.newGovernedPostReviewStep(factory.llm(), env.Profile), nil
				},
				Usage: engineUsage,
			}}
	case "core.validation.evidence", "core.validation.fact":
		// Validators degrade honestly when no LLM is wired (docs/22 D2); a
		// missing factory LLM is exactly that deployment shape.
		return &writingruntime.ValidatorRunner{LLM: factory.llm()}
	case "core.outline.generate":
		return writingruntime.EngineStepRunner{
			Styles: governedStyleResolver{server: server},
			StepFactory: func(env writingruntime.StepEnv) (engine.Step, error) {
				return governedOutlineStep{inner: steps.NewOutlineStepWithProfile(factory.llm(), env.Profile)}, nil
			},
			Usage: engineUsage,
		}
	case "core.retrieval.search", "core.retrieval.strict_search":
		// The research capability runs the legacy multi-step search chain as
		// one composite governed step (M0b-2b SequentialGroup). Without an
		// embedding client the relevance step degrades to score-only mode,
		// mirroring the legacy pipeline's fallback.
		return writingruntime.EngineStepRunner{
			StepFactory: func(writingruntime.StepEnv) (engine.Step, error) {
				return governedResearchStep{
					query:     steps.NewQueryPlanStep(factory.llm()),
					search:    steps.NewSearchStep(nil, server.search), // no LLM-generated search evidence
					relevance: steps.NewRelevanceStepWithEmbedding(server.embedding),
					compress:  steps.NewCompressStep(factory.llm()),
				}, nil
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
		return steps.NewPostReviewStepWithSearch(llm, &sensitiveCheckAdapter{svc: s.sensitiveSvc}, p, s.search)
	}
	return steps.NewPostReviewStepWithSearch(llm, nil, p, s.search)
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
	for _, capability := range []string{"core.draft.generate", "core.outline.generate", "core.retrieval.search", "core.retrieval.strict_search", "core.validation.quality", "core.validation.evidence", "core.validation.fact", "core.document.finalize"} {
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
	// T06 research_review executors (design.md §3): typed direct executors,
	// registered without the legacy adapter path. The scholar worker client
	// is wired only when a worker URL is configured; the draft generator is
	// wired only when a model client exists — otherwise the draft node
	// pauses with RESEARCH_UNAVAILABLE (the honest no-model deployment).
	specs = append(specs, s.governedResearchSpecs(store, canonical, defaults)...)
	return specs, nil
}

// governedResearchSpecs wires the research capabilities' direct executors.
// The worker client comes from the scholar worker configuration; when absent
// the discover/read nodes surface RESEARCH_UNAVAILABLE at dispatch instead of
// being silently skipped (the plan would otherwise compile into a dead end).
func (s *Server) governedResearchSpecs(store *writingstore.Store, canonical writingruntime.ContentGateway, defaults *writingplan.CapabilityRegistry) []governedCapabilitySpec {
	var discover writingruntime.Executor
	var read writingruntime.Executor
	// F5 user-material branch: the SAME policy-branching executors under the
	// material branch's own binding ids — the no-external manifests bind
	// these, so a plan compiled for a no-external contract can never resolve
	// the external bindings.
	var materialDiscover writingruntime.Executor
	var materialRead writingruntime.Executor
	// The scholar operations execute in-process (Go rewrite of the former
	// private-network Python worker). SCHOLAR_WORKER_URL keeps its role as
	// the research-path enablement signal and SCHOLAR_WORKER_TOKEN stays a
	// required configuration guard; PDF text extraction goes through the
	// docreader sidecar (the shared KB parsing surface).
	if workerURL := strings.TrimSpace(os.Getenv("SCHOLAR_WORKER_URL")); workerURL != "" {
		var scholarOpts []scholar.Option
		if docreaderAddr := strings.TrimSpace(os.Getenv("DOCREADER_ADDR")); docreaderAddr != "" {
			scholarOpts = append(scholarOpts, scholar.WithDocumentParser(scholar.NewDocreaderParser(docreaderAddr)))
		}
		scholarClient, scholarErr := scholar.NewClient(workerURL, strings.TrimSpace(os.Getenv("SCHOLAR_WORKER_TOKEN")), scholarOpts...)
		if scholarErr != nil {
			slog.Warn("governed runtime: scholar client invalid; research discover/read pause on dispatch", "error", scholarErr)
		} else {
			if executor, err := writingruntime.NewResearchDiscoverExecutor(scholarClient, canonical); err == nil {
				discover = executor
			} else {
				slog.Warn("governed runtime: research discover executor construction failed", "error", err)
			}
			// Executor-level budget guard (T09, F2): the wall-clock proactive
			// budget as a PURE ledger function — completed attempt durations +
			// persisted sub-task usage + in-flight attempt time vs the run's
			// plan budget MaxDurationMS (30-minute design default when
			// absent). No fire-once: the boundary state lives in the ledger.
			budget := NewWallClockResearchBudgetBoundary(store)
			if executor, err := writingruntime.NewResearchReadExecutor(writingruntime.ScholarParseRead{Client: scholarClient}, canonical, store, budget); err == nil {
				read = executor
			} else {
				slog.Warn("governed runtime: research read executor construction failed", "error", err)
			}
			if executor, err := writingruntime.NewResearchDiscoverMaterialExecutor(scholarClient, canonical); err == nil {
				materialDiscover = executor
			} else {
				slog.Warn("governed runtime: material discover executor construction failed", "error", err)
			}
			if executor, err := writingruntime.NewResearchReadMaterialExecutor(writingruntime.ScholarParseRead{Client: scholarClient}, canonical, store, budget); err == nil {
				materialRead = executor
			} else {
				slog.Warn("governed runtime: material read executor construction failed", "error", err)
			}
		}
	} else {
		discover = writingruntime.NewUnavailableResearchExecutor("engine.step.research_discover", "scholar worker is not configured")
		read = writingruntime.NewUnavailableResearchExecutor("engine.step.research_read", "scholar worker is not configured")
	}
	// Without a configured worker the material branch surfaces
	// RESEARCH_UNAVAILABLE exactly like the external one (honest
	// unavailability, no silent degrade).
	if materialDiscover == nil {
		materialDiscover = writingruntime.NewUnavailableResearchExecutor("engine.step.research_discover_materials", "scholar worker is not configured")
	}
	if materialRead == nil {
		materialRead = writingruntime.NewUnavailableResearchExecutor("engine.step.research_read_materials", "scholar worker is not configured")
	}
	// Outline: deterministic v1 assembly (no model call, no generator wired).
	outline, err := writingruntime.NewResearchOutlineExecutor(canonical, nil)
	if err != nil {
		slog.Warn("governed runtime: research outline executor construction failed", "error", err)
	}
	// Draft: the LLM generator over the server's existing model config; nil
	// LLM defers to an honest RESEARCH_UNAVAILABLE pause at dispatch. The
	// style resolver is optional: a non-empty run style_slug (user opt-in at
	// the research form) injects the global style as advisory prose guidance.
	draft, err := writingruntime.NewResearchDraftExecutor(canonical, writingruntime.LLMResearchDraftGenerator{LLM: factoryLLM(s)}, governedStyleResolver{server: s})
	if err != nil {
		slog.Warn("governed runtime: research draft executor construction failed", "error", err)
	}
	// Citations: the full deterministic T07 check. The run store is wired so
	// the validator resolves the confirmed contract (full_text_required
	// drives the scope-overclaim class) from the run artifacts.
	citations, err := writingruntime.NewResearchCitationValidator(canonical, store)
	if err != nil {
		slog.Warn("governed runtime: research citation validator construction failed", "error", err)
	}
	// Fact: the lightweight semantic review runs when a model client exists;
	// a nil reviewer degrades honestly (no-model deployment). The reviewer is
	// resolved lazily through the factory closure so DB-backed config reloads
	// are picked up per dispatch.
	fact, err := writingruntime.NewResearchFactValidator(canonical, writingruntime.LLMResearchFactReviewer{LLM: factoryLLM(s)})
	if err != nil {
		slog.Warn("governed runtime: research fact validator construction failed", "error", err)
	}
	direct := map[string]writingruntime.Executor{
		writingplan.CapabilityResearchDiscover:  discover,
		writingplan.CapabilityResearchRead:      read,
		writingplan.CapabilityResearchOutline:   outline,
		writingplan.CapabilityResearchDraft:     draft,
		writingplan.CapabilityResearchCitations: citations,
		writingplan.CapabilityResearchFact:      fact,
		// F5 user-material branch: the material executors carry their own
		// binding ids, so a plan compiled for a no-external-research contract
		// can never resolve the external bindings.
		writingplan.CapabilityResearchDiscoverMaterial: materialDiscover,
		writingplan.CapabilityResearchReadMaterial:     materialRead,
	}
	specs := []governedCapabilitySpec{}
	for _, capability := range []string{writingplan.CapabilityResearchDiscover, writingplan.CapabilityResearchRead,
		writingplan.CapabilityResearchOutline, writingplan.CapabilityResearchDraft,
		writingplan.CapabilityResearchCitations, writingplan.CapabilityResearchFact,
		writingplan.CapabilityResearchDiscoverMaterial, writingplan.CapabilityResearchReadMaterial} {
		executor := direct[capability]
		if executor == nil {
			continue
		}
		manifest, ok := defaults.Get(capability)
		if !ok {
			continue
		}
		specs = append(specs, governedCapabilitySpec{
			BindingID: executor.Descriptor().ExecutorID, CandidateID: "governed.candidate." + capability,
			CapabilityID: capability, CapabilityVersion: "1.0.0",
			Inputs:      append(append([]writingplan.ArtifactType(nil), manifest.InputTypes...), manifest.OptionalInputTypes...),
			Outputs:     append([]writingplan.ArtifactType(nil), manifest.OutputTypes...),
			Permissions: append([]writingplan.Permission(nil), manifest.Permissions...),
			Direct:      executor,
		})
	}
	return specs
}

// ResearchQualityGateAdapter is the server-side runner wrapper: the runtime's
// mechanical research gate (evidence_report blocker consumption) wraps the
// legacy model post-review step, so research blockers short-circuit the node
// and the model review only ever runs on a mechanically clean report.
type ResearchQualityGateAdapter struct {
	Inner writingruntime.LegacyNodeRunner
}

// Run implements LegacyNodeRunner.
func (adapter ResearchQualityGateAdapter) Run(ctx context.Context, input writingruntime.LegacyNodeInput) ([]writingruntime.LegacyPayload, writingruntime.LegacyUsage, error) {
	return writingruntime.ResearchQualityGateRunner{Inner: adapter.Inner}.Run(ctx, input)
}

// factoryLLM resolves the server's LLM client lazily (the server may rebuild
// clients after DB-backed configuration loads).
func factoryLLM(s *Server) *tools.LLMClient { return s.llm }

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
		initial:   governedInitialProvider{store: store, server: s},
		materials: store,
		context:   writingruntime.StoreContextSource{Store: store},
		telemetry: s.metrics,
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
		api.controller = governedRunController{orchestrator: runtime.orchestrator, store: store}
		api.trigger = &governedRunTrigger{orchestrator: runtime.orchestrator, store: store}
		// CompilePlan/CreateRun validate against the same executable
		// registry the orchestrator dispatches through; the declared-only
		// default catalog would fail every plan compile as T4.
		api.capabilities = runtime.capabilities
		// T02 research-gate wiring: the decision transaction commits the
		// waiting-gate checkpoint through the store-backed repository.
		api.gateOrchestrator = runtime.orchestrator
		api.gateCheckpoints = &writingruntime.PersistentCheckpointRepository{Store: store,
			Trace: writingstore.TraceContext{Actor: writingstore.Actor{Type: writingstore.ActorSystem, ID: "writingruntime"},
				Provenance: map[string]any{}, SourceRefs: []string{}}}
		s.governedTrigger = api.trigger
	}
	slog.Info("governed writing runtime mounted", "mode", string(mode), "capabilities", len(specs))
}

// governedInitialProvider supplies the contract artifact from the run's
// stored contract so node 1 has its input from the canonical content store.
type governedInitialProvider struct {
	store  *writingstore.Store
	server *Server
}

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
	document, err := provider.store.GetDocument(ctx, run.DocumentID)
	if err != nil {
		return nil, err
	}
	// F5: the run's materials artifact is the structured owner material
	// manifest (MaterialManifest shape): per-material identity, source ref,
	// and content hash, with every material's raw bytes staged content-
	// addressed so the research executors can read them by material_ref
	// without any external fetch. An empty selection is represented honestly;
	// it is not a fabricated source.
	manifest := writingruntime.MaterialManifest{
		SchemaVersion:    writingplan.SchemaVersion,
		RunID:            run.RunID,
		OwnerID:          document.OwnerUserID,
		ConflictHandling: string(contract.Contract.MaterialPolicy.ConflictHandling),
		CapturedAt:       time.Now().UTC(),
		Materials:        []writingruntime.MaterialSnapshot{},
	}
	if raw, ok := document.Metadata["material_refs"]; ok {
		payload, err := json.Marshal(raw)
		if err != nil {
			return nil, err
		}
		var refs []writingtransport.MaterialReference
		if err = json.Unmarshal(payload, &refs); err != nil {
			return nil, err
		}
		if len(refs) > 0 {
			if provider.server == nil {
				return nil, fmt.Errorf("selected material store unavailable")
			}
			resolved, err := provider.server.resolveMaterialContents(ctx, document.OwnerUserID, refs)
			if err != nil {
				return nil, err
			}
			for _, material := range resolved {
				contentHash := "sha256:" + hex.EncodeToString(material.contentSum[:])
				if err := provider.store.PutArtifactContent(ctx, contentHash, material.MediaType, material.Content); err != nil {
					return nil, err
				}
				manifest.Materials = append(manifest.Materials, writingruntime.MaterialSnapshot{
					MaterialID: material.MaterialID, Title: material.Title,
					SourceKind: writingruntime.MaterialSourceKnowledge, SourceRef: material.SourceRef,
					MediaType: material.MediaType, ContentRef: "artifact://" + contentHash,
					ContentHash: contentHash, SourceRefs: canonicalSourceRefs(material.SourceRef),
					UpdatedAt: manifest.CapturedAt,
				})
			}
		}
	}
	materialBody, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	materialSum := sha256.Sum256(materialBody)
	materialHash := "sha256:" + hex.EncodeToString(materialSum[:])
	if err = provider.store.PutArtifactContent(ctx, materialHash, "application/json", materialBody); err != nil {
		return nil, err
	}
	return []writingruntime.InputArtifact{{ArtifactID: "art_" + run.RunID + "_materials", Version: 1, ArtifactType: "materials", ContentHash: materialHash, MediaType: "application/json", ContentRef: "artifact://" + materialHash}, {ArtifactID: "art_" + run.RunID + "_contract", Version: 1,
		ArtifactType: "contract", ContentHash: hash, MediaType: "application/json",
		ContentRef: "artifact://" + hash}}, nil
}

// resolvedMaterial is one owner material's authorized snapshot: identity plus
// the raw bytes the run may read (tenant-scoped, resolved server-side).
type resolvedMaterial struct {
	MaterialID string
	Title      string
	SourceRef  string
	MediaType  string
	Content    []byte
	contentSum [32]byte
}

// canonicalSourceRefs returns the non-blank source refs as a stable slice.
func canonicalSourceRefs(values ...string) []string {
	refs := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			refs = append(refs, value)
		}
	}
	return refs
}

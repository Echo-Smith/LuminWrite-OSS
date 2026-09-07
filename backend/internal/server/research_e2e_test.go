package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/scholar"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingruntime"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// T06 E2E (docs/plans/2026-09-07-research-review-integration.md §T06): the
// tpl_research_review_v1 template drives the full research chain over the
// real HTTP surface with a fake scholar worker (discover/read) and the
// deterministic Go-side outline assembly plus an injected deterministic draft
// generator. Scenarios:
//  1. full chain — run → discover → read → evidence gate → outline → outline
//     gate → draft → citations → fact → quality → finalize; both gates each
//     decide exactly once; draft + citation index coexist and bind.
//  2. budget boundary — the executor-level sentinel pauses the run mid-read;
//     the API resume continues the remaining papers without re-reading
//     finished ones (触界 → 干净暂停 → 恢复续跑).
//  3. no draft generator — the draft node pauses RESEARCH_UNAVAILABLE and no
//     full_draft is ever produced (no fallback to the fast writer).
//  4. legacy regression — TestGovernedP0HTTPTemplates (governed_runners_test.go)
//     keeps passing over the same composition.

const t06E2EUser = "00000000-0000-0000-0000-0000000006e2"

// ── Fake scholar worker ─────────────────────────────────────────────────────

type t06FakeWorker struct {
	mu       sync.Mutex
	discover int
	ranks    int
	reads    map[string]int
	papers   int
	// withFullText makes Discover advertise OA URLs so the read executor can
	// fetch and parse real full text (the T07 scope-overclaim scenario needs
	// genuinely full-text-read papers to overclaim against).
	withFullText bool
}

func newT06FakeWorker(papers int) *t06FakeWorker {
	return &t06FakeWorker{reads: map[string]int{}, papers: papers}
}

func (fake *t06FakeWorker) Discover(_ context.Context, _ string, _ []string, _ int, _ ...scholar.CallOption) (*scholar.DiscoverOutputs, *scholar.OperationResponse, error) {
	fake.mu.Lock()
	fake.discover++
	fake.mu.Unlock()
	papers := make([]scholar.PaperCandidate, 0, fake.papers)
	for index := 0; index < fake.papers; index++ {
		paperID := fmt.Sprintf("t06-paper-%02d", index)
		title := "研究综述论文 " + paperID
		abstract := "背景与结论：" + paperID + " 的确定性摘要内容，用于引用验证。"
		candidate := scholar.PaperCandidate{PaperID: paperID,
			Title: &title, Authors: []string{"作者"}, Aliases: []string{paperID}, Abstract: &abstract}
		if fake.withFullText {
			oaURL := "https://oa.example.org/" + paperID + ".txt"
			candidate.OAURL = &oaURL
		}
		papers = append(papers, candidate)
	}
	return &scholar.DiscoverOutputs{Papers: papers,
			ProviderResults: []scholar.ProviderResult{{Provider: "openalex", Status: "ok"}}},
		&scholar.OperationResponse{Usage: scholar.Usage{Measured: false}}, nil
}

func (fake *t06FakeWorker) Rank(_ context.Context, _ string, candidates []scholar.RankCandidate, _ ...scholar.CallOption) (*scholar.RankOutputs, *scholar.OperationResponse, error) {
	fake.mu.Lock()
	fake.ranks++
	fake.mu.Unlock()
	scores := make([]scholar.RankScore, 0, len(candidates))
	for _, candidate := range candidates {
		scores = append(scores, scholar.RankScore{PaperID: candidate.PaperID, Score: 3, Reason: "直接相关"})
	}
	return &scholar.RankOutputs{Scores: scores}, &scholar.OperationResponse{Usage: scholar.Usage{Measured: false}}, nil
}

func (fake *t06FakeWorker) FetchFullText(_ context.Context, paperID, _ string, _ int64, _ ...scholar.CallOption) (*scholar.FetchFullTextOutputs, *scholar.OperationResponse, error) {
	content := []byte("全文正文（" + paperID + "）：\n\n这是用于验证引用的确定性正文段落，包含核心结论与数据。")
	return &scholar.FetchFullTextOutputs{PaperID: paperID, AcquisitionStatus: "full_text_available",
			ContentHash: t06sha256(content), SizeBytes: int64(len(content)), MediaType: "text/plain",
			ContentBase64: base64.StdEncoding.EncodeToString(content)},
		&scholar.OperationResponse{Usage: scholar.Usage{Measured: false}}, nil
}

func (fake *t06FakeWorker) ParseDocument(_ context.Context, document []byte, mediaType, _ string, _ ...scholar.CallOption) (*writingruntime.ParseOutputs, *scholar.OperationResponse, error) {
	text := string(document)
	blocks := []writingruntime.ParsedBlock{}
	for index, paragraph := range strings.Split(text, "\n\n") {
		trimmed := strings.TrimSpace(paragraph)
		if trimmed == "" {
			continue
		}
		page := index + 1
		blocks = append(blocks, writingruntime.ParsedBlock{BlockID: fmt.Sprintf("blk-%04d", index+1),
			Text: trimmed, Page: &page, BlockHash: t06sha256([]byte(trimmed))})
	}
	scanned := false
	return &writingruntime.ParseOutputs{Blocks: blocks,
			Coverage: writingruntime.ParseCoverage{MediaType: mediaType,
				ParserVersion: writingruntime.ResearchReadParserVersion, TotalBlocks: len(blocks),
				TotalCodepoints: len([]rune(text)), Complete: true, LikelyScanned: &scanned}},
		&scholar.OperationResponse{Usage: scholar.Usage{Measured: false}}, nil
}

func (fake *t06FakeWorker) ReadPaper(_ context.Context, _, paperID string, blocks []writingruntime.ReaderBlock, _ writingruntime.ReaderPolicy, _ ...scholar.CallOption) (*writingruntime.ReadOutputs, *scholar.OperationResponse, error) {
	fake.mu.Lock()
	fake.reads[paperID]++
	fake.mu.Unlock()
	if len(blocks) == 0 {
		return &writingruntime.ReadOutputs{PaperID: paperID, Claims: []writingruntime.ReaderClaimOutput{},
				Evidence: []writingruntime.ReaderEvidence{}, BlocksRead: []string{}},
			&scholar.OperationResponse{Usage: scholar.Usage{Measured: false}}, nil
	}
	block := blocks[0]
	quote := []rune(block.Text)
	if len(quote) > 20 {
		quote = quote[:20]
	}
	return &writingruntime.ReadOutputs{PaperID: paperID,
			Claims: []writingruntime.ReaderClaimOutput{{ClaimID: "c1", Text: paperID + " 的确定性结论",
				Kind: "source_assertion", EvidenceIDs: []string{"e1"}, Limitations: []string{}}},
			Evidence: []writingruntime.ReaderEvidence{{EvidenceID: "e1", BlockID: block.BlockID,
				BlockHash: block.BlockHash, Quote: string(quote), StartChar: 0, EndChar: len(quote),
				Page: block.Page, EvidenceScope: "full_text"}},
			BlocksRead: []string{block.BlockID}, ReaderVersion: "reader-test", ReaderPolicyVersion: "reader/1"},
		&scholar.OperationResponse{Usage: scholar.Usage{Measured: false}}, nil
}

func (fake *t06FakeWorker) totalReads() int {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	total := 0
	for _, count := range fake.reads {
		total += count
	}
	return total
}

func t06sha256(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ── Deterministic draft generator (the mock-friendly seam) ─────────────────

type t06DeterministicGenerator struct{}

// t06SwappableGenerator lets a single harness instance flip the draft
// generator between the deterministic test build and the no-generator
// (RESEARCH_UNAVAILABLE) deployment shape.
type t06SwappableGenerator struct {
	mu       sync.Mutex
	delegate writingruntime.ResearchDraftGenerator
}

func (generator *t06SwappableGenerator) GenerateResearchDraft(ctx context.Context, input writingruntime.ResearchDraftInput) (writingruntime.ResearchDraftOutput, error) {
	generator.mu.Lock()
	delegate := generator.delegate
	generator.mu.Unlock()
	if delegate == nil {
		return writingruntime.ResearchDraftOutput{}, writingruntime.ErrResearchGeneratorUnavailable
	}
	return delegate.GenerateResearchDraft(ctx, input)
}

// rewireDraftUnavailable flips the draft generator off before the run is
// created — the honest SERVICE_UNAVAILABLE deployment shape (nil LLM).
func (h *t06Harness) rewireDraftUnavailable() {
	h.generator.mu.Lock()
	h.generator.delegate = nil
	h.generator.mu.Unlock()
}

func (t06DeterministicGenerator) GenerateResearchDraft(_ context.Context, input writingruntime.ResearchDraftInput) (writingruntime.ResearchDraftOutput, error) {
	text := map[string]string{}
	for _, section := range input.Sections {
		var builder strings.Builder
		builder.WriteString(section.Title + "一节按论点展开：" + section.CentralPoint + " ")
		for _, evidence := range section.Evidence {
			builder.WriteString("证据表明相关结论 [@" + evidence.EvidenceID + "]。")
		}
		if len(section.Omitted) > 0 {
			builder.WriteString("本节证据受上下文预算限制。")
		}
		text[section.SectionID] = builder.String()
	}
	return writingruntime.ResearchDraftOutput{SectionText: text, ModelRef: "deterministic",
		PromptTemplateRef: "research-draft-test/1"}, nil
}

// ── Budget guard (executor-level sentinel) ──────────────────────────────────

// t06BudgetGuard fires the boundary once, after `boundaryAt` completed papers.
type t06BudgetGuard struct {
	boundaryAt int
	fired      bool
}

func (guard *t06BudgetGuard) ResearchBudgetBoundaryReached(_ context.Context, _ writingruntime.ExecutionRequest, papersCompleted, papersTotal int) (bool, string) {
	if guard.fired || guard.boundaryAt <= 0 || guard.boundaryAt >= papersTotal {
		return false, ""
	}
	if papersCompleted >= guard.boundaryAt {
		guard.fired = true
		return true, "t06 test budget boundary"
	}
	return false, ""
}

// ── Harness ─────────────────────────────────────────────────────────────────

type t06Harness struct {
	*t00Harness
	worker    *t06FakeWorker
	budget    *t06BudgetGuard
	generator *t06SwappableGenerator
	api       *persistentWritingAPI
	orchestr  *writingruntime.Orchestrator
	executors *writingruntime.ExecutorRegistry
	caps      *writingplan.CapabilityRegistry
	// qualityWrap lets T07 tests mount the real mechanical quality gate
	// (ResearchQualityGateRunner) around the scripted quality runner; nil
	// keeps the T06 behavior unchanged.
	qualityWrap func(writingruntime.LegacyNodeRunner) writingruntime.LegacyNodeRunner
}

// newT06E2EHarness rebuilds the mounted orchestrator with research executors
// backed by the fake worker and the deterministic generator, keeping the
// scripted legacy capabilities from the T00 harness mount.
func newT06E2EHarness(t *testing.T, papers int, boundaryAt int) *t06Harness {
	t.Helper()
	base := newT00Harness(t)
	worker := newT06FakeWorker(papers)
	budget := &t06BudgetGuard{boundaryAt: boundaryAt}
	generator := &t06SwappableGenerator{delegate: t06DeterministicGenerator{}}
	h := &t06Harness{t00Harness: base, worker: worker, budget: budget, generator: generator}
	store := base.store
	canonical := writingruntime.WritingStoreContentGateway{Store: store}
	discover, err := writingruntime.NewResearchDiscoverExecutor(worker, canonical)
	if err != nil {
		t.Fatal(err)
	}
	read, err := writingruntime.NewResearchReadExecutor(worker, canonical, store, budget)
	if err != nil {
		t.Fatal(err)
	}
	outline, err := writingruntime.NewResearchOutlineExecutor(canonical, nil)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := writingruntime.NewResearchDraftExecutor(canonical, generator)
	if err != nil {
		t.Fatal(err)
	}
	citations, err := writingruntime.NewResearchCitationValidator(canonical, store)
	if err != nil {
		t.Fatal(err)
	}
	fact, err := writingruntime.NewResearchFactValidator(canonical, nil)
	if err != nil {
		t.Fatal(err)
	}
	h.remount(t, map[string]writingruntime.Executor{
		"engine.step.research_discover":  discover,
		"engine.step.research_read":      read,
		"engine.step.research_outline":   outline,
		"engine.step.research_draft":     draft,
		"engine.step.research_citations": citations,
		"engine.step.research_fact":      fact,
	})
	return h
}

// remount assembles a fresh governed runtime whose executor registry serves
// the research replacements for research capabilities and the scripted
// runners for every other catalog capability, then re-binds the API.
func (h *t06Harness) remount(t *testing.T, research map[string]writingruntime.Executor) {
	t.Helper()
	base := h.t00Harness
	store := base.store
	server := base.server
	canonical := writingruntime.WritingStoreContentGateway{Store: store}
	capabilities := writingplan.DefaultCapabilityRegistry()
	executors := writingruntime.NewExecutorRegistry()
	deps := governedRuntimeDependencies{
		canonical:       canonical,
		sink:            server.governedRollout.shadow,
		evidence:        server.governedRollout.evidence,
		transitionStore: writingruntime.WritingStoreTransitionRecorder{Store: store},
		checkpoints: writingruntime.PersistentCheckpointRepository{Store: store,
			Trace: writingstore.TraceContext{Actor: writingstore.Actor{Type: writingstore.ActorSystem, ID: "writingruntime"},
				Provenance: map[string]any{}, SourceRefs: []string{}}},
		initial:   governedInitialProvider{store: store, server: server},
		materials: store,
		context:   writingruntime.StoreContextSource{Store: store},
		telemetry: server.metrics,
	}
	specs, err := server.governedCapabilitySpecs(store, canonical)
	if err != nil {
		t.Fatal(err)
	}
	for index := range specs {
		spec := specs[index]
		if executor, ok := research[spec.BindingID]; ok {
			if err := capabilities.RegisterExecutor(writingplan.ExecutorBinding{ID: spec.BindingID,
				AcceptedInputTypes:  append([]writingplan.ArtifactType(nil), spec.Inputs...),
				ProducedOutputTypes: append([]writingplan.ArtifactType(nil), spec.Outputs...),
				Dispatch: func(context.Context, writingplan.ExecutionRequest) (writingplan.ExecutionResult, error) {
					return writingplan.ExecutionResult{}, nil
				}}); err != nil {
				t.Fatal(err)
			}
			if err := capabilities.Activate(spec.CapabilityID, spec.BindingID); err != nil {
				t.Fatal(err)
			}
			if err := executors.Register(executor); err != nil {
				t.Fatal(err)
			}
			continue
		}
		// Scripted legacy capability via the T00 mount helper.
		var runner writingruntime.LegacyNodeRunner = base.runners[spec.CapabilityID]
		if runner == nil {
			continue
		}
		if spec.CapabilityID == "core.validation.quality" && h.qualityWrap != nil {
			runner = h.qualityWrap(runner)
		}
		if err := t00MountScriptedCapability(capabilities, executors, spec, runner, deps, canonical); err != nil {
			t.Fatalf("mount scripted capability %s: %v", spec.CapabilityID, err)
		}
	}
	// The safe scripted draft capability (used by legacy-mode tests) rides the
	// T00 mount as well.
	if err := t00RegisterSafeDraftCapability(capabilities); err != nil {
		t.Fatal(err)
	}
	if runner := base.runners[t00SafeCapability]; runner != nil {
		if err := t00MountScriptedCapability(capabilities, executors, t00SafeSpec(), runner, deps, canonical); err != nil {
			t.Fatal(err)
		}
	}
	orchestrator := &writingruntime.Orchestrator{Store: store, Capabilities: capabilities, Executors: executors,
		State: writingruntime.NewStateMachine(deps.transitionStore), Checkpoints: deps.checkpoints, Initial: deps.initial,
		Materials: deps.materials, Telemetry: deps.telemetry,
		Subject: func(run writingstore.RuntimeRun) string { return run.OwnerUserID },
		Context: deps.context, Envelopes: store,
		ContextRuntime: &writingruntime.ContextRuntime{},
		Delivery: &writingruntime.DeliveryProtocol{Store: store, Content: canonical,
			Actor: writingstore.Actor{Type: writingstore.ActorSystem, ID: "writingruntime.delivery"}}}
	runtime := &governedWritingRuntime{orchestrator: orchestrator,
		controller: governedRunController{orchestrator: orchestrator, store: store}, capabilities: capabilities, mode: writingruntime.RuntimeModeShadow}
	api, ok := server.writingAPI.(*persistentWritingAPI)
	if !ok {
		t.Fatal("persistent writing API missing")
	}
	api.controller = runtime.controller
	api.trigger = &governedRunTrigger{orchestrator: orchestrator, store: store}
	api.capabilities = capabilities
	server.governedTrigger = api.trigger
	// T02 gate surface (mirrors production wiring).
	api.gateOrchestrator = orchestrator
	api.gateCheckpoints = &writingruntime.PersistentCheckpointRepository{Store: store,
		Trace: writingstore.TraceContext{Actor: writingstore.Actor{Type: writingstore.ActorSystem, ID: "writingruntime"},
			Provenance: map[string]any{}, SourceRefs: []string{}}}
	h.api = api
	h.orchestr = orchestrator
	h.executors = executors
	h.caps = capabilities
	_ = deps
}

// fixture uses the T00 fixture with a v1.1 research contract instead.
func (h *t06Harness) fixture(t *testing.T) *t00Fixture {
	return h.fixtureMutate(t, nil)
}

// fixtureMutate builds the same fixture while letting a test mutate the
// contract before it is sealed (e.g. evidence_requirement for T07).
func (h *t06Harness) fixtureMutate(t *testing.T, mutate func(*writingkernel.WritingContract)) *t00Fixture {
	t.Helper()
	document := e2eRequest(t, h.router, h.token, "POST", "/api/v2/writing/documents", map[string]any{"title": "T06 研究综述"})
	documentID := e2eJSONField(t, document, "document_id")
	payload, err := os.ReadFile(filepath.Join("..", "..", "..", "specs", "lcp", "v1.1", "fixtures", "writing-contract.research-review.valid.json"))
	if err != nil {
		t.Fatal(err)
	}
	var contract writingkernel.WritingContract
	if err := json.Unmarshal(payload, &contract); err != nil {
		t.Fatal(err)
	}
	contract.Status = writingkernel.ContractStatusDraft
	// The fixture demands 5 citable sources; the E2E drives a 2-3 paper
	// workset, so the contract is resealed with a matching floor (the same
	// re-seal discipline the T05 runtime fixture uses).
	contract.Research.MinCitableSources = 1
	if mutate != nil {
		mutate(&contract)
	}
	for i := range contract.SourceAttributions {
		valueHash, hashErr := contract.FieldValueHash(contract.SourceAttributions[i].FieldPath)
		if hashErr != nil {
			t.Fatal(hashErr)
		}
		contract.SourceAttributions[i].ValueHash = valueHash
	}
	contract.ContractID = writingstore.StableID("ctr_", documentID, "t06", time.Now().UTC().Format("150405.000000000"))
	draftContract, err := contract.WithComputedHash()
	if err != nil {
		t.Fatal(err)
	}
	putted := e2eRequest(t, h.router, h.token, "POST", "/api/v2/writing/documents/"+documentID+"/contracts", map[string]any{"contract": draftContract})
	contractID := e2eNestedField(t, putted, "contract", "contract_id")
	confirmed := e2eRequest(t, h.router, h.token, "POST", "/api/v2/writing/contracts/"+contractID+"/confirm",
		map[string]any{"previous_version": 1, "contract": confirmedContract(t, draftContract)})
	if e2eNestedField(t, confirmed, "contract", "status") != string(writingkernel.ContractStatusConfirmed) {
		t.Fatalf("contract not confirmed: %#v", confirmed)
	}
	sealed := confirmedContract(t, draftContract)
	baseVersion := e2eBaseVersion(t, documentID, "T06 基线草稿")
	if _, err := h.store.CommitDocumentVersion(context.Background(), writingstore.CommitDocumentVersionParams{
		Version: baseVersion, ContractID: contractID, ContractVersion: 2,
		Trace: writingstore.TraceContext{Actor: writingstore.Actor{Type: writingstore.ActorUser, ID: h.userID},
			Provenance: map[string]any{}, SourceRefs: []string{}}}); err != nil {
		t.Fatal(err)
	}
	return &t00Fixture{documentID: documentID, contractID: contractID, contract: sealed, baseVersion: baseVersion}
}

// buildResearchEnvelope compiles the research_review template into a
// dispatch-valid envelope through the API's registry (gate exemption active).
func (h *t06Harness) buildResearchEnvelope(t *testing.T, fixture *t00Fixture) writingplan.WritingPlanEnvelope {
	t.Helper()
	contract := fixture.contract
	now := time.Now().UTC()
	intent, err := (writingplan.IntentPlan{IntentPlanID: "iplan_t06_" + now.Format("150405000000000") + fmt.Sprint(now.Nanosecond()),
		ContractRef: writingplan.ObjectRef{ID: contract.ContractID, Version: 2, Hash: contract.ContractHash},
		Summary:     "T06 research review chain", CreatedBy: writingplan.ActorUser, CreatedAt: now,
		ProposedSteps: []writingplan.ProposedStep{{StepID: "research", Objective: "produce a researched review",
			CapabilityHint: writingplan.CapabilityResearchDraft, DependsOn: []string{}}}}).WithComputedHash()
	if err != nil {
		t.Fatal(err)
	}
	budget := writingplan.PlanBudget{MaxCostUSD: 100, MaxDurationMS: 7200000, MaxConcurrency: 1, MaxNodes: 12, MaxItems: 20}
	result, err := writingplan.Compile(writingplan.CompileRequest{IntentPlan: intent, Contract: contract,
		Registry: h.api.capabilities, Templates: writingplan.DefaultTemplateRegistry(),
		InitialArtifactTypes: []writingplan.ArtifactType{"contract", "materials"},
		AllowedPermissions:   governedWritingPermissions, Budget: budget,
		RequiredValidators:    writingplan.RequiredValidatorsForContract(contract),
		RequiredFinalArtifact: "revision_set", SystemRecommendation: writingkernel.OrchestrationModeResearchReview})
	if err != nil {
		t.Fatalf("research_review compile: %v", err)
	}
	if !result.Plan.StaticValidation.Valid {
		t.Fatalf("research plan invalid: %v", result.Plan.StaticValidation.Errors)
	}
	envelope := writingplan.WritingPlanEnvelope{SchemaVersion: writingplan.SchemaVersion, IntentPlan: intent,
		ExecutablePlan: result.Plan, StrategyDecision: result.Decision}
	if err := envelope.Validate(); err != nil {
		t.Fatalf("envelope invalid: %v", err)
	}
	validationContext := writingplan.ValidationContext{Registry: h.api.capabilities,
		InitialArtifactTypes: []writingplan.ArtifactType{"contract", "materials"}, AllowedPermissions: governedWritingPermissions,
		Budget: budget, RequiredValidators: writingplan.RequiredValidatorsForContract(contract),
		RequiredFinalArtifact: "revision_set", ExternalResearchAllowed: contract.MaterialPolicy.AllowExternalResearch, Now: now}
	if err := envelope.ValidateForDispatch(validationContext); err != nil {
		t.Fatalf("envelope not dispatchable: %v", err)
	}
	return envelope
}

// researchRunBudget admits the research template's node bounds: the read
// node carries MaxItems=20 / 20-minute timeout, ten nodes sum past the
// default 50-minute ceiling, and human gate waits are not counted here (the
// proactive execution budget is the BudgetBoundary guard's runtime job).
func (h *t06Harness) researchRunBudget() writingplan.PlanBudget {
	return writingplan.PlanBudget{MaxCostUSD: 100, MaxDurationMS: 7200000, MaxConcurrency: 1, MaxNodes: 12, MaxItems: 20}
}

// createResearchRun drives the HTTP run creation; an expensive plan may be
// parked in awaiting_approval, in which case the test approves it first.
func (h *t06Harness) createResearchRun(t *testing.T, fixture *t00Fixture, envelope writingplan.WritingPlanEnvelope) string {
	t.Helper()
	permissions := permissionsForPlan(envelope.ExecutablePlan, h.api.capabilities)
	run := e2eRequest(t, h.router, h.token, "POST", "/api/v2/writing/runs", map[string]any{
		"document_id": fixture.documentID, "contract_id": fixture.contractID, "contract_version": 2,
		"contract_hash": fixture.contract.ContractHash, "base_version_id": fixture.baseVersion.VersionID,
		"style_slug": "yinyue", "plan": envelope, "budget": h.researchRunBudget(), "permissions": permissions,
	})
	runID := e2eJSONField(t, run, "run_id")
	if e2eJSONField(t, run, "status") == "awaiting_approval" {
		e2eRequest(t, h.router, h.token, "POST", "/api/v2/writing/runs/"+runID+"/approve", map[string]any{
			"plan_id": envelope.ExecutablePlan.PlanID, "plan_version": 1,
			"plan_hash": envelope.ExecutablePlan.PlanHash, "permissions": permissions})
	}
	return runID
}

// ── HTTP helpers over the T02 surface ───────────────────────────────────────

func (h *t06Harness) t02() *t02APIHarness { return &t02APIHarness{t00Harness: h.t00Harness} }

func (h *t06Harness) researchPhase(t *testing.T, runID string) string {
	t.Helper()
	code, payload := t02APIRequest(t, h.router, h.token, http.MethodGet, "/api/v2/writing/runs/"+runID+"/research", "", nil)
	if code != http.StatusOK {
		t.Fatalf("GET research -> %d: %s", code, payload)
	}
	phase, _ := dataOf(t, payload)["phase"].(string)
	return phase
}

func (h *t06Harness) gateState(t *testing.T, runID, gateID string) (map[string]any, map[string]any, float64) {
	t.Helper()
	code, payload := t02APIRequest(t, h.router, h.token, http.MethodGet, "/api/v2/writing/runs/"+runID+"/gates/"+gateID, "", nil)
	if code != http.StatusOK {
		t.Fatalf("GET gate -> %d: %s", code, payload)
	}
	gate := dataOf(t, payload)
	inputRef, _ := gate["input_ref"].(map[string]any)
	revision, _ := gate["revision"].(float64)
	return gate, inputRef, revision
}

func (h *t06Harness) decideGate(t *testing.T, runID, gateID string, envelope writingplan.WritingPlanEnvelope) {
	t.Helper()
	_, inputRef, revision := h.gateState(t, runID, gateID)
	code, payload := t02APIRequest(t, h.router, h.token, http.MethodPost,
		"/api/v2/writing/runs/"+runID+"/gates/"+gateID+"/decisions", t02APIIdempotencyKey("decide-"+gateID),
		map[string]any{"plan_id": envelope.ExecutablePlan.PlanID, "plan_version": 1,
			"plan_hash": envelope.ExecutablePlan.PlanHash, "gate_revision": int(revision),
			"input_ref": inputRef, "decision": "approve"})
	if code != http.StatusAccepted && code != http.StatusOK {
		t.Fatalf("decide %s -> %d: %s", gateID, code, payload)
	}
}

// advanceToGate drives the run until it is paused with a PENDING gate of the
// given kind (evidence | outline) and returns the gate id. The gate-decision
// resume trigger is asynchronous and single-shot; when the run is parked
// paused without a pending gate (a dropped trigger race or a budget-boundary
// pause), the helper drives the plain API resume the way an owner would.
func (h *t06Harness) advanceToGate(t *testing.T, runID, kind string) string {
	t.Helper()
	deadline := time.Now().Add(120 * time.Second)
	var lastResume time.Time
	for time.Now().Before(deadline) {
		code, payload := t02APIRequest(t, h.router, h.token, http.MethodGet, "/api/v2/writing/runs/"+runID+"/research", "", nil)
		if code == http.StatusOK {
			if active, _ := dataOf(t, payload)["active_gate"].(map[string]any); active != nil {
				if gateKind, _ := active["gate_kind"].(string); gateKind == kind {
					gateID, _ := active["gate_id"].(string)
					if gateID != "" {
						return gateID
					}
				}
			}
		}
		if status := h.httpStatus(t, runID); status == "paused" && time.Since(lastResume) > time.Second {
			lastResume = time.Now()
			_, _ = t02APIRequest(t, h.router, h.token, http.MethodPost, "/api/v2/writing/runs/"+runID+"/resume",
				t02APIIdempotencyKey("advance-"+fmt.Sprint(time.Now().UnixNano())), map[string]any{})
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("run %s never reached a pending %q gate (last status %q)", runID, kind, h.httpStatus(t, runID))
	return ""
}

// waitForGatePausedAt waits until the run is paused with a pending gate of the
// given kind (evidence | outline).
func (h *t06Harness) waitForGateKind(t *testing.T, runID, kind string, require bool) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		code, payload := t02APIRequest(t, h.router, h.token, http.MethodGet, "/api/v2/writing/runs/"+runID+"/research", "", nil)
		if code == http.StatusOK {
			if active, _ := dataOf(t, payload)["active_gate"].(map[string]any); active != nil {
				if gateKind, _ := active["gate_kind"].(string); gateKind == kind {
					return
				}
			}
		}
		if status := h.httpStatus(t, runID); status == "paused" && !require {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("run %s never paused at a %q gate (last status %q)", runID, kind, h.httpStatus(t, runID))
}

// driveToTerminal drives the run to a terminal state, kicking the plain API
// resume when it is parked paused (bounded kicks — a genuine stuck pause
// stays paused and the caller asserts the exact terminal).
func (h *t06Harness) driveToTerminal(t *testing.T, runID string, maxKicks int) string {
	t.Helper()
	kicks := 0
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		status := h.httpStatus(t, runID)
		switch status {
		case "completed", "failed", "cancelled":
			return status
		case "paused":
			if kicks < maxKicks {
				kicks++
				_, _ = t02APIRequest(t, h.router, h.token, http.MethodPost, "/api/v2/writing/runs/"+runID+"/resume",
					t02APIIdempotencyKey("drive-"+fmt.Sprint(time.Now().UnixNano())), map[string]any{})
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("run %s never reached a terminal state within the window (last status %q)", runID, h.httpStatus(t, runID))
	return ""
}

// waitForNodeAttempt polls until the node has an attempt in the given state,
// kicking the API resume while the run sits paused (bounded kicks).
func (h *t06Harness) waitForNodeAttempt(t *testing.T, runID, nodeID, status string, maxKicks int) {
	t.Helper()
	kicks := 0
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		attempts, err := h.store.ListRunAttempts(context.Background(), runID)
		if err != nil {
			t.Fatal(err)
		}
		for _, attempt := range attempts {
			if attempt.NodeID == nodeID && attempt.Status == status {
				return
			}
		}
		if h.httpStatus(t, runID) == "paused" && kicks < maxKicks {
			kicks++
			_, _ = t02APIRequest(t, h.router, h.token, http.MethodPost, "/api/v2/writing/runs/"+runID+"/resume",
				t02APIIdempotencyKey("wait-"+fmt.Sprint(time.Now().UnixNano())), map[string]any{})
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("node %s never recorded a %q attempt within the window", nodeID, status)
}

// ── Scenarios ───────────────────────────────────────────────────────────────

func TestT06ResearchReviewFullChainThroughHTTP(t *testing.T) {
	h := newT06E2EHarness(t, 2, 0)
	fixture := h.fixture(t)
	envelope := h.buildResearchEnvelope(t, fixture)
	runID := h.createResearchRun(t, fixture, envelope)

	// discover → read → evidence gate pause.
	evidenceGate := h.advanceToGate(t, runID, "evidence")
	if phase := h.researchPhase(t, runID); phase != "gate_evidence" {
		t.Fatalf("research phase = %q, want gate_evidence", phase)
	}
	h.decideGate(t, runID, evidenceGate, envelope)

	// outline → outline gate pause.
	outlineGate := h.advanceToGate(t, runID, "outline")
	if phase := h.researchPhase(t, runID); phase != "gate_outline" {
		t.Fatalf("research phase = %q, want gate_outline", phase)
	}
	h.decideGate(t, runID, outlineGate, envelope)

	// draft → citations → fact → quality → finalize.
	if status := h.driveToTerminal(t, runID, 3); status != "completed" {
		t.Fatalf("run ended as %q, want completed", status)
	}
	ctx := context.Background()
	artifacts, err := h.store.ListRunArtifacts(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	var draftHash string
	for _, artifact := range artifacts {
		counts[artifact.ArtifactType]++
		if artifact.ArtifactType == "full_draft" {
			draftHash = artifact.ContentHash
		}
	}
	for _, artifactType := range []string{"research_candidates", "research_evidence_pack", "research_outline",
		"approved_research_outline", "evidence_approval", "full_draft", "research_citation_index",
		"evidence_report", "research_validation_details", "fact_report", "quality_report", "revision_set"} {
		if counts[artifactType] == 0 {
			t.Fatalf("run artifacts lack %s (have %#v)", artifactType, counts)
		}
	}
	// The citation index binds the exact draft artifact hash.
	var indexHash string
	for _, artifact := range artifacts {
		if artifact.ArtifactType == "research_citation_index" {
			indexHash = artifact.ContentHash
		}
	}
	_, indexBody, err := h.store.GetArtifactContent(ctx, indexHash)
	if err != nil {
		t.Fatal(err)
	}
	var index writingkernel.ResearchCitationIndex
	if err := json.Unmarshal(indexBody, &index); err != nil {
		t.Fatal(err)
	}
	if index.DraftHash != draftHash {
		t.Fatalf("citation index draft hash %s != draft artifact hash %s", index.DraftHash, draftHash)
	}
	if len(index.Citations) == 0 {
		t.Fatal("citation index empty in the completed run")
	}
	// The draft carries [@ev_] markers, all resolvable against the pack.
	_, packBody, err := h.store.GetArtifactContent(ctx, index.EvidencePackRef.ContentHash)
	if err != nil {
		t.Fatal(err)
	}
	var pack writingkernel.ResearchEvidencePack
	if err := json.Unmarshal(packBody, &pack); err != nil {
		t.Fatal(err)
	}
	if err := writingkernel.ValidateCitationIndexAgainstPack(index, pack); err != nil {
		t.Fatalf("citation index invalid against pack: %v", err)
	}
	_, draftBody, err := h.store.GetArtifactContent(ctx, draftHash)
	if err != nil {
		t.Fatal(err)
	}
	for _, evidence := range pack.Evidence {
		if !strings.Contains(string(draftBody), "[@"+evidence.EvidenceID+"]") {
			t.Fatalf("draft lacks marker for context evidence %s", evidence.EvidenceID)
		}
	}
	// Each gate decided exactly once, with the right kinds.
	var approved, outlineKinds int
	if err := h.server.db.QueryRow(`SELECT COUNT(*) FROM writing_gate_decisions WHERE run_id=$1 AND status='approved'`, runID).Scan(&approved); err != nil {
		t.Fatal(err)
	}
	if approved != 2 {
		t.Fatalf("approved decisions = %d, want exactly 2 (evidence + outline)", approved)
	}
	if err := h.server.db.QueryRow(`SELECT COUNT(*) FROM writing_gate_decisions WHERE run_id=$1 AND gate_kind='outline'`, runID).Scan(&outlineKinds); err != nil {
		t.Fatal(err)
	}
	if outlineKinds != 1 {
		t.Fatalf("outline-kind gates = %d, want 1", outlineKinds)
	}
}

func TestT06ResearchBudgetBoundaryPausesAndResumes(t *testing.T) {
	// 3 papers; the executor-level budget guard fires after the first paper.
	h := newT06E2EHarness(t, 3, 1)
	fixture := h.fixture(t)
	envelope := h.buildResearchEnvelope(t, fixture)
	runID := h.createResearchRun(t, fixture, envelope)

	// First pause: the read node's budget-boundary sentinel (a plain pause,
	// not a gate — the run's FailurePath=pause did its job).
	h.waitForGatePausedAtBoundary(t, runID)
	if reads := h.worker.totalReads(); reads != 1 {
		t.Fatalf("worker reads after boundary = %d, want 1", reads)
	}
	// Plain API resume (via advanceToGate): the sentinel pause is resumable;
	// the read node re-dispatches and continues the remaining papers from the
	// ledger, then the run reaches the evidence gate.
	h.decideGate(t, runID, h.advanceToGate(t, runID, "evidence"), envelope)
	if reads := h.worker.totalReads(); reads != 3 {
		t.Fatalf("worker reads after resume = %d, want 3 (no re-reads of finished papers)", reads)
	}
	h.decideGate(t, runID, h.advanceToGate(t, runID, "outline"), envelope)
	if status := h.driveToTerminal(t, runID, 3); status != "completed" {
		t.Fatalf("run ended as %q, want completed after boundary resume", status)
	}
}

// waitForGatePausedAtBoundary waits for the first pause and asserts it is the
// budget boundary (no pending gate, node_research_read failed).
func (h *t06Harness) waitForGatePausedAtBoundary(t *testing.T, runID string) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if status := h.httpStatus(t, runID); status == "paused" {
			code, payload := t02APIRequest(t, h.router, h.token, http.MethodGet, "/api/v2/writing/runs/"+runID+"/research", "", nil)
			if code == http.StatusOK {
				if active, _ := dataOf(t, payload)["active_gate"].(map[string]any); active == nil {
					// Confirm the sentinel: node.paused with the boundary code.
					events, err := h.store.ListRunEvents(context.Background(), runID, 0, 500)
					if err != nil {
						t.Fatal(err)
					}
					for _, event := range events {
						if event.EventType != "node.paused" {
							continue
						}
						payloadBytes, _ := json.Marshal(event.Payload)
						var decoded struct {
							ErrorCode string `json:"error_code"`
						}
						_ = json.Unmarshal(payloadBytes, &decoded)
						if decoded.ErrorCode == "RESEARCH_BUDGET_BOUNDARY" {
							return
						}
					}
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("run %s never paused at the budget boundary (last status %q)", runID, h.httpStatus(t, runID))
}

func TestT06ResearchDraftWithoutGeneratorPausesUnavailable(t *testing.T) {
	h := newT06E2EHarness(t, 1, 0)
	h.rewireDraftUnavailable()
	fixture := h.fixture(t)
	envelope := h.buildResearchEnvelope(t, fixture)
	runID := h.createResearchRun(t, fixture, envelope)
	h.decideGate(t, runID, h.advanceToGate(t, runID, "evidence"), envelope)
	h.decideGate(t, runID, h.advanceToGate(t, runID, "outline"), envelope)
	// The draft node fails through its pause path with RESEARCH_UNAVAILABLE
	// (first failure parks as unsafe_retry; later attempts exhaust the node
	// bounds and re-pause). The run is terminal-paused by design here: wait
	// for the draft node's failed attempt, kicking resumes meanwhile.
	h.waitForNodeAttempt(t, runID, "node_research_draft", "failed", 5)
	if status := h.httpStatus(t, runID); status != "paused" {
		t.Fatalf("no-generator run ended as %q, want paused", status)
	}
	ctx := context.Background()
	attempts, err := h.store.ListRunAttempts(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	draftFailed := false
	for _, attempt := range attempts {
		if attempt.NodeID == "node_research_draft" && attempt.Status == "failed" {
			draftFailed = true
		}
	}
	if !draftFailed {
		t.Fatal("draft node did not record its failed attempt")
	}
	// No full_draft artifact exists: the fast writer was NOT used as fallback.
	artifacts, err := h.store.ListRunArtifacts(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	for _, artifact := range artifacts {
		if artifact.ArtifactType == "full_draft" {
			t.Fatal("no-generator run produced a full_draft (fallback happened)")
		}
	}
	// The attempt ledger records the typed unavailable code via node.failed.
	events, err := h.store.ListRunEvents(ctx, runID, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	unavailable := false
	for _, event := range events {
		if event.EventType == "node.failed" {
			payloadBytes, _ := json.Marshal(event.Payload)
			var payload struct {
				ErrorCode string `json:"error_code"`
			}
			_ = json.Unmarshal(payloadBytes, &payload)
			if payload.ErrorCode == "RESEARCH_UNAVAILABLE" {
				unavailable = true
			}
		}
	}
	if !unavailable {
		t.Fatal("draft failure did not surface RESEARCH_UNAVAILABLE on node.failed")
	}
}

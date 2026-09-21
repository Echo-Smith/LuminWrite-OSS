package writingruntime

// ⑥C: Editorial roles as governed Executors — real-seam proof.
//
// The offline tests cover EditorialRoleNodeRunner with a fake invoker, and
// the B2 adapter scenarios route the editorial family through the shadow
// rollout. This test closes the gap: it wires the REAL editorial
// RoleAgentRunner (real EditorialToolRegistry, real builtin tools, real
// tool loop over a deterministic LLM stub) as the governed candidate and
// runs it through the governed composition — proving RoleAgentRunner is
// usable as an Executor, not a runtime: it receives governed inputs,
// returns provisional output, and every authoritative commit stays with
// the orchestrator/writingstore.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/editorial"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// capturingEmitter records engine events without asserting on them.
type capturingEmitter struct {
	mu     sync.Mutex
	events []string
}

func (e *capturingEmitter) record(name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, name)
}
func (e *capturingEmitter) StepStart(step engine.StepName, _ int) { e.record("step.start:" + string(step)) }
func (e *capturingEmitter) StepComplete(step engine.StepName, _ interface{}, _ int64) {
	e.record("step.complete:" + string(step))
}
func (e *capturingEmitter) StreamDelta(string)               {}
func (e *capturingEmitter) StreamReset()                     {}
func (e *capturingEmitter) ReasoningDelta(string)            {}
func (e *capturingEmitter) ArticleTitle(string)              {}
func (e *capturingEmitter) StreamDone(string)                {}
func (e *capturingEmitter) AwaitInput(step engine.StepName, _ interface{}, _ []string, _, _ int) {
	e.record("await_input:" + string(step))
}
func (e *capturingEmitter) Paused(step engine.StepName, savedState interface{}) { e.record("paused") }
func (e *capturingEmitter) PausedWithReason(step engine.StepName, savedState interface{}, reason string) {
	e.record("paused:" + reason)
}
func (e *capturingEmitter) Resumed(step engine.StepName) { e.record("resumed") }
func (e *capturingEmitter) Error(code, message string, _ engine.StepName) {
	e.record("error:" + code + ":" + message)
}
func (e *capturingEmitter) Completed(article, articleTitle string, review, tokenUsage interface{}) {
	e.record("completed")
}
func (e *capturingEmitter) Cancelled()                        { e.record("cancelled") }
func (e *capturingEmitter) Compaction(originalMessages, savedTokens int, summaryPreview string, historyVersion uint64, triggerReason string) {
	e.record("compaction")
}

// TestEditorialRoleRunnerExecutesAsGovernedExecutor mounts the REAL
// editorial role machinery behind the governed adapter and runs a node
// through the full composition.
func TestEditorialRoleRunnerExecutesAsGovernedExecutor(t *testing.T) {
	const roleOutput = "## 受控输出\n\n编辑部角色通过 governed executor 产出。"

	// The LLM stub: one deterministic completion. It also records request
	// bodies so the test can prove the real editorial tool registry fed
	// tool definitions into the call.
	var mu sync.Mutex
	var requestBodies []string
	llmStub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requestBodies = append(requestBodies, string(body))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q},\"finish_reason\":\"stop\"}],\"usage\":{\"total_tokens\":7}}\n\n", roleOutput)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer llmStub.Close()

	// Real editorial tool registry with the nine builtins — the same
	// wiring the production DAG path uses.
	toolRegistry := editorial.NewEditorialToolRegistry()
	editorial.RegisterBuiltinTools(toolRegistry)

	llm := tools.NewLLMClient(llmStub.URL, "test-key", "test-model", 4096, 0.1, 5*time.Second)
	styleProfile, ok := profile.NewLoader().Get("default")
	if !ok {
		t.Fatal("default style profile unavailable")
	}
	emitter := &capturingEmitter{}
	roleRunner := editorial.NewRoleAgentRunner(llm, nil, nil, styleProfile, emitter, toolRegistry)

	candidateRunner := EditorialRoleNodeRunner{
		Invoker: roleRunner,
		Config:  &editorial.AgentConfig{ID: "writer", Role: "writer"},
		Seed: engine.CompatibilityInput{TraceID: "run_editorial_governed", UserID: "user_editorial",
			UserInput: "写一篇受控测试文章", StyleSlug: styleProfile.Slug, Mode: "auto"},
		Usage: func(result *editorial.RoleRunResult) (LegacyUsage, error) {
			return LegacyUsage{Measured: true, InputTokens: int64(result.Tokens)}, nil
		},
	}

	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	capability, version := "core.draft.generate", "1.0.0"
	contractHash := hashForTest("editorial-governed-contract")
	runID := "run_editorial_governed"
	plan := writingplan.ExecutablePlan{PlanID: "plan_editorial_governed", Status: writingplan.PlanValidated,
		TrustLevel: writingplan.TrustT1, RootNodeID: "node_draft",
		Nodes: []writingplan.PlanNode{{NodeID: "node_draft", Kind: writingplan.NodeAction, Capability: capability,
			CapabilityVersion: version, DependsOn: []string{}, InputArtifactTypes: []writingplan.ArtifactType{"contract"},
			OutputArtifactTypes: []writingplan.ArtifactType{"full_draft"},
			Bounds:              writingplan.Bounds{MaxAttempts: 1, MaxConcurrency: 1, MaxItems: 1, MaxCostUSD: 2, TimeoutMS: 30000},
			FailurePath:         writingplan.FailureFail}},
		StaticValidation: writingplan.StaticValidation{Valid: true, CheckedAt: now, Errors: []string{},
			CapabilityRegistryVersion: "editorial-governed", BudgetValid: true, PermissionsValid: true,
			ArtifactFlowValid: true, FailurePathsValid: true}}
	plan, err := plan.WithComputedHash()
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeRuntimeStore{run: writingstore.RuntimeRun{RunID: runID, DocumentID: "doc_editorial_governed",
		ContractID: "ctr_editorial_governed", ContractVersion: 1, ContractHash: contractHash,
		Status: string(StatePlanned), ActivePlanID: plan.PlanID, ActivePlanVersion: 1,
		Budget:      writingplan.PlanBudget{MaxCostUSD: 10, MaxDurationMS: 60000, MaxConcurrency: 1, MaxNodes: 2, MaxItems: 1},
		Permissions: []writingplan.Permission{"model.invoke", "materials.read"}},
		plan: writingstore.PlanRecord{RunID: runID, PlanVersion: 1, ApprovalStatus: "not_required",
			Envelope: writingplan.WritingPlanEnvelope{IntentPlan: writingplan.IntentPlan{ContractRef: writingplan.ObjectRef{ID: "ctr_editorial_governed", Version: 1, Hash: contractHash}}, ExecutablePlan: plan}}}

	capabilities := writingplan.NewCapabilityRegistry("editorial-governed")
	if err := capabilities.RegisterExecutor(writingplan.ExecutorBinding{ID: "baseline.engine",
		AcceptedInputTypes: []writingplan.ArtifactType{"contract"}, ProducedOutputTypes: []writingplan.ArtifactType{"full_draft"},
		Dispatch: func(context.Context, writingplan.ExecutionRequest) (writingplan.ExecutionResult, error) {
			return writingplan.ExecutionResult{}, nil
		}}); err != nil {
		t.Fatal(err)
	}
	if err := capabilities.Register(writingplan.CapabilityManifest{ID: capability, Class: "writing.draft", Executor: "baseline.engine",
		InputTypes: []writingplan.ArtifactType{"contract"}, OptionalInputTypes: []writingplan.ArtifactType{}, OutputTypes: []writingplan.ArtifactType{"full_draft"},
		Permissions: []writingplan.Permission{"model.invoke", "materials.read"}, EstimatedCostUSD: 1, EstimatedDurationMS: 100,
		Version: version, SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction}, MaxBounds: plan.Nodes[0].Bounds,
		Idempotency: writingplan.IdempotencySafe, Available: true}); err != nil {
		t.Fatal(err)
	}

	baseline := &fakeGovernedExecutor{descriptor: ExecutorDescriptor{ExecutorID: "baseline.engine", Version: "1", SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction}}}
	canonical := &stageCountingGateway{inner: &memoryGateway{body: []byte("contract")}}
	policy := DefaultShadowPolicy("candidate.editorial_role", AdapterFamilyEditorial, capability, version)
	shadowGateway, err := NewShadowContentGateway(canonical, NewMemoryShadowContentSink(), policy)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := NewShadowIsolatedExecutorAdapter(AdapterFamilyEditorial,
		ExecutorDescriptor{ExecutorID: "candidate.editorial_role", Version: "1", SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction}},
		capability, version, []writingplan.Permission{"model.invoke"}, shadowGateway, candidateRunner)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := NewMutableRolloutPolicyProvider(policy)
	if err != nil {
		t.Fatal(err)
	}
	evidence := &MemoryRolloutEvidenceStore{}
	rollout, err := NewShadowRolloutExecutor(baseline, candidate, provider, evidence, &metricCapture{})
	if err != nil {
		t.Fatal(err)
	}
	executors := NewExecutorRegistry()
	if err := executors.Register(rollout); err != nil {
		t.Fatal(err)
	}
	initial := []InputArtifact{{ArtifactID: "art_contract", Version: 1, ArtifactType: "contract",
		ContentHash: contentHash([]byte("contract")), MediaType: "application/json", ContentRef: "memory://contract"}}
	orchestrator := &Orchestrator{Store: store, Capabilities: capabilities, Executors: executors,
		State: NewStateMachine(store), Checkpoints: &memoryCheckpoints{}, Initial: fixedInitialProvider(initial),
		Materials: store, Now: func() time.Time { return now }}

	out, err := orchestrator.Execute(context.Background(), runID)
	if err != nil || out.State != StateCompleted || len(out.CompletedNodes) != 1 {
		t.Fatalf("out=%#v err=%v", out, err)
	}

	// The real role's output must be the canonical committed artifact —
	// the editorial machinery ran end to end through the governed path.
	persisted, err := store.ListRunArtifacts(context.Background(), runID)
	if err != nil || len(persisted) != 2 {
		t.Fatalf("artifacts=%#v err=%v", persisted, err)
	}
	nodeOutput := persisted[len(persisted)-1]
	if nodeOutput.ArtifactType != "full_draft" || nodeOutput.Status != "provisional" {
		t.Fatalf("node output=%#v", nodeOutput)
	}
	if nodeOutput.NodeID != "node_draft" || nodeOutput.Producer != capability {
		t.Fatalf("lineage producer=%#v", nodeOutput)
	}
	// The shadow lane staged the real role output; canonical stayed clean.
	eventually(t, 2*time.Second, "real editorial role staged through shadow lane", func() bool {
		keys := shadowGateway.writes.(*MemoryShadowContentSink).Keys()
		for _, key := range keys {
			body, getErr := shadowGateway.writes.(*MemoryShadowContentSink).Get(context.Background(), key)
			if getErr == nil && strings.Contains(string(body), roleOutput) {
				return true
			}
		}
		return false
	})
	if canonical.stages != 0 {
		t.Fatalf("canonical gateway received %d shadow stage calls", canonical.stages)
	}

	// The real editorial tool registry fed tool definitions into the LLM
	// call — proof the role ran with the production tool surface.
	mu.Lock()
	defer mu.Unlock()
	sawTools := false
	for _, body := range requestBodies {
		if strings.Contains(body, "\"tools\"") && strings.Contains(body, "generate_outline") {
			sawTools = true
		}
	}
	if !sawTools {
		t.Fatalf("LLM stub never received editorial tool definitions; requests=%d", len(requestBodies))
	}
	var lastBody map[string]any
	if err := json.Unmarshal([]byte(requestBodies[len(requestBodies)-1]), &lastBody); err != nil {
		t.Fatalf("decode last LLM request: %v", err)
	}
	if _, ok := lastBody["tools"]; !ok {
		t.Fatal("final LLM request carried no tools — role ran without its registry")
	}
}

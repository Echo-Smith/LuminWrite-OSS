package writingruntime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/editorial"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
)

func TestLegacyExecutorVerifiesContentBeforeCallingRunner(t *testing.T) {
	gateway := &memoryGateway{body: []byte("tampered")}
	runner := &fakeLegacyRunner{}
	executor := mustLegacyExecutor(t, gateway, runner)
	request := legacyRequest([]byte("expected"))
	if _, err := executor.Execute(context.Background(), request); !errors.Is(err, ErrLegacyContentIntegrity) {
		t.Fatalf("integrity error=%v", err)
	}
	if runner.calls != 0 {
		t.Fatalf("runner calls=%d", runner.calls)
	}
}

func TestLegacyExecutorFailsClosedWithoutMeasuredUsage(t *testing.T) {
	body := []byte("contract")
	gateway := &memoryGateway{body: body}
	runner := &fakeLegacyRunner{outputs: []LegacyPayload{{OutputKey: "draft", ArtifactType: "full_draft", MediaType: "text/markdown", Body: []byte("draft"), Provenance: map[string]any{}, SourceRefs: []string{}}}}
	executor := mustLegacyExecutor(t, gateway, runner)
	if _, err := executor.Execute(context.Background(), legacyRequest(body)); !errors.Is(err, ErrLegacyUsageMissing) {
		t.Fatalf("usage error=%v", err)
	}
}

func TestLegacyExecutorStagesTypedOutputWithCompleteLineage(t *testing.T) {
	body := []byte("contract")
	gateway := &memoryGateway{body: body}
	runner := &fakeLegacyRunner{usage: LegacyUsage{Measured: true, CostUSD: .2, InputTokens: 10, OutputTokens: 20}, outputs: []LegacyPayload{{OutputKey: "draft", ArtifactType: "full_draft", MediaType: "text/markdown", Body: []byte("draft"), Provenance: map[string]any{"legacy": true}, SourceRefs: []string{}}}}
	executor := mustLegacyExecutor(t, gateway, runner)
	result, err := executor.Execute(context.Background(), legacyRequest(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Artifacts) != 1 || len(result.Artifacts[0].Parents) != 1 || result.Artifacts[0].Producer != "core.draft.generate" || result.Usage.CostUSD != .2 {
		t.Fatalf("result=%#v", result)
	}
}

func TestLegacyExecutorCannotClaimCancellationUntilLegacyStackPropagatesContext(t *testing.T) {
	descriptor := ExecutorDescriptor{ExecutorID: "engine.step.write", Version: "adapter-1", SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction}, Cancellable: true}
	if _, err := NewLegacyExecutor(descriptor, "core.draft.generate", "1.0.0", []writingplan.Permission{"model.invoke"}, &memoryGateway{}, &fakeLegacyRunner{}); !errors.Is(err, ErrExecutorMismatch) {
		t.Fatalf("error=%v", err)
	}
}

type memoryGateway struct {
	body         []byte
	corruptStage bool
}

func (gateway *memoryGateway) Load(context.Context, InputArtifact) ([]byte, error) {
	return append([]byte(nil), gateway.body...), nil
}
func (gateway *memoryGateway) Stage(_ context.Context, key, _ string, body []byte) (string, string, error) {
	hash := contentHash(body)
	if gateway.corruptStage {
		hash = hashForTest("wrong")
	}
	return "memory://" + key, hash, nil
}

type fakeLegacyRunner struct {
	calls   int
	outputs []LegacyPayload
	usage   LegacyUsage
	err     error
}

func (runner *fakeLegacyRunner) Run(context.Context, LegacyNodeInput) ([]LegacyPayload, LegacyUsage, error) {
	runner.calls++
	return runner.outputs, runner.usage, runner.err
}

func mustLegacyExecutor(t *testing.T, gateway ContentGateway, runner LegacyNodeRunner) *LegacyExecutor {
	t.Helper()
	executor, err := NewLegacyExecutor(ExecutorDescriptor{ExecutorID: "engine.step.write", Version: "adapter-1", SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction}}, "core.draft.generate", "1.0.0", []writingplan.Permission{"model.invoke"}, gateway, runner)
	if err != nil {
		t.Fatal(err)
	}
	executor.now = func() time.Time { return time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC) }
	return executor
}
func legacyRequest(body []byte) ExecutionRequest {
	node := writingplan.PlanNode{NodeID: "node_write", Kind: writingplan.NodeAction, Capability: "core.draft.generate", CapabilityVersion: "1.0.0", DependsOn: []string{}, InputArtifactTypes: []writingplan.ArtifactType{"contract"}, OutputArtifactTypes: []writingplan.ArtifactType{"full_draft"}, Bounds: writingplan.Bounds{MaxAttempts: 1, MaxConcurrency: 1, MaxItems: 1, MaxCostUSD: 1, TimeoutMS: 1000}, FailurePath: writingplan.FailureFail}
	return ExecutionRequest{RunID: "run_legacy", PlanID: "plan_legacy", PlanVersion: 1, NodeID: node.NodeID, Attempt: 1, IdempotencyKey: "run_legacy:node_write:1", ContractRef: writingplan.ObjectRef{ID: "ctr_legacy", Version: 1, Hash: hashForTest("contract-ref")}, Node: node, Inputs: []InputArtifact{{ArtifactID: "art_contract", Version: 1, ArtifactType: "contract", ContentHash: contentHash(body), MediaType: "application/json", ContentRef: "memory://contract"}}, Permissions: []writingplan.Permission{"model.invoke"}}
}

func TestHarnessAdapterIsExplicitlyUnavailable(t *testing.T) {
	if _, err := NewHarnessExecutorAdapter(); !errors.Is(err, ErrLegacyHarnessUnsafe) || ErrorCodeOf(err) != CodeLegacyWriteViolation {
		t.Fatalf("error=%v", err)
	}
}

func TestEditorialDAGAdapterIsExplicitlyUnavailable(t *testing.T) {
	if _, err := NewEditorialDAGExecutorAdapter(); !errors.Is(err, ErrLegacyDAGUnsafe) || ErrorCodeOf(err) != CodeLegacyWriteViolation {
		t.Fatalf("error=%v", err)
	}
}

func TestLegacyAdapterFamiliesShareOfflineConformanceContract(t *testing.T) {
	families := []AdapterFamily{AdapterFamilyEngine, AdapterFamilyEditorial, AdapterFamilyHarness}
	for _, family := range families {
		t.Run(string(family), func(t *testing.T) {
			body := []byte("contract")
			gateway := &memoryGateway{body: body}
			runner := &fakeLegacyRunner{usage: LegacyUsage{Measured: true, InputTokens: 1, OutputTokens: 1}, outputs: []LegacyPayload{{OutputKey: "draft", ArtifactType: "full_draft", MediaType: "text/markdown", Body: []byte("draft"), Provenance: map[string]any{"family": family}, SourceRefs: []string{}}}}
			executor, err := NewLegacyExecutorAdapter(family, ExecutorDescriptor{ExecutorID: "legacy." + string(family), Version: "adapter-1", SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction}}, "core.draft.generate", "1.0.0", []writingplan.Permission{"model.invoke"}, gateway, runner)
			if err != nil {
				t.Fatal(err)
			}
			request := legacyRequest(body)
			request.Node.Capability = "core.draft.generate"
			if _, err := executor.Execute(context.Background(), request); err != nil {
				t.Fatalf("offline conformance execution: %v", err)
			}
			if executor.AdapterPolicy().TrafficMode != AdapterTrafficOffline {
				t.Fatalf("policy=%#v", executor.AdapterPolicy())
			}
			registry := NewExecutorRegistry()
			if err := registry.Register(executor); !errors.Is(err, ErrExecutorTrafficDisabled) {
				t.Fatalf("production registration error=%v", err)
			}
		})
	}
}

func TestAdapterPolicyRejectsAuthoritativeWrites(t *testing.T) {
	policy := OfflineAdapterPolicy(AdapterFamilyEngine)
	policy.Authority.DocumentWrite = true
	if err := policy.Validate(); ErrorCodeOf(err) != CodeLegacyWriteViolation {
		t.Fatalf("error=%v code=%s", err, ErrorCodeOf(err))
	}
}

func TestExecutionIdentityBindsCanonicalAttempt(t *testing.T) {
	request := legacyRequest([]byte("contract"))
	identity := request.Identity()
	if err := identity.Validate(); err != nil || identity.IdempotencyKey != "run_legacy:node_write:1" || identity.ContractRef != request.ContractRef {
		t.Fatalf("identity=%#v error=%v", identity, err)
	}
}

func TestEditorialRoleRunnerReturnsValueWithoutLegacyStore(t *testing.T) {
	invoker := &fakeRoleInvoker{result: &editorial.RoleRunResult{Output: "# draft", Tokens: 12}}
	runner := EditorialRoleNodeRunner{Invoker: invoker, Config: &editorial.AgentConfig{ID: "writer", Role: "writer"},
		Usage: func(result *editorial.RoleRunResult) (LegacyUsage, error) {
			return LegacyUsage{Measured: true, InputTokens: int64(result.Tokens)}, nil
		}}
	request := legacyRequest([]byte("contract"))
	request.Node.OutputArtifactTypes = []writingplan.ArtifactType{"full_draft"}
	outputs, usage, err := runner.Run(context.Background(), LegacyNodeInput{Request: request,
		Payloads: map[writingplan.ArtifactType][][]byte{"contract": {[]byte("contract")}}})
	if err != nil || len(outputs) != 1 || string(outputs[0].Body) != "# draft" || !usage.Measured || invoker.config.Task.ID != "node_write" {
		t.Fatalf("outputs=%#v usage=%#v err=%v", outputs, usage, err)
	}
}

type fakeRoleInvoker struct {
	result *editorial.RoleRunResult
	config editorial.RoleRunConfig
}

func (invoker *fakeRoleInvoker) Run(_ context.Context, config editorial.RoleRunConfig) (*editorial.RoleRunResult, error) {
	invoker.config = config
	return invoker.result, nil
}

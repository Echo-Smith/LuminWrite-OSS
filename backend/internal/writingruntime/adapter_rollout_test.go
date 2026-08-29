package writingruntime

import (
	"context"
	"errors"
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/editorial"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
)

func TestThreeB2AdaptersShareOfflineAuthorityBoundary(t *testing.T) {
	request := legacyRequest([]byte("contract"))
	descriptor := func(id string) ExecutorDescriptor {
		return ExecutorDescriptor{ExecutorID: id, Version: "task12-1", SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction}}
	}
	engineAdapter, err := NewEngineStepExecutorAdapter(descriptor("candidate.engine"), request.Node.Capability,
		request.Node.CapabilityVersion, []writingplan.Permission{"model.invoke"}, &memoryGateway{body: []byte("contract")},
		EngineStepRunner{StepFactory: func() engine.Step { return &fixedEngineStep{} }, Usage: func(*engine.ExecutionContext) (LegacyUsage, error) {
			return LegacyUsage{Measured: true, InputTokens: 1, OutputTokens: 2}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	editorialAdapter, err := NewEditorialRoleExecutorAdapter(descriptor("candidate.editorial"), request.Node.Capability,
		request.Node.CapabilityVersion, []writingplan.Permission{"model.invoke"}, &memoryGateway{body: []byte("contract")},
		EditorialRoleNodeRunner{Invoker: &fakeRoleInvoker{result: &editorial.RoleRunResult{Output: "editorial draft", Tokens: 3}},
			Config: &editorial.AgentConfig{ID: "writer", Role: "writer"}, Usage: func(*editorial.RoleRunResult) (LegacyUsage, error) {
				return LegacyUsage{Measured: true, InputTokens: 2, OutputTokens: 3}, nil
			}})
	if err != nil {
		t.Fatal(err)
	}
	harnessInvoker := &fakeHarnessCoreInvoker{result: HarnessCoreResult{Usage: LegacyUsage{Measured: true, InputTokens: 4, OutputTokens: 5},
		Outputs: []LegacyPayload{{OutputKey: "draft", ArtifactType: "full_draft", MediaType: "text/markdown", Body: []byte("harness draft")}}}}
	harnessAdapter, err := NewHarnessCoreExecutorAdapter(descriptor("candidate.harness"), request.Node.Capability,
		request.Node.CapabilityVersion, []writingplan.Permission{"model.invoke"}, &memoryGateway{body: []byte("contract")}, harnessInvoker)
	if err != nil {
		t.Fatal(err)
	}

	for _, adapter := range []*LegacyExecutor{engineAdapter, editorialAdapter, harnessAdapter} {
		result, executeErr := adapter.Execute(context.Background(), request)
		if executeErr != nil || len(result.Artifacts) != 1 || adapter.AdapterPolicy().TrafficMode != AdapterTrafficOffline {
			t.Fatalf("adapter=%s result=%#v error=%v policy=%#v", adapter.Descriptor().ExecutorID, result, executeErr, adapter.AdapterPolicy())
		}
		if err := NewExecutorRegistry().Register(adapter); err == nil {
			t.Fatalf("adapter %s bypassed rollout registry boundary", adapter.Descriptor().ExecutorID)
		}
	}
	if harnessInvoker.request.Identity != request.Identity() || harnessInvoker.request.MaxItems != request.Node.Bounds.MaxItems {
		t.Fatalf("harness request=%#v", harnessInvoker.request)
	}
}

type fixedEngineStep struct{}

func (*fixedEngineStep) Name() engine.StepName { return engine.StepName("fixed") }
func (*fixedEngineStep) CanPause() bool        { return false }
func (*fixedEngineStep) Execute(_ context.Context, execCtx *engine.ExecutionContext, _ engine.EventEmitter) error {
	execCtx.Article = "engine draft"
	return nil
}

type fakeHarnessCoreInvoker struct {
	request HarnessCoreRequest
	result  HarnessCoreResult
	err     error
}

func (invoker *fakeHarnessCoreInvoker) RunCore(_ context.Context, request HarnessCoreRequest) (HarnessCoreResult, error) {
	invoker.request = request
	return invoker.result, invoker.err
}

func harnessCoreDraftOutputs() []LegacyPayload {
	return []LegacyPayload{{OutputKey: "draft", ArtifactType: "full_draft", MediaType: "text/markdown", Body: []byte("harness draft")}}
}

func TestHarnessCoreAdapterFailsClosedOnMissingRequiredArtifact(t *testing.T) {
	request := legacyRequest([]byte("contract"))
	invoker := &fakeHarnessCoreInvoker{result: HarnessCoreResult{Usage: LegacyUsage{Measured: true}, Outputs: harnessCoreDraftOutputs()}}
	runner := HarnessCoreNodeRunner{Invoker: invoker}
	request.Node.OutputArtifactTypes = []writingplan.ArtifactType{"full_draft", "quality_report"}
	_, _, err := runner.Run(context.Background(), LegacyNodeInput{Request: request, Payloads: map[writingplan.ArtifactType][][]byte{"contract": {[]byte("contract")}}})
	if !errors.Is(err, ErrLegacyOutputMissing) || ErrorCodeOf(err) != CodeExecutorOutputInvalid {
		t.Fatalf("err=%v code=%s", err, ErrorCodeOf(err))
	}
}

func TestHarnessCoreAdapterFailsClosedOnUndeclaredOrDuplicateOutput(t *testing.T) {
	request := legacyRequest([]byte("contract"))
	request.Node.OutputArtifactTypes = []writingplan.ArtifactType{"full_draft"}
	runner := HarnessCoreNodeRunner{Invoker: &fakeHarnessCoreInvoker{result: HarnessCoreResult{Usage: LegacyUsage{Measured: true},
		Outputs: []LegacyPayload{{OutputKey: "draft", ArtifactType: "full_draft", Body: []byte("a")},
			{OutputKey: "draft", ArtifactType: "full_draft", Body: []byte("b")}}}}}
	if _, _, err := runner.Run(context.Background(), LegacyNodeInput{Request: request}); !errors.Is(err, ErrLegacyOutputMissing) {
		t.Fatalf("duplicate output err=%v", err)
	}
	runner = HarnessCoreNodeRunner{Invoker: &fakeHarnessCoreInvoker{result: HarnessCoreResult{Usage: LegacyUsage{Measured: true},
		Outputs: []LegacyPayload{{OutputKey: "source_pack", ArtifactType: "source_pack", Body: []byte("x")}}}}}
	if _, _, err := runner.Run(context.Background(), LegacyNodeInput{Request: request}); !errors.Is(err, ErrLegacyOutputMissing) {
		t.Fatalf("undeclared output err=%v", err)
	}
	runner = HarnessCoreNodeRunner{Invoker: &fakeHarnessCoreInvoker{result: HarnessCoreResult{Usage: LegacyUsage{Measured: true},
		Outputs: []LegacyPayload{{OutputKey: "draft", ArtifactType: "full_draft", Body: nil}}}}}
	if _, _, err := runner.Run(context.Background(), LegacyNodeInput{Request: request}); !errors.Is(err, ErrLegacyOutputMissing) {
		t.Fatalf("empty body err=%v", err)
	}
}

func TestHarnessCoreAdapterReturnsStableUsageUnmeasuredCode(t *testing.T) {
	request := legacyRequest([]byte("contract"))
	runner := HarnessCoreNodeRunner{Invoker: &fakeHarnessCoreInvoker{result: HarnessCoreResult{Usage: LegacyUsage{Measured: false}, Outputs: harnessCoreDraftOutputs()}}}
	_, _, err := runner.Run(context.Background(), LegacyNodeInput{Request: request})
	if !errors.Is(err, ErrLegacyUsageMissing) || ErrorCodeOf(err) != CodeExecutorUsageUnmeasured {
		t.Fatalf("err=%v code=%s", err, ErrorCodeOf(err))
	}
}

// emittingEngineStep exercises every emitter method, so a mis-wired emitter
// would panic or leak instead of passing silently.
type emittingEngineStep struct{}

func (*emittingEngineStep) Name() engine.StepName { return engine.StepName("emitting") }
func (*emittingEngineStep) CanPause() bool        { return false }
func (*emittingEngineStep) Execute(_ context.Context, execCtx *engine.ExecutionContext, emitter engine.EventEmitter) error {
	emitter.StepStart(engine.StepName("emit"), 0)
	emitter.StreamDelta("hello")
	emitter.StreamReset()
	emitter.ReasoningDelta("why")
	emitter.ArticleTitle("title")
	emitter.StreamDone("partial")
	emitter.AwaitInput(engine.StepName("await"), nil, []string{}, 1, 2)
	emitter.Paused(engine.StepName("paused"), nil)
	emitter.PausedWithReason(engine.StepName("paused"), nil, "why")
	emitter.Resumed(engine.StepName("resumed"))
	emitter.Error("code", "message", engine.StepName("error"))
	emitter.StepComplete(engine.StepName("emit"), nil, 1)
	emitter.Completed("article", "title", nil, map[string]any{})
	emitter.Cancelled()
	emitter.Compaction(1, 2, "preview", 3, "threshold")
	execCtx.Article = "engine scenario draft"
	return nil
}

// foreignStepEmitter stands in for any legacy emitter (session writer, HTTP
// stream, old event store) that must never reach a governed engine step.
type foreignStepEmitter struct {
	deltas int
}

func (emitter *foreignStepEmitter) StepStart(engine.StepName, int)                  {}
func (emitter *foreignStepEmitter) StepComplete(engine.StepName, interface{}, int64) {}
func (emitter *foreignStepEmitter) StreamDelta(string)                              { emitter.deltas++ }
func (emitter *foreignStepEmitter) StreamReset()                                    {}
func (emitter *foreignStepEmitter) ReasoningDelta(string)                           {}
func (emitter *foreignStepEmitter) ArticleTitle(string)                             {}
func (emitter *foreignStepEmitter) StreamDone(string)                               {}
func (emitter *foreignStepEmitter) AwaitInput(engine.StepName, interface{}, []string, int, int) {}
func (emitter *foreignStepEmitter) Paused(engine.StepName, interface{})             {}
func (emitter *foreignStepEmitter) PausedWithReason(engine.StepName, interface{}, string) {}
func (emitter *foreignStepEmitter) Resumed(engine.StepName)                         {}
func (emitter *foreignStepEmitter) Error(string, string, engine.StepName)           {}
func (emitter *foreignStepEmitter) Completed(string, string, interface{}, interface{}) {}
func (emitter *foreignStepEmitter) Cancelled()                                      {}
func (emitter *foreignStepEmitter) Compaction(int, int, string, uint64, string)     {}

func TestEngineStepAdapterRejectsLegacyEmitters(t *testing.T) {
	request := legacyRequest([]byte("contract"))
	runner := EngineStepRunner{StepFactory: func() engine.Step { return &emittingEngineStep{} }, Emitter: &foreignStepEmitter{},
		Usage: func(*engine.ExecutionContext) (LegacyUsage, error) { return LegacyUsage{Measured: true}, nil }}
	adapter, err := NewEngineStepExecutorAdapter(ExecutorDescriptor{ExecutorID: "candidate.engine", Version: "1", SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction}},
		request.Node.Capability, request.Node.CapabilityVersion, []writingplan.Permission{"model.invoke"}, &memoryGateway{body: []byte("contract")}, runner)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Execute(context.Background(), request)
	if !errors.Is(err, ErrLegacyEmitterUnsafe) || ErrorCodeOf(err) != CodeLegacyWriteViolation {
		t.Fatalf("err=%v code=%s", err, ErrorCodeOf(err))
	}
}

func TestEngineStepAdapterRunsOnNilOrGovernedEmitterOnly(t *testing.T) {
	request := legacyRequest([]byte("contract"))
	usage := func(*engine.ExecutionContext) (LegacyUsage, error) { return LegacyUsage{Measured: true, InputTokens: 1, OutputTokens: 2}, nil }
	for _, tt := range []struct {
		name    string
		emitter engine.EventEmitter
	}{
		{"nil emitter defaults to governed observer", nil},
		{"governed observer emitter", NewGovernedStepEmitter()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			runner := EngineStepRunner{StepFactory: func() engine.Step { return &emittingEngineStep{} }, Emitter: tt.emitter, Usage: usage}
			adapter, err := NewEngineStepExecutorAdapter(ExecutorDescriptor{ExecutorID: "candidate.engine", Version: "1", SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction}},
				request.Node.Capability, request.Node.CapabilityVersion, []writingplan.Permission{"model.invoke"}, &memoryGateway{body: []byte("contract")}, runner)
			if err != nil {
				t.Fatal(err)
			}
			result, err := adapter.Execute(context.Background(), request)
			if err != nil || len(result.Artifacts) != 1 || result.Artifacts[0].ContentHash != contentHash([]byte("engine scenario draft")) {
				t.Fatalf("result=%#v error=%v", result, err)
			}
		})
	}
}

package writingruntime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/contextcompiler"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// envelopeCaptureStore captures envelope persistence on top of the orchestrator
// fixture's fake runtime store.
type envelopeCaptureStore struct {
	*fakeRuntimeStore
	envelopes []writingstore.ContextEnvelopeRecord
	fail      bool
}

func (store *envelopeCaptureStore) SaveContextEnvelope(_ context.Context, record writingstore.ContextEnvelopeRecord) error {
	if store.fail {
		return context.DeadlineExceeded
	}
	store.envelopes = append(store.envelopes, record)
	return nil
}

// fixedContextSource returns a canned input regardless of the node: the
// orchestrator must assemble it through the manifest contract.
type fixedContextSource struct {
	input contextcompiler.Input
	err   error
	calls int
}

func (source *fixedContextSource) CompileInputs(context.Context, writingstore.RuntimeRun, writingplan.PlanNode) (contextcompiler.Input, error) {
	source.calls++
	return source.input, source.err
}

func TestContextShadowWiringCompilesPersistsAndInjects(t *testing.T) {
	fixture := newOrchestratorFixture(t, writingplan.IdempotencySafe, false)
	source := &fixedContextSource{input: contextcompiler.Input{
		ContractDigest: "长文报告：检索优于重排",
		ThreadLabels:   []string{"论点链：检索优于重排"},
		FactLines:      []string{"luminbuddy | state | 开源"},
	}}
	envelopes := &envelopeCaptureStore{fakeRuntimeStore: fixture.store}
	fixture.orchestrator.Context = source
	fixture.orchestrator.Envelopes = envelopes
	fixture.executor.contextOut = make(chan *contextcompiler.Envelope, 1)

	// The draft capability contract: required digest + document state, plus
	// optional blocks including the resident layer.
	manifest, _ := fixture.orchestrator.Capabilities.Get("core.draft.generate")
	wanted := manifest.Context.ContextWanted()
	if len(wanted) == 0 {
		t.Fatal("draft capability has no context contract")
	}

	if _, err := fixture.orchestrator.Execute(context.Background(), fixture.store.run.RunID); err != nil {
		t.Fatal(err)
	}
	if source.calls != 1 {
		t.Fatalf("compilations=%d", source.calls)
	}
	if len(envelopes.envelopes) != 1 {
		t.Fatalf("envelopes=%d", len(envelopes.envelopes))
	}
	persisted := envelopes.envelopes[0]
	if persisted.RunID != fixture.store.run.RunID || persisted.NodeID != "node_draft" || persisted.Attempt != 1 {
		t.Fatalf("persisted=%#v", persisted)
	}
	// The envelope is bound to the manifest contract: declared blocks with
	// data render; document_state was required but unsupplied, so it is an
	// explicit missing entry — recorded, not fatal (M4a shadow semantics).
	var compiled contextcompiler.Envelope
	if err := json.Unmarshal(persisted.Payload, &compiled); err != nil {
		t.Fatal(err)
	}
	if compiled.Hash != persisted.EnvelopeHash {
		t.Fatalf("hash mismatch %s vs %s", compiled.Hash, persisted.EnvelopeHash)
	}
	missing := map[string]string{}
	for _, entry := range compiled.Missing {
		missing[entry.Block] = entry.Reason
	}
	if missing["document_state"] != "no data supplied" {
		t.Fatalf("missing=%v", compiled.Missing)
	}
	// The executor observed the injected envelope.
	select {
	case injected := <-fixture.executor.contextOut:
		if injected == nil || injected.Hash != compiled.Hash {
			t.Fatalf("injected=%v", injected)
		}
	default:
		t.Fatal("executor did not observe an envelope")
	}
}

func TestContextShadowWiringDegradesWithoutChangingOutcome(t *testing.T) {
	fixture := newOrchestratorFixture(t, writingplan.IdempotencySafe, false)
	// Source errors must degrade: the run still completes, no envelope lands.
	fixture.orchestrator.Context = &fixedContextSource{err: context.DeadlineExceeded}
	envelopes := &envelopeCaptureStore{fakeRuntimeStore: fixture.store}
	fixture.orchestrator.Envelopes = envelopes
	outcome, err := fixture.orchestrator.Execute(context.Background(), fixture.store.run.RunID)
	if err != nil {
		t.Fatalf("shadow failure changed the outcome: %v", err)
	}
	if len(envelopes.envelopes) != 0 {
		t.Fatalf("envelopes=%d", len(envelopes.envelopes))
	}
	if outcome.State != StateCompleted {
		t.Fatalf("state=%s", outcome.State)
	}
}

func TestContextShadowWiringIsDisabledByDefault(t *testing.T) {
	fixture := newOrchestratorFixture(t, writingplan.IdempotencySafe, false)
	if fixture.orchestrator.Context != nil {
		t.Fatal("default orchestrator must not compile context")
	}
	if _, err := fixture.orchestrator.Execute(context.Background(), fixture.store.run.RunID); err != nil {
		t.Fatal(err)
	}
}

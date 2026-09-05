// Kernel invariant guard tests (V3.0 M2, docs/26): the thin-orchestrator
// refactor must not open a path for capabilities to bypass kernel invariants.
// The structural guarantee is the capability contract itself — a runner
// receives request/envvelope/payloads by value and returns artifact drafts;
// every write (attempt ledger, artifacts, documents, checkpoints, state) is
// orchestrator-owned. These tests pin that shape against drift.
package writingruntime

import (
	"context"
	"reflect"
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

func TestCapabilityContractHasNoWriteSurface(t *testing.T) {
	// M2 kernel invariant (docs/26): what a capability receives and returns
	// must carry no executable surface — no callbacks, no channels, no
	// kernel store handles. Artifacts travel by value (hash + ref); the
	// orchestrator owns every commit.
	for _, sample := range []any{ExecutionRequest{}, ExecutionResult{}, LegacyNodeInput{}, OutputArtifactDraft{}} {
		checkNoWriteSurface(t, reflect.TypeOf(sample))
	}
}

func checkNoWriteSurface(t *testing.T, typ reflect.Type) {
	t.Helper()
	if typ == nil {
		return
	}
	switch typ.Kind() {
	case reflect.Func, reflect.Chan, reflect.UnsafePointer:
		t.Fatalf("capability contract carries an executable field of type %s — capabilities must not receive code or channels", typ)
	case reflect.Ptr:
		if typ == reflect.TypeOf(&writingstore.Store{}) {
			t.Fatalf("capability contract carries a kernel store handle (%s) — capabilities must not reach the store", typ)
		}
		// Pointers to plain structs are read-only projections (e.g. the
		// compiled context envelope); descend.
		checkNoWriteSurface(t, typ.Elem())
	case reflect.Struct:
		if typ == reflect.TypeOf(writingstore.Store{}) {
			t.Fatalf("capability contract carries a kernel store value (%s)", typ)
		}
		for i := 0; i < typ.NumField(); i++ {
			checkNoWriteSurface(t, typ.Field(i).Type)
		}
	case reflect.Slice, reflect.Array, reflect.Map:
		checkNoWriteSurface(t, typ.Elem())
	default:
	}
}

func TestDeliveryDriveUnknownClassIsNoop(t *testing.T) {
	// Unknown classes are no-ops — including on a wired protocol: delivery
	// semantics are opt-in per class via the routing table, and the
	// orchestrator loop stays capability-agnostic.
	var protocol *DeliveryProtocol
	delivery, err := protocol.Drive(context.Background(), "writing.unknown",
		writingstore.RuntimeRun{}, writingplan.PlanNode{}, nil)
	if err != nil || delivery != nil {
		t.Fatalf("unknown class must be a no-op: %v %v", delivery, err)
	}
	wired := &DeliveryProtocol{}
	delivery, err = wired.Drive(context.Background(), "writing.unknown",
		writingstore.RuntimeRun{}, writingplan.PlanNode{}, nil)
	if err != nil || delivery != nil {
		t.Fatalf("wired protocol, unknown class: %v %v", delivery, err)
	}
	// A registered class runs its driver: missing inputs surface as the
	// driver's own validation error (fail closed), never a silent skip.
	if _, err := wired.Drive(context.Background(), DeliveryClassDraft,
		writingstore.RuntimeRun{}, writingplan.PlanNode{}, nil); err == nil {
		t.Fatal("registered class with missing inputs must fail closed")
	}
}

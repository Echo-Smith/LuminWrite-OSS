// M3 hook bus tests (docs/27): bus fan-out semantics, the kernel guard on
// the hook snapshot, and the lifecycle sequence observed over a real
// orchestrator run.
package writingruntime

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// recordingHook captures every event it observes.
type recordingHook struct {
	events []LifecycleEvent
	ids    []string
}

func (hook *recordingHook) ObserveLifecycle(_ context.Context, event LifecycleEvent, snapshot LifecycleSnapshot) {
	hook.events = append(hook.events, event)
	hook.ids = append(hook.ids, snapshot.RunID)
}

// panickingHook explodes on the first observation — the bus must contain it.
type panickingHook struct{}

func (panickingHook) ObserveLifecycle(context.Context, LifecycleEvent, LifecycleSnapshot) {
	panic("hook blew up")
}

func TestHookBusFansOutAndContainsPanics(t *testing.T) {
	first, second := &recordingHook{}, &recordingHook{}
	bus := newHookBus(first, panickingHook{}, second)
	bus.emit(context.Background(), LifecycleRunDispatched, LifecycleSnapshot{RunID: "run_x", State: StateRunning})
	if len(first.events) != 1 || first.events[0] != LifecycleRunDispatched {
		t.Fatalf("first hook events=%v", first.events)
	}
	// The panicking hook must not prevent later observers from running.
	if len(second.events) != 1 || second.events[0] != LifecycleRunDispatched {
		t.Fatalf("second hook events=%v", second.events)
	}
	// Nil hooks are dropped at registration.
	bus.register(nil)
	bus.emit(context.Background(), LifecycleRunCompleted, LifecycleSnapshot{RunID: "run_x"})
	if len(first.events) != 2 {
		t.Fatalf("post-register events=%v", first.events)
	}
	// A nil bus is a safe no-op.
	var empty *hookBus
	empty.emit(context.Background(), LifecycleRunDispatched, LifecycleSnapshot{})
}

func TestLifecycleSnapshotHasNoWriteSurface(t *testing.T) {
	// M3 kernel guard (docs/27): what a hook sees carries no funcs, channels,
	// or kernel store handles — hooks observe, they cannot act.
	typ := reflect.TypeOf(LifecycleSnapshot{})
	var walk func(reflect.Type)
	walk = func(typ reflect.Type) {
		if typ == nil {
			return
		}
		switch typ.Kind() {
		case reflect.Func, reflect.Chan, reflect.UnsafePointer:
			t.Fatalf("lifecycle snapshot carries an executable field of type %s", typ)
		case reflect.Ptr:
			if typ == reflect.TypeOf(&writingstore.Store{}) {
				t.Fatalf("lifecycle snapshot carries a kernel store handle")
			}
			walk(typ.Elem())
		case reflect.Struct:
			if typ == reflect.TypeOf(writingstore.Store{}) {
				t.Fatalf("lifecycle snapshot carries a kernel store value")
			}
			for i := 0; i < typ.NumField(); i++ {
				walk(typ.Field(i).Type)
			}
		case reflect.Slice, reflect.Array, reflect.Map:
			walk(typ.Elem())
		}
	}
	walk(typ)
}

func TestTerminalLifecycleClassification(t *testing.T) {
	cases := map[error]LifecycleEvent{
		nil:                      LifecycleRunCompleted,
		ErrRunCancelled:          LifecycleRunCancelled,
		ErrRunPaused:             LifecycleRunPaused,
		ErrApprovalRequired:      LifecycleRunPaused,
		ErrHumanRecoveryRequired: LifecycleRunPaused,
		errors.New("broken"):     LifecycleRunFailed,
		ErrNoReadyNode:           LifecycleRunFailed,
	}
	for err, want := range cases {
		if got := terminalLifecycleEvent(err); got != want {
			t.Fatalf("terminal(%v)=%s, want %s", err, got, want)
		}
	}
}

// TestOrchestratorEmitsLifecycleSequence drives the M3 acceptance over the
// real orchestrator fixture: a completing run observes dispatched →
// before → artifact.submitted → after → checkpoint.before → completed, with
// the telemetry hook projecting onto the same bus.
func TestOrchestratorEmitsLifecycleSequence(t *testing.T) {
	fixture := newOrchestratorFixture(t, writingplan.IdempotencySafe, false)
	hook := &recordingHook{}
	metrics := &metricCapture{}
	fixture.orchestrator.Hooks = []RuntimeHook{hook}
	fixture.orchestrator.Telemetry = metrics
	if _, err := fixture.orchestrator.Execute(context.Background(), fixture.store.run.RunID); err != nil {
		t.Fatal(err)
	}
	want := []LifecycleEvent{
		LifecycleRunDispatched, LifecycleBeforeCapability, LifecycleArtifactSubmitted,
		LifecycleCheckpointBefore, LifecycleAfterCapability, LifecycleRunCompleted,
	}
	if len(hook.events) != len(want) {
		t.Fatalf("lifecycle events=%v", hook.events)
	}
	for i, event := range want {
		if hook.events[i] != event {
			t.Fatalf("event[%d]=%s, want %s (all=%v)", i, hook.events[i], event, hook.events)
		}
		if hook.ids[i] != "run_runtime" {
			t.Fatalf("snapshot run id %q", hook.ids[i])
		}
	}
	// The telemetry projection hook rode the same bus.
	if !metrics.has(MetricLifecycle, string(LifecycleRunCompleted)) {
		t.Fatalf("telemetry missing lifecycle metrics: %#v", metrics.metrics)
	}
}

// Runtime lifecycle + hook bus (V3.0 M3, docs/27): named lifecycle moments
// fanned out to read-only observers. The kernel guard is structural — the
// hook contract receives a value snapshot and returns nothing, so a hook can
// observe, persist its own records, and enrich telemetry, but it can never
// alter the run, reject a transition, mutate artifacts, or implement a
// bypassable Contract/Plan/Artifact/Permission/Quality/Commit. Kernel writes
// stay orchestrator-owned (M2 dispatch.go verbs); the bus only widens the
// Observe verb's audience.
package writingruntime

import (
	"context"
	"log/slog"
	"sync"
)

// LifecycleEvent names one run lifecycle moment. The set mirrors the
// docs/20 lifecycle sequence, rooted at what the orchestrator actually owns
// (dispatch) — run creation itself stays an API-layer fact.
type LifecycleEvent string

const (
	// LifecycleRunDispatched is emitted when a run begins executing (after
	// plan load and recovery, before the first node).
	LifecycleRunDispatched LifecycleEvent = "run.dispatched"
	// LifecycleBeforeCapability is emitted before one node dispatch.
	LifecycleBeforeCapability LifecycleEvent = "capability.before"
	// LifecycleArtifactSubmitted is emitted when a node's artifacts are
	// durable in the ledger (post completeAttempt, pre-delivery).
	LifecycleArtifactSubmitted LifecycleEvent = "artifact.submitted"
	// LifecycleAfterCapability is emitted after a node fully commits
	// (artifacts + usage + delivery aftermath).
	LifecycleAfterCapability LifecycleEvent = "capability.after"
	// LifecycleCheckpointBefore is emitted before each checkpoint save (the
	// BeforeQualityGate/BeforeCommit moments of the reference sequence).
	LifecycleCheckpointBefore LifecycleEvent = "checkpoint.before"
	// Terminal events close the run.
	LifecycleRunCompleted LifecycleEvent = "run.completed"
	LifecycleRunPaused    LifecycleEvent = "run.paused"
	LifecycleRunFailed    LifecycleEvent = "run.failed"
	LifecycleRunCancelled LifecycleEvent = "run.cancelled"
)

// LifecycleSnapshot is the read-only, value-typed view of one lifecycle
// moment. Artifacts appear as hash+ref projections; nothing here can reach
// the kernel store (guarded by TestLifecycleSnapshotHasNoWriteSurface).
type LifecycleSnapshot struct {
	RunID          string
	DocumentID     string
	PlanID         string
	PlanVersion    int
	NodeID         string
	Capability     string
	Attempt        int
	State          RunState
	CompletedNodes []string
	CostUSD        float64
	DurationMS     int64
	// ArtifactTypes lists the artifact types involved at artifact-bearing
	// moments (full_draft, quality_report, …) — types, not bodies, so hooks
	// stay light and cannot move canonical bytes.
	ArtifactTypes []string
}

// RuntimeHook is a read-only lifecycle observer.
type RuntimeHook interface {
	ObserveLifecycle(ctx context.Context, event LifecycleEvent, snapshot LifecycleSnapshot)
}

// RuntimeHookFunc adapts a function to RuntimeHook.
type RuntimeHookFunc func(ctx context.Context, event LifecycleEvent, snapshot LifecycleSnapshot)

func (function RuntimeHookFunc) ObserveLifecycle(ctx context.Context, event LifecycleEvent, snapshot LifecycleSnapshot) {
	function(ctx, event, snapshot)
}

// hookBus fans lifecycle events out to ordered observers. Emission is
// synchronous (deterministic ordering for tests and audit), panics inside a
// hook are contained and logged — a broken hook must never fail a run.
type hookBus struct {
	mu    sync.RWMutex
	hooks []RuntimeHook
}

func newHookBus(hooks ...RuntimeHook) *hookBus {
	return &hookBus{hooks: append([]RuntimeHook(nil), hooks...)}
}

// register appends an observer.
func (bus *hookBus) register(hook RuntimeHook) {
	if hook == nil {
		return
	}
	bus.mu.Lock()
	defer bus.mu.Unlock()
	bus.hooks = append(bus.hooks, hook)
}

// emit fans the event out in registration order. Containment: a panicking
// hook is logged and skipped; the remaining hooks still run.
func (bus *hookBus) emit(ctx context.Context, event LifecycleEvent, snapshot LifecycleSnapshot) {
	if bus == nil {
		return
	}
	bus.mu.RLock()
	hooks := append([]RuntimeHook(nil), bus.hooks...)
	bus.mu.RUnlock()
	for _, hook := range hooks {
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					slog.Error("lifecycle hook panicked", "event", string(event), "panic", recovered)
				}
			}()
			hook.ObserveLifecycle(ctx, event, snapshot)
		}()
	}
}

// lifecycleTelemetryHook projects lifecycle moments onto the bounded
// RuntimeMetric stream — the first hook consumer, keeping the M2 telemetry
// surface and the M3 bus in one pipeline.
type lifecycleTelemetryHook struct{ telemetry RuntimeTelemetry }

func (hook lifecycleTelemetryHook) ObserveLifecycle(ctx context.Context, event LifecycleEvent, snapshot LifecycleSnapshot) {
	observeRuntime(ctx, hook.telemetry, RuntimeMetric{Kind: MetricLifecycle,
		Capability: snapshot.Capability, Status: string(event), Reason: string(snapshot.State),
		CostUSD: snapshot.CostUSD, DurationMS: snapshot.DurationMS})
}

// terminalLifecycleEvent maps outcome errors to terminal lifecycle events so
// call sites stay declarative.
func terminalLifecycleEvent(err error) LifecycleEvent {
	switch {
	case err == nil:
		return LifecycleRunCompleted
	case err == ErrRunCancelled:
		return LifecycleRunCancelled
	case err == ErrRunPaused || err == ErrApprovalRequired || err == ErrHumanRecoveryRequired:
		return LifecycleRunPaused
	default:
		return LifecycleRunFailed
	}
}

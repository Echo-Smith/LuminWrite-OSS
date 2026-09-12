package server

import (
	"context"
	"fmt"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingruntime"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
	"log/slog"
	"sync"
	"time"
)

type governedRunTrigger struct {
	orchestrator *writingruntime.Orchestrator
	store        *writingstore.Store
	mu           sync.Mutex
	parent       context.Context
	started      map[string]bool
}

func (t *governedRunTrigger) TriggerAfterApproval(runID string) {
	t.launch(context.Background(), runID)
}

func (t *governedRunTrigger) launch(parent context.Context, runID string) {
	if t == nil || t.orchestrator == nil || t.store == nil {
		return
	}
	t.mu.Lock()
	if t.parent != nil {
		parent = t.parent
	}
	if t.started == nil {
		t.started = map[string]bool{}
	}
	// Bound active workers and dedicated lock connections. Durable queue is
	// scanned periodically, so saturation never loses an approved run.
	if t.started[runID] || len(t.started) >= 2 {
		t.mu.Unlock()
		return
	}
	t.started[runID] = true
	t.mu.Unlock()
	go func() {
		defer func() { t.mu.Lock(); delete(t.started, runID); t.mu.Unlock() }()
		ctx, cancel := context.WithTimeout(parent, 30*time.Minute)
		defer cancel()
		_, err := t.store.WithRunExecutionLock(ctx, runID, func(owned context.Context) error {
			run, err := t.store.LoadRuntimeRun(owned, runID)
			if err != nil {
				return err
			}
			if run.Status == "pausing" || run.Status == "cancelling" {
				return t.finishIdleControl(owned, run)
			}
			if run.Status != "running" && run.Status != "planned" {
				return nil
			}
			stop := watchGovernedControl(owned, t.store, t.orchestrator, runID)
			defer stop()
			result, err := t.orchestrator.Execute(owned, runID)
			if err != nil {
				slog.Warn("governed run ended", "run_id", runID, "state", result.State, "error", err)
				// Early infrastructure/input failures must not spin the persistent queue.
				current, loadErr := t.store.LoadRuntimeRun(owned, runID)
				if loadErr == nil && (current.Status == "planned" || current.Status == "running") {
					if current.Status == "planned" {
						_, transitionErr := t.orchestrator.State.Transition(owned, writingruntime.TransitionRequest{CommandID: writingstore.StableID("command_", runID, "failed-dispatch-start", fmt.Sprint(current.LastEventSequence)), RunID: runID, From: writingruntime.StatePlanned, To: writingruntime.StateRunning, Cause: "dispatch_failure_recovery", Summary: "Record dispatch failure", Actor: writingstore.Actor{Type: writingstore.ActorSystem, ID: "governed.worker"}})
						if transitionErr != nil {
							return transitionErr
						}
						current.Status = "running"
					}
					to := writingruntime.StatePausing
					_, _ = t.orchestrator.State.Transition(owned, writingruntime.TransitionRequest{CommandID: writingstore.StableID("command_", runID, "dispatch-failed", fmt.Sprint(current.LastEventSequence)), RunID: runID, From: writingruntime.RunState(current.Status), To: to, Cause: "dispatch_failed", ReasonCode: "dispatch_failed", Summary: "Execution requires inspection before retry", Actor: writingstore.Actor{Type: writingstore.ActorSystem, ID: "governed.worker"}})
					if to == writingruntime.StatePausing {
						current.Status = "pausing"
						_ = t.finishIdleControl(owned, current)
					}
				}
			}
			return err
		})
		if err != nil && ctx.Err() == nil {
			slog.Warn("governed worker", "run_id", runID, "error", err)
		}
	}()
}
func (t *governedRunTrigger) finishIdleControl(ctx context.Context, run writingstore.RuntimeRun) error {
	target := writingruntime.StatePaused
	if run.Status == "cancelling" {
		target = writingruntime.StateCancelled
	}
	_, err := t.orchestrator.State.Transition(ctx, writingruntime.TransitionRequest{CommandID: writingstore.StableID("command_", run.RunID, "idle-control", fmt.Sprint(run.LastEventSequence)), RunID: run.RunID, From: writingruntime.RunState(run.Status), To: target, Cause: "recovered_control", Summary: "Persisted control completed under exclusive ownership", Actor: writingstore.Actor{Type: writingstore.ActorSystem, ID: "governed.worker"}})
	return err
}

// TriggerAfterGateDecision resumes a paused run right after a gate decision
// committed (design.md §5.3 route A: API-direct trigger). The database state
// stays authoritative: the gate-resume scan below re-drives the same resume
// after process restarts, so a lost in-memory trigger never strands a
// decided run.
func (t *governedRunTrigger) TriggerAfterGateDecision(runID string) {
	t.launchGateResume(context.Background(), runID)
}

func (t *governedRunTrigger) launchGateResume(parent context.Context, runID string) {
	if t == nil || t.orchestrator == nil || t.store == nil {
		return
	}
	t.mu.Lock()
	if t.parent != nil {
		parent = t.parent
	}
	if t.started == nil {
		t.started = map[string]bool{}
	}
	if t.started[runID] || len(t.started) >= 2 {
		t.mu.Unlock()
		return
	}
	t.started[runID] = true
	t.mu.Unlock()
	go func() {
		defer func() { t.mu.Lock(); delete(t.started, runID); t.mu.Unlock() }()
		ctx, cancel := context.WithTimeout(parent, 30*time.Minute)
		defer cancel()
		_, err := t.store.WithRunExecutionLock(ctx, runID, func(owned context.Context) error {
			run, err := t.store.LoadRuntimeRun(owned, runID)
			if err != nil {
				return err
			}
			if run.Status == "pausing" || run.Status == "cancelling" {
				return t.finishIdleControl(owned, run)
			}
			// Only paused runs with every gate decided reach here; the
			// orchestrator's own resume guard re-blocks pending gates.
			if run.Status != "paused" {
				return nil
			}
			stop := watchGovernedControl(owned, t.store, t.orchestrator, runID)
			defer stop()
			_, err = t.orchestrator.Resume(owned, runID,
				writingstore.StableID("command_", runID, "gate_resume", fmt.Sprint(run.LastEventSequence)),
				writingstore.Actor{Type: writingstore.ActorSystem, ID: "governed.worker"})
			if err != nil {
				slog.Warn("governed gate resume ended", "run_id", runID, "state", err)
			}
			return err
		})
		if err != nil && ctx.Err() == nil {
			slog.Warn("governed worker gate resume", "run_id", runID, "error", err)
		}
	}()
}
func (t *governedRunTrigger) Serve(ctx context.Context) {
	t.mu.Lock()
	t.parent = ctx
	t.mu.Unlock()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		ids, err := t.store.DispatchableRunIDs(ctx, 100)
		if err == nil {
			for _, id := range ids {
				t.launch(ctx, id)
			}
		} else if ctx.Err() == nil {
			slog.Warn("governed recovery scan failed", "error", err)
		}
		// Gate-resume scan (design.md §5.3): paused runs whose gates are all
		// decided — the durable backstop when the API-direct trigger lost a
		// race with a restart.
		resumable, err := t.store.GateResumableRunIDs(ctx, 100)
		if err == nil {
			for _, id := range resumable {
				t.launchGateResume(ctx, id)
			}
		} else if ctx.Err() == nil {
			slog.Warn("governed gate-resume scan failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func watchGovernedControl(ctx context.Context, store *writingstore.Store, o *writingruntime.Orchestrator, runID string) context.CancelFunc {
	watched, cancel := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-watched.Done():
				return
			case <-ticker.C:
				run, err := store.LoadRuntimeRun(watched, runID)
				if err == nil {
					o.NotifyPersistedControl(runID, run.Status)
				}
			}
		}
	}()
	return cancel
}

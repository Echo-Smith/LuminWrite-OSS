package server

// WallClockResearchBudgetBoundary (T09, T06 遗留 #2; F2 2026-09-08 review
// fix): the production executor-level budget guard for the research read
// workset loop. The proactive execution budget (design.md §7) is wall-clock
// time spent actively executing the run's nodes. The guard is a PURE ledger
// function: spent = Σ(completed attempt actual_duration_ms)
//        + Σ(research sub-task usage duration_ms)
//        + in-flight attempt time (started_at .. now)
// Human-gate waits never record an attempt duration, so waiting for a user
// decision is naturally free; the ceiling is the run's plan budget
// MaxDurationMS (the budget CreateRun validated and persisted — a changed
// budget means a new run, never a live mutation). When the plan budget
// carries no explicit MaxDurationMS the design default applies: the whole
// proactive execution budget is 30 minutes (ResearchBudgetDefaultMS).
//
// Design.md §3 boundary semantics (F2): there is NO fire-once. The verdict is
// recomputed from the ledger on every check, so the boundary state persists
// with the ledger itself — a plain resume with an unchanged budget re-fires
// immediately (the owner must create a new contract version + run to raise
// the budget). While the boundary holds, the caller stops scheduling paid
// sub-calls; the read executor decides between a partial pack (enough
// citable sources → evidence gate) and an INSUFFICIENT_EVIDENCE pause.
//
// Fail-closed: a ledger read failure reports the boundary as reached. The
// guard can only ever STOP paid sub-calls, so its failure mode must be
// stop, not go.

import (
	"context"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingruntime"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// ResearchBudgetDefaultMS is the default proactive execution budget when the
// run's plan budget carries no explicit MaxDurationMS (design.md §7: 整个主动
// 执行预算 30 分钟).
const ResearchBudgetDefaultMS = 30 * 60 * 1000

// researchUsageDurationKey is the research sub-task ledger's duration entry
// (usage_json → {"duration_ms": ...}, written by CompleteResearchTask
// callers that track per-phase wall time).
const researchUsageDurationKey = "duration_ms"

// ResearchBudgetLedger is the store surface the guard reads. *writingstore.Store
// implements it.
type ResearchBudgetLedger interface {
	ListRunAttempts(context.Context, string) ([]writingstore.NodeAttempt, error)
	ListResearchTasks(context.Context, string, string) ([]writingstore.ResearchTask, error)
	LoadRuntimeRun(context.Context, string) (writingstore.RuntimeRun, error)
}

// WallClockResearchBudgetBoundary implements writingruntime.ResearchBudgetBoundary.
// It recomputes the spent budget from the run ledger on every check — no
// in-process fired marker, no forced-spent override: the verdict is a pure
// function of persisted facts (attempt rows, sub-task usage, run budget), so
// resumes and process restarts cannot bypass it.
type WallClockResearchBudgetBoundary struct {
	store ResearchBudgetLedger
	// now is swappable for tests (injectable clock).
	now func() time.Time
}

// NewWallClockResearchBudgetBoundary wires the guard over the run store.
func NewWallClockResearchBudgetBoundary(store ResearchBudgetLedger) *WallClockResearchBudgetBoundary {
	return &WallClockResearchBudgetBoundary{store: store, now: func() time.Time { return time.Now().UTC() }}
}

// ResearchBudgetBoundaryReached reports whether the run's accumulated active
// execution time has reached the plan budget's MaxDurationMS. The executor
// calls this between papers only, so a paper in flight always completes —
// but its ACTIVE time is already counted via the attempt's started_at.
func (guard *WallClockResearchBudgetBoundary) ResearchBudgetBoundaryReached(ctx context.Context, request writingruntime.ExecutionRequest, _, _ int) (bool, string) {
	spentMS, err := guard.spentMS(ctx, request, guard.now())
	if err != nil {
		// Fail-closed (F2): an unreadable ledger must stop new paid sub-calls.
		// The executor's boundary branch still lets a sufficient workset reach
		// the evidence gate through its already-read results.
		return true, "proactive execution budget ledger could not be read (fail-closed)"
	}
	limitMS, limitErr := guard.limitMS(ctx, request.RunID)
	if limitErr != nil {
		return true, "proactive execution budget could not be loaded (fail-closed)"
	}
	if limitMS <= 0 || spentMS < limitMS {
		return false, ""
	}
	return true, "proactive execution budget reached (accumulated active execution time)"
}

// spentMS sums the run's persisted active execution time: every attempt's
// recorded duration plus the active time of the IN-FLIGHT attempt(s)
// (status running, started_at without a completion) — the long read attempt
// between papers can no longer blind the guard.
func (guard *WallClockResearchBudgetBoundary) spentMS(ctx context.Context, request writingruntime.ExecutionRequest, now time.Time) (int64, error) {
	runID := request.RunID
	attempts, err := guard.store.ListRunAttempts(ctx, runID)
	if err != nil {
		return 0, err
	}
	var spent int64
	for _, attempt := range attempts {
		spent += attempt.ActualDurationMS
		// Only a genuinely in-flight attempt contributes live time. Paused /
		// expired rows keep their recorded duration (if any) but must not
		// accrue wall time while the run waits for a human decision —
		// human-gate waiting is free (design.md §7).
		if attempt.Status == "running" && !attempt.StartedAt.IsZero() {
			if active := now.Sub(attempt.StartedAt).Milliseconds(); active > 0 {
				spent += active
			}
		}
	}
	// Persisted research sub-task usage (design.md §6 usage_json): the
	// durable per-phase call cost, counted even when the owning attempt row
	// was replayed or lost its duration.
	tasks, err := guard.store.ListResearchTasks(ctx, runID, request.UserID)
	if err != nil {
		return 0, err
	}
	for _, task := range tasks {
		spent += researchTaskUsageMS(task)
	}
	return spent, nil
}

// limitMS resolves the run's budget ceiling (plan budget MaxDurationMS; the
// design default when the budget carries none).
func (guard *WallClockResearchBudgetBoundary) limitMS(ctx context.Context, runID string) (int64, error) {
	run, err := guard.store.LoadRuntimeRun(ctx, runID)
	if err != nil {
		return 0, err
	}
	if run.Budget.MaxDurationMS > 0 {
		return run.Budget.MaxDurationMS, nil
	}
	return ResearchBudgetDefaultMS, nil
}

// researchTaskUsageMS extracts the persisted duration from one sub-task's
// usage map; zero when the ledger row carries no duration entry.
func researchTaskUsageMS(task writingstore.ResearchTask) int64 {
	if task.Usage == nil {
		return 0
	}
	switch value := task.Usage[researchUsageDurationKey].(type) {
	case float64:
		if value > 0 {
			return int64(value)
		}
	case int64:
		if value > 0 {
			return value
		}
	case int:
		if value > 0 {
			return int64(value)
		}
	}
	return 0
}

// isTerminalAttemptStatus reports whether an attempt row already finalized
// (its duration is recorded and its dispatch window is closed).
func isTerminalAttemptStatus(status string) bool {
	switch status {
	case "succeeded", "failed", "paused", "cancelled", "pending", "expired":
		return true
	}
	return false
}

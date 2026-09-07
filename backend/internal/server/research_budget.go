package server

// WallClockResearchBudgetBoundary (T09, T06 遗留 #2): the production
// executor-level budget guard for the research read workset loop. The
// proactive execution budget (design.md §7) is wall-clock time spent actively
// executing the run's nodes: the sum of every node attempt's
// actual_duration_ms. Human-gate waits never record an attempt duration, so
// waiting for a user decision is naturally free; the ceiling is the run's
// plan budget MaxDurationMS (the budget CreateRun validated and persisted —
// a changed budget means a new run, never a live mutation). When the plan
// budget carries no explicit MaxDurationMS the design default applies: the
// whole proactive execution budget is 30 minutes.
//
// When the accumulated active time reaches the ceiling the guard fires
// between papers (the ResearchReadExecutor's sentinel): the node pauses with
// RESEARCH_BUDGET_BOUNDARY, completed sub-tasks stay in the ledger, and the
// plain resume continues the remaining papers (触界 → 干净暂停 → 恢复续跑).
//
// Fire-once per run+node chain, durably: the boundary pause itself records a
// node.paused event carrying RESEARCH_BUDGET_BOUNDARY, and the guard treats
// that event as the durable "already fired" marker. The owner's resume is
// therefore the decision to continue past the boundary (上调数量/预算走新合同
// 版本 + 新运行 — a resume never re-imposes the same ceiling mid-run, which
// would make it impossible to finish the remaining papers).

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingruntime"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// ResearchBudgetDefaultMS is the default proactive execution budget when the
// run's plan budget carries no explicit MaxDurationMS (design.md §7: 整个主动
// 执行预算 30 分钟).
const ResearchBudgetDefaultMS = 30 * 60 * 1000

// WallClockResearchBudgetBoundary implements writingruntime.ResearchBudgetBoundary.
// It reads the run's node-attempt ledger on every check — attempt completions
// are terminal rows, so a resume re-dispatch naturally accounts for the time
// already spent, including across process restarts.
type WallClockResearchBudgetBoundary struct {
	store *writingstore.Store
	// now is swappable for tests (injectable clock).
	now func() time.Time
	// Test seam: force the accumulated duration (and skip the ledger read).
	// A negative value disables the guard entirely.
	overrideMu    sync.Mutex
	forcedSpentMS int64
	// fired chains (runID|nodeID) already observed firing in this process —
	// the fast path behind the durable event marker.
	firedMu sync.Mutex
	fired   map[string]bool
}

// NewWallClockResearchBudgetBoundary wires the guard over the run store.
func NewWallClockResearchBudgetBoundary(store *writingstore.Store) *WallClockResearchBudgetBoundary {
	return &WallClockResearchBudgetBoundary{store: store, now: func() time.Time { return time.Now().UTC() },
		fired: map[string]bool{}}
}

// SetForcedSpentMS forces the accumulated active duration the next checks
// report (test seam for 触界 without waiting real time). A negative value
// disables the guard; zero restores ledger reading.
func (guard *WallClockResearchBudgetBoundary) SetForcedSpentMS(ms int64) {
	guard.overrideMu.Lock()
	guard.forcedSpentMS = ms
	guard.overrideMu.Unlock()
}

// ResearchBudgetBoundaryReached reports whether the run's accumulated active
// execution time has reached the plan budget's MaxDurationMS. The executor
// calls this between papers only, so a paper in flight always completes.
func (guard *WallClockResearchBudgetBoundary) ResearchBudgetBoundaryReached(ctx context.Context, request writingruntime.ExecutionRequest, _, _ int) (bool, string) {
	guard.overrideMu.Lock()
	forced := guard.forcedSpentMS
	disabled := forced < 0
	guard.overrideMu.Unlock()
	if disabled {
		return false, ""
	}
	spentMS := forced
	if forced == 0 {
		attempts, err := guard.store.ListRunAttempts(ctx, request.RunID)
		if err != nil {
			// A ledger read failure must never fail the node: fail open (keep
			// going) and let the store error surface through its own paths.
			return false, ""
		}
		for _, attempt := range attempts {
			spentMS += attempt.ActualDurationMS
		}
	}
	limitMS := int64(ResearchBudgetDefaultMS)
	if run, err := guard.store.LoadRuntimeRun(ctx, request.RunID); err == nil && run.Budget.MaxDurationMS > 0 {
		limitMS = run.Budget.MaxDurationMS
	}
	if limitMS <= 0 || spentMS < limitMS {
		return false, ""
	}
	if guard.alreadyFired(ctx, request.RunID, request.NodeID) {
		// The boundary already paused this run's node (durably, possibly in a
		// previous process); the owner resumed, which is the decision to
		// continue the remaining papers.
		return false, ""
	}
	guard.markFired(request.RunID, request.NodeID)
	return true, "proactive execution budget reached (accumulated active execution time)"
}

// alreadyFired reports whether a RESEARCH_BUDGET_BOUNDARY pause event for the
// node is already on the run's event ledger (the durable fire-once marker).
func (guard *WallClockResearchBudgetBoundary) alreadyFired(ctx context.Context, runID, nodeID string) bool {
	chain := runID + "|" + nodeID
	guard.firedMu.Lock()
	if guard.fired == nil {
		guard.fired = map[string]bool{}
	}
	if guard.fired[chain] {
		guard.firedMu.Unlock()
		return true
	}
	guard.firedMu.Unlock()
	events, err := guard.store.ListRunEvents(ctx, runID, 0, 500)
	if err != nil {
		return false
	}
	for _, event := range events {
		if event.NodeID != nodeID || (event.EventType != "node.paused" && event.EventType != "node.failed") {
			continue
		}
		payload, marshalErr := json.Marshal(event.Payload)
		if marshalErr != nil {
			continue
		}
		var decoded struct {
			ErrorCode string `json:"error_code"`
		}
		if json.Unmarshal(payload, &decoded) == nil && decoded.ErrorCode == string(writingruntime.CodeResearchBudgetBoundary) {
			guard.markFired(runID, nodeID)
			return true
		}
	}
	return false
}

func (guard *WallClockResearchBudgetBoundary) markFired(runID, nodeID string) {
	guard.firedMu.Lock()
	if guard.fired == nil {
		guard.fired = map[string]bool{}
	}
	guard.fired[runID+"|"+nodeID] = true
	guard.firedMu.Unlock()
}

// compile-time interface check
var _ writingruntime.ResearchBudgetBoundary = (*WallClockResearchBudgetBoundary)(nil)

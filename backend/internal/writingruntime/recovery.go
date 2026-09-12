package writingruntime

import (
	"errors"
	"fmt"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

var (
	ErrPlanChangedDuringRecovery = errors.New("writingruntime: checkpoint plan changed")
	ErrHumanRecoveryRequired     = errors.New("writingruntime: human recovery required")
)

type RecoveryState struct {
	CompletedNodes  map[string]int
	NextAttempts    map[string]int
	HumanRequired   []string
	SpentCostUSD    float64
	SpentDurationMS int64
}

// nodeAttemptOutcome aggregates one plan node's attempt-ledger evidence for
// the unsafe-in-flight reconciliation: live means an attempt whose outcome is
// still unresolved (running, leased, or expired); terminal means the node's
// latest work reached a final ledger status (succeeded, failed, paused, or
// cancelled).
type nodeAttemptOutcome struct {
	live     bool
	terminal bool
}

func Recover(plan writingplan.ExecutablePlan, planVersion int, checkpoint *Checkpoint, attempts []writingstore.NodeAttempt, manifests map[string]writingplan.CapabilityManifest) (RecoveryState, error) {
	state := RecoveryState{CompletedNodes: map[string]int{}, NextAttempts: map[string]int{}, HumanRequired: []string{}}
	outcomes := map[string]nodeAttemptOutcome{}
	if checkpoint != nil {
		if checkpoint.PlanID != plan.PlanID || checkpoint.PlanVersion != planVersion || checkpoint.PlanHash != plan.PlanHash {
			return RecoveryState{}, ErrPlanChangedDuringRecovery
		}
		for nodeID, attempt := range checkpoint.CompletedNodes {
			state.CompletedNodes[nodeID] = attempt
		}
		state.SpentCostUSD, state.SpentDurationMS = checkpoint.SpentCostUSD, checkpoint.SpentDurationMS
		state.HumanRequired = append(state.HumanRequired, checkpoint.UnsafeInFlight...)
	}
	for _, attempt := range attempts {
		if attempt.PlanID != plan.PlanID || attempt.PlanVersion != planVersion {
			continue
		}
		// The M1.0 delivery protocol's synthetic "initial" capture attempt is
		// bookkeeping, not a plan node: it neither completes a plan node nor
		// counts toward the loop-exit condition.
		if attempt.NodeID == InitialCaptureNodeID {
			continue
		}
		outcome := outcomes[attempt.NodeID]
		switch attempt.Status {
		case "running", "leased", "expired":
			outcome.live = true
		case "succeeded", "failed", "paused", "cancelled":
			outcome.terminal = true
		}
		outcomes[attempt.NodeID] = outcome
		if attempt.Attempt >= state.NextAttempts[attempt.NodeID] {
			state.NextAttempts[attempt.NodeID] = attempt.Attempt + 1
		}
		if attempt.Status == "succeeded" {
			_, checkpointed := state.CompletedNodes[attempt.NodeID]
			if attempt.Attempt > state.CompletedNodes[attempt.NodeID] {
				state.CompletedNodes[attempt.NodeID] = attempt.Attempt
			}
			if !checkpointed {
				state.SpentCostUSD += attempt.ActualCostUSD
				state.SpentDurationMS += attempt.ActualDurationMS
			}
			continue
		}
		if attempt.Status != "running" && attempt.Status != "leased" && attempt.Status != "expired" {
			continue
		}
		manifest, exists := manifests[attempt.CapabilityID]
		if !exists || manifest.Idempotency != writingplan.IdempotencySafe {
			state.HumanRequired = appendUniqueString(state.HumanRequired, attempt.NodeID)
		}
	}
	// T00 root-cause fix (docs/plans/2026-09-07-research-review-integration.md):
	// the failure-pause paths checkpoint the failed node as UnsafeInFlight, and
	// Recover used to merge that marker into HumanRequired unconditionally —
	// so a paused run could never be resumed past a terminally failed node:
	// every resume re-entered unsafe_recovery and re-paused with no state
	// change (the resume-after-pause dead end). A terminally recorded attempt
	// is no longer in flight, so the marker is stale: the ledger below already
	// re-flags genuinely unresolved attempts (running/leased/expired) for
	// non-idempotent-safe capabilities, and node bounds still cap how often a
	// resumed retry may run. Nodes without any terminal attempt (human-gate
	// pauses, which record no attempt) keep blocking human recovery.
	state.HumanRequired = dropResolvedUnsafeNodes(state.HumanRequired, outcomes)
	if len(state.HumanRequired) > 0 {
		return state, fmt.Errorf("%w: %v", ErrHumanRecoveryRequired, state.HumanRequired)
	}
	return state, nil
}

// dropResolvedUnsafeNodes removes a node from the human-required set when its
// attempt ledger shows terminal work and nothing live: the paused "in-flight"
// concern has since resolved into an inspectable outcome, and an explicit
// user resume may retry it within the node's bounds.
func dropResolvedUnsafeNodes(humanRequired []string, outcomes map[string]nodeAttemptOutcome) []string {
	if len(humanRequired) == 0 {
		return humanRequired
	}
	kept := make([]string, 0, len(humanRequired))
	for _, nodeID := range humanRequired {
		if outcome := outcomes[nodeID]; outcome.terminal && !outcome.live {
			continue
		}
		kept = append(kept, nodeID)
	}
	return kept
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

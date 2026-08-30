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

func Recover(plan writingplan.ExecutablePlan, planVersion int, checkpoint *Checkpoint, attempts []writingstore.NodeAttempt, manifests map[string]writingplan.CapabilityManifest) (RecoveryState, error) {
	state := RecoveryState{CompletedNodes: map[string]int{}, NextAttempts: map[string]int{}, HumanRequired: []string{}}
	planNodes := make(map[string]struct{}, len(plan.Nodes))
	for _, node := range plan.Nodes {
		planNodes[node.NodeID] = struct{}{}
	}
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
		// Ledger-owned pseudo attempts (for example node_initial) are valid
		// artifact lineage roots but are not executable plan nodes. They must
		// never satisfy graph dependencies or change retry accounting.
		if attempt.PlanID != plan.PlanID || attempt.PlanVersion != planVersion {
			continue
		}
		if len(planNodes) > 0 {
			if _, executable := planNodes[attempt.NodeID]; !executable {
				continue
			}
		}
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
	if len(state.HumanRequired) > 0 {
		return state, fmt.Errorf("%w: %v", ErrHumanRecoveryRequired, state.HumanRequired)
	}
	return state, nil
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

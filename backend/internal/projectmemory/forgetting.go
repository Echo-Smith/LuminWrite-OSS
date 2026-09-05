// Memory forgetting (roadmap §12, V2.9 item 12): the lifecycle policy that
// keeps ProjectMemory pools honest without ever silently deleting or
// weakening canon. Projecting the roadmap's strategy table onto the
// ProjectMemory object set:
//
//	Project Canon (facts)      → never auto-decays; validity intervals and
//	                             user-driven supersede are the only exits
//	Staged / candidate objects → archived past the candidate horizon (fact
//	                              lane candidates are rejected — that lane's
//	                              terminal state)
//	Claims (advisory)          → rejected when uncorroborated past the decay
//	                             horizon; terminal claims stay as history
//	Superseded decisions       → archived past the cold horizon
//
// The policy is a pure, versioned decision table. Applying it is a store
// operation that writes an append-only forgetting log (migration 103); the
// policy hash binds the log rows so evidence can replay exactly which rule
// forgot what. Canon facts have no horizon field and no decision branch —
// the type system keeps the invariant from being a runtime check.
package projectmemory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// ForgetPolicyVersion pins the decision table: rule names, horizons, and the
// covered object set. Changing any of them is a new version, never an
// in-place edit — forgetting log rows reference the version and hash that
// produced them.
const ForgetPolicyVersion = 1

// ForgetAction names what the policy says about one object.
type ForgetAction string

const (
	// ForgetKeep leaves the object alone: it is either canon (never
	// forgotten), still fresh, or already in a terminal state that
	// compilation excludes.
	ForgetKeep ForgetAction = "keep"
	// ForgetArchive moves a stale candidate or cold terminal object to its
	// archived status, freeing any unique-slot pressure it holds.
	ForgetArchive ForgetAction = "archive"
	// ForgetReject decays an advisory object into its rejected terminal
	// state (fact-lane candidates and claims).
	ForgetReject ForgetAction = "reject"
)

// Rule names are bounded identifiers recorded in the forgetting log.
const (
	RuleStaleCandidate  = "stale_candidate"
	RuleClaimDecay      = "claim_decay"
	RuleColdSuperseded  = "cold_superseded"
	RuleCanonPreserved  = "canon_preserved"
	RuleTerminalHistory = "terminal_history"
)

// ForgetDecision is the policy's verdict on one object.
type ForgetDecision struct {
	Action ForgetAction `json:"action"`
	Rule   string       `json:"rule"`
}

// Keep is the benign verdict: no transition, no log row.
func Keep(rule string) ForgetDecision { return ForgetDecision{Action: ForgetKeep, Rule: rule} }

// ForgetPolicy is the versioned lifecycle configuration. Zero-value horizons
// mean "never forget that class" — forgetting is opt-in per horizon, so an
// unset policy can only keep.
type ForgetPolicy struct {
	// CandidateHorizon retires staged candidates (fact-lane staged rows,
	// terminology / decision / thread / entity candidates) older than the
	// horizon.
	CandidateHorizon time.Duration `json:"candidate_horizon_ns,omitempty"`
	// ClaimDecayHorizon rejects open/supported claims whose last touch
	// (staging or corroboration) is older than the horizon.
	ClaimDecayHorizon time.Duration `json:"claim_decay_horizon_ns,omitempty"`
	// DecisionColdHorizon archives superseded decisions whose supersede
	// time is older than the horizon.
	DecisionColdHorizon time.Duration `json:"decision_cold_horizon_ns,omitempty"`
}

// DefaultForgetPolicy is the shipped v1 configuration: a month of candidate
// patience, two months of claim patience, a quarter of cold-decision
// patience. Deliberately conservative — forgetting is hygiene, not urgency.
func DefaultForgetPolicy() ForgetPolicy {
	return ForgetPolicy{
		CandidateHorizon:    30 * 24 * time.Hour,
		ClaimDecayHorizon:   60 * 24 * time.Hour,
		DecisionColdHorizon: 90 * 24 * time.Hour,
	}
}

// Hash binds the exact configuration into the forgetting log: any horizon
// change is a different hash, so replayed sweeps are attributable to the
// policy that ran them.
func (policy ForgetPolicy) Hash() (string, error) {
	payload, err := json.Marshal(policy)
	if err != nil {
		return "", fmt.Errorf("marshal forget policy: %w", err)
	}
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// DecideFact is the canon invariant: facts never auto-decay, regardless of
// age. The function exists so tests can pin the invariant and callers can
// log the rule name; it has no horizon input by design.
func (policy ForgetPolicy) DecideFact() ForgetDecision {
	return Keep(RuleCanonPreserved)
}

// DecideCandidate covers every staged/candidate lane: fact-lane staged rows,
// terminology, decision, thread, and entity candidates. The action differs
// by lane (fact-lane rejects, curated lanes archive) — the caller supplies
// the lane's terminal semantics.
func (policy ForgetPolicy) DecideCandidate(stagedAt, now time.Time) ForgetDecision {
	if policy.CandidateHorizon > 0 && now.Sub(stagedAt) >= policy.CandidateHorizon {
		return ForgetDecision{Action: ForgetArchive, Rule: RuleStaleCandidate}
	}
	return Keep(RuleStaleCandidate)
}

// DecideClaim covers the advisory claims lane. Only open and supported
// claims can decay; promoted claims have already become canon history and
// rejected claims are already terminal. lastTouched is the claim's updated
// timestamp — corroboration refreshes it, so an actively evidenced claim
// survives.
func (policy ForgetPolicy) DecideClaim(status string, lastTouched, now time.Time) ForgetDecision {
	switch status {
	case "open", "supported":
		if policy.ClaimDecayHorizon > 0 && now.Sub(lastTouched) >= policy.ClaimDecayHorizon {
			return ForgetDecision{Action: ForgetReject, Rule: RuleClaimDecay}
		}
		return Keep(RuleClaimDecay)
	default:
		return Keep(RuleTerminalHistory)
	}
}

// DecideDecision covers the settled-decisions lane: candidates retire with
// the candidate horizon, superseded decisions go cold and archive, active
// decisions are settled calls that only a newer decision (or a user) may
// retire.
func (policy ForgetPolicy) DecideDecision(status string, lastTouched, now time.Time) ForgetDecision {
	switch status {
	case "candidate":
		if policy.CandidateHorizon > 0 && now.Sub(lastTouched) >= policy.CandidateHorizon {
			return ForgetDecision{Action: ForgetArchive, Rule: RuleStaleCandidate}
		}
		return Keep(RuleStaleCandidate)
	case "superseded":
		if policy.DecisionColdHorizon > 0 && now.Sub(lastTouched) >= policy.DecisionColdHorizon {
			return ForgetDecision{Action: ForgetArchive, Rule: RuleColdSuperseded}
		}
		return Keep(RuleColdSuperseded)
	default:
		return Keep(RuleTerminalHistory)
	}
}

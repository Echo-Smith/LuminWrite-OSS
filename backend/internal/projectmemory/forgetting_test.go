package projectmemory

import (
	"strings"
	"testing"
	"time"
)

func TestForgetPolicyCanonIsNeverForgotten(t *testing.T) {
	// The invariant the whole policy hangs on (roadmap §12: Canon 不能因为
	// 时间过去而被自动遗忘): facts have no horizon and no decay branch.
	policy := DefaultForgetPolicy()
	decision := policy.DecideFact()
	if decision.Action != ForgetKeep || decision.Rule != RuleCanonPreserved {
		t.Fatalf("canon decision=%#v", decision)
	}
	// No configuration of horizons can change that: the type has no fact
	// horizon to set, and an aggressive policy keeps the same verdict.
	aggressive := ForgetPolicy{CandidateHorizon: time.Nanosecond, ClaimDecayHorizon: time.Nanosecond, DecisionColdHorizon: time.Nanosecond}
	if got := aggressive.DecideFact(); got.Action != ForgetKeep {
		t.Fatalf("aggressive policy forgot canon: %#v", got)
	}
}

func TestForgetPolicyCandidateHorizon(t *testing.T) {
	policy := DefaultForgetPolicy()
	now := time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC)
	if decision := policy.DecideCandidate(now.Add(-29*24*time.Hour), now); decision.Action != ForgetKeep {
		t.Fatalf("fresh candidate retired: %#v", decision)
	}
	decision := policy.DecideCandidate(now.Add(-31*24*time.Hour), now)
	if decision.Action != ForgetArchive || decision.Rule != RuleStaleCandidate {
		t.Fatalf("stale candidate kept: %#v", decision)
	}
	// Zero horizon disables the class entirely.
	if decision := (ForgetPolicy{}).DecideCandidate(now.Add(-10*365*24*time.Hour), now); decision.Action != ForgetKeep {
		t.Fatalf("disabled horizon retired a candidate: %#v", decision)
	}
}

func TestForgetPolicyClaimDecay(t *testing.T) {
	policy := DefaultForgetPolicy()
	now := time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC)
	stale := now.Add(-61 * 24 * time.Hour)
	fresh := now.Add(-10 * 24 * time.Hour)
	// Open and supported claims decay when untouched past the horizon.
	for _, status := range []string{"open", "supported"} {
		if decision := policy.DecideClaim(status, stale, now); decision.Action != ForgetReject || decision.Rule != RuleClaimDecay {
			t.Fatalf("%s stale claim: %#v", status, decision)
		}
		if decision := policy.DecideClaim(status, fresh, now); decision.Action != ForgetKeep {
			t.Fatalf("%s fresh claim retired: %#v", status, decision)
		}
	}
	// Terminal claims are history, never re-decayed.
	for _, status := range []string{"promoted", "rejected"} {
		if decision := policy.DecideClaim(status, stale, now); decision.Action != ForgetKeep || decision.Rule != RuleTerminalHistory {
			t.Fatalf("%s terminal claim: %#v", status, decision)
		}
	}
}

func TestForgetPolicyDecisionLanes(t *testing.T) {
	policy := DefaultForgetPolicy()
	now := time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC)
	// Stale candidates archive.
	if decision := policy.DecideDecision("candidate", now.Add(-40*24*time.Hour), now); decision.Action != ForgetArchive || decision.Rule != RuleStaleCandidate {
		t.Fatalf("stale decision candidate: %#v", decision)
	}
	// Superseded decisions go cold then archive.
	if decision := policy.DecideDecision("superseded", now.Add(-10*24*time.Hour), now); decision.Action != ForgetKeep || decision.Rule != RuleColdSuperseded {
		t.Fatalf("warm superseded: %#v", decision)
	}
	decision := policy.DecideDecision("superseded", now.Add(-91*24*time.Hour), now)
	if decision.Action != ForgetArchive || decision.Rule != RuleColdSuperseded {
		t.Fatalf("cold superseded kept: %#v", decision)
	}
	// Active decisions are settled calls: untouched by the sweep.
	if decision := policy.DecideDecision("active", now.Add(-10*365*24*time.Hour), now); decision.Action != ForgetKeep || decision.Rule != RuleTerminalHistory {
		t.Fatalf("active decision retired: %#v", decision)
	}
}

func TestForgetPolicyHashBindsConfiguration(t *testing.T) {
	first, err := DefaultForgetPolicy().Hash()
	if err != nil {
		t.Fatal(err)
	}
	again, err := DefaultForgetPolicy().Hash()
	if err != nil {
		t.Fatal(err)
	}
	if first != again || !strings.HasPrefix(first, "sha256:") {
		t.Fatalf("hash unstable: %s vs %s", first, again)
	}
	changed := DefaultForgetPolicy()
	changed.CandidateHorizon += time.Hour
	other, err := changed.Hash()
	if err != nil {
		t.Fatal(err)
	}
	if other == first {
		t.Fatal("horizon change did not change the policy hash")
	}
}

package writingruntime

import "fmt"

// Shared definition of the governed allowlist policies used by the evidence
// accumulation harness and cmd/evidence-policy-dump: one scenario table so
// the runs, the dumped policy files, and therefore the promotion-gate policy
// hashes always describe the same governed nodes.

const EvidenceHarnessActivationKey = "change-local-evidence-accumulation"

type EvidenceScenarioPolicy struct {
	// Name is the harness scenario name ("evidence_" + Name).
	Name string
	// GovernedIndex is the plan node index bound to the allowlist policy.
	GovernedIndex int
	// NodeName is the expected plan node name at GovernedIndex.
	NodeName string
}

func EvidenceScenarioPolicies() []EvidenceScenarioPolicy {
	return []EvidenceScenarioPolicy{
		{Name: "long_form", GovernedIndex: 1, NodeName: "draft"},
		{Name: "multi_material", GovernedIndex: 1, NodeName: "synthesis"},
		{Name: "faithful_rewrite", GovernedIndex: 0, NodeName: "rewrite"},
	}
}

func EvidenceScenarioPolicyFor(name string) (EvidenceScenarioPolicy, bool) {
	for _, spec := range EvidenceScenarioPolicies() {
		if spec.Name == name {
			return spec, true
		}
	}
	return EvidenceScenarioPolicy{}, false
}

// allowlistPolicyForHarness derives the governed allowlist policy for one
// vertical node: identity fields stay aligned with the shadow candidate so
// bindRolloutPolicy accepts it, and the hash is stable across invocations
// because every identity field is deterministic.
func allowlistPolicyForHarness(capability, candidateID string) AdapterRolloutPolicy {
	policy := DefaultShadowPolicy(candidateID, AdapterFamilyEngine, capability, "1.0.0")
	policy.PolicyVersion = 2
	policy.Mode = RolloutAllowlist
	policy.ActivationKey = EvidenceHarnessActivationKey
	policy.AllowSubjects = []string{"user_operator_first"}
	policy.Reason = "local allowlist evidence accumulation; no production authorization"
	policy, _ = policy.WithComputedHash()
	return policy
}

// EvidenceGovernedPolicy builds the governed allowlist policy for a scenario
// exactly as the accumulation harness binds it at runtime.
func EvidenceGovernedPolicy(scenario string) (AdapterRolloutPolicy, bool) {
	spec, ok := EvidenceScenarioPolicyFor(scenario)
	if !ok {
		return AdapterRolloutPolicy{}, false
	}
	scenarioName := "evidence_" + spec.Name
	capability := "core.vertical." + scenarioName + "." + spec.NodeName
	candidateID := fmt.Sprintf("vertical.%s.candidate.%d", scenarioName, spec.GovernedIndex)
	return allowlistPolicyForHarness(capability, candidateID), true
}

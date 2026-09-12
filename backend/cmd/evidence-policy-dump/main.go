package main

// evidence-policy-dump writes the exact allowlist policy JSON files bound by
// the evidence accumulation harness, so governance-gate assesses and approves
// the same policy hashes that the runs produce. It performs no database or
// network access.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingruntime"
)

func main() {
	dir := flag.String("out", "", "output directory for <scenario>.json policy files")
	flag.Parse()
	if *dir == "" {
		fmt.Fprintln(os.Stderr, "evidence-policy-dump: -out directory is required")
		os.Exit(2)
	}
	if err := os.MkdirAll(*dir, 0o700); err != nil {
		fmt.Fprintln(os.Stderr, "evidence-policy-dump:", err)
		os.Exit(1)
	}
	encoder := json.NewEncoder(os.Stdout)
	for _, spec := range writingruntime.EvidenceScenarioPolicies() {
		policy, ok := writingruntime.EvidenceGovernedPolicy(spec.Name)
		if !ok {
			fmt.Fprintf(os.Stderr, "evidence-policy-dump: scenario %q has no governed policy\n", spec.Name)
			os.Exit(1)
		}
		if err := policy.Validate(); err != nil {
			fmt.Fprintf(os.Stderr, "evidence-policy-dump: scenario %q policy invalid: %v\n", spec.Name, err)
			os.Exit(1)
		}
		payload, err := json.MarshalIndent(policy, "", "  ")
		if err != nil {
			fmt.Fprintln(os.Stderr, "evidence-policy-dump:", err)
			os.Exit(1)
		}
		target := filepath.Join(*dir, spec.Name+".json")
		if err := os.WriteFile(target, payload, 0o600); err != nil {
			fmt.Fprintln(os.Stderr, "evidence-policy-dump:", err)
			os.Exit(1)
		}
		if err := encoder.Encode(map[string]string{"scenario": spec.Name, "policy_hash": policy.PolicyHash, "file": target}); err != nil {
			fmt.Fprintln(os.Stderr, "evidence-policy-dump:", err)
			os.Exit(1)
		}
	}
}

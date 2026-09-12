package server

import (
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/capability"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/editorial"
)

// TestUnifiedCapabilityResolverCoversSurfaces drives the M1.5 slice-1
// acceptance on a booted server: every configured surface is visible through
// one resolver, ids stay in their per-surface namespaces, and the
// single-authority guard passes.
func TestUnifiedCapabilityResolverCoversSurfaces(t *testing.T) {
	server, _, _ := newGovernedE2EServer(t)
	resolver, err := server.newCapabilityResolver()
	if err != nil {
		t.Fatal(err)
	}
	if err := resolver.CheckSingleAuthority(); err != nil {
		t.Fatalf("single authority violated: %v", err)
	}

	sources := map[string]bool{}
	for _, source := range resolver.Sources() {
		sources[string(source)] = true
	}
	// The governed catalog is always present; the tool and validator
	// surfaces are process defaults; editorial appears because the e2e
	// server initializes the editorial DAG when the DB is available.
	for _, want := range []string{"governed", "tool", "validator", "editorial"} {
		if !sources[want] {
			t.Fatalf("surface %q missing from resolver: %v", want, sources)
		}
	}

	// Spot checks per surface.
	if _, ok := resolver.ByID("core.draft.generate"); !ok {
		t.Fatal("governed capability missing")
	}
	if _, ok := resolver.ByID("validator.quality_gate"); !ok {
		t.Logf("note: validator ids seen: %v", resolver.ByClass("validation"))
	}
	if drafts := resolver.ByClass("writing.draft"); len(drafts) == 0 {
		t.Fatal("governed draft class missing from unified view")
	}
}

// TestUnifiedCapabilityGuardKeepsNamespacesDisjoint pins the guard
// semantics that keep the convergence from becoming a second authority: the
// per-surface prefix namespaces make cross-surface claims structurally
// inexpressible, and the guard verifies the assembled reality.
func TestUnifiedCapabilityGuardKeepsNamespacesDisjoint(t *testing.T) {
	server, _, _ := newGovernedE2EServer(t)
	resolver, err := server.newCapabilityResolver()
	if err != nil {
		t.Fatal(err)
	}
	// Sanity: the real surfaces pass.
	if err := resolver.CheckSingleAuthority(); err != nil {
		t.Fatalf("baseline surfaces must pass: %v", err)
	}
	// The prefix namespaces make cross-surface claims structurally
	// inexpressible: an editorial entry named after a governed capability
	// lands at editorial.agent.<id>, leaving the governed id unique.
	drifting, err := capability.NewResolver(
		governedCapabilityCatalog{registry: server.governedCapabilityRegistry()},
		editorialCapabilityCatalog{agents: &syntheticAgents{ids: []string{"core.draft.generate"}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := drifting.CheckSingleAuthority(); err != nil {
		t.Fatalf("namespaced entry must not clash: %v", err)
	}
	if _, ok := drifting.ByID("editorial.agent.core.draft.generate"); !ok {
		t.Fatal("drifting entry must live under the editorial namespace")
	}
	if manifest, ok := drifting.ByID("core.draft.generate"); !ok || manifest.Source != capability.SourceGoverned {
		t.Fatal("governed id must still resolve to the governed surface only")
	}
}

// syntheticAgents is a test-only editorial catalog source.
type syntheticAgents struct{ ids []string }

func (agents *syntheticAgents) ListAll() []*editorial.AgentConfig {
	configs := make([]*editorial.AgentConfig, 0, len(agents.ids))
	for _, id := range agents.ids {
		configs = append(configs, &editorial.AgentConfig{ID: id, Name: "drift", Role: "researcher"})
	}
	return configs
}

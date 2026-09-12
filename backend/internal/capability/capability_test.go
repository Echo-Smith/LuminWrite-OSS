package capability

import (
	"strings"
	"testing"
)

type staticCatalog struct {
	source    Source
	manifests []UnifiedManifest
}

func (catalog staticCatalog) Source() Source               { return catalog.source }
func (catalog staticCatalog) Manifests() []UnifiedManifest { return catalog.manifests }

func TestResolverAssemblesAndQueries(t *testing.T) {
	resolver, err := NewResolver(
		staticCatalog{source: SourceGoverned, manifests: []UnifiedManifest{{ID: "core.draft.generate", Class: "writing.draft", Source: SourceGoverned, Available: true}}},
		staticCatalog{source: SourceTool, manifests: []UnifiedManifest{{ID: "tool.search", Class: "retrieval", Source: SourceTool, Available: true}}},
		staticCatalog{source: SourceValidator, manifests: []UnifiedManifest{{ID: "validator.quality", Class: "validation", Source: SourceValidator}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if sources := resolver.Sources(); len(sources) != 3 || sources[0] != SourceGoverned {
		t.Fatalf("sources=%v", sources)
	}
	if _, ok := resolver.ByID("tool.search"); !ok {
		t.Fatal("tool manifest missing")
	}
	if _, ok := resolver.ByID("core.validation.quality"); ok {
		t.Fatal("absent manifest must not resolve")
	}
	drafts := resolver.ByClass("writing.draft")
	if len(drafts) != 1 || drafts[0].ID != "core.draft.generate" {
		t.Fatalf("by-class=%v", drafts)
	}
}

func TestResolverRejectsDuplicateSourceCatalogs(t *testing.T) {
	if _, err := NewResolver(
		staticCatalog{source: SourceTool},
		staticCatalog{source: SourceTool},
	); err == nil || !strings.Contains(err.Error(), "duplicate catalog") {
		t.Fatalf("duplicate source err=%v", err)
	}
	if _, err := NewResolver(staticCatalog{source: Source("mcp")}); err == nil ||
		!strings.Contains(err.Error(), "not a converged surface") {
		t.Fatalf("unknown source err=%v", err)
	}
}

func TestCheckSingleAuthority(t *testing.T) {
	resolver, err := NewResolver(
		staticCatalog{source: SourceGoverned, manifests: []UnifiedManifest{{ID: "core.draft.generate"}}},
		staticCatalog{source: SourceTool, manifests: []UnifiedManifest{{ID: "tool.core.draft.generate"}}},
		staticCatalog{source: SourceEditorial, manifests: []UnifiedManifest{{ID: "editorial.researcher"}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := resolver.CheckSingleAuthority(); err != nil {
		t.Fatalf("disjoint namespaces must pass: %v", err)
	}

	// A tool claiming the governed id cannot even exist: the namespace shape
	// contract rejects it before the clash check fires — either guard trips.
	drifting, err := NewResolver(
		staticCatalog{source: SourceGoverned, manifests: []UnifiedManifest{{ID: "core.draft.generate"}}},
		staticCatalog{source: SourceTool, manifests: []UnifiedManifest{{ID: "core.draft.generate"}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := drifting.CheckSingleAuthority(); err == nil ||
		(!strings.Contains(err.Error(), "tool.*") && !strings.Contains(err.Error(), "declared by both")) {
		t.Fatalf("id clash err=%v", err)
	}

	// A tool manifest without its namespace breaks the shape contract.
	misnamed, err := NewResolver(staticCatalog{source: SourceTool, manifests: []UnifiedManifest{{ID: "search"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := misnamed.CheckSingleAuthority(); err == nil || !strings.Contains(err.Error(), "tool.*") {
		t.Fatalf("namespace err=%v", err)
	}
}

func TestVisibleToRoleFiltersByPrefix(t *testing.T) {
	resolver, err := NewResolver(
		staticCatalog{source: SourceGoverned, manifests: []UnifiedManifest{
			{ID: "core.draft.generate", Source: SourceGoverned},
			{ID: "core.validation.quality", Source: SourceGoverned},
		}},
		staticCatalog{source: SourceTool, manifests: []UnifiedManifest{
			{ID: "tool.search", Source: SourceTool},
			{ID: "tool.write", Source: SourceTool},
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	policy := PrefixRolePolicy{Grants: map[string][]string{
		"writer":   {"core.draft.generate", "tool.write"},
		"admin":    {"*"},
		"stranger": {},
	}}
	visible, err := resolver.VisibleToRole("writer", policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(visible) != 2 || visible[0].ID != "core.draft.generate" || visible[1].ID != "tool.write" {
		t.Fatalf("writer view=%v", visible)
	}
	// Wildcard grant sees everything.
	if all, err := resolver.VisibleToRole("admin", policy); err != nil || len(all) != 4 {
		t.Fatalf("admin view len=%d err=%v", len(all), err)
	}
	// Default-deny for unlisted roles; unknown capability ids never appear.
	if empty, err := resolver.VisibleToRole("stranger", policy); err != nil || len(empty) != 0 {
		t.Fatalf("stranger view=%v err=%v", empty, err)
	}
	// Nil policy is the AllowAll posture.
	if all, err := resolver.VisibleToRole("anyone", nil); err != nil || len(all) != 4 {
		t.Fatalf("nil policy view len=%d err=%v", len(all), err)
	}
}

func TestGovernedPermissionsMapping(t *testing.T) {
	if got := GovernedPermissions(LegacyTool); len(got) != 4 || got[0] != "model.invoke" {
		t.Fatalf("legacy.tool mapping=%v", got)
	}
	if got := GovernedPermissions(LegacyEditorial); len(got) != 4 {
		t.Fatalf("legacy.editorial mapping=%v", got)
	}
	// Governed names and unknown markers map through unchanged.
	if got := GovernedPermissions("model.invoke"); len(got) != 1 || got[0] != "model.invoke" {
		t.Fatalf("governed passthrough=%v", got)
	}
	if got := GovernedPermissions(""); len(got) != 0 {
		t.Fatalf("empty marker=%v", got)
	}
}

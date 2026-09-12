// Package capability defines the unified capability view (V3.0 M1.5,
// docs/25): one manifest shape and one resolver over every registration
// surface the backend operates. The five surfaces — the governed writingplan
// catalog, the engine ToolRegistry (which also carries MCP tools under the
// mcp__ prefix), the editorial tool/agent registries, and the writingquality
// validator registry — keep their own semantics; this package is the read-only
// convergence layer that answers "what can this process do" without each
// consumer hard-wiring five registries.
//
// Authority discipline: a surface's adapter is a view, never a second write
// path. Registration stays with the owning registry; CheckSingleAuthority is
// the standing guard that the ID namespaces stay disjoint.
package capability

import (
	"fmt"
	"sort"
	"strings"
)

// Source names the registration surface a manifest came from.
type Source string

const (
	// SourceGoverned is the writingplan capability catalog (the kernel model
	// plans are compiled against).
	SourceGoverned Source = "governed"
	// SourceTool is the engine ToolRegistry (pipeline steps as agent tools,
	// plugin tools, and MCP tools under the mcp__ prefix).
	SourceTool Source = "tool"
	// SourceEditorial is the editorial tool + dynamic agent registries (the
	// legacy editorial DAG surface).
	SourceEditorial Source = "editorial"
	// SourceValidator is the writingquality validator registry.
	SourceValidator Source = "validator"
)

// ValidSource reports whether the source is one of the converged surfaces.
func ValidSource(source Source) bool {
	switch source {
	case SourceGoverned, SourceTool, SourceEditorial, SourceValidator:
		return true
	}
	return false
}

// UnifiedManifest is the read-only projection every surface's entries share.
// Surfaces keep richer native types; this is the common spine: identity,
// classification, IO surface, and availability in the owning process.
type UnifiedManifest struct {
	// ID is globally unique across surfaces. Governed capabilities keep
	// their canonical ids (core.*); every other surface is namespaced:
	// tool.<name>, tool.mcp__<server>__<tool>, editorial.<name>,
	// validator.<id>.
	ID string `json:"id"`
	// Class groups entries for resolver queries (governed classes pass
	// through; tools use their descriptor category; editorial agents use
	// their role; validators use "validation").
	Class string `json:"class"`
	// Source is the owning surface.
	Source Source `json:"source"`
	// Version is the owning registry's version string for this entry.
	Version string `json:"version"`
	// Description is the human-readable summary (LLM-facing where the
	// surface provides one).
	Description string `json:"description"`
	// Inputs/Outputs are the declared artifact/type names (governed artifact
	// types, tool schema marker "tool://schema", validator IO types).
	Inputs, Outputs []string `json:"inputs,omitempty"`
	// Permissions are the governed permission names the entry requires;
	// surfaces that predate governed permissions carry their legacy marker
	// (e.g. "legacy.agent") instead of inventing equivalents.
	Permissions []string `json:"permissions,omitempty"`
	// Available reports whether the entry is dispatchable in this process.
	// Governed declared-only capabilities are false here.
	Available bool `json:"available"`
}

// Catalog is one surface's read-only view for the resolver.
type Catalog interface {
	// Source names the surface.
	Source() Source
	// Manifests returns a snapshot of the surface's entries.
	Manifests() []UnifiedManifest
}

// Resolver answers "what can this process do" over the assembled catalogs.
type Resolver struct {
	catalogs map[Source]Catalog
}

// NewResolver assembles catalogs; one catalog per source — two catalogs on
// the same surface would itself be a dual-authority smell.
func NewResolver(catalogs ...Catalog) (*Resolver, error) {
	resolver := &Resolver{catalogs: make(map[Source]Catalog, len(catalogs))}
	for _, catalog := range catalogs {
		if catalog == nil {
			continue
		}
		source := catalog.Source()
		if !ValidSource(source) {
			return nil, fmt.Errorf("capability: catalog source %q is not a converged surface", source)
		}
		if _, exists := resolver.catalogs[source]; exists {
			return nil, fmt.Errorf("capability: duplicate catalog for source %q", source)
		}
		resolver.catalogs[source] = catalog
	}
	return resolver, nil
}

// ByID returns one manifest by its globally unique id.
func (resolver *Resolver) ByID(id string) (UnifiedManifest, bool) {
	for _, catalog := range resolver.catalogs {
		for _, manifest := range catalog.Manifests() {
			if manifest.ID == id {
				return manifest, true
			}
		}
	}
	return UnifiedManifest{}, false
}

// ByClass returns all manifests in one class, sorted by id for determinism.
func (resolver *Resolver) ByClass(class string) []UnifiedManifest {
	result := []UnifiedManifest{}
	for _, catalog := range resolver.catalogs {
		for _, manifest := range catalog.Manifests() {
			if manifest.Class == class {
				result = append(result, manifest)
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

// BySource returns all manifests of one surface, sorted by id.
func (resolver *Resolver) BySource(source Source) []UnifiedManifest {
	catalog, ok := resolver.catalogs[source]
	if !ok {
		return []UnifiedManifest{}
	}
	result := append([]UnifiedManifest(nil), catalog.Manifests()...)
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

// Sources lists the assembled surfaces in stable order.
func (resolver *Resolver) Sources() []Source {
	sources := make([]Source, 0, len(resolver.catalogs))
	for source := range resolver.catalogs {
		sources = append(sources, source)
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i] < sources[j] })
	return sources
}

// CheckSingleAuthority is the standing M1.5 guard: no two surfaces may
// declare the same capability id, and every manifest must carry a
// namespaced/canonical id for its surface. A violation means a surface
// started claiming another surface's identity — the dual-authority drift the
// convergence exists to prevent.
func (resolver *Resolver) CheckSingleAuthority() error {
	owners := map[string]Source{}
	for _, source := range resolver.Sources() {
		for _, manifest := range resolver.catalogs[source].Manifests() {
			if err := validateNamespace(source, manifest.ID); err != nil {
				return err
			}
			if first, clash := owners[manifest.ID]; clash {
				return fmt.Errorf("capability: id %q declared by both %s and %s", manifest.ID, first, source)
			}
			owners[manifest.ID] = source
		}
	}
	return nil
}

// validateNamespace enforces each surface's id shape. Governed capabilities
// own the bare canonical ids; every other surface must namespace theirs.
func validateNamespace(source Source, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("capability: %s manifest has an empty id", source)
	}
	switch source {
	case SourceGoverned:
		return nil // canonical core.* ids
	case SourceTool:
		if !strings.HasPrefix(id, "tool.") {
			return fmt.Errorf("capability: tool manifest id %q must be namespaced tool.*", id)
		}
	case SourceEditorial:
		if !strings.HasPrefix(id, "editorial.") {
			return fmt.Errorf("capability: editorial manifest id %q must be namespaced editorial.*", id)
		}
	case SourceValidator:
		if !strings.HasPrefix(id, "validator.") {
			return fmt.Errorf("capability: validator manifest id %q must be namespaced validator.*", id)
		}
	default:
		return fmt.Errorf("capability: unknown source %q", source)
	}
	return nil
}

// RolePolicy answers "what may this role see". It is the resolver-side pipe
// for role capability isolation (docs/20 §20.7, M1.5 slice 3): execution
// surfaces consult it before offering capabilities to a role's agents. The
// policy sees fully-resolved manifests and returns the subset — it never
// mutates registries, so it cannot become a second registration authority.
type RolePolicy interface {
	// VisibleTo returns the manifests the role may use. Returning every
	// manifest (or an error) both means unrestricted — callers decide which
	// posture fits their surface.
	VisibleTo(role string, manifests []UnifiedManifest) ([]UnifiedManifest, error)
}

// AllowAllRolePolicy is the default posture: every role sees everything.
// It preserves pre-M1.5 behavior exactly and is the explicit baseline other
// policies are compared against.
type AllowAllRolePolicy struct{}

func (AllowAllRolePolicy) VisibleTo(string, []UnifiedManifest) ([]UnifiedManifest, error) {
	return nil, nil // nil means "no restriction" by contract
}

// PrefixRolePolicy grants roles capability-id prefixes: a role sees only
// manifests whose id starts with one of its prefixes (a bare prefix matches
// exactly). Simple, deterministic, and auditable; richer matchers can wrap
// the interface without changing callers.
type PrefixRolePolicy struct {
	// Grants maps role → allowed id prefixes. A role missing from the map
	// sees nothing (default-deny).
	Grants map[string][]string
}

func (policy PrefixRolePolicy) VisibleTo(role string, manifests []UnifiedManifest) ([]UnifiedManifest, error) {
	prefixes, ok := policy.Grants[role]
	if !ok {
		return []UnifiedManifest{}, nil
	}
	for _, prefix := range prefixes {
		if prefix == "*" {
			// Explicit grant-everything marker: return nil so the resolver's
			// AllowAll contract applies.
			return nil, nil
		}
	}
	visible := []UnifiedManifest{}
	for _, manifest := range manifests {
		for _, prefix := range prefixes {
			if prefix == "" {
				continue
			}
			if manifest.ID == prefix || strings.HasPrefix(manifest.ID, prefix) {
				visible = append(visible, manifest)
				break
			}
		}
	}
	return visible, nil
}

// VisibleToRole resolves the full inventory and filters it through the
// policy. A nil policy is the AllowAll posture.
func (resolver *Resolver) VisibleToRole(role string, policy RolePolicy) ([]UnifiedManifest, error) {
	all := []UnifiedManifest{}
	for _, source := range resolver.Sources() {
		all = append(all, resolver.catalogs[source].Manifests()...)
	}
	if policy == nil {
		return all, nil
	}
	visible, err := policy.VisibleTo(role, all)
	if err != nil {
		return nil, err
	}
	// nil from the policy means "no restriction" (AllowAll contract).
	if visible == nil {
		return all, nil
	}
	sort.Slice(visible, func(i, j int) bool { return visible[i].ID < visible[j].ID })
	return visible, nil
}

// Legacy permission markers (docs/25 D5) and their governed-semantics
// mapping (M1.5 slice 4). The mapping is documentation-as-code: it states
// which governed permission names each legacy surface's operations fall
// under, so cross-surface authorization questions have one answer. It does
// NOT retrofit enforcement onto legacy registries — their gateways keep
// their own checks; governed dispatch already enforces the names directly.
const (
	// LegacyTool marks engine-tool operations: they run with the caller's
	// already-authorized context (model.invoke for LLM-backed steps,
	// external.research for search-backed ones) and add no new permission
	// surface of their own.
	LegacyTool = "legacy.tool"
	// LegacyEditorial marks editorial tool/agent operations, which execute
	// under the editorial DAG's own task authorization.
	LegacyEditorial = "legacy.editorial"
)

// GovernedPermissions maps a legacy marker to the governed permission names
// its operations are considered to exercise. The mapping is advisory for
// legacy surfaces (their own gateways stay authoritative for them) and exact
// for governed ones.
func GovernedPermissions(marker string) []string {
	switch marker {
	case LegacyTool:
		// Tools may invoke the model, read materials, run external research,
		// or run validation checks — the union a tool-side gateway must
		// already be inside.
		return []string{"model.invoke", "materials.read", "external.research", "validation.run"}
	case LegacyEditorial:
		// Editorial agents draft (model.invoke), read materials, research,
		// and validate.
		return []string{"model.invoke", "materials.read", "external.research", "validation.run"}
	default:
		// Unknown markers (including governed permission names) map to
		// themselves so the field stays honest for every manifest.
		if marker != "" {
			return []string{marker}
		}
		return []string{}
	}
}

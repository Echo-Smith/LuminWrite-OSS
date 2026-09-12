// Surface catalogs for the unified capability view (V3.0 M1.5, docs/25).
// Each adapter projects one registration surface onto the read-only
// capability.Catalog contract; registration authority stays with the owning
// registry. The server assembles all four so operators and diagnostics can
// answer "what can this process do" and CheckSingleAuthority can enforce
// that the surfaces' id namespaces stay disjoint.
package server

import (
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/capability"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/editorial"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingquality"
)

// governedCapabilityCatalog projects the writingplan capability catalog —
// the kernel model plans compile against. Declared-only capabilities stay
// marked unavailable, exactly as the registry reports them.
type governedCapabilityCatalog struct {
	registry *writingplan.CapabilityRegistry
}

func (catalog governedCapabilityCatalog) Source() capability.Source { return capability.SourceGoverned }

func (catalog governedCapabilityCatalog) Manifests() []capability.UnifiedManifest {
	manifests := []capability.UnifiedManifest{}
	if catalog.registry == nil {
		return manifests
	}
	for _, manifest := range catalog.registry.All() {
		manifests = append(manifests, capability.UnifiedManifest{
			ID: manifest.ID, Class: manifest.Class, Source: capability.SourceGoverned,
			Version: manifest.Version, Description: manifest.ID,
			Inputs:      artifactNames(manifest.InputTypes, manifest.OptionalInputTypes),
			Outputs:     artifactNames(manifest.OutputTypes, nil),
			Permissions: permissionNames(manifest.Permissions),
			Available:   manifest.Available,
		})
	}
	return manifests
}

// toolCapabilityCatalog projects the engine ToolRegistry. MCP tools arrive
// in the same registry under the mcp__ prefix and keep it in their
// namespaced ids, so the convergence sees them as first-class entries.
type toolCapabilityCatalog struct {
	registry *engine.ToolRegistry
	// legacyCategory is the descriptor category of each tool name; tools
	// registered without a descriptor fall back to "tool".
	categories map[string]string
}

func (catalog toolCapabilityCatalog) Source() capability.Source { return capability.SourceTool }

func (catalog toolCapabilityCatalog) Manifests() []capability.UnifiedManifest {
	manifests := []capability.UnifiedManifest{}
	if catalog.registry == nil {
		return manifests
	}
	for _, tool := range catalog.registry.All() {
		class := "tool"
		if category, ok := catalog.categories[tool.Name()]; ok && category != "" {
			class = category
		}
		manifests = append(manifests, capability.UnifiedManifest{
			ID: "tool." + tool.Name(), Class: class, Source: capability.SourceTool,
			Version: "1", Description: tool.Description(),
			Inputs:      []string{"tool://schema"},
			Outputs:     []string{"tool://result"},
			Permissions: capability.GovernedPermissions(capability.LegacyTool),
			Available:   true,
		})
	}
	return manifests
}

// editorialAgentsView is the slice of the dynamic agent registry the catalog
// reads; *editorial.DynamicAgentRegistry satisfies it.
type editorialAgentsView interface {
	ListAll() []*editorial.AgentConfig
}

// editorialCapabilityCatalog projects the legacy editorial surface: tools
// and dynamic agents, both namespaced under editorial.*.
type editorialCapabilityCatalog struct {
	tools  *editorial.EditorialToolRegistry
	agents editorialAgentsView
}

func (catalog editorialCapabilityCatalog) Source() capability.Source {
	return capability.SourceEditorial
}

func (catalog editorialCapabilityCatalog) Manifests() []capability.UnifiedManifest {
	manifests := []capability.UnifiedManifest{}
	if catalog.tools != nil {
		for _, tool := range catalog.tools.All() {
			manifests = append(manifests, capability.UnifiedManifest{
				ID: "editorial.tool." + tool.Name(), Class: "editorial.tool", Source: capability.SourceEditorial,
				Version: "1", Description: tool.Description(),
				Inputs:      []string{"tool://schema"},
				Outputs:     []string{"tool://result"},
				Permissions: capability.GovernedPermissions(capability.LegacyEditorial),
				Available:   true,
			})
		}
	}
	if catalog.agents != nil {
		for _, agent := range catalog.agents.ListAll() {
			manifests = append(manifests, capability.UnifiedManifest{
				ID: "editorial.agent." + agent.ID, Class: "editorial.agent." + agent.Role, Source: capability.SourceEditorial,
				Version: "1", Description: agent.Name,
				Inputs:      []string{"editorial://task"},
				Outputs:     []string{"editorial://artifact"},
				Permissions: capability.GovernedPermissions(capability.LegacyEditorial),
				Available:   true,
			})
		}
	}
	return manifests
}

// validatorCapabilityCatalog projects the writingquality validator registry.
type validatorCapabilityCatalog struct {
	registry *writingquality.ValidatorRegistry
}

func (catalog validatorCapabilityCatalog) Source() capability.Source {
	return capability.SourceValidator
}

func (catalog validatorCapabilityCatalog) Manifests() []capability.UnifiedManifest {
	manifests := []capability.UnifiedManifest{}
	if catalog.registry == nil {
		return manifests
	}
	for _, spec := range catalog.registry.List() {
		manifests = append(manifests, capability.UnifiedManifest{
			ID: "validator." + spec.ID, Class: "validation", Source: capability.SourceValidator,
			Version: spec.Version, Description: string(spec.Criticality) + " validator",
			Inputs:      append([]string(nil), spec.InputTypes...),
			Outputs:     append([]string(nil), spec.OutputTypes...),
			Permissions: []string{"validation.run"},
			Available:   true,
		})
	}
	return manifests
}

// newCapabilityResolver assembles the server's four surface catalogs.
func (s *Server) newCapabilityResolver() (*capability.Resolver, error) {
	return capability.NewResolver(
		governedCapabilityCatalog{registry: s.governedCapabilityRegistry()},
		toolCapabilityCatalog{registry: s.toolRegistry, categories: s.toolCategories()},
		editorialCapabilityCatalog{tools: s.editorialToolRegistry(), agents: s.editorialAgentRegistry()},
		validatorCapabilityCatalog{registry: s.validatorRegistry()},
	)
}

func artifactNames(types ...[]writingplan.ArtifactType) []string {
	names := []string{}
	for _, group := range types {
		for _, artifactType := range group {
			names = append(names, string(artifactType))
		}
	}
	return names
}

func permissionNames(permissions []writingplan.Permission) []string {
	names := make([]string, 0, len(permissions))
	for _, permission := range permissions {
		names = append(names, string(permission))
	}
	return names
}

// ── surface accessors ──
// Each accessor resolves the owning registry from the server's composition;
// absent surfaces contribute empty catalogs (the resolver then reports the
// surface as simply not present in this deployment).

func (s *Server) governedCapabilityRegistry() *writingplan.CapabilityRegistry {
	if api, ok := s.writingAPI.(*persistentWritingAPI); ok {
		return api.capabilities
	}
	return nil
}

func (s *Server) editorialToolRegistry() *editorial.EditorialToolRegistry {
	return s.editorialTools
}

func (s *Server) editorialAgentRegistry() editorialAgentsView {
	return s.editorialAgents
}

func (s *Server) validatorRegistry() *writingquality.ValidatorRegistry {
	return writingquality.DefaultValidatorRegistry()
}

// toolCategories snapshots the descriptor categories of the current tool
// registry so the unified view keeps the planner's classification.
func (s *Server) toolCategories() map[string]string {
	categories := map[string]string{}
	if s.toolRegistry == nil {
		return categories
	}
	for name, descriptor := range s.toolRegistry.Descriptors() {
		categories[name] = descriptor.Category
	}
	return categories
}

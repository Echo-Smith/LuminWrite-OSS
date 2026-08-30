package writingplan

import (
	"fmt"
	"sort"
	"sync"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
)

type TemplateNode struct {
	NodeID              string
	Kind                NodeKind
	CapabilityClass     string
	DependsOn           []string
	InputArtifactTypes  []ArtifactType
	OutputArtifactTypes []ArtifactType
	Bounds              Bounds
	FailurePath         FailurePath
	FallbackNodeID      string
}

type PlanTemplate struct {
	ID                 string
	Mode               writingkernel.OrchestrationMode
	TrustLevel         TrustLevel
	RootNodeID         string
	Nodes              []TemplateNode
	RequiredValidators []string
}

type TemplateRegistry struct {
	mu        sync.RWMutex
	templates map[writingkernel.OrchestrationMode]PlanTemplate
}

func NewTemplateRegistry() *TemplateRegistry {
	return &TemplateRegistry{templates: map[writingkernel.OrchestrationMode]PlanTemplate{}}
}

func (r *TemplateRegistry) Register(template PlanTemplate) error {
	if template.ID == "" || template.Mode == writingkernel.OrchestrationModeAuto || !template.Mode.Valid() || len(template.Nodes) == 0 {
		return fmt.Errorf("invalid plan template %q", template.ID)
	}
	if template.TrustLevel != TrustT1 && template.TrustLevel != TrustT2 {
		return fmt.Errorf("template %q must be T1 or T2", template.ID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.templates[template.Mode]; exists {
		return fmt.Errorf("template already registered for %s", template.Mode)
	}
	r.templates[template.Mode] = cloneTemplate(template)
	return nil
}

func (r *TemplateRegistry) Get(mode writingkernel.OrchestrationMode) (PlanTemplate, bool) {
	if r == nil {
		return PlanTemplate{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	template, ok := r.templates[mode]
	return cloneTemplate(template), ok
}

func (r *TemplateRegistry) Modes() []writingkernel.OrchestrationMode {
	r.mu.RLock()
	defer r.mu.RUnlock()
	modes := make([]writingkernel.OrchestrationMode, 0, len(r.templates))
	for mode := range r.templates {
		modes = append(modes, mode)
	}
	sort.Slice(modes, func(i, j int) bool { return modes[i] < modes[j] })
	return modes
}

func DefaultTemplateRegistry() *TemplateRegistry {
	registry := NewTemplateRegistry()
	common := func(id string, mode writingkernel.OrchestrationMode, nodes []TemplateNode, validators ...string) {
		if err := registry.Register(PlanTemplate{ID: id, Mode: mode, TrustLevel: TrustT1, RootNodeID: nodes[0].NodeID, Nodes: nodes, RequiredValidators: validators}); err != nil {
			panic(err)
		}
	}
	common("tpl_fast_v1", writingkernel.OrchestrationModeFast, []TemplateNode{
		templateNode("node_draft", NodeAction, "writing.draft", nil, []ArtifactType{"contract", "materials"}, []ArtifactType{"full_draft"}),
		templateNode("node_quality", NodeValidate, "validation.quality", []string{"node_draft"}, []ArtifactType{"full_draft"}, []ArtifactType{"quality_report"}),
		templateNode("node_finalize", NodeAction, "document.finalize", []string{"node_draft", "node_quality"}, []ArtifactType{"full_draft", "quality_report"}, []ArtifactType{"revision_set"}),
	}, "core.validation.quality")
	common("tpl_outline_first_v1", writingkernel.OrchestrationModeOutlineFirst, []TemplateNode{
		templateNode("node_outline", NodeAction, "writing.outline", nil, []ArtifactType{"contract", "materials"}, []ArtifactType{"outline"}),
		templateNode("node_draft", NodeAction, "writing.draft", []string{"node_outline"}, []ArtifactType{"contract", "materials", "outline"}, []ArtifactType{"full_draft"}),
		templateNode("node_quality", NodeValidate, "validation.quality", []string{"node_draft"}, []ArtifactType{"full_draft"}, []ArtifactType{"quality_report"}),
		templateNode("node_finalize", NodeAction, "document.finalize", []string{"node_draft", "node_quality"}, []ArtifactType{"full_draft", "quality_report"}, []ArtifactType{"revision_set"}),
	}, "core.validation.quality")
	common("tpl_sourced_v1", writingkernel.OrchestrationModeSourced, []TemplateNode{
		templateNode("node_research", NodeAction, "research.collect", nil, []ArtifactType{"contract", "materials"}, []ArtifactType{"source_pack"}),
		templateNode("node_outline", NodeAction, "writing.outline", []string{"node_research"}, []ArtifactType{"contract", "source_pack"}, []ArtifactType{"outline"}),
		templateNode("node_draft", NodeAction, "writing.draft", []string{"node_research", "node_outline"}, []ArtifactType{"contract", "materials", "source_pack", "outline"}, []ArtifactType{"full_draft"}),
		templateNode("node_evidence", NodeValidate, "validation.evidence", []string{"node_research", "node_draft"}, []ArtifactType{"source_pack", "full_draft"}, []ArtifactType{"evidence_report"}),
		templateNode("node_quality", NodeValidate, "validation.quality", []string{"node_draft", "node_evidence"}, []ArtifactType{"full_draft", "evidence_report"}, []ArtifactType{"quality_report"}),
		templateNode("node_finalize", NodeAction, "document.finalize", []string{"node_draft", "node_quality"}, []ArtifactType{"full_draft", "quality_report"}, []ArtifactType{"revision_set"}),
	}, "core.validation.evidence", "core.validation.quality")
	common("tpl_strict_research_v1", writingkernel.OrchestrationModeStrictResearch, []TemplateNode{
		templateNode("node_research", NodeAction, "research.strict", nil, []ArtifactType{"contract", "materials"}, []ArtifactType{"source_pack"}),
		templateNode("node_outline", NodeAction, "writing.outline", []string{"node_research"}, []ArtifactType{"contract", "source_pack"}, []ArtifactType{"outline"}),
		templateNode("node_draft", NodeAction, "writing.draft", []string{"node_research", "node_outline"}, []ArtifactType{"contract", "materials", "source_pack", "outline"}, []ArtifactType{"full_draft"}),
		templateNode("node_factcheck", NodeValidate, "validation.fact", []string{"node_research", "node_draft"}, []ArtifactType{"source_pack", "full_draft"}, []ArtifactType{"fact_report"}),
		templateNode("node_evidence", NodeValidate, "validation.evidence", []string{"node_research", "node_draft"}, []ArtifactType{"source_pack", "full_draft"}, []ArtifactType{"evidence_report"}),
		templateNode("node_quality", NodeValidate, "validation.quality", []string{"node_draft", "node_factcheck", "node_evidence"}, []ArtifactType{"full_draft", "fact_report", "evidence_report"}, []ArtifactType{"quality_report"}),
		templateNode("node_finalize", NodeAction, "document.finalize", []string{"node_draft", "node_quality"}, []ArtifactType{"full_draft", "quality_report"}, []ArtifactType{"revision_set"}),
	}, "core.validation.fact", "core.validation.evidence", "core.validation.quality")
	return registry
}

func templateNode(id string, kind NodeKind, class string, deps []string, inputs, outputs []ArtifactType) TemplateNode {
	return TemplateNode{NodeID: id, Kind: kind, CapabilityClass: class, DependsOn: deps, InputArtifactTypes: inputs, OutputArtifactTypes: outputs,
		Bounds: Bounds{MaxAttempts: 1, MaxConcurrency: 1, MaxItems: 1, MaxCostUSD: 5, TimeoutMS: 300000}, FailurePath: FailurePause}
}

func cloneTemplate(template PlanTemplate) PlanTemplate {
	template.Nodes = append([]TemplateNode(nil), template.Nodes...)
	for i := range template.Nodes {
		template.Nodes[i].DependsOn = append([]string(nil), template.Nodes[i].DependsOn...)
		template.Nodes[i].InputArtifactTypes = append([]ArtifactType(nil), template.Nodes[i].InputArtifactTypes...)
		template.Nodes[i].OutputArtifactTypes = append([]ArtifactType(nil), template.Nodes[i].OutputArtifactTypes...)
	}
	template.RequiredValidators = append([]string(nil), template.RequiredValidators...)
	return template
}

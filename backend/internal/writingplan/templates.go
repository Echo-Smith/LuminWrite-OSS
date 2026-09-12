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
	// tpl_research_review_v1 (T06, design.md §3 fixed node table): the ten
	// research-review nodes with their exact dependency edges — discover(0);
	// read→discover; gate_evidence→read; outline→read,gate_evidence;
	// gate_outline→outline; draft→read,gate_evidence,gate_outline;
	// citations→read,draft; fact→read,draft; quality→draft,citations,fact;
	// finalize→draft,quality.
	researchReview := []TemplateNode{
		researchNode("node_research_discover", NodeAction, ClassResearchDiscover, nil,
			[]ArtifactType{"contract", "materials"}, []ArtifactType{"research_candidates"}),
		researchNode("node_research_read", NodeAction, ClassResearchRead, []string{"node_research_discover"},
			[]ArtifactType{"contract", "research_candidates", "materials"}, []ArtifactType{"research_evidence_pack"},
			// MaxAttempts=2: the research budget boundary pauses inside the
			// node (T05 sentinel); the resume re-dispatch continues the
			// remaining papers from the sub-task ledger without re-reading
			// finished ones (design.md §3/§7).
			bounds(2, 1, 20, 20*60*1000)),
		researchNode("node_gate_evidence", NodeHumanGate, ClassResearchGateEvidence, []string{"node_research_read"},
			[]ArtifactType{"research_evidence_pack"}, []ArtifactType{"evidence_approval"}, gateBounds()),
		researchNode("node_research_outline", NodeAction, ClassResearchOutline, []string{"node_research_read", "node_gate_evidence"},
			[]ArtifactType{"contract", "research_evidence_pack", "evidence_approval"}, []ArtifactType{"research_outline"}),
		researchNode("node_gate_outline", NodeHumanGate, ClassResearchGateOutline, []string{"node_research_outline"},
			[]ArtifactType{"research_outline"}, []ArtifactType{"approved_research_outline"}, gateBounds()),
		researchNode("node_research_draft", NodeAction, ClassResearchDraft, []string{"node_research_read", "node_gate_evidence", "node_gate_outline"},
			[]ArtifactType{"contract", "research_evidence_pack", "evidence_approval", "approved_research_outline"},
			[]ArtifactType{"full_draft", "research_citation_index"}),
		researchNode("node_research_citations", NodeValidate, ClassResearchValidateCitation, []string{"node_research_read", "node_research_draft"},
			[]ArtifactType{"research_evidence_pack", "full_draft", "research_citation_index"},
			[]ArtifactType{"evidence_report", "research_validation_details"}),
		researchNode("node_research_fact", NodeValidate, ClassResearchValidateFact, []string{"node_research_read", "node_research_draft"},
			[]ArtifactType{"research_evidence_pack", "full_draft"}, []ArtifactType{"fact_report"}),
		researchNode("node_quality", NodeValidate, "validation.quality", []string{"node_research_draft", "node_research_citations", "node_research_fact"},
			[]ArtifactType{"full_draft", "evidence_report", "fact_report"}, []ArtifactType{"quality_report"}),
		researchNode("node_finalize", NodeAction, "document.finalize", []string{"node_research_draft", "node_quality"},
			[]ArtifactType{"full_draft", "quality_report"}, []ArtifactType{"revision_set"}),
	}
	if err := registry.Register(PlanTemplate{ID: "tpl_research_review_v1", Mode: writingkernel.OrchestrationModeResearchReview,
		TrustLevel: TrustT1, RootNodeID: researchReview[0].NodeID, Nodes: researchReview,
		RequiredValidators: []string{CapabilityResearchCitations, CapabilityResearchFact, "core.validation.quality"}}); err != nil {
		panic(err)
	}
	return registry
}

// researchNode is one research-review template node. Every node keeps
// MaxConcurrency=1 and FailurePath=FailurePause (a research failure pauses
// for an owner decision rather than failing the run); MaxCostUSD keeps the
// 5-per-node convention as a conservative ceiling, but the research path's
// real budget is the duration boundary (design.md §3: read ≤ 20 minutes per
// node, 30 minutes proactive execution overall), which lives in TimeoutMS and
// the manifest ceilings. Gate nodes are kernel-owned: zero cost, the pinned
// human-gate bounds, and their own (default) pause failure path.
func researchNode(id string, kind NodeKind, class string, deps []string, inputs, outputs []ArtifactType, overrides ...Bounds) TemplateNode {
	node := templateNode(id, kind, class, deps, inputs, outputs)
	if len(overrides) > 0 {
		node.Bounds = overrides[0]
	}
	return node
}

func bounds(maxAttempts, maxConcurrency, maxItems int, timeoutMS int64) Bounds {
	return Bounds{MaxAttempts: maxAttempts, MaxConcurrency: maxConcurrency, MaxItems: maxItems,
		MaxCostUSD: 5, TimeoutMS: timeoutMS}
}

// gateBounds pins the kernel gate node's bounds: the gate never executes, so
// it carries zero cost and a nominal ceiling from its manifest (the manifest
// MaxBounds timeout stays the 20-minute research ceiling; the plan-level
// timeout records the decision window).
func gateBounds() Bounds {
	return Bounds{MaxAttempts: 1, MaxConcurrency: 1, MaxItems: 1, MaxCostUSD: 0, TimeoutMS: 120000}
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

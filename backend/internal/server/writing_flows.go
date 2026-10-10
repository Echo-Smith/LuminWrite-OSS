package server

// 写作流程词表单源（GET /api/v2/writing/flows）。前端流程选择器与启动链
// 从本端点取流程 spec，不再在 TypeScript 里镜像 Go 侧映射表。
//
// 单源保证：语义字段（intentOperation / orchestration / summary / steps）
// 逐字取自 writingFlowContractPlans —— 密封合同消费的同一张表；evidence
// policy 运行时调用 writingruntime.EvidenceScenarioPolicyFor 拿活的策略表，
// 两边永不可能漂移。纯展示字段（label / description / templateId /
// initialArtifactTypes / 步骤 inputs-outputs）集中在本文件的 enrichment 表，
// 与权威表同文件共存；research_review 的执行链以研究运行时模板为权威，
// 这里只承载选择器词表并标注 launch:"research"（其 evidencePolicy 在
// legacy 证据情景表中刻意没有对应行，置 null——与后端现实一致）。

import (
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingruntime"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
	"net/http"
)

// writingFlowStepIO 补全步骤的展示用输入/输出（intent plan 的 ProposedStep
// 只有 step_id/objective/capability_hint/depends_on 四字段；inputs/outputs
// 是节点图的前端词汇，随 flow 变化——quality/finalize 的输入在各流程不同，
// 所以按 flow+step 双层键）。新增密封步骤时必须同步补一行，测试
// TestWritingFlowSpecsStepWiringComplete 会拦截缺行。
var writingFlowStepIO = map[string]map[string][2][]string{
	"long_form": {
		"outline":   {[]string{"contract"}, []string{"outline"}},
		"draft":     {[]string{"contract", "outline"}, []string{"full_draft"}},
		"quality":   {[]string{"contract", "full_draft"}, []string{"quality_report"}},
		"finalize":  {[]string{"full_draft", "quality_report"}, []string{"revision_set"}},
	},
	"multi_material": {
		"synthesis": {[]string{"contract", "materials"}, []string{"full_draft"}},
		"quality":   {[]string{"contract", "materials", "full_draft"}, []string{"quality_report"}},
		"finalize":  {[]string{"full_draft", "quality_report"}, []string{"revision_set"}},
	},
	"faithful_rewrite": {
		"rewrite":  {[]string{"contract", "materials", "article"}, []string{"full_draft"}},
		"quality":  {[]string{"contract", "materials", "article", "full_draft"}, []string{"quality_report"}},
		"finalize": {[]string{"full_draft", "quality_report"}, []string{"revision_set"}},
	},
}

// writingFlowDisplayTerms 选择器展示文案与启动链静态字段；research_review
// 的 steps 是十节点模板主链的线性化简述（真实依赖边以服务端模板为权威）。
var writingFlowDisplayTerms = map[string]struct {
	label                string
	description          string
	templateID           string
	initialArtifactTypes []string
	launch               string
	steps                []writingFlowStepView
}{
	"long_form": {
		label:                "长文创作",
		description:          "从简报与 3–5 份材料产出约 3000 字行业分析长文。",
		templateID:           "tpl_outline_first_v1",
		initialArtifactTypes: []string{"contract"},
		launch:               "writing",
	},
	"multi_material": {
		label:                "多材料综合",
		description:          "把多份（可含冲突数据的）材料综合成统一分析并标注分歧出处。",
		templateID:           "tpl_sourced_v1",
		initialArtifactTypes: []string{"contract", "materials"},
		launch:               "writing",
	},
	"faithful_rewrite": {
		label:                "忠实改写",
		description:          "润色/重构文稿，保留事实、观点与作者语感，剔除标记的内部信息。",
		templateID:           "tpl_fast_v1",
		initialArtifactTypes: []string{"contract", "materials"},
		launch:               "writing",
	},
	"research_review": {
		label:                "深度研究",
		description:          "围绕研究问题检索与精读文献，逐条引用核查，经两个确认点产出学术综述。",
		templateID:           "tpl_research_review_v1",
		initialArtifactTypes: []string{"contract", "materials"},
		launch:               "research",
		steps: []writingFlowStepView{
			{StepID: "discover", Capability: "core.research.discover", Description: "Discover candidate literature", Inputs: []string{"contract", "materials"}, Outputs: []string{"research_candidates"}},
			{StepID: "read", Capability: "core.research.read", Description: "Rank, fetch and read papers into an evidence pack", Inputs: []string{"contract", "research_candidates", "materials"}, Outputs: []string{"research_evidence_pack"}, DependsOn: []string{"discover"}},
			{StepID: "evidence_gate", Capability: "core.research.gate.evidence", Description: "Human gate: confirm the evidence pack", Inputs: []string{"research_evidence_pack"}, Outputs: []string{"evidence_approval"}, DependsOn: []string{"read"}},
			{StepID: "outline", Capability: "core.research.outline", Description: "Assemble the grounded review outline", Inputs: []string{"contract", "research_evidence_pack", "evidence_approval"}, Outputs: []string{"research_outline"}, DependsOn: []string{"evidence_gate"}},
			{StepID: "outline_gate", Capability: "core.research.gate.outline", Description: "Human gate: confirm the outline", Inputs: []string{"research_outline"}, Outputs: []string{"approved_research_outline"}, DependsOn: []string{"outline"}},
			{StepID: "draft", Capability: "core.research.draft", Description: "Write the cited review draft", Inputs: []string{"contract", "research_evidence_pack", "approved_research_outline"}, Outputs: []string{"full_draft"}, DependsOn: []string{"outline_gate"}},
			{StepID: "citations", Capability: "core.research.validate.citations", Description: "Validate every citation against the evidence pack", Inputs: []string{"research_evidence_pack", "full_draft"}, Outputs: []string{"evidence_report"}, DependsOn: []string{"draft"}},
			{StepID: "fact", Capability: "core.research.validate.fact", Description: "Fact-review the draft claims", Inputs: []string{"research_evidence_pack", "full_draft"}, Outputs: []string{"fact_report"}, DependsOn: []string{"draft"}},
			{StepID: "quality", Capability: "core.validation.quality", Description: "Quality validation", Inputs: []string{"full_draft", "evidence_report", "fact_report"}, Outputs: []string{"quality_report"}, DependsOn: []string{"draft"}},
			{StepID: "finalize", Capability: "core.document.finalize", Description: "Finalize revision set", Inputs: []string{"full_draft", "quality_report"}, Outputs: []string{"revision_set"}, DependsOn: []string{"quality"}},
		},
	},
}

// writingFlowOrder 是下发顺序（选择器展示序）；canonical flows 在前，
// research_review 收尾——与流程选择器的历史排布一致。
var writingFlowOrder = []string{"long_form", "multi_material", "faithful_rewrite", "research_review"}

type writingFlowEvidencePolicyView struct {
	Name          string `json:"name"`
	GovernedIndex int    `json:"governedIndex"`
	NodeName      string `json:"governedNode"`
}

type writingFlowStepView struct {
	StepID      string   `json:"step_id"`
	Capability  string   `json:"capability"`
	Description string   `json:"description"`
	Inputs      []string `json:"inputs,omitempty"`
	Outputs     []string `json:"outputs,omitempty"`
	DependsOn   []string `json:"depends_on,omitempty"`
}

type writingFlowSpecView struct {
	Type                 string                         `json:"type"`
	Label                string                         `json:"label"`
	Description          string                         `json:"description"`
	Launch               string                         `json:"launch"`
	IntentOperation      string                         `json:"intentOperation"`
	Orchestration        string                         `json:"orchestration"`
	TemplateID           string                         `json:"templateId"`
	EvidencePolicy       *writingFlowEvidencePolicyView `json:"evidencePolicy"`
	InitialArtifactTypes []string                       `json:"initialArtifactTypes"`
	Steps                []writingFlowStepView          `json:"steps"`
	Summary              string                         `json:"summary"`
}

type writingFlowsPayload struct {
	Flows       []writingFlowSpecView `json:"flows"`
	DefaultFlow string                `json:"default_flow"`
}

// writingFlowsPayload 构建下发视图。writing 流的语义字段直接读
// writingFlowContractPlans（不复制），evidence policy 调活的策略表。
func buildWritingFlowsPayload() writingFlowsPayload {
	payload := writingFlowsPayload{DefaultFlow: "long_form"}
	for _, flow := range writingFlowOrder {
		terms := writingFlowDisplayTerms[flow]
		view := writingFlowSpecView{
			Type:                 flow,
			Label:                terms.label,
			Description:          terms.description,
			Launch:               terms.launch,
			TemplateID:           terms.templateID,
			InitialArtifactTypes: terms.initialArtifactTypes,
			// research_review 的节点简图（display terms）；writing 流在下方
			// 密封分支里用权威 steps 覆盖。
			Steps: terms.steps,
		}
		if seal, ok := writingFlowContractPlans[flow]; ok {
			view.Launch = "writing"
			view.IntentOperation = string(seal.operation)
			view.Orchestration = string(seal.orchestration)
			view.Summary = seal.summary
			wiring := writingFlowStepIO[flow]
			for _, step := range seal.steps {
				io := wiring[step.StepID]
				view.Steps = append(view.Steps, writingFlowStepView{
					StepID:      step.StepID,
					Capability:  step.CapabilityHint,
					Description: step.Objective,
					Inputs:      io[0],
					Outputs:     io[1],
					DependsOn:   step.DependsOn,
				})
			}
			if policy, ok := writingruntime.EvidenceScenarioPolicyFor(flow); ok {
				view.EvidencePolicy = &writingFlowEvidencePolicyView{
					Name:          policy.Name,
					GovernedIndex: policy.GovernedIndex,
					NodeName:      policy.NodeName,
				}
			}
		}
		payload.Flows = append(payload.Flows, view)
	}
	return payload
}

func (s *Server) handleListWritingFlows(w http.ResponseWriter, r *http.Request) {
	response.OK(w, buildWritingFlowsPayload())
}

/**
 * WP4 三大写作流程映射表 — docs/28-wp4-pilot-scenarios.md 的前端侧唯一映射来源。
 *
 * 选择链路：flow-picker → AgentStartPayload.flow → startWritingRun 的
 * contract / intent plan 构建统一从本表取值：
 * - orchestration → 合同 collaboration.orchestration_mode（真实枚举值，服务端
 *   按此选择 tpl_outline_first_v1 / tpl_sourced_v1 / tpl_fast_v1 计划模板）
 * - intentOperation → 合同 intent.operation（create / synthesize / rewrite）
 * - steps → intent plan proposed_steps 节点序列。docs/28 节点名映射到现有 plan
 *   builder 的 artifact 词汇：draft / synthesis / rewrite 均产出 full_draft，
 *   outline 产出 outline，quality 产出 quality_report，finalize 产出 revision_set
 * - evidencePolicy → docs/28 的命名 evidence policy 与 governed 节点索引，逐字
 *   对齐后端 internal/writingruntime/evidence_policy.go 的 EvidenceScenarioPolicies
 *   （Name / GovernedIndex / NodeName）。该命名 policy 在 plan/contract 线上
 *   schema 中没有对应字段（合同 evidence_policy 是 level/unsupported_claims
 *   词表，语义不同，不冒充），落点即本映射表 + plan 节点序列（governed 节点
 *   就是产出 full_draft 的生成步骤）+ intent plan summary 标注。
 */

// ─── 流程类型 ────────────────────────────────────────────

export type WritingFlowType = "long_form" | "multi_material" | "faithful_rewrite";

export const WRITING_FLOW_TYPES = ["long_form", "multi_material", "faithful_rewrite"] as const;

/** 缺省流程：与引入选择 UI 之前的行为等价。 */
export const DEFAULT_WRITING_FLOW: WritingFlowType = "long_form";

/** 与后端 writingruntime.EvidenceScenarioPolicies 逐字对齐的 evidence policy 条目。 */
export interface WritingFlowEvidencePolicy {
  name: WritingFlowType;
  /** governed 节点在后端 evidence 情景节点列表中的索引 */
  governedIndex: number;
  /** governed 节点名（docs/28 叫法） */
  governedNode: "draft" | "synthesis" | "rewrite";
}

/** intent plan proposed_steps 单步（沿用现有 buildIntentPlan 的步骤形状）。 */
export interface WritingFlowPlanStep {
  step_id: string;
  capability: string;
  description: string;
  inputs: string[];
  outputs: string[];
  depends_on?: string[];
}

export interface WritingFlowSpec {
  type: WritingFlowType;
  /** UI 标签（中文） */
  label: string;
  /** UI 一句话说明（中文，docs/28 试点场景文案） */
  description: string;
  /** 合同 collaboration.orchestration_mode（决定服务端计划模板） */
  orchestration: "outline_first" | "sourced" | "fast";
  /** 服务端计划模板 id（docs/28；由 orchestration 间接选定） */
  templateId: "tpl_outline_first_v1" | "tpl_sourced_v1" | "tpl_fast_v1";
  /** 合同 intent.operation（writingkernel.Operation 枚举值） */
  intentOperation: "create" | "synthesize" | "rewrite";
  /** docs/28 命名 evidence policy + governed 节点索引 */
  evidencePolicy: WritingFlowEvidencePolicy;
  /** 运行初始 artifact（后端 CompilePlan 仅接受 ["contract"] 或 ["contract","materials"]） */
  initialArtifactTypes: string[];
  /** intent plan proposed_steps 节点序列（docs/28 节点图） */
  steps: WritingFlowPlanStep[];
  /** intent plan summary（附 evidence policy 落点标注） */
  summary: string;
}

// ─── 三大流程 ────────────────────────────────────────────

export const WRITING_FLOW_SPECS: Record<WritingFlowType, WritingFlowSpec> = {
  // 长文创作：contract → outline → draft → quality → finalize → revision_set
  long_form: {
    type: "long_form",
    label: "长文创作",
    description: "从简报与 3–5 份材料产出约 3000 字行业分析长文。",
    orchestration: "outline_first",
    templateId: "tpl_outline_first_v1",
    intentOperation: "create",
    evidencePolicy: { name: "long_form", governedIndex: 1, governedNode: "draft" },
    initialArtifactTypes: ["contract"],
    summary:
      "Long-form writing pipeline (tpl_outline_first_v1): outline → draft → quality → finalize; evidence policy long_form governs draft@index 1",
    steps: [
      {
        step_id: "outline",
        capability: "core.outline.generate",
        description: "Generate article outline",
        inputs: ["contract"],
        outputs: ["outline"],
      },
      {
        step_id: "draft",
        capability: "core.draft.generate",
        description: "Write full draft",
        inputs: ["contract", "outline"],
        outputs: ["full_draft"],
        depends_on: ["outline"],
      },
      {
        step_id: "quality",
        capability: "core.validation.quality",
        description: "Quality validation",
        inputs: ["contract", "full_draft"],
        outputs: ["quality_report"],
        depends_on: ["draft"],
      },
      {
        step_id: "finalize",
        capability: "core.document.finalize",
        description: "Finalize revision set",
        inputs: ["full_draft", "quality_report"],
        outputs: ["revision_set"],
        depends_on: ["quality"],
      },
    ],
  },
  // 多材料综合：contract + materials → synthesis → quality → finalize → revision_set
  multi_material: {
    type: "multi_material",
    label: "多材料综合",
    description: "把多份（可含冲突数据的）材料综合成统一分析并标注分歧出处。",
    orchestration: "sourced",
    templateId: "tpl_sourced_v1",
    intentOperation: "synthesize",
    evidencePolicy: { name: "multi_material", governedIndex: 1, governedNode: "synthesis" },
    initialArtifactTypes: ["contract", "materials"],
    summary:
      "Multi-material synthesis pipeline (tpl_sourced_v1): synthesis → quality → finalize; evidence policy multi_material governs synthesis@index 1",
    steps: [
      {
        step_id: "synthesis",
        capability: "core.draft.generate",
        description: "Synthesize materials into unified analysis",
        inputs: ["contract", "materials"],
        outputs: ["full_draft"],
      },
      {
        step_id: "quality",
        capability: "core.validation.quality",
        description: "Quality validation",
        inputs: ["contract", "materials", "full_draft"],
        outputs: ["quality_report"],
        depends_on: ["synthesis"],
      },
      {
        step_id: "finalize",
        capability: "core.document.finalize",
        description: "Finalize revision set",
        inputs: ["full_draft", "quality_report"],
        outputs: ["revision_set"],
        depends_on: ["quality"],
      },
    ],
  },
  // 忠实改写：contract + materials + article → rewrite → quality → finalize → revision_set
  // （article 是用户随消息提供的原稿上下文，后端初始 artifact 只收 contract/materials 对，
  //   故 article 仅出现在 rewrite/quality 步骤输入，不冒充服务端持久化产物）
  faithful_rewrite: {
    type: "faithful_rewrite",
    label: "忠实改写",
    description: "润色/重构文稿，保留事实、观点与作者语感，剔除标记的内部信息。",
    orchestration: "fast",
    templateId: "tpl_fast_v1",
    intentOperation: "rewrite",
    evidencePolicy: { name: "faithful_rewrite", governedIndex: 0, governedNode: "rewrite" },
    initialArtifactTypes: ["contract", "materials"],
    summary:
      "Faithful rewrite pipeline (tpl_fast_v1): rewrite → quality → finalize; evidence policy faithful_rewrite governs rewrite@index 0",
    steps: [
      {
        step_id: "rewrite",
        capability: "core.draft.generate",
        description: "Rewrite preserving facts, opinions and author voice",
        inputs: ["contract", "materials", "article"],
        outputs: ["full_draft"],
      },
      {
        step_id: "quality",
        capability: "core.validation.quality",
        description: "Quality validation",
        inputs: ["contract", "materials", "article", "full_draft"],
        outputs: ["quality_report"],
        depends_on: ["rewrite"],
      },
      {
        step_id: "finalize",
        capability: "core.document.finalize",
        description: "Finalize revision set",
        inputs: ["full_draft", "quality_report"],
        outputs: ["revision_set"],
        depends_on: ["quality"],
      },
    ],
  },
};

/**
 * 解析流程类型：缺省或非法值一律回退 long_form（默认行为与现状等价）。
 * 入参放宽为 unknown，覆盖来自 payload / URL / 存储的不可信值。
 */
export function resolveWritingFlow(flow?: unknown): WritingFlowSpec {
  if (typeof flow === "string" && flow in WRITING_FLOW_SPECS) {
    return WRITING_FLOW_SPECS[flow as WritingFlowType];
  }
  return WRITING_FLOW_SPECS[DEFAULT_WRITING_FLOW];
}

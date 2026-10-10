/**
 * WP4 写作流程词表 — 单源在服务端（GET /api/v2/writing/flows，见后端
 * writing_flows.go）。本模块是前端消费层：
 *
 * - WRITING_FLOW_SPECS 由 loadWritingFlows() 用服务端 payload 整体覆盖；
 *   下面的 FALLBACK 表只在服务端不可达（离线/启动竞态）时兜底，允许与
 *   服务端漂移——语义真源永远是服务端下发的那份。
 * - 合同与 intent plan 早已封存下沉服务端（POST /documents/{id}/writing-
 *   contract-draft）：启动请求只透传用户选择，本表不参与请求构造。
 * - composer 挂载时调用 ensureWritingFlowsLoaded()（幂等、失败静默保持
 *   fallback）；startWritingRun 读 initialArtifactTypes 等语义字段发生在
 *   用户提交时，此时水合已完成。
 *
 * FALLBACK 表结构注释（历史语义，仅兜底用）：
 * - orchestration → 合同 collaboration.orchestration_mode 的服务端取值
 * - intentOperation → 合同 intent.operation 的服务端取值
 * - steps → 服务端意图计划节点序列的前端展示词汇（含 inputs/outputs 装饰）
 * - evidencePolicy → 逐字对齐后端 EvidenceScenarioPolicies（research_review
 *   在 legacy 情景表刻意无行；服务端下发时该字段为 null）
 */

// ─── 流程类型 ────────────────────────────────────────────

export type WritingFlowType = "long_form" | "multi_material" | "faithful_rewrite" | "research_review";

export const WRITING_FLOW_TYPES = ["long_form", "multi_material", "faithful_rewrite", "research_review"] as const;

/** 缺省流程：与引入选择 UI 之前的行为等价。 */
export const DEFAULT_WRITING_FLOW: WritingFlowType = "long_form";

/** 流程的启动链归属：writing = writing-contract-draft 封存链；research = research-contract-draft 专属链。 */
export type WritingFlowLaunch = "writing" | "research";

/** 与后端 writingruntime.EvidenceScenarioPolicies 对应的 evidence policy 条目（服务端下发；research_review 恒为 null）。 */
export interface WritingFlowEvidencePolicy {
  name: WritingFlowType;
  /** governed 节点在后端 evidence 情景节点列表中的索引 */
  governedIndex: number;
  /** governed 节点名（docs/28 叫法） */
  governedNode: "draft" | "synthesis" | "rewrite";
}

/** intent plan proposed_steps 单步（展示词汇；真实提交由服务端构造四字段 ProposedStep）。 */
export interface WritingFlowPlanStep {
  step_id: string;
  capability: string;
  description: string;
  inputs?: string[];
  outputs?: string[];
  depends_on?: string[];
}

export interface WritingFlowSpec {
  type: WritingFlowType;
  /** UI 标签（中文） */
  label: string;
  /** UI 一句话说明（中文，docs/28 试点场景文案） */
  description: string;
  /** 启动链归属（服务端下发；决定 startWritingRun / startResearchRun 分流） */
  launch: WritingFlowLaunch;
  /** 合同 collaboration.orchestration_mode（决定服务端计划模板） */
  orchestration: "outline_first" | "sourced" | "fast" | "research_review";
  /** 服务端计划模板 id（docs/28；由 orchestration 间接选定） */
  templateId: string;
  /** 合同 intent.operation（writingkernel.Operation 枚举值） */
  intentOperation: string;
  /** docs/28 命名 evidence policy + governed 节点索引（服务端活表下发；research_review 为 null） */
  evidencePolicy: WritingFlowEvidencePolicy | null;
  /** 运行初始 artifact（后端 CompilePlan 仅接受 ["contract"] 或 ["contract","materials"]） */
  initialArtifactTypes: string[];
  /** intent plan proposed_steps 节点序列（docs/28 节点图展示词汇） */
  steps: WritingFlowPlanStep[];
  /** intent plan summary */
  summary: string;
}

// ─── 服务端 payload 契约（GET /api/v2/writing/flows） ──────────────────────

interface WritingFlowsPayload {
  flows: WritingFlowSpec[];
  default_flow: WritingFlowType;
}

// ─── FALLBACK 表（仅离线兜底，允许与服务端漂移） ────────────────────────────

const FALLBACK_WRITING_FLOW_SPECS: Record<WritingFlowType, WritingFlowSpec> = {
  // 长文创作：contract → outline → draft → quality → finalize → revision_set
  long_form: {
    type: "long_form",
    label: "长文创作",
    description: "从简报与 3–5 份材料产出约 3000 字行业分析长文。",
    launch: "writing",
    orchestration: "outline_first",
    templateId: "tpl_outline_first_v1",
    intentOperation: "create",
    evidencePolicy: { name: "long_form", governedIndex: 1, governedNode: "draft" },
    initialArtifactTypes: ["contract"],
    summary:
      "Long-form writing pipeline (tpl_outline_first_v1): outline → draft → quality → finalize; evidence policy long_form governs draft@index 1",
    steps: [
      { step_id: "outline", capability: "core.outline.generate", description: "Generate article outline", inputs: ["contract"], outputs: ["outline"] },
      { step_id: "draft", capability: "core.draft.generate", description: "Write full draft", inputs: ["contract", "outline"], outputs: ["full_draft"], depends_on: ["outline"] },
      { step_id: "quality", capability: "core.validation.quality", description: "Quality validation", inputs: ["contract", "full_draft"], outputs: ["quality_report"], depends_on: ["draft"] },
      { step_id: "finalize", capability: "core.document.finalize", description: "Finalize revision set", inputs: ["full_draft", "quality_report"], outputs: ["revision_set"], depends_on: ["quality"] },
    ],
  },
  // 多材料综合：contract + materials → synthesis → quality → finalize → revision_set
  multi_material: {
    type: "multi_material",
    label: "多材料综合",
    description: "把多份（可含冲突数据的）材料综合成统一分析并标注分歧出处。",
    launch: "writing",
    orchestration: "sourced",
    templateId: "tpl_sourced_v1",
    intentOperation: "synthesize",
    evidencePolicy: { name: "multi_material", governedIndex: 1, governedNode: "synthesis" },
    initialArtifactTypes: ["contract", "materials"],
    summary:
      "Multi-material synthesis pipeline (tpl_sourced_v1): synthesis → quality → finalize; evidence policy multi_material governs synthesis@index 1",
    steps: [
      { step_id: "synthesis", capability: "core.draft.generate", description: "Synthesize materials into unified analysis", inputs: ["contract", "materials"], outputs: ["full_draft"] },
      { step_id: "quality", capability: "core.validation.quality", description: "Quality validation", inputs: ["contract", "materials", "full_draft"], outputs: ["quality_report"], depends_on: ["synthesis"] },
      { step_id: "finalize", capability: "core.document.finalize", description: "Finalize revision set", inputs: ["full_draft", "quality_report"], outputs: ["revision_set"], depends_on: ["quality"] },
    ],
  },
  // 忠实改写：contract + materials + article → rewrite → quality → finalize → revision_set
  // （article 是用户随消息提供的原稿上下文，仅出现在 rewrite/quality 步骤输入）
  faithful_rewrite: {
    type: "faithful_rewrite",
    label: "忠实改写",
    description: "润色/重构文稿，保留事实、观点与作者语感，剔除标记的内部信息。",
    launch: "writing",
    orchestration: "fast",
    templateId: "tpl_fast_v1",
    intentOperation: "rewrite",
    evidencePolicy: { name: "faithful_rewrite", governedIndex: 0, governedNode: "rewrite" },
    initialArtifactTypes: ["contract", "materials"],
    summary:
      "Faithful rewrite pipeline (tpl_fast_v1): rewrite → quality → finalize; evidence policy faithful_rewrite governs rewrite@index 0",
    steps: [
      { step_id: "rewrite", capability: "core.draft.generate", description: "Rewrite preserving facts, opinions and author voice", inputs: ["contract", "materials", "article"], outputs: ["full_draft"] },
      { step_id: "quality", capability: "core.validation.quality", description: "Quality validation", inputs: ["contract", "materials", "article", "full_draft"], outputs: ["quality_report"], depends_on: ["rewrite"] },
      { step_id: "finalize", capability: "core.document.finalize", description: "Finalize revision set", inputs: ["full_draft", "quality_report"], outputs: ["revision_set"], depends_on: ["quality"] },
    ],
  },
  // 深度研究（第四流程）：启动链走 research-contract-draft；fallback 里
  // evidencePolicy 保留旧值仅为兜底渲染，服务端下发后该字段为 null。
  research_review: {
    type: "research_review",
    label: "深度研究",
    description: "围绕研究问题检索与精读文献，逐条引用核查，经两个确认点产出学术综述。",
    launch: "research",
    orchestration: "research_review",
    templateId: "tpl_research_review_v1",
    intentOperation: "create",
    evidencePolicy: { name: "research_review", governedIndex: 5, governedNode: "draft" },
    initialArtifactTypes: ["contract", "materials"],
    summary:
      "Deep research pipeline (tpl_research_review_v1): discover → read → evidence gate → outline → outline gate → draft → citations/fact → quality → finalize; human gates at evidence and outline; evidence policy research_review governs draft@index 5",
    steps: [
      { step_id: "discover", capability: "core.research.discover", description: "Discover candidate literature", inputs: ["contract", "materials"], outputs: ["research_candidates"] },
      { step_id: "read", capability: "core.research.read", description: "Rank, fetch and read papers into an evidence pack", inputs: ["contract", "research_candidates", "materials"], outputs: ["research_evidence_pack"], depends_on: ["discover"] },
      { step_id: "evidence_gate", capability: "core.research.gate.evidence", description: "Human gate: confirm the evidence pack", inputs: ["research_evidence_pack"], outputs: ["evidence_approval"], depends_on: ["read"] },
      { step_id: "outline", capability: "core.research.outline", description: "Assemble the grounded review outline", inputs: ["contract", "research_evidence_pack", "evidence_approval"], outputs: ["research_outline"], depends_on: ["evidence_gate"] },
      { step_id: "outline_gate", capability: "core.research.gate.outline", description: "Human gate: confirm the outline", inputs: ["research_outline"], outputs: ["approved_research_outline"], depends_on: ["outline"] },
      { step_id: "draft", capability: "core.research.draft", description: "Write the cited review draft", inputs: ["contract", "research_evidence_pack", "approved_research_outline"], outputs: ["full_draft"], depends_on: ["outline_gate"] },
      { step_id: "citations", capability: "core.research.validate.citations", description: "Validate every citation against the evidence pack", inputs: ["research_evidence_pack", "full_draft"], outputs: ["evidence_report"], depends_on: ["draft"] },
      { step_id: "fact", capability: "core.research.validate.fact", description: "Fact-review the draft claims", inputs: ["research_evidence_pack", "full_draft"], outputs: ["fact_report"], depends_on: ["draft"] },
      { step_id: "quality", capability: "core.validation.quality", description: "Quality validation", inputs: ["full_draft", "evidence_report", "fact_report"], outputs: ["quality_report"], depends_on: ["draft"] },
      { step_id: "finalize", capability: "core.document.finalize", description: "Finalize revision set", inputs: ["full_draft", "quality_report"], outputs: ["revision_set"], depends_on: ["quality"] },
    ],
  },
};

// ─── 活词表（服务端下发覆盖；消费者同步读取） ────────────────────────────────

export const WRITING_FLOW_SPECS: Record<WritingFlowType, WritingFlowSpec> = structuredClone(FALLBACK_WRITING_FLOW_SPECS);

let flowsLoad: Promise<void> | null = null;

/** 从服务端拉取流程词表并整体覆盖活表。失败静默（保持 fallback），幂等。 */
export function loadWritingFlows(): Promise<void> {
  if (flowsLoad) return flowsLoad;
  flowsLoad = (async () => {
    try {
      const token = localStorage.getItem("token");
      const response = await fetch("/api/v2/writing/flows", {
        headers: {
          Accept: "application/json",
          ...(token ? { Authorization: `Bearer ${token}` } : {}),
        },
      });
      if (!response.ok) return;
      const body = (await response.json()) as { success?: boolean; data?: WritingFlowsPayload };
      const payload = body?.data;
      if (!payload?.flows?.length) return;
      const next = {} as Record<WritingFlowType, WritingFlowSpec>;
      for (const spec of payload.flows) {
        if (WRITING_FLOW_TYPES.includes(spec.type as WritingFlowType)) {
          next[spec.type as WritingFlowType] = spec;
        }
      }
      for (const flow of WRITING_FLOW_TYPES) {
        if (next[flow]) WRITING_FLOW_SPECS[flow] = next[flow];
      }
    } catch {
      // 服务端不可达：保持 fallback（允许漂移的兜底）
    }
  })();
  return flowsLoad;
}

/** 幂等水合入口：composer 挂载时调用；resolve 后活表已是「服务端或 fallback」。 */
export function ensureWritingFlowsLoaded(): Promise<void> {
  return loadWritingFlows();
}

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

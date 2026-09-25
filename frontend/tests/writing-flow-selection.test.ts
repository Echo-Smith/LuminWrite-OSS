/**
 * WP4 写作流程选择测试（docs/28-wp4-pilot-scenarios.md + WP4 扩展第四流程）：
 * - 四流程各自 plan 构建的节点形状/顺序（docs/28 节点图 → 实际 artifact 词汇）
 * - evidence policy 与 governed 节点索引映射（逐字对齐后端 evidence_policy.go；
 *   深度研究刻意不在 legacy 情景表内，治理点在模板内人工门与验证器）
 * - 缺省 flow=long_form（默认与现状等价）、非法值回退 long_form
 * - 流程贯穿 startWritingRun：contract（intent.operation / orchestration_mode）
 *   与 plan（节点序列 / initial_artifact_types / summary 落点标注）
 * - 深度研究（research_review）有专属启动链（research-contract-draft 封存
 *   合同 → startResearchRun），startWritingRun 提前拒绝
 * - 初始 artifact、模板 id、能力 id 与后端约束一致（不发明后端不认识的字段）
 * - UI 接线：flow-picker 挂在写作入口，payload 携带 flow；研究流选项不受
 *   前端开关限制
 */
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

import { buildIntentPlan, buildWritingContract, startWritingRun } from "../src/lib/writing-api.ts";
import { buildResearchSpec, defaultResearchSpecDraft, startResearchRun } from "../src/lib/research-api.ts";
import {
  DEFAULT_WRITING_FLOW,
  WRITING_FLOW_SPECS,
  WRITING_FLOW_TYPES,
  resolveWritingFlow,
  type WritingFlowType,
} from "../src/lib/writing-flows.ts";
import { ORCHESTRATION_MODES } from "../src/lib/writing-runtime-types.ts";

const source = (path: string) => readFileSync(new URL(path, import.meta.url), "utf8");

// node 测试环境没有 localStorage（writingFetch 读取 token 用），补最小桩
globalThis.localStorage = {
  getItem: () => null,
  setItem: () => {},
  removeItem: () => {},
} as unknown as Storage;

const CONTRACT_REF = { id: "ctr_flow_test", version: 1, hash: `sha256:${"ab".repeat(32)}` };

/** 节点摘要形状（不含 description），便于对齐 docs/28 节点图断言 */
function stepShapes(flow: WritingFlowType) {
  return buildIntentPlan(CONTRACT_REF, flow).proposed_steps.map((step) => ({
    step_id: step.step_id,
    capability: step.capability,
    inputs: step.inputs,
    outputs: step.outputs,
    depends_on: step.depends_on ?? [],
  }));
}

// ─── fetch mock 基建（沿用 research-launch.test.ts 的模式） ─────────────────

interface RecordedCall {
  path: string;
  method: string;
  body: Record<string, unknown> | null;
}

type MockResult = { status: number; body: unknown };

const originalFetch = globalThis.fetch;
let calls: RecordedCall[] = [];

function ok(data: unknown, status = 200): MockResult {
  return { status, body: { success: true, data } };
}

function fail(status: number, code: string, message: string): MockResult {
  return { status, body: { success: false, error: { code, message } } };
}

function installFetch(handler: (path: string, method: string, body: Record<string, unknown> | null) => MockResult | undefined): void {
  calls = [];
  globalThis.fetch = (async (input: RequestInfo | URL, init: RequestInit = {}) => {
    const path = typeof input === "string" ? input : String(input instanceof URL ? input : input.url);
    const method = (init.method ?? "GET").toUpperCase();
    const body = typeof init.body === "string" ? (JSON.parse(init.body) as Record<string, unknown>) : null;
    calls.push({ path, method, body });
    const result = handler(path, method, body) ?? fail(404, "WRITING_RESOURCE_NOT_FOUND", `unmocked ${method} ${path}`);
    return new Response(JSON.stringify(result.body), { status: result.status, headers: { "Content-Type": "application/json" } });
  }) as typeof fetch;
}

test.afterEach(() => {
  globalThis.fetch = originalFetch;
});

// ─── plan 节点形状/顺序 ─────────────────────────────────────────────────────

test("三个写作流程各自产出 docs/28 节点图对应的 plan 节点形状与顺序", () => {
  // 长文创作：contract → outline → draft → quality → finalize → revision_set
  const longForm = stepShapes("long_form");
  assert.deepEqual(longForm.map((step) => step.step_id), ["outline", "draft", "quality", "finalize"]);
  assert.deepEqual(longForm[0], {
    step_id: "outline",
    capability: "core.outline.generate",
    inputs: ["contract"],
    outputs: ["outline"],
    depends_on: [],
  });
  assert.deepEqual(longForm[1], {
    step_id: "draft",
    capability: "core.draft.generate",
    inputs: ["contract", "outline"],
    outputs: ["full_draft"],
    depends_on: ["outline"],
  });
  assert.deepEqual(longForm[2], {
    step_id: "quality",
    capability: "core.validation.quality",
    inputs: ["contract", "full_draft"],
    outputs: ["quality_report"],
    depends_on: ["draft"],
  });

  // 多材料综合：contract + materials → synthesis → quality → finalize → revision_set
  const multiMaterial = stepShapes("multi_material");
  assert.deepEqual(multiMaterial.map((step) => step.step_id), ["synthesis", "quality", "finalize"]);
  assert.deepEqual(multiMaterial[0], {
    step_id: "synthesis",
    capability: "core.draft.generate",
    inputs: ["contract", "materials"],
    outputs: ["full_draft"],
    depends_on: [],
  });
  assert.deepEqual(multiMaterial[1].inputs, ["contract", "materials", "full_draft"]);
  assert.deepEqual(multiMaterial[1].depends_on, ["synthesis"]);

  // 忠实改写：contract + materials + article → rewrite → quality → finalize → revision_set
  const faithfulRewrite = stepShapes("faithful_rewrite");
  assert.deepEqual(faithfulRewrite.map((step) => step.step_id), ["rewrite", "quality", "finalize"]);
  assert.deepEqual(faithfulRewrite[0], {
    step_id: "rewrite",
    capability: "core.draft.generate",
    inputs: ["contract", "materials", "article"],
    outputs: ["full_draft"],
    depends_on: [],
  });
  assert.deepEqual(faithfulRewrite[1].depends_on, ["rewrite"]);

  // docs/28 节点名 → 实际 artifact 词汇：draft/synthesis/rewrite 均产出 full_draft；
  // quality 产出 quality_report；收尾 finalize 产出 revision_set（与现状 builder 词汇一致）
  for (const flow of WRITING_FLOW_TYPES) {
    const steps = stepShapes(flow);
    const generation = steps.filter((step) => step.outputs.includes("full_draft"));
    assert.equal(generation.length, 1, `${flow} 应有唯一生成节点产出 full_draft`);
    assert.equal(generation[0].step_id, WRITING_FLOW_SPECS[flow].evidencePolicy.governedNode);
    assert.deepEqual(steps.find((step) => step.step_id === "quality")?.outputs, ["quality_report"]);
    const last = steps[steps.length - 1];
    assert.deepEqual(last, {
      step_id: "finalize",
      capability: "core.document.finalize",
      inputs: ["full_draft", "quality_report"],
      outputs: ["revision_set"],
      depends_on: ["quality"],
    });
  }
});

// ─── 第四流程：深度研究（research_review） ─────────────────────────────────

test("深度研究 spec 承载 tpl_research_review_v1 十节点主链的简述形式", () => {
  const research = WRITING_FLOW_SPECS.research_review;
  assert.equal(research.label, "深度研究");
  assert.equal(research.orchestration, "research_review");
  assert.equal(research.templateId, "tpl_research_review_v1");
  assert.equal(research.intentOperation, "create");
  assert.deepEqual(research.initialArtifactTypes, ["contract", "materials"]);

  // 节点图简述：discover → read → 证据门 → outline → 提纲门 → draft →
  // citations → fact → quality → finalize（线性主链；真实依赖边以服务端模板为权威）
  const steps = stepShapes("research_review");
  assert.deepEqual(steps.map((step) => step.step_id), [
    "discover", "read", "evidence_gate", "outline", "outline_gate",
    "draft", "citations", "fact", "quality", "finalize",
  ]);
  assert.deepEqual(steps.map((step) => step.capability), [
    "core.research.discover",
    "core.research.read",
    "core.research.gate.evidence",
    "core.research.outline",
    "core.research.gate.outline",
    "core.research.draft",
    "core.research.validate.citations",
    "core.research.validate.fact",
    "core.validation.quality",
    "core.document.finalize",
  ]);
  // 两个人工门产出审批产物；draft 产出 full_draft（唯一生成节点）
  assert.deepEqual(steps[2].outputs, ["evidence_approval"]);
  assert.deepEqual(steps[4].outputs, ["approved_research_outline"]);
  assert.deepEqual(steps[5].outputs, ["full_draft"]);
  // 模板里 draft 之后的验证器消费 citation index / evidence pack（简述保留关键输入）
  assert.deepEqual(steps[6].inputs, ["research_evidence_pack", "full_draft"]);
  assert.deepEqual(steps[7].inputs, ["research_evidence_pack", "full_draft"]);
  assert.deepEqual(steps[8].inputs, ["full_draft", "evidence_report", "fact_report"]);
});

// ─── evidence policy 与 governed 索引映射 ───────────────────────────────────

test("evidence policy 与 governed 索引映射逐字对齐后端 scenario 表", () => {
  assert.deepEqual(WRITING_FLOW_SPECS.long_form.evidencePolicy, { name: "long_form", governedIndex: 1, governedNode: "draft" });
  assert.deepEqual(WRITING_FLOW_SPECS.multi_material.evidencePolicy, { name: "multi_material", governedIndex: 1, governedNode: "synthesis" });
  assert.deepEqual(WRITING_FLOW_SPECS.faithful_rewrite.evidencePolicy, { name: "faithful_rewrite", governedIndex: 0, governedNode: "rewrite" });

  // 后端 EvidenceScenarioPolicies 是权威表（Name / GovernedIndex / NodeName），逐字比对
  const backend = source("../../backend/internal/writingruntime/evidence_policy.go");
  const rows = [...backend.matchAll(/\{Name: "(\w+)", GovernedIndex: (\d+), NodeName: "(\w+)"\}/g)].map((match) => ({
    name: String(match[1]),
    governedIndex: Number(match[2]),
    governedNode: String(match[3]),
  }));
  assert.deepEqual(rows, [
    { name: "long_form", governedIndex: 1, governedNode: "draft" },
    { name: "multi_material", governedIndex: 1, governedNode: "synthesis" },
    { name: "faithful_rewrite", governedIndex: 0, governedNode: "rewrite" },
  ]);
  // 前三个流程逐字对齐后端表
  const legacyFlows: WritingFlowType[] = ["long_form", "multi_material", "faithful_rewrite"];
  for (const flow of legacyFlows) {
    const policy = WRITING_FLOW_SPECS[flow].evidencePolicy;
    assert.deepEqual(
      { name: policy.name, governedIndex: policy.governedIndex, governedNode: policy.governedNode },
      rows.find((row) => row.name === flow),
      `${flow} 的 evidence policy 应与后端表一致`,
    );
  }
  // 深度研究刻意不在 legacy evidence 情景表内：它的治理点是模板内两个人工门
  // 与 citations/fact/quality 验证器，不接入 legacy evidence harness。
  assert.equal(rows.find((row) => row.name === "research_review"), undefined);
  const research = WRITING_FLOW_SPECS.research_review;
  assert.deepEqual(research.evidencePolicy, { name: "research_review", governedIndex: 5, governedNode: "draft" });

  // 落点核对：governed 节点即 plan 里产出 full_draft 的生成步骤；summary 标注 policy 名与索引
  for (const flow of WRITING_FLOW_TYPES) {
    const spec = WRITING_FLOW_SPECS[flow];
    const steps = stepShapes(flow);
    const governed = steps.filter((step) => step.step_id === spec.evidencePolicy.governedNode);
    assert.equal(governed.length, 1, `${flow} 的 governed 节点应唯一出现在 plan 节点序列`);
    assert.deepEqual(governed[0].outputs, ["full_draft"]);
    const plan = buildIntentPlan(CONTRACT_REF, flow);
    assert.ok(
      plan.summary.includes(`evidence policy ${flow} governs ${spec.evidencePolicy.governedNode}@index ${spec.evidencePolicy.governedIndex}`),
      `${flow} 的 summary 应标注 evidence policy 落点`,
    );
  }
});

// ─── 缺省与回退 ─────────────────────────────────────────────────────────────

test("缺省 flow = long_form，默认行为与现状等价", () => {
  assert.equal(DEFAULT_WRITING_FLOW, "long_form");
  assert.deepEqual(WRITING_FLOW_TYPES, ["long_form", "multi_material", "faithful_rewrite", "research_review"]);
  assert.equal(resolveWritingFlow(undefined), WRITING_FLOW_SPECS.long_form);

  // 计划：缺省调用与显式 long_form 一致，且前三步与引入流程选择前的 builder 逐字一致
  const implicit = buildIntentPlan(CONTRACT_REF);
  const explicit = buildIntentPlan(CONTRACT_REF, "long_form");
  assert.deepEqual(implicit.proposed_steps, explicit.proposed_steps);
  assert.equal(implicit.summary, explicit.summary);
  assert.deepEqual(implicit.proposed_steps.slice(0, 3), [
    { step_id: "outline", capability: "core.outline.generate", description: "Generate article outline", inputs: ["contract"], outputs: ["outline"] },
    { step_id: "draft", capability: "core.draft.generate", description: "Write full draft", inputs: ["contract", "outline"], outputs: ["full_draft"], depends_on: ["outline"] },
    { step_id: "quality", capability: "core.validation.quality", description: "Quality validation", inputs: ["contract", "full_draft"], outputs: ["quality_report"], depends_on: ["draft"] },
  ]);

  // 合同：缺省 payload 与显式 long_form 完全一致
  assert.deepEqual(buildWritingContract({ message: "  写一篇行业分析  " }), buildWritingContract({ message: "  写一篇行业分析  ", flow: "long_form" }));
  const contract = buildWritingContract({ message: "写一篇行业分析" });
  assert.equal(contract.intent.operation, "create");
  assert.equal(contract.collaboration.orchestration_mode, "outline_first");
});

test("非法 flow 值回退 long_form", () => {
  for (const bad of ["", "LONG_FORM", "long-form", "auto", "topic", 42, null, true, {}, []]) {
    assert.equal(resolveWritingFlow(bad), WRITING_FLOW_SPECS.long_form, `非法值 ${String(bad)} 应回退 long_form`);
  }

  const fallbackPlan = buildIntentPlan(CONTRACT_REF, "unknown_flow" as WritingFlowType);
  assert.deepEqual(fallbackPlan.proposed_steps, buildIntentPlan(CONTRACT_REF, "long_form").proposed_steps);
  assert.equal(fallbackPlan.summary, WRITING_FLOW_SPECS.long_form.summary);

  const fallbackContract = buildWritingContract({ message: "m", flow: "nonsense" as WritingFlowType });
  assert.deepEqual(fallbackContract, buildWritingContract({ message: "m", flow: "long_form" }));
  assert.deepEqual(fallbackContract, buildWritingContract({ message: "m" }));
});

// ─── contract 贯穿与后端约束对齐 ────────────────────────────────────────────

test("流程贯穿 contract：intent.operation 与 orchestration_mode 按流程变化", () => {
  const multi = buildWritingContract({ message: "综合这批材料", flow: "multi_material" });
  assert.equal(multi.intent.operation, "synthesize");
  assert.equal(multi.collaboration.orchestration_mode, "sourced");

  const rewrite = buildWritingContract({ message: "改写这篇稿子", flow: "faithful_rewrite" });
  assert.equal(rewrite.intent.operation, "rewrite");
  assert.equal(rewrite.collaboration.orchestration_mode, "fast");

  // 值全部落在真实词表内：orchestration 与后端 OrchestrationMode 枚举一致，
  // operation 与 writingkernel.Operation（create/synthesize/rewrite）一致
  for (const flow of WRITING_FLOW_TYPES) {
    const spec = WRITING_FLOW_SPECS[flow];
    assert.ok(ORCHESTRATION_MODES.includes(spec.orchestration));
    assert.ok(["create", "synthesize", "rewrite"].includes(spec.intentOperation));
  }
  assert.equal(WRITING_FLOW_SPECS.long_form.templateId, "tpl_outline_first_v1");
  assert.equal(WRITING_FLOW_SPECS.multi_material.templateId, "tpl_sourced_v1");
  assert.equal(WRITING_FLOW_SPECS.faithful_rewrite.templateId, "tpl_fast_v1");
  assert.equal(WRITING_FLOW_SPECS.research_review.templateId, "tpl_research_review_v1");
});

test("初始 artifact 与模板/能力 id 符合后端约束", () => {
  // 后端 CompilePlan 只接受 ["contract"] 或 ["contract","materials"]；article 是改写
  // 流程的原稿上下文，仅作为 rewrite/quality 步骤输入，不冒充初始 artifact
  assert.deepEqual(WRITING_FLOW_SPECS.long_form.initialArtifactTypes, ["contract"]);
  assert.deepEqual(WRITING_FLOW_SPECS.multi_material.initialArtifactTypes, ["contract", "materials"]);
  assert.deepEqual(WRITING_FLOW_SPECS.faithful_rewrite.initialArtifactTypes, ["contract", "materials"]);
  assert.deepEqual(WRITING_FLOW_SPECS.research_review.initialArtifactTypes, ["contract", "materials"]);

  const templates = source("../../backend/internal/writingplan/templates.go");
  const capabilities = source("../../backend/internal/writingplan/capability.go");
  for (const flow of WRITING_FLOW_TYPES) {
    const spec = WRITING_FLOW_SPECS[flow];
    assert.ok(templates.includes(`"${spec.templateId}"`), `${spec.templateId} 应在服务端模板注册表中`);
    for (const step of spec.steps) {
      assert.ok(capabilities.includes(`"${step.capability}"`), `${step.capability} 应是后端真实能力 id`);
    }
  }
});

// ─── startWritingRun 全链路贯穿（HTTP-mocked fetch） ────────────────────────

test("startWritingRun 把流程贯穿到 contract 与 plan 请求体（多材料综合）", async () => {
  let draftContract: Record<string, unknown> | null = null;
  let planBody: Record<string, unknown> | null = null;
  installFetch((path, method, body) => {
    if (method === "POST" && path === "/api/v2/documents") return ok({ document_id: "doc_flow_1", current_version_id: "" }, 201);
    if (method === "POST" && path === "/api/v2/documents/doc_flow_1/contracts") {
      draftContract = body?.contract as Record<string, unknown>;
      return ok({ contract_id: "ctr_flow_1", version: 1, contract_hash: CONTRACT_REF.hash, status: "draft" }, 201);
    }
    if (method === "POST" && path === "/api/v2/contracts/ctr_flow_1/confirm") {
      return ok({ contract_id: "ctr_flow_1", version: 2, contract_hash: CONTRACT_REF.hash, status: "confirmed" });
    }
    if (method === "POST" && path === "/api/v2/documents/doc_flow_1/plans") {
      planBody = body;
      return ok({ plan: { executable_plan: { plan_id: "plan_flow_1", plan_hash: CONTRACT_REF.hash } }, permissions: ["model.invoke"] });
    }
    if (method === "POST" && path === "/api/v2/runs") return ok({ run_id: "run_flow_1", status: "awaiting_approval" }, 201);
    if (method === "POST" && path === "/api/v2/runs/run_flow_1/approve") return ok({ run_id: "run_flow_1", status: "planned" });
    return undefined;
  });

  const result = await startWritingRun({ message: "综合这批材料", flow: "multi_material" });
  assert.equal(result.run_id, "run_flow_1");

  const contract = draftContract as unknown as { intent: { operation: string }; collaboration: { orchestration_mode: string } };
  assert.equal(contract.intent.operation, "synthesize");
  assert.equal(contract.collaboration.orchestration_mode, "sourced");

  const plan = planBody as unknown as {
    initial_artifact_types: string[];
    required_final_artifact: string;
    intent_plan: { summary: string; proposed_steps: Array<{ step_id: string }> };
  };
  assert.deepEqual(plan.initial_artifact_types, ["contract", "materials"]);
  assert.equal(plan.required_final_artifact, "revision_set");
  assert.deepEqual(plan.intent_plan.proposed_steps.map((step) => step.step_id), ["synthesis", "quality", "finalize"]);
  assert.ok(plan.intent_plan.summary.includes("evidence policy multi_material governs synthesis@index 1"));

  // awaiting_approval → 自动 approve
  assert.deepEqual(calls.map((call) => call.path), [
    "/api/v2/documents",
    "/api/v2/documents/doc_flow_1/contracts",
    "/api/v2/contracts/ctr_flow_1/confirm",
    "/api/v2/documents/doc_flow_1/plans",
    "/api/v2/runs",
    "/api/v2/runs/run_flow_1/approve",
  ]);
});

test("startWritingRun 缺省 flow 走 long_form 链路（初始 artifact 仅 contract）", async () => {
  let draftContract: Record<string, unknown> | null = null;
  let planBody: Record<string, unknown> | null = null;
  installFetch((path, method, body) => {
    if (method === "POST" && path === "/api/v2/documents") return ok({ document_id: "doc_flow_2", current_version_id: "" }, 201);
    if (method === "POST" && path === "/api/v2/documents/doc_flow_2/contracts") {
      draftContract = body?.contract as Record<string, unknown>;
      return ok({ contract_id: "ctr_flow_2", version: 1, contract_hash: CONTRACT_REF.hash, status: "draft" }, 201);
    }
    if (method === "POST" && path === "/api/v2/contracts/ctr_flow_2/confirm") {
      return ok({ contract_id: "ctr_flow_2", version: 2, contract_hash: CONTRACT_REF.hash, status: "confirmed" });
    }
    if (method === "POST" && path === "/api/v2/documents/doc_flow_2/plans") {
      planBody = body;
      return ok({ plan: { executable_plan: { plan_id: "plan_flow_2", plan_hash: CONTRACT_REF.hash } }, permissions: ["model.invoke"] });
    }
    if (method === "POST" && path === "/api/v2/runs") return ok({ run_id: "run_flow_2", status: "planned" }, 201);
    return undefined;
  });

  const result = await startWritingRun({ message: "写一篇行业分析" });
  assert.equal(result.run_id, "run_flow_2");

  const contract = draftContract as unknown as { intent: { operation: string }; collaboration: { orchestration_mode: string } };
  assert.equal(contract.intent.operation, "create");
  assert.equal(contract.collaboration.orchestration_mode, "outline_first");

  const plan = planBody as unknown as { initial_artifact_types: string[]; intent_plan: { proposed_steps: Array<{ step_id: string }> } };
  assert.deepEqual(plan.initial_artifact_types, ["contract"]);
  assert.deepEqual(plan.intent_plan.proposed_steps.map((step) => step.step_id), ["outline", "draft", "quality", "finalize"]);
  // planned 状态不需要 approve
  assert.equal(calls.at(-1)?.path, "/api/v2/runs");
});

// ─── UI 接线 ────────────────────────────────────────────────────────────────

test("写作入口提供流程选择并把所选流程注入 startWriting 数据流", () => {
  const picker = source("../src/components/composer/flow-picker.tsx");
  const composer = source("../src/components/composer/writing-composer.tsx");
  const flows = source("../src/lib/writing-flows.ts");

  // 四个选项与试点场景/深度研究的一句话说明（中文文案）
  assert.ok(flows.includes('label: "长文创作"'));
  assert.ok(flows.includes('label: "多材料综合"'));
  assert.ok(flows.includes('label: "忠实改写"'));
  assert.ok(flows.includes('label: "深度研究"'));
  assert.ok(flows.includes("从简报与 3–5 份材料产出约 3000 字行业分析长文。"));
  assert.ok(flows.includes("把多份（可含冲突数据的）材料综合成统一分析并标注分歧出处。"));
  assert.ok(flows.includes("润色/重构文稿，保留事实、观点与作者语感，剔除标记的内部信息。"));
  assert.ok(flows.includes("围绕研究问题检索与精读文献，逐条引用核查，经两个确认点产出学术综述。"));

  // 入口接线：控制行挂 FlowPicker（ModePicker 之前），payload 带 flow
  assert.match(composer, /<FlowPicker value=\{flow\} onChange=\{handleFlowChange\} compact=\{compact\} \/>/);
  assert.ok(composer.indexOf("<FlowPicker") < composer.indexOf("<ModePicker"));
  assert.ok(composer.includes("approval_mode: approvalMode,\n      flow,\n    });"));

  // 样式与 mode-picker.tsx 同款触发器/弹层结构
  assert.match(picker, /composer-mode-trigger/);
  assert.match(picker, /composer-control-label anim-fade-scale/);
  assert.match(picker, /composer-control-chevron/);
  assert.match(picker, /<PopoverContent align="start" className="w-72 p-1">/);
  assert.match(picker, /aria-label=\{`写作流程：\$\{spec\.label\}`\}/);
});

// ─── 第四流程启动链与开关产品化 ──────────────────────────────────────────────

test("flow-picker 的深度研究选项不受前端开关限制，选中即打开研究设置表单", () => {
  const picker = source("../src/components/composer/flow-picker.tsx");
  const composer = source("../src/components/composer/writing-composer.tsx");
  const modePicker = source("../src/components/composer/mode-picker.tsx");
  const researchApi = source("../src/lib/research-api.ts");

  // Telescope 图标 + research_review 选项；选择器不读任何 env/flag
  assert.match(picker, /research_review: Telescope/);
  assert.doesNotMatch(picker, /VITE_|envFlag|isResearchReviewHardOff/);

  // composer：选中研究流 → 编排模式 research_review + strict + 打开悬浮表单；
  // 发送键在研究流下打开表单而不是走 startWritingRun
  assert.match(composer, /if \(next === "research_review"\) \{/);
  assert.match(composer, /setOrchestrationMode\("research_review"\)/);
  assert.match(composer, /if \(flow === "research_review"\) \{\s*\n\s*setResearchSettingsOpen\(true\);\s*\n\s*return;/);

  // VITE 门控已删除：research-api 不再导出部署级硬开关，mode-picker 不再读取
  assert.doesNotMatch(researchApi, /VITE_RESEARCH_REVIEW_ENABLED|isResearchReviewHardOff|researchReviewDisabledReason/);
  assert.doesNotMatch(modePicker, /VITE_|isResearchReviewHardOff|enableResearchReview/);
});

test("深度研究的启动链不走 startWritingRun（提前拒绝，不发孤儿请求）", async () => {
  installFetch(() => {
    assert.fail("research_review flow must not drive startWritingRun");
  });
  await assert.rejects(
    startWritingRun({ message: "固态电解质界面稳定性综述", flow: "research_review" }),
    (error: unknown) => {
      assert.equal((error as { code?: string }).code, "RESEARCH_REVIEW_CONTRACT_INVALID");
      assert.equal((error as { status?: number }).status, 400);
      assert.match((error as Error).message, /研究设置/);
      return true;
    },
  );
  assert.equal(calls.length, 0);
});

test("深度研究启动链 mock：draft（服务端封存合同）→ contract → confirm → plans → runs", async () => {
  const draftContract = {
    schema_version: "lcp/1.1",
    contract_id: "ctr_flow_research",
    version: 1,
    status: "draft",
    collaboration: { task_mode: "guided", orchestration_mode: "research_review", assurance_level: "sourced", approval_mode: "conditional" },
    research: { version: "research-spec/1", max_papers: 10 },
    contract_hash: "sha256:" + "cd".repeat(32),
  };
  const confirmedContract = { ...draftContract, version: 2, status: "confirmed" };
  installFetch((path, method, body) => {
    if (method === "POST" && path === "/api/v2/documents") return ok({ document_id: "doc_flow_research", current_version_id: "" }, 201);
    if (method === "POST" && path === "/api/v2/documents/doc_flow_research/research-contract-draft") {
      // 服务端封存：请求体是「用户选择 + research-spec/1」，响应是两个合同版本
      assert.equal(body?.central_question, "固态电解质界面稳定性目前的共识与分歧是什么？");
      assert.equal(body?.audience, "材料学方向的研究生");
      assert.deepEqual(body?.research?.version, "research-spec/1");
      return ok({ document_id: "doc_flow_research", contract: draftContract, confirmed_contract: confirmedContract }, 201);
    }
    if (method === "POST" && path === "/api/v2/documents/doc_flow_research/contracts") {
      assert.deepEqual(body?.contract, draftContract, "封存合同必须原样转发");
      return ok({ document_id: "doc_flow_research", contract: { contract_id: "ctr_flow_research", version: 1, contract_hash: draftContract.contract_hash, status: "draft" } }, 201);
    }
    if (method === "POST" && path === "/api/v2/contracts/ctr_flow_research/confirm") {
      assert.equal(body?.previous_version, 1);
      assert.deepEqual(body?.contract, confirmedContract, "确认版本必须原样转发");
      return ok({ document_id: "doc_flow_research", contract: { contract_id: "ctr_flow_research", version: 2, contract_hash: "sha256:" + "ef".repeat(32), status: "confirmed" } });
    }
    if (method === "POST" && path === "/api/v2/documents/doc_flow_research/plans") {
      return ok({ plan: { executable_plan: { plan_id: "plan_flow_research", plan_hash: "sha256:" + "ab".repeat(32) } }, permissions: ["model.invoke"] });
    }
    if (method === "POST" && path === "/api/v2/runs") return ok({ run_id: "run_flow_research", status: "planned" }, 201);
    return undefined;
  });

  const { spec } = buildResearchSpec({ ...defaultResearchSpecDraft(), central_question: "固态电解质界面稳定性目前的共识与分歧是什么？" });
  assert.ok(spec);
  const { run_id } = await startResearchRun({
    spec,
    central_question: "固态电解质界面稳定性目前的共识与分歧是什么？",
    audience: "材料学方向的研究生",
    language: "中文",
    length_min: "3000",
    length_max: "6000",
    allow_external_research: true,
  });
  assert.equal(run_id, "run_flow_research");
  assert.deepEqual(calls.map((call) => `${call.method} ${call.path}`), [
    "POST /api/v2/documents",
    "POST /api/v2/documents/doc_flow_research/research-contract-draft",
    "POST /api/v2/documents/doc_flow_research/contracts",
    "POST /api/v2/contracts/ctr_flow_research/confirm",
    "POST /api/v2/documents/doc_flow_research/plans",
    "POST /api/v2/runs",
  ]);
});

test("非法 spec 在 draft 端点得到明确报错并在表单中呈现", async () => {
  // 表单层：buildResearchSpec 返回问题清单（submit 前拦截，不发起任何请求）
  const invalid = buildResearchSpec({ ...defaultResearchSpecDraft(), central_question: "", max_papers: "25" });
  assert.equal(invalid.spec, null);
  assert.deepEqual(invalid.problems, ["阅读上限需在 1–20 篇之间", "请填写研究问题"]);

  // 服务端层：draft 端点 400 INVALID_RESEARCH_SPEC 明确到字段
  installFetch((path, method) => {
    if (method === "POST" && path === "/api/v2/documents") return ok({ document_id: "doc_flow_bad", current_version_id: "" }, 201);
    if (method === "POST" && path === "/api/v2/documents/doc_flow_bad/research-contract-draft") {
      return fail(400, "INVALID_RESEARCH_SPEC", "writing api: invalid research request: research spec: research.max_papers must not exceed 20");
    }
    return undefined;
  });
  let presented: string | null = null;
  try {
    const { spec } = buildResearchSpec({ ...defaultResearchSpecDraft(), central_question: "问题" });
    await startResearchRun({ spec: spec!, central_question: "问题", audience: "读者", language: "中文", length_min: "3000", length_max: "6000", allow_external_research: true });
    assert.fail("invalid spec must reject");
  } catch (error) {
    presented = error instanceof Error ? error.message : String(error);
  }
  assert.match(presented ?? "", /research\.max_papers/);

  // 呈现层：research-settings 把问题与 apiError 渲染进 role="alert" 列表
  const settings = source("../src/components/writing/research-settings.tsx");
  assert.match(settings, /role="alert"/);
  assert.match(settings, /\{problems\.map\(\(problem\) => <li key=\{problem\}>\{problem\}<\/li>\)\}/);
  assert.match(settings, /\{apiError && <li>\{apiError\}<\/li>\}/);
  // 产品化：面板提交不再被实验室勾选或 env 开关禁用
  assert.doesNotMatch(settings, /featureEnabled|isResearchReviewHardOff|enableResearchReview/);
});

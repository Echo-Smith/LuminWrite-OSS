/**
 * 研究综述（research_review）API 层 — mock 开关集中在本模块。
 *
 * 默认关闭功能与 mock。演示必须显式设置 VITE_RESEARCH_MOCK=on；
 * 真实读取/确认/运行创建使用 /api/v2（与生产 server.go:761 的 writing 路由挂载一致；
 * 后端 e2e harness 自挂 /api/v2/writing，与其不同，以生产为准）：
 * startResearchRun 走 document → contract(v1.1) → confirm → compile → run
 * （→ awaiting_approval 时自动 approve）的真实创建链路。
 * 类型与 specs/research-review/contracts.md §2/§3、
 * backend/internal/server/writing_research_api.go 的视图一一对应。
 */
import type {
  DocumentRecord,
  EvidenceRequirement,
  ResearchSpec,
  RuntimeRun,
} from "./writing-runtime-types.ts";

// ─── 合同视图类型（contracts.md §3 / writing_research_api.go） ───

export interface ArtifactRef {
  artifact_id: string;
  version: number;
  content_hash: string;
}

export type ResearchGateKind = "evidence" | "outline";
export type ResearchGateStatus = "pending" | "approved" | "dismissed";

export interface GateView {
  gate_id: string;
  run_id: string;
  node_id: string;
  gate_kind: ResearchGateKind;
  plan_id: string;
  plan_version: number;
  plan_hash: string;
  revision: number;
  status: ResearchGateStatus;
  decision?: string;
  input_ref?: ArtifactRef;
  allowed_operations: string[];
  blocked_reason?: string;
  last_event_sequence: number;
}

export interface GateDecisionRequest {
  plan_id: string;
  plan_version: number;
  plan_hash: string;
  gate_revision: number;
  input_ref: ArtifactRef;
  decision: "approve";
}

export interface GateDecisionView {
  decision_id: string;
  gate_id: string;
  run_id: string;
  status: ResearchGateStatus;
  resume_status: string;
}

export interface OutlineRevisionRequest {
  plan_id: string;
  plan_version: number;
  plan_hash: string;
  gate_revision: number;
  outline_ref: ArtifactRef;
  outline: {
    sections: OutlineSection[];
    limitations: string[];
  };
}

export interface OutlineRevisionView {
  gate_id: string;
  gate_revision: number;
  outline_ref: ArtifactRef;
}

export interface ResearchTaskView {
  task_key: string;
  node_id: string;
  phase: string;
  status: string;
  attempt: number;
  error_code?: string;
}

/**
 * 运行合同的 spec 投影（T09：GET /runs/{id}/research 响应新增字段，
 * 从 research-spec/1 运行合同投影，字段为 UI 展示所需子集）。
 */
export interface ResearchSpecProjection {
  max_papers: number;
  min_citable_sources: number;
  evidence_requirement: EvidenceRequirement;
  max_queries: number;
  max_candidates: number;
}

export interface ResearchProgressView {
  run_id: string;
  phase: string;
  /**
   * 任务与论文计数。T09 起新增论文阅读键族：papers_full_text / papers_abstract /
   * papers_unread（按阅读范围统计的论文篇数）；既有键（total/completed/…）保留。
   */
  counts: Record<string, number>;
  /** 运行合同投影（T09 起提供）；旧后端缺省为 null/undefined，前端回退启动表单值。 */
  spec?: ResearchSpecProjection | null;
  tasks: ResearchTaskView[];
  pack_ref?: ArtifactRef;
  active_gate?: GateView;
  errors: ResearchTaskView[];
  last_event_sequence: number;
}

export interface ArtifactContentView {
  artifact_id: string;
  version: number;
  content_hash: string;
  media_type: string;
  content: unknown;
}

// ─── 校验产物视图（T07 evidence_report / research_validation_details，UI 需要的字段子集） ───

/** evidence_report.issues（validatorReport 的 issues 数组条目）。 */
export interface EvidenceReportIssueView {
  severity: string;
  type: string;
  message: string;
}

export interface EvidenceReportView {
  validator?: string;
  mode?: string;
  passed?: boolean;
  issues?: EvidenceReportIssueView[];
}

/** research_validation_details.findings 条目：可检查到句/证据的具体发现。 */
export interface ResearchValidationFindingView {
  type: string;
  /** blocker 在质量门阻断正式交付；review 仅等待人工复核，不自行阻断。 */
  severity: string;
  evidence_id?: string;
  claim_id?: string;
  excerpt?: string;
  message: string;
  source?: string;
}

/** research_validation_details.bibliography 条目：编号仅是显示顺序（T07）。 */
export interface ResearchBibliographyEntryView {
  number: number;
  evidence_id: string;
  paper_id?: string;
  title?: string;
  authors?: string[];
  year?: number | null;
  venue?: string;
  doi?: string;
  url?: string;
}

export interface ResearchValidationDetailsView {
  schema_version?: string;
  invalid_citations?: string[];
  unsupported_claims?: string[];
  scope_overclaims?: string[];
  unreviewed_claims?: string[];
  omitted_evidence?: string[];
  findings?: ResearchValidationFindingView[];
  bibliography?: ResearchBibliographyEntryView[];
  checked_at?: string;
}

// ─── 证据包 / 提纲 / 引用索引（contracts.md §2，UI 需要的字段子集） ───

export interface PaperBibliography {
  title: string;
  authors: string[];
  year?: number | null;
  venue?: string | null;
  doi?: string | null;
  canonical_url?: string | null;
}

export interface PaperEvidence {
  paper_id: string;
  bibliography: PaperBibliography;
  origin: "user_material" | "external";
  selection_reason: string;
  relevance_status: "scored" | "unscored";
  reading_scope: "unread" | "abstract" | "full_text";
  document_ref?: ArtifactRef;
  parsed_document_ref?: ArtifactRef;
  read_block_ids: number[];
  total_blocks: number;
  truncated: boolean;
  acquisition_status?: string;
  possible_duplicate_of?: string;
  license?: string;
}

export interface EvidenceItem {
  evidence_id: string;
  paper_id: string;
  document_ref?: ArtifactRef;
  parsed_document_ref?: ArtifactRef;
  block_id: string;
  block_hash: string;
  quote: string;
  start_char: number;
  end_char: number;
  page: number | null;
  section: string | null;
  evidence_scope: "abstract" | "full_text";
}

export interface ResearchEvidencePack {
  schema_version: "research-evidence-pack/1";
  contract_hash: string;
  candidates_ref: ArtifactRef;
  papers: PaperEvidence[];
  evidence: EvidenceItem[];
  coverage: {
    topics: string[];
    gaps: string[];
    contradictions: string[];
  };
}

export interface OutlineSection {
  section_id: string;
  title: string;
  central_point: string;
  evidence_ids: string[];
  gaps: string[];
}

export interface ResearchOutline {
  schema_version: "research-outline/1";
  contract_hash: string;
  evidence_pack_ref: ArtifactRef;
  sections: OutlineSection[];
  limitations: string[];
}

export interface CitationIndexEntry {
  citation_id: string;
  evidence_id: string;
  paper_id: string;
  paper_title: string;
  quote: string;
  page: string;
  block_id: string;
  evidence_scope: "abstract" | "full_text";
  /** 摘要证据或部分全文覆盖时必须向用户明确标注 */
  partial: boolean;
}

export interface ResearchCitationIndex {
  schema_version: "research-citation-index/1";
  citations: CitationIndexEntry[];
}

// ─── 错误语义（contracts.md §3 错误码） ───

export type ResearchErrorCode =
  | "INVALID_RESEARCH_SPEC"
  | "WRITING_RESOURCE_NOT_FOUND"
  | "STALE_GATE"
  | "IDEMPOTENCY_CONFLICT"
  | "GATE_ALREADY_DECIDED"
  | "GATE_APPROVAL_REQUIRED"
  | "INSUFFICIENT_EVIDENCE"
  | "EVIDENCE_INVALID"
  | "OUTLINE_EVIDENCE_MISMATCH"
  | "RESEARCH_UNAVAILABLE";

export class ResearchApiError extends Error {
  readonly status: number;
  readonly code: ResearchErrorCode;

  constructor(code: ResearchErrorCode, status: number, message?: string) {
    super(message ?? code);
    this.name = "ResearchApiError";
    this.code = code;
    this.status = status;
  }
}

export function isResearchApiError(error: unknown): error is ResearchApiError {
  return error instanceof ResearchApiError;
}

// ─── mock 开关与功能开关 ───

const envFlag = (name: string): string | undefined => {
  try {
    const env = (import.meta as unknown as { env?: Record<string, string> }).env;
    return env?.[name];
  } catch {
    return undefined;
  }
};

let researchMockEnabled = envFlag("VITE_RESEARCH_MOCK") === "on";

/** mock 开关：联调时切 false，组件零改动。 */
export function setResearchMockEnabled(enabled: boolean): void {
  researchMockEnabled = enabled;
}
export function isResearchMockEnabled(): boolean {
  return researchMockEnabled;
}

/**
 * 部署级硬开关（ops kill switch）：显式 false 时研究综述对所有人隐藏
 * （连实验室功能列表都不出现）；缺省或 true 时入口交给「实验室功能」
 * 的用户勾选（settings-store.enableResearchReview，云端跟随账号）。
 * 服务端另有 RESEARCH_REVIEW_ENABLED 权威 flag：勾选但后端未开启时，
 * 启动请求得到 503 RESEARCH_UNAVAILABLE 的明确错误（R14，不静默降级）。
 */
export function isResearchReviewHardOff(): boolean {
  return envFlag("VITE_RESEARCH_REVIEW_ENABLED") === "false";
}
export function researchReviewDisabledReason(): string | null {
  if (isResearchReviewHardOff()) {
    return "研究综述功能未开启（RESEARCH_REVIEW_ENABLED=false）";
  }
  return "研究综述为实验功能，请在 设置 → 实验室功能 中开启";
}

export const MOCK_RESEARCH_RUN_ID = "run_research_demo";

export function isMockResearchRunId(runId: string): boolean {
  return researchMockEnabled && runId.startsWith("run_research_");
}

// ─── ResearchSpec 草稿（表单 ↔ 合同） ───

export interface ResearchSpecDraft {
  central_question: string;
  audience: string;
  language: string;
  length_min: string;
  length_max: string;
  year_from: string;
  year_to: string;
  exclusion_terms: string;
  max_queries: string;
  max_candidates: string;
  max_papers: string;
  min_citable_sources: string;
  evidence_requirement: EvidenceRequirement;
  allow_external_research: boolean;
}

/**
 * 默认值与 contracts.md §1 示例逐字段一致（数量字段以表单字符串承载）。
 * length 默认值是交付设定兜底：真实合同的 delivery.length 要求正整数。
 */
export function defaultResearchSpecDraft(): ResearchSpecDraft {
  return {
    central_question: "",
    audience: "",
    language: "中文",
    length_min: "3000",
    length_max: "6000",
    year_from: "2020",
    year_to: "2026",
    exclusion_terms: "",
    max_queries: "3",
    max_candidates: "60",
    max_papers: "10",
    min_citable_sources: "5",
    evidence_requirement: "abstract_allowed",
    allow_external_research: true,
  };
}

function toInt(value: string): number | null {
  const trimmed = value.trim();
  if (!trimmed) return null;
  const parsed = Number(trimmed);
  return Number.isInteger(parsed) ? parsed : null;
}

/** 客户端约束提示（contracts.md §1）；提交仍以服务端校验为准。 */
export function validateResearchSpecDraft(draft: ResearchSpecDraft): string[] {
  const problems: string[] = [];
  const maxQueries = toInt(draft.max_queries);
  const maxCandidates = toInt(draft.max_candidates);
  const maxPapers = toInt(draft.max_papers);
  const minCitable = toInt(draft.min_citable_sources);
  const yearFrom = toInt(draft.year_from);
  const yearTo = toInt(draft.year_to);
  if (maxQueries === null || maxQueries < 1 || maxQueries > 3) problems.push("检索式数量需在 1–3 条之间");
  if (maxPapers === null || maxPapers < 1 || maxPapers > 20) problems.push("阅读上限需在 1–20 篇之间");
  if (minCitable !== null && maxPapers !== null && minCitable > maxPapers) problems.push("最少可引用来源不能超过阅读上限");
  if (maxCandidates === null || maxCandidates < 1 || maxCandidates > 60) problems.push("候选上限需在 1–60 条之间");
  if (minCitable !== null && maxCandidates !== null && minCitable > maxCandidates) problems.push("候选上限不能少于最少可引用来源");
  if (yearFrom !== null && yearTo !== null && yearFrom > yearTo) problems.push("起始年份不能晚于结束年份");
  if (!draft.central_question.trim()) problems.push("请填写研究问题");
  return problems;
}

export interface BuiltResearchSpec {
  spec: ResearchSpec | null;
  problems: string[];
}

/** 表单草稿 → research-spec/1 合同；非法草稿返回问题清单且 spec 为 null。 */
export function buildResearchSpec(draft: ResearchSpecDraft): BuiltResearchSpec {
  const problems = validateResearchSpecDraft(draft);
  if (problems.length > 0) return { spec: null, problems };
  return {
    spec: {
      version: "research-spec/1",
      review_kind: "narrative",
      year_from: toInt(draft.year_from),
      year_to: toInt(draft.year_to),
      exclusion_terms: draft.exclusion_terms
        .split(/[,，;；\n]/)
        .map((term) => term.trim())
        .filter(Boolean),
      max_queries: toInt(draft.max_queries) as number,
      max_candidates: toInt(draft.max_candidates) as number,
      max_papers: toInt(draft.max_papers) as number,
      min_citable_sources: toInt(draft.min_citable_sources) as number,
      evidence_requirement: draft.evidence_requirement,
      selection_policy_version: "selection/1",
      reader_policy_version: "reader/1",
      generator: "lumin_writer",
      citation_style: "numeric",
    },
    problems: [],
  };
}

// ─── 纯展示投影（测试目标） ───

export interface ResearchCounts {
  completed?: number;
  total?: number;
  failed?: number;
  deferred?: number;
}

/** counts.papers_* 的投影（T09：按阅读范围统计的论文篇数）。 */
export interface PaperReadingCounts {
  fullText: number;
  abstract: number;
  unread: number;
}

export interface ResearchCountSummary {
  label: string;
  /** 阅读上限是预算配置值，不等于已读篇数；未完成时必须显式标注。 */
  capNote: string | null;
}

/**
 * T08 任务计数摘要（保持不变）：阅读上限是预算配置值，不等于已读篇数，
 * 任务未全部完成时必须显式标注。T09 的「全文 x / 摘要 y / 未读 z」论文计数
 * 由面板直接消费 counts.papers_* 渲染，不混入本摘要。
 */
export function describeResearchCounts(counts: ResearchCounts, maxPapers: number | null): ResearchCountSummary {
  const total = counts.total ?? 0;
  const completed = counts.completed ?? 0;
  if (total === 0) return { label: "尚未开始", capNote: null };
  const label = `任务 ${completed}/${total} 完成`;
  if (completed < total) {
    return { label, capNote: `阅读上限 ${maxPapers ?? "—"} 篇是预算配置值，≠ 已读篇数（任务未全部完成）` };
  }
  return { label, capNote: null };
}

/**
 * T09：counts.papers_*（按阅读范围统计的论文篇数）投影。
 * 键族完全缺失时返回 null（面板回退到任务计数摘要）。
 */
export function paperReadingCounts(counts: Record<string, number>): PaperReadingCounts | null {
  const fullText = counts["papers_full_text"];
  const abstract = counts["papers_abstract"];
  const unread = counts["papers_unread"];
  if (typeof fullText !== "number" && typeof abstract !== "number" && typeof unread !== "number") return null;
  return { fullText: fullText ?? 0, abstract: abstract ?? 0, unread: unread ?? 0 };
}

/** 阅读上限解析：服务端运行合同投影（spec.max_papers）优先，缺省回退启动表单值。 */
export function resolveReadingCap(specMaxPapers: number | null | undefined, formMaxPapers: number | null | undefined): number | null {
  if (typeof specMaxPapers === "number" && Number.isFinite(specMaxPapers)) return specMaxPapers;
  if (typeof formMaxPapers === "number" && Number.isFinite(formMaxPapers)) return formMaxPapers;
  return null;
}

export const CITATION_MARKER_PATTERN = /\[@(ev_[A-Za-z0-9_-]+)\]/g;

/** 扫描草稿文本中的 [@ev_xxx] 引用标记，按出现顺序去重。 */
export function findCitationMarkers(text: string): string[] {
  const found: string[] = [];
  for (const match of text.matchAll(CITATION_MARKER_PATTERN)) {
    if (!found.includes(match[1])) found.push(match[1]);
  }
  return found;
}

// ─── 纸面内联引用（T09：仅渲染时转换，模型层数据不改写） ───

export type CitationSegment =
  | { kind: "text"; text: string }
  | { kind: "citation"; evidenceId: string; number: number; known: boolean };

/**
 * 把含 [@ev_xxx] 的文本切成渲染分段。
 * 编号优先取 bibliography 顺序（T07 research_validation_details 投影，即草稿
 * 首次出现顺序），没有编号数据时按文本内出现顺序本地编号。known=false 表示
 * 引用索引中不存在该证据——渲染为警示样式，绝不静默消失。
 */
export function splitCitationSegments(
  text: string,
  options: { numbers?: Record<string, number> | null; knownEvidenceIds?: ReadonlySet<string> | null } = {},
): CitationSegment[] {
  const segments: CitationSegment[] = [];
  const numbers = options.numbers ?? null;
  const knownEvidenceIds = options.knownEvidenceIds ?? null;
  const localNumbers = new Map<string, number>();
  let cursor = 0;
  for (const match of text.matchAll(CITATION_MARKER_PATTERN)) {
    const start = match.index ?? 0;
    if (start > cursor) segments.push({ kind: "text", text: text.slice(cursor, start) });
    const evidenceId = match[1];
    let localNumber = localNumbers.get(evidenceId);
    if (localNumber === undefined) {
      localNumber = localNumbers.size + 1;
      localNumbers.set(evidenceId, localNumber);
    }
    const number = numbers?.[evidenceId] ?? localNumber;
    segments.push({ kind: "citation", evidenceId, number, known: knownEvidenceIds ? knownEvidenceIds.has(evidenceId) : true });
    cursor = start + match[0].length;
  }
  if (cursor < text.length || segments.length === 0) segments.push({ kind: "text", text: text.slice(cursor) });
  return segments;
}

/** T07 bibliography 投影 → evidence_id → 显示编号；无编号数据时返回 null（本地编号回退）。 */
export function buildBibliographyNumbers(details: ResearchValidationDetailsView | null | undefined): Record<string, number> | null {
  const numbers: Record<string, number> = {};
  for (const entry of details?.bibliography ?? []) {
    if (entry?.evidence_id && typeof entry.number === "number") numbers[entry.evidence_id] = entry.number;
  }
  return Object.keys(numbers).length > 0 ? numbers : null;
}

// ─── 质量门暂停引导（T09） ───

export interface QualityGateGuidanceInput {
  runStatus: string | null | undefined;
  /** writing.node.status 投影：node_id → status（node_quality failed 语义为 EVIDENCE_INVALID）。 */
  nodeStatuses: Record<string, string>;
  /** evidence_report（T07 校验产物）中的 issues。 */
  reportIssues?: EvidenceReportIssueView[] | null;
  /** research_validation_details 中的可检查发现。 */
  validationFindings?: ResearchValidationFindingView[] | null;
  /** 运行级服务端错误码（如 run gate 记录的 EVIDENCE_INVALID）。 */
  serverErrorCode?: string | null;
}

export interface QualityGateGuidance {
  headline: string;
  note: string;
  findings: string[];
}

const QUALITY_GATE_FINDING_PREFIX = "质量门拦截：";

/**
 * run 处于 paused 且 node_quality failed（EVIDENCE_INVALID 语义：引用校验
 * blocker 让质量节点 fail closed）时给出引导；没有「继续」路径——必须修正
 * 草稿引用后创建新运行。列出 evidence_report / 校验明细 / 服务端错误里的
 * 具体发现，用户看到可检查的句子而不只是总分。
 */
export function qualityGatePauseGuidance(input: QualityGateGuidanceInput): QualityGateGuidance | null {
  if (input.runStatus !== "paused") return null;
  const failedQualityNode = Object.entries(input.nodeStatuses ?? {}).some(
    ([nodeId, status]) => nodeId === "node_quality" && status === "failed",
  );
  if (!failedQualityNode) return null;

  const findings: string[] = [];
  for (const issue of input.reportIssues ?? []) {
    const message = issue?.message?.trim();
    if (!message) continue;
    findings.push(`${QUALITY_GATE_FINDING_PREFIX}${issue.type ?? "unknown"}（${issue.severity ?? "severity 未知"}）：${message}`);
  }
  for (const finding of input.validationFindings ?? []) {
    const message = finding?.message?.trim();
    if (!message) continue;
    // 可检查到具体句子和证据（T07 验收：不只有总分）。
    const where = [
      finding.evidence_id ? `，证据 ${finding.evidence_id}` : "",
      finding.excerpt ? `，句子「${finding.excerpt}」` : "",
    ].join("");
    findings.push(`校验发现 ${finding.type ?? "unknown"}（${finding.severity ?? "severity 未知"}${where}）：${message}`);
  }
  if (input.serverErrorCode) {
    findings.push(`服务端错误码：${input.serverErrorCode}`);
  }
  const deduped = [...new Set(findings)];
  return {
    headline: "引用校验未通过——请修正草稿引用后创建新运行（当前运行不会自动重试）",
    note: "质量门 fail closed：阻断级发现（EVIDENCE_INVALID）会阻止正式交付。当前运行没有可继续的路径；修正草稿中的引用标记后请创建新运行。",
    findings: deduped,
  };
}

// ─── gate 确认请求（全部取服务端 GET 返回值，客户端不计算 hash） ───

export function buildGateDecisionRequest(gate: GateView): GateDecisionRequest {
  if (!gate.input_ref) throw new ResearchApiError("EVIDENCE_INVALID", 422, "gate 缺少输入产物引用，无法确认");
  return {
    plan_id: gate.plan_id,
    plan_version: gate.plan_version,
    plan_hash: gate.plan_hash,
    gate_revision: gate.revision,
    input_ref: gate.input_ref,
    decision: "approve",
  };
}

export function uuidV4(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") return crypto.randomUUID();
  // 保守回退（测试环境/旧浏览器）：v4 格式占位
  return "xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx".replace(/[xy]/g, (ch) => {
    const rand = (Math.random() * 16) | 0;
    const value = ch === "x" ? rand : (rand & 0x3) | 0x8;
    return value.toString(16);
  });
}

/** 202 后轮询 GET 直到状态离开 pending（或超时）。 */
export async function pollGateUntilResolved(
  runId: string,
  gateId: string,
  options: { maxAttempts?: number; delayMs?: number; sleep?: (ms: number) => Promise<void> } = {},
): Promise<GateView> {
  const maxAttempts = options.maxAttempts ?? 15;
  const delayMs = options.delayMs ?? 500;
  const sleep = options.sleep ?? ((ms) => new Promise<void>((resolve) => setTimeout(resolve, ms)));
  for (let attempt = 0; attempt < maxAttempts; attempt += 1) {
    const gate = await fetchGate(runId, gateId);
    if (gate.status !== "pending") return gate;
    if (attempt + 1 < maxAttempts) await sleep(delayMs);
  }
  throw new ResearchApiError("GATE_APPROVAL_REQUIRED", 409, `确认已提交，但状态尚未变化（gate ${gateId}），请稍后刷新查看`);
}

// ─── 真实请求层 ───

const WRITING_PREFIX = "/api/v2";

interface APIEnvelope<T> { success?: boolean; data?: T }

interface ErrorBody { error?: { code?: string; message?: string }; code?: string; message?: string }

async function researchFetch<T>(path: string, init: RequestInit = {}): Promise<T> {
  const response = await fetch(path, { ...init, headers: { Accept: "application/json", ...init.headers } });
  if (!response.ok) {
    let code: ResearchErrorCode = "RESEARCH_UNAVAILABLE";
    let message: string | undefined;
    try {
      const body = (await response.json()) as ErrorBody;
      const reported = body.error?.code ?? body.code;
      if (reported) code = reported as ResearchErrorCode;
      if (body.error?.message) message = body.error.message;
    } catch {
      // 保留默认错误码
    }
    throw new ResearchApiError(code, response.status, message);
  }
  const body = (await response.json()) as APIEnvelope<T> & T;
  return (body.data ?? body) as T;
}

function authHeaders(token?: string): Record<string, string> {
  return token ? { Authorization: `Bearer ${token}` } : {};
}

// ─── 公共 API（mock 优先） ───

export async function fetchResearchProgress(runId: string, token?: string): Promise<ResearchProgressView> {
  if (researchMockEnabled) return mockProgress(runId);
  return researchFetch<ResearchProgressView>(`${WRITING_PREFIX}/runs/${encodeURIComponent(runId)}/research`, { headers: authHeaders(token) });
}

export async function fetchGate(runId: string, gateId: string, token?: string): Promise<GateView> {
  if (researchMockEnabled) return mockGate(runId, gateId);
  return researchFetch<GateView>(`${WRITING_PREFIX}/runs/${encodeURIComponent(runId)}/gates/${encodeURIComponent(gateId)}`, { headers: authHeaders(token) });
}

export async function postGateDecision(
  runId: string,
  gateId: string,
  request: GateDecisionRequest,
  token?: string,
): Promise<GateDecisionView> {
  const idempotencyKey = uuidV4();
  if (researchMockEnabled) return mockDecideGate(runId, gateId, request, idempotencyKey);
  return researchFetch<GateDecisionView>(`${WRITING_PREFIX}/runs/${encodeURIComponent(runId)}/gates/${encodeURIComponent(gateId)}/decisions`, {
    method: "POST",
    headers: { "Content-Type": "application/json", "Idempotency-Key": idempotencyKey, ...authHeaders(token) },
    body: JSON.stringify(request),
  });
}

export async function postOutlineRevision(
  runId: string,
  gateId: string,
  request: OutlineRevisionRequest,
  token?: string,
): Promise<OutlineRevisionView> {
  const idempotencyKey = uuidV4();
  if (researchMockEnabled) return mockSaveOutlineRevision(runId, gateId, request, idempotencyKey);
  return researchFetch<OutlineRevisionView>(`${WRITING_PREFIX}/runs/${encodeURIComponent(runId)}/gates/${encodeURIComponent(gateId)}/outline-revisions`, {
    method: "POST",
    headers: { "Content-Type": "application/json", "Idempotency-Key": idempotencyKey, ...authHeaders(token) },
    body: JSON.stringify(request),
  });
}

/** GET /runs/{runId}/artifacts/{artifactId}/content — 返回原始内容。 */
export async function fetchRunArtifactContent(runId: string, artifactId: string, token?: string): Promise<unknown> {
  if (researchMockEnabled) return mockArtifactContent(runId, artifactId);
  const response = await fetch(`${WRITING_PREFIX}/runs/${encodeURIComponent(runId)}/artifacts/${encodeURIComponent(artifactId)}/content`, {
    headers: authHeaders(token),
  });
  if (!response.ok) {
    let code: ResearchErrorCode = "WRITING_RESOURCE_NOT_FOUND";
    let message: string | undefined;
    try {
      const body = (await response.json()) as ErrorBody;
      if (body.error?.code) code = body.error.code as ResearchErrorCode;
      if (body.error?.message) message = body.error.message;
    } catch {
      // 保留默认错误码
    }
    throw new ResearchApiError(code, response.status, message);
  }
  return response.json();
}

export async function fetchEvidencePack(runId: string, packRef: ArtifactRef, token?: string): Promise<ResearchEvidencePack> {
  const content = (await fetchRunArtifactContent(runId, packRef.artifact_id, token)) as ResearchEvidencePack;
  if (content?.schema_version !== "research-evidence-pack/1") {
    throw new ResearchApiError("EVIDENCE_INVALID", 422, "证据包内容格式不符合 research-evidence-pack/1");
  }
  return content;
}

export async function fetchResearchOutline(runId: string, outlineRef: ArtifactRef, token?: string): Promise<ResearchOutline> {
  const content = (await fetchRunArtifactContent(runId, outlineRef.artifact_id, token)) as ResearchOutline;
  if (content?.schema_version !== "research-outline/1") {
    throw new ResearchApiError("EVIDENCE_INVALID", 422, "提纲内容格式不符合 research-outline/1");
  }
  return content;
}

/** mock：种子一次研究综述运行（真实路径见下方 startRealResearchRun）。 */
let lastStartedSpec: ResearchSpec | null = null;

/** 最近一次启动的研究运行使用的合同（进度面板用它标注阅读上限）。 */
export function getLastResearchSpec(): ResearchSpec | null {
  return lastStartedSpec;
}

// ─── 真实启动（F1）：document → contract(v1.1) → confirm → compile → run（→ approve） ───

/** composer 挂载素材的服务端引用（material_id 来自现有素材系统；source_ref 缺省由服务端解析）。 */
export interface ResearchMaterialRef {
  material_id: string;
  source_ref?: string;
  title?: string;
}

export interface ResearchLaunchInput {
  /** 已通过 buildResearchSpec 校验的 research-spec/1（合同的 research 字段）。 */
  spec: ResearchSpec;
  /** composer 表单的完整研究问题（合同 content.central_question）。 */
  central_question: string;
  /** 交付字段：目标读者（合同 audience.role，服务端要求非空）。 */
  audience: string;
  /** 交付字段：语言（合同 delivery.language）。 */
  language: string;
  /** 交付字段：长度上下限（合同 delivery.length）。 */
  length_min: string | number;
  length_max: string | number;
  /** 素材引用透传（创建文档时写入 metadata.material_refs，由服务端在运行开始时快照）。 */
  material_refs?: ResearchMaterialRef[];
  /** 联网检索开关（合同 material_policy.allow_external_research）。 */
  allow_external_research: boolean;
  /** 可选文档标题；缺省取研究问题前缀。 */
  document_title?: string;
}

function isBareResearchSpec(value: ResearchSpec | ResearchLaunchInput): value is ResearchSpec {
  return typeof (value as ResearchSpec).version === "string";
}

/** 真实启动前的本地校验（合同必填字段：audience.role / delivery.language / length 均非空且 min ≤ max）。 */
export function researchLaunchProblems(launch: ResearchLaunchInput): string[] {
  const problems: string[] = [];
  const question = String(launch.central_question ?? "").trim();
  const audience = String(launch.audience ?? "").trim();
  const language = String(launch.language ?? "").trim();
  if (!question) problems.push("请填写研究问题（central_question）");
  if (!audience) problems.push("请填写目标读者（合同的 audience.role 不能为空）");
  if (!language) problems.push("请填写交付语言");
  const min = toInt(String(launch.length_min ?? ""));
  const max = toInt(String(launch.length_max ?? ""));
  if (min === null || min < 1) problems.push("长度下限需要为正整数");
  if (max === null || max < 1) problems.push("长度上限需要为正整数");
  if (min !== null && max !== null && min > max) problems.push("长度下限不能大于上限");
  return problems;
}

// ─── Go encoding/json 兼容的规范化序列化（客户端封存合同/意图哈希） ───

/**
 * 后端 writingkernel 契约哈希是 Go json.Marshal 的 sha256：字符串按 Go 规则
 * 转义（HTML 字符 < > & 与 U+2028/2029 输出 \u00XX），字段顺序由调用方按
 * Go 结构体声明顺序构造。数字仅支持安全整数（研究合同不含浮点字段）。
 */
export function canonicalGoJSON(value: unknown): string {
  const escapeString = (text: string): string =>
    JSON.stringify(text)
      .replace(/</g, "\\u003c")
      .replace(/>/g, "\\u003e")
      .replace(/&/g, "\\u0026")
      .replace(/\u2028/g, "\\u2028")
      .replace(/\u2029/g, "\\u2029");
  const encode = (node: unknown): string => {
    if (node === null) return "null";
    if (typeof node === "string") return escapeString(node);
    if (typeof node === "boolean") return node ? "true" : "false";
    if (typeof node === "number") {
      if (!Number.isSafeInteger(node)) throw new ResearchApiError("INVALID_RESEARCH_SPEC", 400, `合同/意图计划中的数字必须是安全整数：${node}`);
      return String(node);
    }
    if (Array.isArray(node)) return `[${node.map(encode).join(",")}]`;
    if (typeof node === "object") {
      return `{${Object.entries(node as Record<string, unknown>).map(([key, child]) => `${escapeString(key)}:${encode(child)}`).join(",")}}`;
    }
    throw new ResearchApiError("INVALID_RESEARCH_SPEC", 400, `合同/意图计划包含不可序列化的值：${typeof node}`);
  };
  return encode(value);
}

/** sha256 → "sha256:<hex64>"（与后端 hashPattern 一致）。 */
export async function goContentHash(payload: string): Promise<string> {
  const subtle = globalThis.crypto?.subtle;
  if (!subtle) throw new ResearchApiError("RESEARCH_UNAVAILABLE", 503, "当前环境缺少 WebCrypto，无法封存合同哈希");
  const digest = await subtle.digest("SHA-256", new TextEncoder().encode(payload));
  return "sha256:" + Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, "0")).join("");
}

/** RFC3339（秒级精度）：Go time.Time 重新封送时零纳秒会省略小数部分。 */
function rfc3339Seconds(date: Date): string {
  return date.toISOString().replace(/\.\d{3}Z$/, "Z");
}

interface ResearchContractRecordView {
  document_id: string;
  contract: {
    contract_id: string;
    version: number;
    contract_hash: string;
    status: string;
  };
}

interface ResearchPlanPreviewView {
  plan: {
    schema_version: string;
    intent_plan: Record<string, unknown>;
    executable_plan: {
      plan_id: string;
      plan_hash: string;
      intent_plan_ref?: { id: string; version: number; hash: string };
    };
    strategy_decision: Record<string, unknown>;
  };
  budget: Record<string, number>;
  permissions: string[];
  base_version_id: string;
}

/** e2e（createResearchRun/researchRunBudget）验证过的运行预算：读节点 bounds 需 ≥20 items / 12 nodes / 2h。 */
const RESEARCH_RUN_BUDGET = { max_cost_usd: 100, max_duration_ms: 7200000, max_concurrency: 1, max_nodes: 12, max_items: 20 };

/** 运行记录的 style_slug（与后端 e2e 约定一致；服务端不校验该值）。 */
const RESEARCH_STYLE_SLUG = "yinyue";

/**
 * 构造 lcp/1.1 研究合同草稿与确认版本（客户端封存哈希：服务端 PutContract/
 * ConfirmContract/ValidateTransition 会重算 canonical sha256 并拒绝不一致）。
 * 字段插入顺序必须与 Go WritingContract/ResearchSpec 结构体声明顺序一致。
 */
async function buildResearchContracts(launch: ResearchLaunchInput): Promise<{ draft: Record<string, unknown>; confirmed: Record<string, unknown> }> {
  const question = String(launch.central_question).trim();
  const now = rfc3339Seconds(new Date());
  const contractId = `ctr_${uuidV4()}`;
  const collaboration = { task_mode: "guided", orchestration_mode: "research_review", assurance_level: "sourced", approval_mode: "conditional" };
  const attributions = await Promise.all(
    (["task_mode", "orchestration_mode", "assurance_level", "approval_mode"] as const).map(async (field) => ({
      field_path: `/collaboration/${field}`,
      source: "user",
      value_hash: await goContentHash(canonicalGoJSON(collaboration[field])),
      recorded_at: now,
    })),
  );
  const base = {
    schema_version: "lcp/1.1",
    contract_id: contractId,
    version: 1,
    status: "draft",
    intent: { operation: "create", genre: "literature_review", purpose: question },
    audience: { role: launch.audience.trim(), knowledge_level: "professional" },
    content: { topic: question, central_question: question, required_points: [], prohibited_points: [] },
    voice: { tone: "professional", preserve_user_voice: true },
    material_policy: {
      user_material_priority: "highest",
      allow_external_research: launch.allow_external_research,
      conflict_handling: "ask_user",
    },
    evidence_policy: { level: "sourced", unsupported_claims: "prohibit" },
    delivery: {
      format: "markdown",
      language: launch.language.trim(),
      length: { min: Number(launch.length_min), max: Number(launch.length_max) },
    },
    collaboration,
    source_attributions: attributions,
    inferences: [],
    research: launch.spec,
  };
  const draftHash = await goContentHash(canonicalGoJSON(base));
  const confirmedBase = { ...base, version: 2, status: "confirmed" };
  const confirmedHash = await goContentHash(canonicalGoJSON(confirmedBase));
  return {
    draft: { ...base, contract_hash: draftHash },
    confirmed: { ...confirmedBase, contract_hash: confirmedHash },
  };
}

/**
 * 意图计划（客户端构造并封存 intent_plan_hash；服务端 Compile 会重算校验）。
 * capability_hint 与后端 T06 e2e 的 research intent 一致（模板按合同 mode 选定）。
 */
async function buildResearchIntentPlan(contractRef: { id: string; version: number; hash: string }, question: string) {
  const base = {
    intent_plan_id: `iplan_${uuidV4()}`,
    contract_ref: contractRef,
    // Go IntentPlan.ComputeHash marshals the struct in field order with no
    // omitempty — the empty intent_plan_hash occupies its declared position
    // (ir.go: IntentPlanID, ContractRef, IntentPlanHash, Summary, …) and MUST
    // be present in the hashed base or the server-side recompute mismatches.
    intent_plan_hash: "",
    summary: `研究综述：${question.slice(0, 80)}`,
    created_by: "user",
    created_at: rfc3339Seconds(new Date()),
    proposed_steps: [
      { step_id: "research", objective: "produce a researched literature review", capability_hint: "core.research.draft", depends_on: [] },
    ],
  };
  const hash = await goContentHash(canonicalGoJSON(base));
  return { ...base, intent_plan_hash: hash };
}

export { buildResearchIntentPlan };

/**
 * 真实创建链路（与后端 research_e2e_test.go 驱动的请求序列逐一对齐）：
 * 1. POST /documents（Idempotency-Key；metadata.material_refs 透传素材引用）
 * 2. POST /documents/{id}/contracts（lcp/1.1 草稿 + research 字段，客户端封存哈希）
 * 3. POST /contracts/{id}/confirm（previous_version=1，确认版本=2，取服务端返回的合同记录）
 * 4. POST /documents/{id}/plans（intent_plan 持服务端确认合同 ref；base_version_id 取文档当前版本）
 * 5. POST /runs（contract id/version/hash、plan envelope、permissions 全部沿用服务端返回值）
 * 6. 返回 awaiting_approval 时 POST /runs/{id}/approve（计划级放行；证据/提纲 gate 仍由用户确认）
 */
async function startRealResearchRun(launch: ResearchLaunchInput): Promise<{ run_id: string }> {
  const problems = researchLaunchProblems(launch);
  if (problems.length > 0) {
    throw new ResearchApiError("INVALID_RESEARCH_SPEC", 400, problems.join("；"));
  }
  try {
    const question = String(launch.central_question).trim();
    const title = (launch.document_title?.trim() || question).slice(0, 60);
    const materialRefs = (launch.material_refs ?? []).filter((ref) => ref.material_id.trim() !== "");
    // 1. 创建文档（素材引用随 metadata.material_refs 透传给运行时初始产物）
    const document = await researchFetch<DocumentRecord>(`${WRITING_PREFIX}/documents`, {
      method: "POST",
      headers: { "Content-Type": "application/json", "Idempotency-Key": uuidV4() },
      body: JSON.stringify({ title, metadata: { material_refs: materialRefs } }),
    });
    // 2+3. v1.1 合同草稿 → 确认（confirm 步骤必须显式走，PutContract 只收 draft）；
    //      后续步骤的 contract id/version/hash 全部取服务端返回的合同记录。
    const { draft, confirmed } = await buildResearchContracts(launch);
    const draftRecord = await researchFetch<ResearchContractRecordView>(
      `${WRITING_PREFIX}/documents/${encodeURIComponent(document.document_id)}/contracts`,
      { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ contract: draft }) },
    );
    const confirmedRecord = await researchFetch<ResearchContractRecordView>(
      `${WRITING_PREFIX}/contracts/${encodeURIComponent(draftRecord.contract.contract_id)}/confirm`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ previous_version: draftRecord.contract.version, contract: confirmed }),
      },
    );
    const sealed = confirmedRecord.contract;
    // 4. 编译计划（服务端已确认合同；ref/hash 与 plan envelope 以服务端返回值为准）
    const intentPlan = await buildResearchIntentPlan(
      { id: sealed.contract_id, version: sealed.version, hash: sealed.contract_hash },
      question,
    );
    const baseVersionId = document.current_version_id ?? "";
    const preview = await researchFetch<ResearchPlanPreviewView>(
      `${WRITING_PREFIX}/documents/${encodeURIComponent(document.document_id)}/plans`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          contract_id: sealed.contract_id,
          contract_version: sealed.version,
          base_version_id: baseVersionId,
          intent_plan: intentPlan,
          budget: RESEARCH_RUN_BUDGET,
          initial_artifact_types: ["contract", "materials"],
          required_final_artifact: "revision_set",
        }),
      },
    );
    // 5. 创建运行
    let run = await researchFetch<RuntimeRun>(`${WRITING_PREFIX}/runs`, {
      method: "POST",
      headers: { "Content-Type": "application/json", "Idempotency-Key": uuidV4() },
      body: JSON.stringify({
        document_id: document.document_id,
        contract_id: sealed.contract_id,
        contract_version: sealed.version,
        contract_hash: sealed.contract_hash,
        base_version_id: baseVersionId,
        style_slug: RESEARCH_STYLE_SLUG,
        plan: preview.plan,
        budget: RESEARCH_RUN_BUDGET,
        permissions: preview.permissions,
      }),
    });
    // 6. 计划级审批：awaiting_approval 的运行不会启动，需 approve 后才进入执行
    if (run.status === "awaiting_approval") {
      run = await researchFetch<RuntimeRun>(`${WRITING_PREFIX}/runs/${encodeURIComponent(run.run_id)}/approve`, {
        method: "POST",
        headers: { "Content-Type": "application/json", "Idempotency-Key": uuidV4() },
        body: JSON.stringify({
          plan_id: preview.plan.executable_plan.plan_id,
          plan_version: 1,
          plan_hash: preview.plan.executable_plan.plan_hash,
          permissions: preview.permissions,
        }),
      });
    }
    lastStartedSpec = launch.spec;
    return { run_id: run.run_id };
  } catch (error) {
    if (isResearchApiError(error)) throw error;
    throw new ResearchApiError("RESEARCH_UNAVAILABLE", 503, `研究服务暂不可用：${error instanceof Error ? error.message : String(error)}`);
  }
}

export async function startResearchRun(input: ResearchSpec | ResearchLaunchInput): Promise<{ run_id: string }> {
  if (!researchMockEnabled) {
    const launch = isBareResearchSpec(input)
      ? ({ spec: input } as ResearchLaunchInput)
      : input;
    return startRealResearchRun(launch);
  }
  lastStartedSpec = isBareResearchSpec(input) ? input : input.spec;
  researchMock.reset();
  return { run_id: MOCK_RESEARCH_RUN_ID };
}

/**
 * mock：GET /runs/{id} 的研究运行投影（v2 RuntimeRun 形状的最小子集），
 * 让 writing-runtime-store.loadRun 能在纯 mock 下打开演示运行。
 */
export async function fetchRunShim(runId: string): Promise<RuntimeRun> {
  if (researchMockEnabled && isMockResearchRunId(runId)) {
    requireMockRun(runId);
    return {
      run_id: runId,
      document_id: "doc_research_demo",
      contract_id: "contract_research_demo",
      contract_hash: "sha256:contract-demo",
      contract_version: 2,
      status: "paused",
      active_plan_id: "plan_research_demo",
      active_plan_version: 1,
      approval_mode: "always",
      approval_status: "not_required",
      budget: { max_cost_usd: 0, max_duration_ms: 0, max_concurrency: 1, max_nodes: 0, max_items: 0 },
      permissions: [],
      last_event_sequence: 42,
    };
  }
  throw new ResearchApiError("WRITING_RESOURCE_NOT_FOUND", 404, `run not found: ${runId}`);
}

// ─── mock 场景（固定样例数据 + 可驱动状态机） ───

interface MockGate {
  gate: GateView;
}

interface MockRun {
  runId: string;
  phase: string;
  counts: Record<string, number>;
  /** 运行合同投影（T09 GET research 响应新增）；null 演练「无 spec 回退表单值」。 */
  spec: ResearchSpecProjection | null;
  /** writing.node.status 投影（node_id → status），loadRun 时灌入 store。 */
  nodeStatuses: Record<string, string>;
  tasks: ResearchTaskView[];
  errors: ResearchTaskView[];
  packRef: ArtifactRef;
  gates: Map<string, MockGate>;
  activeGateId: string | null;
  decidedKeys: Map<string, string>;
}

const MOCK_PLAN_HASH = "sha256:mock-plan-hash-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef";
const PACK_ARTIFACT_ID = "art_evidence_pack_demo";
const PACK_CONTENT_HASH = "sha256:pack-demo-hash";
const OUTLINE_ARTIFACT_IDS = ["art_outline_demo_v1", "art_outline_demo_v2"];

const mockState = {
  fetchDelayMs: 0,
  runs: new Map<string, MockRun>(),
};

export const researchMock = {
  /** 测试与演示重置：重新播种演示运行（证据 gate pending）。 */
  reset(): void {
    mockState.runs.clear();
  },
  configure(options: { fetchDelayMs?: number }): void {
    if (options.fetchDelayMs !== undefined) mockState.fetchDelayMs = options.fetchDelayMs;
  },
  /** 直接推进 phase，用于演练失败/待补充等状态。 */
  setPhase(runId: string, phase: string): void {
    const run = ensureMockRun(runId);
    run.phase = phase;
  },
  /** 覆盖运行合同的 spec 投影（演练「无 spec 回退启动表单值」）。 */
  setSpec(runId: string, spec: ResearchSpecProjection | null): void {
    const run = ensureMockRun(runId);
    run.spec = spec;
  },
  seedFailed(runId: string): void {
    const run = ensureMockRun(runId);
    run.phase = "failed";
    run.errors = [{ task_key: "fetch_full_text:p_meta_1", node_id: "research_fetch", phase: "fetch", status: "failed", attempt: 2, error_code: "SCHOLAR_UNREACHABLE" }];
  },
  /**
   * 演练质量门暂停（EVIDENCE_INVALID 语义）：run 暂停在 node_quality failed，
   * 没有 pending gate，也没有任何继续路径；发现清单来自 evidence_report /
   * research_validation_details mock 产物。
   */
  seedQualityGateFailure(runId: string): void {
    const run = ensureMockRun(runId);
    run.phase = "quality_gate_paused";
    run.nodeStatuses = { ...run.nodeStatuses, node_quality: "failed" };
    run.activeGateId = null;
    run.errors = [
      { task_key: "quality:node_quality", node_id: "node_quality", phase: "validate", status: "failed", attempt: 1, error_code: "EVIDENCE_INVALID" },
    ];
  },
};
/**
 * mock 运行的 writing.node.status 投影（loadRun 灌入 store 的 nodeStatuses，
 * 与真实事件流投影语义一致）。
 */
export function mockNodeStatuses(runId: string): Record<string, string> {
  if (!researchMockEnabled || !isMockResearchRunId(runId)) return {};
  const run = mockState.runs.get(runId);
  return run ? { ...run.nodeStatuses } : {};
}

async function mockDelay(): Promise<void> {
  if (mockState.fetchDelayMs > 0) await new Promise<void>((resolve) => setTimeout(resolve, mockState.fetchDelayMs));
}

function requireMockRun(runId: string): MockRun {
  const run = mockState.runs.get(runId);
  if (!run) {
    if (isMockResearchRunId(runId)) return ensureMockRun(runId);
    throw new ResearchApiError("WRITING_RESOURCE_NOT_FOUND", 404, `research run not found: ${runId}`);
  }
  return run;
}

function ensureMockRun(runId: string): MockRun {
  let run = mockState.runs.get(runId);
  if (run) return run;
  run = seedDemoRun(runId);
  mockState.runs.set(runId, run);
  return run;
}

function seedDemoRun(runId: string): MockRun {
  const packRef: ArtifactRef = { artifact_id: PACK_ARTIFACT_ID, version: 1, content_hash: PACK_CONTENT_HASH };
  const evidenceGate: MockGate = {
    gate: {
      gate_id: `${runId}_evidence`,
      run_id: runId,
      node_id: "research_evidence_gate",
      gate_kind: "evidence",
      plan_id: "plan_research_demo",
      plan_version: 1,
      plan_hash: MOCK_PLAN_HASH,
      revision: 1,
      status: "pending",
      input_ref: packRef,
      allowed_operations: ["decide"],
      blocked_reason: "awaiting_gate_decision",
      last_event_sequence: 42,
    },
  };
  return {
    runId,
    phase: "gate_evidence",
    // T09 合同形状：counts 既有键保留 + 新增论文阅读键族；spec 从运行合同投影。
    counts: { total: 14, completed: 9, failed: 1, deferred: 2, papers_full_text: 3, papers_abstract: 2, papers_unread: 1 },
    spec: { max_papers: 10, min_citable_sources: 5, evidence_requirement: "abstract_allowed", max_queries: 3, max_candidates: 60 },
    nodeStatuses: { node_research_read: "completed" },
    tasks: [
      { task_key: "discover:q1", node_id: "research_discover", phase: "discover", status: "completed", attempt: 1 },
      { task_key: "discover:q2", node_id: "research_discover", phase: "discover", status: "completed", attempt: 1 },
      { task_key: "rank:batch1", node_id: "research_rank", phase: "rank", status: "completed", attempt: 1 },
      { task_key: "read:p_full_1", node_id: "research_read", phase: "read", status: "completed", attempt: 1 },
      { task_key: "read:p_abs_1", node_id: "research_read", phase: "read", status: "completed", attempt: 1 },
      { task_key: "fetch:p_meta_1", node_id: "research_fetch", phase: "fetch", status: "failed", attempt: 2, error_code: "SCHOLAR_UNREACHABLE" },
      { task_key: "fetch:p_defer_1", node_id: "research_fetch", phase: "fetch", status: "deferred", attempt: 1 },
    ],
    errors: [
      { task_key: "fetch:p_meta_1", node_id: "research_fetch", phase: "fetch", status: "failed", attempt: 2, error_code: "SCHOLAR_UNREACHABLE" },
    ],
    packRef,
    gates: new Map([[evidenceGate.gate.gate_id, evidenceGate]]),
    activeGateId: evidenceGate.gate.gate_id,
    decidedKeys: new Map(),
  };
}

async function mockProgress(runId: string): Promise<ResearchProgressView> {
  await mockDelay();
  const run = requireMockRun(runId);
  return {
    run_id: runId,
    phase: run.phase,
    counts: { ...run.counts },
    spec: run.spec,
    tasks: run.tasks,
    pack_ref: run.packRef,
    active_gate: run.activeGateId ? run.gates.get(run.activeGateId)?.gate : undefined,
    errors: run.errors,
    last_event_sequence: 42,
  };
}

async function mockGate(runId: string, gateId: string): Promise<GateView> {
  await mockDelay();
  const run = requireMockRun(runId);
  const gate = run.gates.get(gateId);
  if (!gate) throw new ResearchApiError("WRITING_RESOURCE_NOT_FOUND", 404, `gate not found: ${gateId}`);
  return { ...gate.gate };
}

function decisionBodyFingerprint(request: GateDecisionRequest): string {
  return JSON.stringify(request);
}

async function mockDecideGate(runId: string, gateId: string, request: GateDecisionRequest, idempotencyKey: string): Promise<GateDecisionView> {
  await mockDelay();
  const run = requireMockRun(runId);
  const gate = run.gates.get(gateId);
  if (!gate) throw new ResearchApiError("WRITING_RESOURCE_NOT_FOUND", 404, `gate not found: ${gateId}`);
  const fingerprint = decisionBodyFingerprint(request);
  const previous = run.decidedKeys.get(`${gateId}:${idempotencyKey}`);
  if (previous !== undefined) {
    if (previous !== fingerprint) throw new ResearchApiError("IDEMPOTENCY_CONFLICT", 409, "相同幂等键被用于不同请求体");
    return { decision_id: gateId, gate_id: gateId, run_id: runId, status: gate.gate.status, resume_status: "queued" };
  }
  if (gate.gate.status !== "pending") throw new ResearchApiError("GATE_ALREADY_DECIDED", 409, `gate 已完成确认（${gate.gate.status}）`);
  if (request.gate_revision !== gate.gate.revision || request.plan_hash !== gate.gate.plan_hash || !request.input_ref
    || request.input_ref.artifact_id !== gate.gate.input_ref?.artifact_id
    || request.input_ref.version !== gate.gate.input_ref.version
    || request.input_ref.content_hash !== gate.gate.input_ref.content_hash) {
    throw new ResearchApiError("STALE_GATE", 409, "确认信息已过期（gate revision/plan/input_ref 不匹配），请刷新后重试");
  }
  gate.gate = { ...gate.gate, status: "approved", decision: "approve", allowed_operations: [], blocked_reason: undefined };
  run.decidedKeys.set(`${gateId}:${idempotencyKey}`, fingerprint);
  advanceAfterDecision(run, gate.gate.gate_kind);
  return { decision_id: gateId, gate_id: gateId, run_id: runId, status: "approved", resume_status: "queued" };
}

function advanceAfterDecision(run: MockRun, decidedKind: ResearchGateKind): void {
  if (decidedKind === "evidence") {
    const evidenceGateId = run.activeGateId!;
    const outlineRef: ArtifactRef = { artifact_id: OUTLINE_ARTIFACT_IDS[0], version: 1, content_hash: "sha256:outline-demo-v1" };
    const outlineGate: MockGate = {
      gate: {
        gate_id: `${evidenceGateId.replace("_evidence", "_outline")}`,
        run_id: run.runId,
        node_id: "research_outline_gate",
        gate_kind: "outline",
        plan_id: "plan_research_demo",
        plan_version: 1,
        plan_hash: MOCK_PLAN_HASH,
        revision: 1,
        status: "pending",
        input_ref: outlineRef,
        allowed_operations: ["decide", "save_outline_revision"],
        blocked_reason: "awaiting_gate_decision",
        last_event_sequence: 58,
      },
    };
    run.gates.set(outlineGate.gate.gate_id, outlineGate);
    run.activeGateId = outlineGate.gate.gate_id;
    run.phase = "gate_outline";
    run.counts = { ...run.counts, total: 18, completed: 14 };
    return;
  }
  run.activeGateId = null;
  run.phase = "writing";
  run.counts = { ...run.counts, total: 22, completed: 18 };
}

async function mockSaveOutlineRevision(runId: string, gateId: string, request: OutlineRevisionRequest, idempotencyKey: string): Promise<OutlineRevisionView> {
  await mockDelay();
  const run = requireMockRun(runId);
  const gate = run.gates.get(gateId);
  if (!gate) throw new ResearchApiError("WRITING_RESOURCE_NOT_FOUND", 404, `gate not found: ${gateId}`);
  if (gate.gate.status !== "pending") throw new ResearchApiError("GATE_ALREADY_DECIDED", 409, "gate 已确认，不能再保存提纲修订");
  if (gate.gate.gate_kind !== "outline") throw new ResearchApiError("OUTLINE_EVIDENCE_MISMATCH", 422, "提纲修订仅适用于提纲确认点");
  if (request.gate_revision !== gate.gate.revision || request.plan_hash !== gate.gate.plan_hash) {
    throw new ResearchApiError("STALE_GATE", 409, "提纲编辑基于过期版本，请刷新后重试");
  }
  if (request.outline.sections.length === 0) throw new ResearchApiError("INVALID_RESEARCH_SPEC", 400, "提纲至少需要一个章节");
  run.decidedKeys.set(`${gateId}:outline-rev:${idempotencyKey}`, decisionBodyFingerprint(request as unknown as GateDecisionRequest));
  const nextRevision = gate.gate.revision + 1;
  const nextArtifactId = OUTLINE_ARTIFACT_IDS[Math.min(nextRevision - 1, OUTLINE_ARTIFACT_IDS.length - 1)];
  const nextRef: ArtifactRef = { artifact_id: nextArtifactId, version: 1, content_hash: `sha256:outline-demo-v${nextRevision}` };
  gate.gate = {
    ...gate.gate,
    revision: nextRevision,
    input_ref: nextRef,
    last_event_sequence: gate.gate.last_event_sequence + 1,
  };
  outlineRevisions.set(nextRef.artifact_id, { sections: request.outline.sections, limitations: request.outline.limitations });
  return { gate_id: gateId, gate_revision: nextRevision, outline_ref: nextRef };
}

// ─── mock 产物内容 ───

const outlineRevisions = new Map<string, { sections: OutlineSection[]; limitations: string[] }>();

async function mockArtifactContent(runId: string, artifactId: string): Promise<unknown> {
  await mockDelay();
  if (!isMockResearchRunId(runId)) throw new ResearchApiError("WRITING_RESOURCE_NOT_FOUND", 404, `artifact ${artifactId} 不在该运行范围`);
  if (artifactId === PACK_ARTIFACT_ID) return mockEvidencePack();
  if (outlineRevisions.has(artifactId)) {
    const revision = outlineRevisions.get(artifactId)!;
    return {
      schema_version: "research-outline/1",
      contract_hash: "sha256:contract-demo",
      evidence_pack_ref: { artifact_id: PACK_ARTIFACT_ID, version: 1, content_hash: PACK_CONTENT_HASH },
      sections: revision.sections,
      limitations: revision.limitations,
    } satisfies ResearchOutline;
  }
  if (artifactId === OUTLINE_ARTIFACT_IDS[0]) return mockOutline();
  if (artifactId === "art_citation_index_demo") return mockCitationIndex();
  if (artifactId === EVIDENCE_REPORT_ARTIFACT_ID) return mockEvidenceReport();
  if (artifactId === VALIDATION_DETAILS_ARTIFACT_ID) return mockValidationDetails();
  if (artifactId === "art_parsed_full_1") return mockParsedDocument();
  throw new ResearchApiError("WRITING_RESOURCE_NOT_FOUND", 404, `artifact not found: ${artifactId}`);
}

function mockEvidencePack(): ResearchEvidencePack {
  return {
    schema_version: "research-evidence-pack/1",
    contract_hash: "sha256:contract-demo",
    candidates_ref: { artifact_id: "art_candidates_demo", version: 1, content_hash: "sha256:candidates-demo" },
    papers: [
      {
        paper_id: "p_full_1",
        bibliography: { title: "固态电解质界面稳定性：机理与改性策略综述", authors: ["陈立", "M. Okada"], year: 2023, venue: "Journal of Power Sources", doi: "10.1000/jpss.2023.118" },
        origin: "external",
        selection_reason: "与核心问题直接相关，全文可获取",
        relevance_status: "scored",
        reading_scope: "full_text",
        parsed_document_ref: { artifact_id: "art_parsed_full_1", version: 1, content_hash: "sha256:parsed-full-1" },
        read_block_ids: [0, 1, 2, 3, 4, 5, 6, 7, 8],
        total_blocks: 14,
        truncated: true,
        acquisition_status: "full_text_available",
        license: "CC-BY-4.0",
      },
      {
        paper_id: "p_abs_1",
        bibliography: { title: "Perovskite solar cell stability under thermal cycling", authors: ["J. Rivera", "K. Tanaka"], year: 2024, venue: "Solar Energy Materials", doi: "10.1000/sem.2024.077" },
        origin: "external",
        selection_reason: "摘要与问题相关，全文受版权限制",
        relevance_status: "scored",
        reading_scope: "abstract",
        document_ref: { artifact_id: "art_abstract_abs_1", version: 1, content_hash: "sha256:abstract-abs-1" },
        read_block_ids: [0],
        total_blocks: 1,
        truncated: false,
        acquisition_status: "abstract_available",
      },
      {
        paper_id: "p_meta_1",
        bibliography: { title: "高分子固态电解质的加工窗口", authors: ["王砚"], year: 2022, venue: "Polymer Reviews", doi: "10.1000/pr.2022.045" },
        origin: "external",
        selection_reason: "全文获取失败（SCHOLAR_UNREACHABLE），待补充",
        relevance_status: "unscored",
        reading_scope: "unread",
        read_block_ids: [],
        total_blocks: 0,
        truncated: false,
        acquisition_status: "metadata_only",
      },
      {
        paper_id: "p_defer_1",
        bibliography: { title: "界面阻抗谱分析方法对比", authors: ["李青", "P. Novak"], year: 2021, venue: "Electrochimica Acta", doi: "10.1000/ea.2021.233" },
        origin: "external",
        selection_reason: "全文获取排队超时，进入待补充队列",
        relevance_status: "unscored",
        reading_scope: "unread",
        read_block_ids: [],
        total_blocks: 0,
        truncated: false,
        acquisition_status: "failed",
      },
      {
        paper_id: "p_dup_1",
        bibliography: { title: "固态电解质界面稳定性：机理与改性策略综述", authors: ["陈立", "M. Okada"], year: 2023, venue: "Journal of Power Sources", doi: "10.1000/jpss.2023.118-alt" },
        origin: "external",
        selection_reason: "题名与作者同 p_full_1，但 DOI 不同，保留独立记录",
        relevance_status: "unscored",
        reading_scope: "unread",
        read_block_ids: [],
        total_blocks: 0,
        truncated: false,
        acquisition_status: "metadata_only",
        possible_duplicate_of: "p_full_1",
      },
    ],
    evidence: [
      {
        evidence_id: "ev_1001",
        paper_id: "p_full_1",
        block_id: "blk_04",
        block_hash: "sha256:blk04",
        quote: "界面副产物层在循环 200 次后厚度趋于稳定，阻抗增量低于初始值的 15%。",
        start_char: 210,
        end_char: 256,
        page: 4,
        section: "3.2 界面稳定性",
        evidence_scope: "full_text",
      },
      {
        evidence_id: "ev_1002",
        paper_id: "p_abs_1",
        block_id: "blk_abs_00",
        block_hash: "sha256:absblk00",
        quote: "After 500 thermal cycles, devices retained 91% of initial efficiency.",
        start_char: 88,
        end_char: 138,
        page: null,
        section: null,
        evidence_scope: "abstract",
      },
      {
        evidence_id: "ev_1003",
        paper_id: "p_full_1",
        block_id: "blk_02",
        block_hash: "sha256:blk02",
        quote: "高温工况下容量衰减速率的两组独立测量结果方向相反，尚未有统一解释。",
        start_char: 45,
        end_char: 103,
        page: 2,
        section: "2.1 衰减速率分歧",
        evidence_scope: "full_text",
      },
    ],
    coverage: {
      topics: ["固态电解质界面稳定性（2 篇已读）", "钙钛矿热循环稳定性（1 篇仅摘要）"],
      gaps: ["高温长期循环数据不足", "扫描件 p_defer_1 缺少可读文本"],
      contradictions: ["p_full_1 §2.1 与引文 [5] 对容量衰减速率的结论相反"],
    },
  };
}

function mockOutline(): ResearchOutline {
  return {
    schema_version: "research-outline/1",
    contract_hash: "sha256:contract-demo",
    evidence_pack_ref: { artifact_id: PACK_ARTIFACT_ID, version: 1, content_hash: PACK_CONTENT_HASH },
    sections: [
      { section_id: "sec_intro", title: "引言与研究问题", central_point: "界定综述范围与核心问题，不引入事实性断言。", evidence_ids: [], gaps: [] },
      { section_id: "sec_interface", title: "界面稳定性的机理证据", central_point: "副产物层与阻抗演化支撑界面稳定性判断。", evidence_ids: ["ev_1001"], gaps: [] },
      { section_id: "sec_contrast", title: "结论分歧与数据缺口", central_point: "呈现衰减速率的矛盾结论。", evidence_ids: ["ev_1003", "ev_1002"], gaps: ["高温长期数据不足"] },
      { section_id: "sec_outlook", title: "展望", central_point: "指出需要补充的实验方向。", evidence_ids: [], gaps: ["扫描件待补充可读文本"] },
    ],
    limitations: ["仅摘要来源不支撑定量结论", "部分全文为截断阅读"],
  };
}

function mockCitationIndex(): ResearchCitationIndex {
  return {
    schema_version: "research-citation-index/1",
    citations: [
      { citation_id: "cit_1", evidence_id: "ev_1001", paper_id: "p_full_1", paper_title: "固态电解质界面稳定性：机理与改性策略综述", quote: "界面副产物层在循环 200 次后厚度趋于稳定，阻抗增量低于初始值的 15%。", page: "4", block_id: "blk_04", evidence_scope: "full_text", partial: false },
      { citation_id: "cit_2", evidence_id: "ev_1002", paper_id: "p_abs_1", paper_title: "Perovskite solar cell stability under thermal cycling", quote: "After 500 thermal cycles, devices retained 91% of initial efficiency.", page: "摘要", block_id: "blk_abs_00", evidence_scope: "abstract", partial: false },
      { citation_id: "cit_3", evidence_id: "ev_1003", paper_id: "p_full_1", paper_title: "固态电解质界面稳定性：机理与改性策略综述", quote: "高温工况下容量衰减速率的两组独立测量结果方向相反，尚未有统一解释。", page: "2–3", block_id: "blk_02", evidence_scope: "full_text", partial: true },
    ],
  };
}

function mockParsedDocument(): Record<string, unknown> {
  return {
    schema_version: "parsed-document/1",
    blocks: [
      { block_id: "blk_02", text: "高温工况下容量衰减速率的两组独立测量结果方向相反，尚未有统一解释。", page: 2 },
      { block_id: "blk_03", text: "本节讨论测量条件的差异：温度设置与循环倍率不同导致结论不可直接比较。", page: 3 },
      { block_id: "blk_04", text: "界面副产物层在循环 200 次后厚度趋于稳定，阻抗增量低于初始值的 15%。", page: 4 },
    ],
  };
}

// ─── mock：质量门校验产物（evidence_report + research_validation_details） ───

export const EVIDENCE_REPORT_ARTIFACT_ID = "art_evidence_report_demo";
export const VALIDATION_DETAILS_ARTIFACT_ID = "art_validation_details_demo";

/**
 * 引用索引不可用或引用丢失的 blocker 演示：与 T07 五类失败语义对齐
 * （invalid_citation / wrong_pack / hash_tamper / scope_overclaim / unsupported_assertion）。
 */
function mockEvidenceReport(): EvidenceReportView {
  return {
    validator: "core.research.validate.citations",
    mode: "structural",
    passed: false,
    issues: [
      { severity: "blocker", type: "invalid_citation", message: "草稿标记 [@ev_1042] 在引用索引中不存在：引用可能被删除或改写，请核对草稿引用。" },
      { severity: "review", type: "unreviewed_claim", message: "句子「两组独立测量的偏差可忽略」未经过语义复核，建议人工确认。" },
    ],
  };
}

/** 校验明细演示：findings 可检查到句/证据；bibliography 编号来自 T07 投影（按草稿首次出现顺序）。 */
function mockValidationDetails(): ResearchValidationDetailsView {
  return {
    schema_version: "research-validation-details/1",
    invalid_citations: ["ev_1042"],
    findings: [
      {
        type: "invalid_citation",
        severity: "blocker",
        evidence_id: "ev_1042",
        excerpt: "阻抗增量在 200 次循环后完全消失 [@ev_1042]。",
        message: "草稿标记 [@ev_1042] 在引用索引中不存在：引用可能被删除或改写，请核对草稿引用。",
        source: "structural",
      },
      {
        type: "unreviewed_claim",
        severity: "review",
        claim_id: "claim_07",
        excerpt: "两组独立测量的偏差可忽略。",
        message: "该断言未经过语义复核（fact reviewer 未给出结论），请人工确认。",
        source: "heuristic",
      },
    ],
    bibliography: [
      { number: 1, evidence_id: "ev_1001", paper_id: "p_full_1", title: "固态电解质界面稳定性：机理与改性策略综述", authors: ["陈立", "M. Okada"], year: 2023 },
      { number: 2, evidence_id: "ev_1002", paper_id: "p_abs_1", title: "Perovskite solar cell stability under thermal cycling", authors: ["J. Rivera", "K. Tanaka"], year: 2024 },
      { number: 3, evidence_id: "ev_1003", paper_id: "p_full_1", title: "固态电解质界面稳定性：机理与改性策略综述", authors: ["陈立", "M. Okada"], year: 2023 },
    ],
    checked_at: "2026-09-07T00:00:00Z",
  };
}

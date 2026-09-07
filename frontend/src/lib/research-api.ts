/**
 * 研究综述（research_review）API 层 — mock 开关集中在本模块。
 *
 * 联调切换：调用 setResearchMockEnabled(false)（或设置 VITE_RESEARCH_MOCK=off）
 * 即走真实后端 /api/v2/writing 前缀，组件代码零改动。
 * 类型与 specs/research-review/contracts.md §2/§3、
 * backend/internal/server/writing_research_api.go 的视图一一对应。
 */
import type { EvidenceRequirement, ResearchSpec, RuntimeRun } from "./writing-runtime-types.ts";

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

export interface ResearchProgressView {
  run_id: string;
  phase: string;
  counts: Record<string, number>;
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

let researchMockEnabled = envFlag("VITE_RESEARCH_MOCK") !== "off";
let researchReviewEnabled = envFlag("VITE_RESEARCH_REVIEW_ENABLED") !== "false";

/** mock 开关：联调时切 false，组件零改动。 */
export function setResearchMockEnabled(enabled: boolean): void {
  researchMockEnabled = enabled;
}
export function isResearchMockEnabled(): boolean {
  return researchMockEnabled;
}

/** RESEARCH_REVIEW_ENABLED 的 UI 表达：false 时入口禁用并显示原因。 */
export function setResearchReviewEnabled(enabled: boolean): void {
  researchReviewEnabled = enabled;
}
export function isResearchReviewEnabled(): boolean {
  return researchReviewEnabled;
}
export function researchReviewDisabledReason(): string | null {
  return researchReviewEnabled ? null : "研究综述功能未开启（RESEARCH_REVIEW_ENABLED=false）";
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

/** 默认值与 contracts.md §1 示例逐字段一致（数量字段以表单字符串承载）。 */
export function defaultResearchSpecDraft(): ResearchSpecDraft {
  return {
    central_question: "",
    audience: "",
    language: "中文",
    length_min: "",
    length_max: "",
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

export interface ResearchCountSummary {
  label: string;
  /** 阅读上限是预算配置值，不等于已读篇数；未完成时必须显式标注。 */
  capNote: string | null;
}

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

export const CITATION_MARKER_PATTERN = /\[@(ev_[A-Za-z0-9_-]+)\]/g;

/** 扫描草稿文本中的 [@ev_xxx] 引用标记，按出现顺序去重。 */
export function findCitationMarkers(text: string): string[] {
  const found: string[] = [];
  for (const match of text.matchAll(CITATION_MARKER_PATTERN)) {
    if (!found.includes(match[1])) found.push(match[1]);
  }
  return found;
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

const WRITING_PREFIX = "/api/v2/writing";

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

/** mock：种子一次研究综述运行（真实路径为合同创建接口，T09 联调）。 */
let lastStartedSpec: ResearchSpec | null = null;

/** 最近一次启动的研究运行使用的合同（进度面板用它标注阅读上限）。 */
export function getLastResearchSpec(): ResearchSpec | null {
  return lastStartedSpec;
}

export async function startResearchRun(spec: ResearchSpec): Promise<{ run_id: string }> {
  if (!researchMockEnabled) {
    throw new ResearchApiError("RESEARCH_UNAVAILABLE", 503, "研究综述运行创建接口尚未接入真实后端");
  }
  lastStartedSpec = spec;
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
  seedFailed(runId: string): void {
    const run = ensureMockRun(runId);
    run.phase = "failed";
    run.errors = [{ task_key: "fetch_full_text:p_meta_1", node_id: "research_fetch", phase: "fetch", status: "failed", attempt: 2, error_code: "SCHOLAR_UNREACHABLE" }];
  },
};

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
    counts: { total: 14, completed: 9, failed: 1, deferred: 2 },
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

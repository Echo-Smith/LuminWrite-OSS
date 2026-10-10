/**
 * 通用写作 REST API — 替代 Legacy WebSocket agent.start。
 *
 * startWritingRun 走 document → writing-contract-draft（服务端封存 lcp/1.0
 * 合同 draft v1 + confirmed v2，并服务端构造/封存 intent plan）→ contract →
 * confirm → compile → run（→ awaiting_approval 时自动 approve）的真实创建链路。
 * 前端不手写合同/计划 JSON、不计算任何哈希：schema_version（lcp/1.0）、
 * ctr_ contract_id、contract_hash、intent_plan_hash 全部由服务端按 Go 结构体
 * 封存（与 research-contract-draft 同一模式），六步请求原样转发服务端返回值。
 */
import type {
  AgentStartPayload,
  DocumentRecord,
  RuntimeRun,
} from "./writing-runtime-types.ts";
import { resolveWritingFlow } from "./writing-flows.ts";

// ─── Constants ──────────────────────────────────────────

const WRITING_PREFIX = "/api/v2";

// ─── Helpers ────────────────────────────────────────────

function uuidV4(): string {
  return crypto.randomUUID();
}

interface APIEnvelope<T> { success?: boolean; data?: T }
interface ErrorBody { error?: { code?: string; message?: string }; code?: string; message?: string }

export class WritingApiError extends Error {
  code: string;
  status: number;
  constructor(code: string, status: number, message?: string) {
    super(message ?? code);
    this.code = code;
    this.status = status;
  }
}

async function writingFetch<T>(path: string, init: RequestInit = {}): Promise<T> {
  const token = localStorage.getItem("token");
  const headers: Record<string, string> = {
    Accept: "application/json",
    "Content-Type": "application/json",
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
    ...(init.headers as Record<string, string> ?? {}),
  };
  const response = await fetch(path, { ...init, headers });
  // 非 JSON 响应（nginx 502/503 错误页等）在解析前转为友好错误——
  // 裸 response.json() 会抛 "Unexpected token '<'" 这类无意义异常。
  const contentType = response.headers.get("content-type") || "";
  if (!contentType.includes("application/json")) {
    throw new WritingApiError("WRITING_UNAVAILABLE", response.status, `服务暂时不可用（HTTP ${response.status}），请稍后重试`);
  }
  if (!response.ok) {
    let code = "WRITING_UNAVAILABLE";
    let message: string | undefined;
    try {
      const body = (await response.json()) as ErrorBody;
      const reported = body.error?.code ?? body.code;
      if (reported) code = reported;
      if (body.error?.message) message = body.error.message;
    } catch { /* keep defaults */ }
    throw new WritingApiError(code, response.status, message);
  }
  const body = (await response.json()) as APIEnvelope<T> & T;
  return (body.data ?? body) as T;
}

// ─── 服务端封存视图 ─────────────────────────────────────

interface SealedContractRef {
  contract_id: string;
  version: number;
  contract_hash: string;
  status: string;
}

/**
 * /contracts 与 /confirm 的响应形状：writingstore.ContractRecord——合同身份
 * 嵌套在 contract 字段里（{document_id, contract:{…}, trace, created_at}），
 * 与 research 流 researchFetch 的读取方式一致；读扁平字段会得到 undefined。
 */
interface SealedContractRecord {
  document_id: string;
  contract: SealedContractRef;
}

/**
 * writing-contract-draft 端点响应：服务端按 Go 结构体声明序构造并封存的
 * lcp/1.0 合同两个版本（draft v1 + confirmed v2）+ 服务端构造并封存的
 * intent plan（contract_ref 绑定 confirmed v2）。contract 直发
 * POST /documents/{id}/contracts，confirmed_contract 直发
 * POST /contracts/{id}/confirm，intent_plan 直发 /documents/{id}/plans —
 * 全部原样转发，前端零哈希计算。
 */
export interface WritingContractDraftView {
  document_id: string;
  contract: Record<string, unknown>;
  confirmed_contract: Record<string, unknown>;
  contract_hash: string;
  intent_plan: Record<string, unknown>;
  intent_plan_hash: string;
}

/** writing-contract-draft 请求体：composer 的用户可选维度。 */
export interface WritingContractDraftRequest {
  message: string;
  style?: string;
  mode?: string;
  flow: string;
  assurance_level?: string;
  approval_mode?: string;
  language?: string;
}

// 运行预算：compile 要求 run budget ≥ Σ 节点 bound——普通流四节点
// （outline/draft/quality/finalize）每节点 MaxCostUSD $5、TimeoutMS OSS 300s
// /商业版 600s，故取 $20 与 2,400,000ms（40 分钟）覆盖两线模板。与研究流
// RESEARCH_RUN_BUDGET 同思路：预算信封包不住服务端模板的节点 bound 之和时，
// compile 会以 PLAN_NOT_EXECUTED（BUDGET_COST/DURATION_EXCEEDED）拒绝。
const DEFAULT_BUDGET = {
  max_cost_usd: 20,
  max_duration_ms: 2400000,
  max_concurrency: 1,
  max_nodes: 10,
  max_items: 4,
};

// ─── Public API ─────────────────────────────────────────

export async function startWritingRun(payload: AgentStartPayload): Promise<{ run_id: string }> {
  // 深度研究（research_review）有专属启动链（research-settings 表单 →
  // research-contract-draft 封存 lcp/1.1 合同 → startResearchRun）：
  // 提前拒绝而不是误入普通流封存端点（服务端也拒绝并指回）。
  if (payload.flow === "research_review") {
    throw new WritingApiError("RESEARCH_REVIEW_CONTRACT_INVALID", 400, "深度研究流程请通过研究设置面板启动");
  }
  try {
    const title = payload.message.slice(0, 60) || "新写作";
    const materialRefs = (payload.material_refs ?? []).filter((r) => r.material_id.trim() !== "");

    // 1. 创建文档（Idempotency-Key 必带：服务端要求非空幂等键）
    const document = await writingFetch<DocumentRecord>(`${WRITING_PREFIX}/documents`, {
      method: "POST",
      headers: { "Idempotency-Key": uuidV4() },
      body: JSON.stringify({ title, metadata: { material_refs: materialRefs } }),
    });

    // 2. 服务端封存：用户选择 → lcp/1.0 合同 draft v1 + confirmed v2 +
    //    intent plan（非法选择在这里得到 400 INVALID_WRITING_SPEC，明确到字段）
    const draftRequest: WritingContractDraftRequest = {
      message: payload.message.trim(),
      style: payload.style || undefined,
      mode: payload.mode || undefined,
      flow: payload.flow ?? "long_form",
      assurance_level: payload.assurance_level || undefined,
      approval_mode: payload.approval_mode || undefined,
    };
    const draftView = await writingFetch<WritingContractDraftView>(
      `${WRITING_PREFIX}/documents/${encodeURIComponent(document.document_id)}/writing-contract-draft`,
      { method: "POST", body: JSON.stringify(draftRequest) },
    );

    // 3+4. 合同草稿 → 确认（封存合同原样转发；/contracts 与 /confirm 均返回
    //        ContractRecord 嵌套形状，合同身份取 record.contract.*）
    const draftRecord = await writingFetch<SealedContractRecord>(
      `${WRITING_PREFIX}/documents/${encodeURIComponent(document.document_id)}/contracts`,
      { method: "POST", body: JSON.stringify({ contract: draftView.contract }) },
    );
    const confirmedRecord = await writingFetch<SealedContractRecord>(
      `${WRITING_PREFIX}/contracts/${encodeURIComponent(draftRecord.contract.contract_id)}/confirm`,
      {
        method: "POST",
        body: JSON.stringify({
          previous_version: draftRecord.contract.version,
          contract: draftView.confirmed_contract,
        }),
      },
    );
    const sealed = confirmedRecord.contract;

    // 5. 编译计划（intent_plan 由服务端封存并随 draft 响应返回，原样转发；
    //    初始 artifact 按流程映射：long_form 仅 contract，多材料/改写附 materials）
    const baseVersionId = document.current_version_id ?? "";
    const initialArtifactTypes = resolveWritingFlow(payload.flow).initialArtifactTypes;
    const preview = await writingFetch<{
      plan: Record<string, unknown>;
      permissions: string[];
    }>(`${WRITING_PREFIX}/documents/${encodeURIComponent(document.document_id)}/plans`, {
      method: "POST",
      body: JSON.stringify({
        contract_id: sealed.contract_id,
        contract_version: sealed.version,
        base_version_id: baseVersionId,
        intent_plan: draftView.intent_plan,
        budget: DEFAULT_BUDGET,
        initial_artifact_types: initialArtifactTypes,
        required_final_artifact: "revision_set",
      }),
    });

    // 6. 创建运行
    let run = await writingFetch<RuntimeRun>(`${WRITING_PREFIX}/runs`, {
      method: "POST",
      headers: { "Idempotency-Key": uuidV4() },
      body: JSON.stringify({
        document_id: document.document_id,
        contract_id: sealed.contract_id,
        contract_version: sealed.version,
        contract_hash: sealed.contract_hash,
        base_version_id: baseVersionId,
        style_slug: payload.style || "default",
        plan: preview.plan,
        budget: DEFAULT_BUDGET,
        permissions: preview.permissions,
      }),
    });

    // 7. 自动审批（如果需要）
    if (run.status === "awaiting_approval") {
      const planId = (preview.plan as Record<string, unknown>).executable_plan as Record<string, unknown>;
      run = await writingFetch<RuntimeRun>(`${WRITING_PREFIX}/runs/${encodeURIComponent(run.run_id)}/approve`, {
        method: "POST",
        body: JSON.stringify({
          plan_id: planId?.plan_id,
          plan_version: 1,
          plan_hash: planId?.plan_hash,
          permissions: preview.permissions,
        }),
      });
    }

    return { run_id: run.run_id };
  } catch (error) {
    if (error instanceof WritingApiError) throw error;
    throw new WritingApiError("WRITING_UNAVAILABLE", 503, `写作服务暂不可用：${error instanceof Error ? error.message : String(error)}`);
  }
}

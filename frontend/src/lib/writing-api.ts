/**
 * 通用写作 REST API — 替代 Legacy WebSocket agent.start。
 *
 * startWritingRun 走 POST /api/v2/writing/launch 单入口：documents →
 * writing-contract-draft（服务端封存 lcp/1.0 合同 draft v1 + confirmed v2 +
 * intent plan）→ contract → confirm → compile → run（→ awaiting_approval 时
 * 服务端自动 approve）全部在服务端编排。前端不手写合同/计划 JSON、不计算
 * 任何哈希、不算预算信封——全部由服务端按 Go 结构体封存/单源下发。
 */
import type {
  AgentStartPayload,
} from "./writing-runtime-types.ts";

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

// ─── Public API ─────────────────────────────────────────

/** POST /api/v2/writing/launch 响应：服务端一次编排的启动结果。 */
export interface WritingLaunchResult {
  document_id: string;
  base_version_id?: string;
  contract_id: string;
  contract_version: number;
  contract_hash: string;
  plan_id: string;
  plan_hash: string;
  run_id: string;
  run_status: string;
  approved: boolean;
}

export async function startWritingRun(payload: AgentStartPayload): Promise<WritingLaunchResult> {
  // 深度研究（research_review）有专属启动链（research-settings 表单 →
  // research-contract-draft 封存 lcp/1.1 合同 → startResearchRun）：
  // 提前拒绝而不是误入普通流封存端点（服务端也拒绝并指回）。
  if (payload.flow === "research_review") {
    throw new WritingApiError("RESEARCH_REVIEW_CONTRACT_INVALID", 400, "深度研究流程请通过研究设置面板启动");
  }
  try {
    // 单入口：documents → 封存合同 → confirm → 计划 → 运行（→ 视需要
    // approve）全部在服务端编排（writing_launch.go）。语义字段——预算信封、
    // 初始 artifact、final artifact——服务端单源，前端只透传用户选择；
    // 一个 Idempotency-Key 覆盖整条链（服务端派生各步子键）。
    return await writingFetch<WritingLaunchResult>("/api/v2/writing/launch", {
      method: "POST",
      headers: { "Idempotency-Key": uuidV4() },
      body: JSON.stringify({
        message: payload.message.trim(),
        material_refs: payload.material_refs,
        flow: payload.flow ?? "long_form",
        style: payload.style || undefined,
        mode: payload.mode || undefined,
        assurance_level: payload.assurance_level || undefined,
        approval_mode: payload.approval_mode || undefined,
      }),
    });
  } catch (error) {
    if (error instanceof WritingApiError) throw error;
    throw new WritingApiError("WRITING_UNAVAILABLE", 503, `写作服务暂不可用：${error instanceof Error ? error.message : String(error)}`);
  }
}

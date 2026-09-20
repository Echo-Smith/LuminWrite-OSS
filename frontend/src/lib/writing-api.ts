/**
 * 通用写作 REST API — 替代 Legacy WebSocket agent.start。
 *
 * startWritingRun 走 document → contract → confirm → compile → run
 * （→ awaiting_approval 时自动 approve）的真实创建链路。
 */
import type {
  AgentStartPayload,
  DocumentRecord,
  RuntimeRun,
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

// ─── Contract Builder ───────────────────────────────────

function buildWritingContract(payload: AgentStartPayload) {
  const message = payload.message.trim();
  const style = payload.style || "default";
  return {
    schema_version: "1.0",
    status: "draft",
    intent: {
      operation: "create",
      genre: "article",
      purpose: message,
    },
    audience: {
      role: "reader",
      knowledge_level: "intermediate",
    },
    content: {
      topic: message.slice(0, 100),
      central_question: message,
      required_points: [],
      prohibited_points: [],
    },
    voice: {
      tone: "professional",
      formality: "neutral",
      person: "third",
      style_directives: [],
    },
    material_policy: {
      citation_style: "inline",
      require_sources: false,
      max_age_days: 0,
      min_sources: 0,
    },
    evidence_policy: {
      require_evidence: false,
      validators: [],
      assurance_level: "candidate",
    },
    delivery: {
      format: "markdown",
      max_words: 5000,
      sections: [],
    },
    collaboration: {
      approval_mode: payload.approval_mode || "auto",
      max_revisions: 3,
      human_gate: false,
    },
    source_attributions: [],
    inferences: [],
  };
}

function buildIntentPlan(contractRef: { id: string; version: number; hash: string }) {
  return {
    intent_plan_id: `intent_${uuidV4().slice(0, 8)}`,
    contract_ref: contractRef,
    summary: "Standard writing pipeline: outline → draft → quality",
    created_by: { type: "user" },
    created_at: new Date().toISOString(),
    proposed_steps: [
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
    ],
  };
}

const DEFAULT_BUDGET = {
  max_cost_usd: 2.0,
  max_duration_ms: 300000,
  max_concurrency: 1,
  max_nodes: 10,
  max_items: 4,
};

const DEFAULT_PERMISSIONS = ["model.invoke", "materials.read"];

// ─── Public API ─────────────────────────────────────────

export async function startWritingRun(payload: AgentStartPayload): Promise<{ run_id: string }> {
  try {
    const title = payload.message.slice(0, 60) || "新写作";
    const materialRefs = (payload.material_refs ?? []).filter((r) => r.material_id.trim() !== "");

    // 1. 创建文档
    const document = await writingFetch<DocumentRecord>(`${WRITING_PREFIX}/documents`, {
      method: "POST",
      body: JSON.stringify({ title, metadata: { material_refs: materialRefs } }),
    });

    // 2. 创建合同草稿
    const contract = buildWritingContract(payload);
    const draftRecord = await writingFetch<{ contract_id: string; version: number; contract_hash: string }>(
      `${WRITING_PREFIX}/documents/${encodeURIComponent(document.document_id)}/contracts`,
      { method: "POST", body: JSON.stringify({ contract }) },
    );

    // 3. 确认合同
    const confirmedRecord = await writingFetch<{ contract_id: string; version: number; contract_hash: string }>(
      `${WRITING_PREFIX}/contracts/${encodeURIComponent(draftRecord.contract_id)}/confirm`,
      {
        method: "POST",
        body: JSON.stringify({ previous_version: draftRecord.version, contract: { ...contract, status: "confirmed" } }),
      },
    );

    // 4. 编译计划
    const contractRef = {
      id: confirmedRecord.contract_id,
      version: confirmedRecord.version,
      hash: confirmedRecord.contract_hash,
    };
    const intentPlan = buildIntentPlan(contractRef);
    const baseVersionId = document.current_version_id ?? "";

    const preview = await writingFetch<{
      plan: Record<string, unknown>;
      permissions: string[];
    }>(`${WRITING_PREFIX}/documents/${encodeURIComponent(document.document_id)}/plans`, {
      method: "POST",
      body: JSON.stringify({
        contract_id: confirmedRecord.contract_id,
        contract_version: confirmedRecord.version,
        base_version_id: baseVersionId,
        intent_plan: intentPlan,
        budget: DEFAULT_BUDGET,
        initial_artifact_types: ["contract"],
        required_final_artifact: "revision_set",
      }),
    });

    // 5. 创建运行
    let run = await writingFetch<RuntimeRun>(`${WRITING_PREFIX}/runs`, {
      method: "POST",
      body: JSON.stringify({
        document_id: document.document_id,
        contract_id: confirmedRecord.contract_id,
        contract_version: confirmedRecord.version,
        contract_hash: confirmedRecord.contract_hash,
        base_version_id: baseVersionId,
        style_slug: payload.style || "default",
        plan: preview.plan,
        budget: DEFAULT_BUDGET,
        permissions: preview.permissions,
      }),
    });

    // 6. 自动审批（如果需要）
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

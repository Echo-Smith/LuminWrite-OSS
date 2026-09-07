/**
 * 研究综述切片 reducer（stores/writing-runtime-store.ts 的纯函数部分）。
 *
 * 事件来源：writing.ledger.event（payload.event_type = research.progress /
 * gate.pending / gate.decided，见 backend/internal/server/writing_event_adapter.go），
 * 以及未来可能原生透传的 research.* / gate.* 事件类型——两种形态都消费。
 * GET 快照按 last_event_sequence 合并：旧 GET 不得覆盖新事件已推进的状态。
 */
import type { GateView, ResearchProgressView, ResearchSpecProjection, ResearchTaskView } from "../lib/research-api.ts";

export interface ResearchSlice {
  runId: string | null;
  progress: ResearchProgressView | null;
  /**
   * 运行合同的 spec 投影（GET /runs/{id}/research 响应新增字段）。事件流不携带
   * spec；GET 快照合并时写入并保留——上限等展示以它优先，缺省回退启动表单值。
   */
  spec: ResearchSpecProjection | null;
  gates: Record<string, GateView>;
  /** 研究家族已消费的最大序列（事件或 GET 快照）；旧事件/旧 GET 在此被拒。 */
  lastEventSequence: number;
  /** 刷新后从 GET 投影重建待确认页面的次数（诊断用）。 */
  restoredFromGet: number;
  restoredFromGetAt: number | null;
}

export const initialResearchSlice: ResearchSlice = {
  runId: null,
  progress: null,
  spec: null,
  gates: {},
  lastEventSequence: 0,
  restoredFromGet: 0,
  restoredFromGetAt: null,
};

const RESEARCH_EVENT_TYPES = new Set(["research.progress", "gate.pending", "gate.decided"]);

interface LedgerEventPayload {
  event_type?: unknown;
  entity_id?: unknown;
  data?: unknown;
}

function extractResearchEvent(event: { type: string; payload: unknown }): { kind: string; data: Record<string, unknown> } | null {
  if (event.type === "writing.ledger.event") {
    const payload = event.payload as LedgerEventPayload | null;
    if (!payload || typeof payload.event_type !== "string") return null;
    if (!RESEARCH_EVENT_TYPES.has(payload.event_type)) return null;
    const data = (payload.data && typeof payload.data === "object" ? payload.data : {}) as Record<string, unknown>;
    return { kind: payload.event_type, data };
  }
  if (RESEARCH_EVENT_TYPES.has(event.type)) {
    const data = (event.payload && typeof event.payload === "object" ? event.payload : {}) as Record<string, unknown>;
    return { kind: event.type, data };
  }
  return null;
}

function readString(data: Record<string, unknown>, key: string): string | undefined {
  const value = data[key];
  return typeof value === "string" && value ? value : undefined;
}

function readNumber(data: Record<string, unknown>, key: string): number | undefined {
  const value = data[key];
  return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

function normalizeInputRef(value: unknown): GateView["input_ref"] {
  if (!value || typeof value !== "object") return undefined;
  const ref = value as Record<string, unknown>;
  const artifactId = readString(ref, "artifact_id");
  const contentHash = readString(ref, "content_hash");
  if (!artifactId || !contentHash) return undefined;
  return { artifact_id: artifactId, version: readNumber(ref, "version") ?? 1, content_hash: contentHash };
}

function gateFromEventData(data: Record<string, unknown>, fallbackGateId: string | undefined, sequence: number, status: string): GateView | null {
  const gateId = readString(data, "gate_id") ?? fallbackGateId;
  if (!gateId) return null;
  const kind = readString(data, "gate_kind");
  return {
    gate_id: gateId,
    run_id: readString(data, "run_id") ?? "",
    node_id: readString(data, "node_id") ?? "",
    gate_kind: kind === "outline" ? "outline" : "evidence",
    plan_id: readString(data, "plan_id") ?? "",
    plan_version: readNumber(data, "plan_version") ?? 1,
    plan_hash: readString(data, "plan_hash") ?? "",
    revision: readNumber(data, "revision") ?? 1,
    status: status === "approved" ? "approved" : "pending",
    decision: readString(data, "decision"),
    input_ref: normalizeInputRef(data.input_ref),
    allowed_operations: status === "pending" ? ["decide"] : [],
    blocked_reason: status === "pending" ? "awaiting_gate_decision" : undefined,
    last_event_sequence: sequence,
  };
}

/** 研究事件 reducer：未知事件类型返回原切片（容错不崩）。 */
export function applyResearchEvent(slice: ResearchSlice, event: { protocol: string; type: string; run_id: string; sequence: number; payload: unknown }): ResearchSlice {
  if (event.protocol !== "lumin-writing.v2") return slice;
  if (slice.runId !== null && event.run_id !== slice.runId) return slice; // 跨 run 事件
  const extracted = extractResearchEvent(event);
  if (!extracted) return slice; // 未知/非研究事件：不崩、不误伤
  if (event.sequence <= slice.lastEventSequence) return slice; // 重复/旧事件

  const base: ResearchSlice = { ...slice, lastEventSequence: event.sequence };

  if (extracted.kind === "research.progress") {
    const phase = readString(extracted.data, "phase");
    const counts = { ...(slice.progress?.counts ?? {}) };
    for (const key of ["completed", "total", "failed", "deferred"] as const) {
      const value = readNumber(extracted.data, key);
      if (value !== undefined) counts[key] = value;
    }
    return {
      ...base,
      progress: slice.progress
        ? { ...slice.progress, phase: phase ?? slice.progress.phase, counts }
        : {
            run_id: event.run_id,
            phase: phase ?? "pending",
            counts,
            tasks: [],
            errors: [],
            last_event_sequence: event.sequence,
          },
    };
  }

  const gate = gateFromEventData(extracted.data, typeof extracted.data.entity_id === "string" ? extracted.data.entity_id : undefined, event.sequence, extracted.kind === "gate.decided" ? "approved" : "pending");
  if (!gate) return base;
  const existing = slice.gates[gate.gate_id];
  // gate.pending 重放不得回退已确认状态
  const merged = existing && existing.last_event_sequence > gate.last_event_sequence ? existing : gate;
  return {
    ...base,
    gates: { ...slice.gates, [gate.gate_id]: merged },
    progress: slice.progress ? { ...slice.progress, active_gate: merged.status === "pending" ? merged : slice.progress.active_gate } : slice.progress,
  };
}

/**
 * GET /runs/{id}/research 快照合并。
 * 新快照（last_event_sequence >= 本地水位）整体生效；
 * 旧快照只补充事件流不携带的字段（tasks/errors），不得覆盖事件已推进的 phase/counts/gate。
 */
export function mergeResearchProgress(slice: ResearchSlice, view: ResearchProgressView): ResearchSlice {
  if (slice.runId !== null && view.run_id !== slice.runId) return slice; // 跨 run
  if (view.last_event_sequence < slice.lastEventSequence) {
    // 旧 GET：仅当事件流尚未建立 progress 时整体采用，否则只补充事件不携带的字段。
    if (!slice.progress) {
      return {
        ...slice,
        runId: slice.runId ?? view.run_id,
        progress: { ...view, tasks: view.tasks ?? [] },
        spec: view.spec ?? slice.spec,
      };
    }
    const progress: ResearchProgressView = {
      ...slice.progress,
      tasks: view.tasks ?? slice.progress.tasks,
      errors: view.errors ?? slice.progress.errors,
      pack_ref: view.pack_ref ?? slice.progress.pack_ref,
      run_id: view.run_id,
    };
    return { ...slice, progress, spec: view.spec ?? slice.spec, runId: slice.runId ?? view.run_id };
  }
  const gates = { ...slice.gates };
  if (view.active_gate) {
    const existing = gates[view.active_gate.gate_id];
    if (!existing || existing.last_event_sequence <= view.active_gate.last_event_sequence) {
      gates[view.active_gate.gate_id] = view.active_gate;
    }
  }
  return {
    ...slice,
    runId: view.run_id,
    progress: { ...view, tasks: view.tasks ?? [] },
    spec: view.spec ?? slice.spec,
    gates,
    lastEventSequence: Math.max(slice.lastEventSequence, view.last_event_sequence),
  };
}

/** 单个 gate 的 GET 结果合并（202 后轮询路径）；旧结果不得回退新状态。 */
export function mergeGateView(gates: Record<string, GateView>, gate: GateView): Record<string, GateView> {
  const existing = gates[gate.gate_id];
  if (existing && existing.last_event_sequence > gate.last_event_sequence) return gates;
  return { ...gates, [gate.gate_id]: gate };
}

export function pendingGate(slice: ResearchSlice): GateView | null {
  for (const gate of Object.values(slice.gates)) {
    if (gate.status === "pending") return gate;
  }
  return null;
}

export function latestErrorTasks(slice: ResearchSlice): ResearchTaskView[] {
  return slice.progress?.errors ?? [];
}

/** 研究切片是否处于活跃运行（非研究运行的 pending 空投影不渲染面板）。 */
export function researchSliceActive(slice: ResearchSlice): boolean {
  if (!slice.progress) return false;
  return slice.progress.phase !== "pending" || Object.keys(slice.gates).length > 0;
}

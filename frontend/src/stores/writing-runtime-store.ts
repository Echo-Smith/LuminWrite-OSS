import { create } from "zustand";
import type {
  AuditQualityReport,
  DocumentRecord,
  RuntimeRun,
  StoredDocumentVersion,
  UserQualitySummary,
  WritingArtifactEventPayload,
  WritingEvent,
} from "../lib/writing-runtime-types.ts";
import type { GateView, ResearchProgressView } from "../lib/research-api.ts";
import { fetchResearchProgress, fetchRunShim, isMockResearchRunId } from "../lib/research-api.ts";
import { applyResearchEvent, initialResearchSlice, mergeGateView, mergeResearchProgress, type ResearchSlice } from "./research-slice.ts";

export interface WritingRuntimeProjection {
  document: DocumentRecord | null;
  versions: StoredDocumentVersion[];
  run: RuntimeRun | null;
  lastSequence: number;
  provisionalDeltas: Record<string, string>;
  committedVersionId: string | null;
  nodeStatuses: Record<string, string>;
  artifacts: WritingArtifactEventPayload[];
  quality: UserQualitySummary | null;
  auditReport: AuditQualityReport | null;
  events: WritingEvent[];
  /** 研究综述切片：进度投影 + 两个 gate 的最新已知状态。 */
  research: ResearchSlice;
}

export const initialWritingRuntimeProjection: WritingRuntimeProjection = {
  document: null,
  versions: [],
  run: null,
  lastSequence: 0,
  provisionalDeltas: {},
  committedVersionId: null,
  nodeStatuses: {},
  artifacts: [],
  quality: null,
  auditReport: null,
  events: [],
  research: initialResearchSlice,
};

export function projectWritingEvent(
  current: WritingRuntimeProjection,
  event: WritingEvent,
): WritingRuntimeProjection {
  if (event.protocol !== "lumin-writing.v2" || event.sequence <= current.lastSequence) return current;
  if (current.run && event.run_id !== current.run.run_id) return current;

  const next: WritingRuntimeProjection = {
    ...current,
    lastSequence: event.sequence,
    events: [...current.events, event].slice(-200),
    // 研究事件家族（research.progress / gate.pending / gate.decided / 未来扩展）
    // 由独立 reducer 消费；未知研究事件类型只更新序列，不使页面崩溃。
    research: applyResearchEvent(current.research, event),
  };

  if (event.type === "writing.run.status") {
    const payload = event.payload as { to?: RuntimeRun["status"] };
    if (next.run && payload.to) next.run = { ...next.run, status: payload.to, last_event_sequence: event.sequence };
  }
  if (event.type === "writing.document.delta") {
    const payload = event.payload as { block_id?: string; delta?: string; lifecycle?: string };
    if (payload.lifecycle === "provisional" && payload.delta) {
      const blockId = payload.block_id || "__document__";
      next.provisionalDeltas = {
        ...next.provisionalDeltas,
        [blockId]: `${next.provisionalDeltas[blockId] ?? ""}${payload.delta}`,
      };
    }
  }
  if (event.type === "writing.document.committed") {
    const payload = event.payload as { version_id?: string; lifecycle?: string };
    if (payload.lifecycle === "committed" && payload.version_id) {
      next.committedVersionId = payload.version_id;
      next.provisionalDeltas = {};
    }
  }
  if (event.type === "writing.node.status") {
    const payload = event.payload as { node_id?: string; status?: string };
    if (payload.node_id && payload.status) {
      next.nodeStatuses = { ...next.nodeStatuses, [payload.node_id]: payload.status };
    }
  }
  if (event.type === "writing.artifact.created") {
    const payload = event.payload as unknown as WritingArtifactEventPayload;
    if (payload.artifact_id) next.artifacts = [...next.artifacts, payload];
  }
  if (event.type === "writing.quality.updated") {
    const payload = event.payload as { quality_state?: UserQualitySummary["quality_state"]; achieved_assurance?: UserQualitySummary["achieved_assurance"] };
    if (next.quality && payload.quality_state && payload.achieved_assurance) {
      next.quality = { ...next.quality, quality_state: payload.quality_state, achieved_assurance: payload.achieved_assurance };
    }
  }
  return next;
}

interface APIEnvelope<T> { success?: boolean; data?: T }

async function writingRequest<T>(path: string, init: RequestInit = {}): Promise<T> {
  if (!path.startsWith("/api/v2/")) throw new Error("governed writing store only accepts /api/v2 resources");
  const response = await fetch(path, { ...init, headers: { Accept: "application/json", ...init.headers } });
  const body = await response.json() as APIEnvelope<T> & T;
  if (!response.ok) throw new Error(`writing API request failed (${response.status})`);
  return (body.data ?? body) as T;
}

interface WritingRuntimeActions {
  loading: boolean;
  error: string | null;
  loadDocument: (documentId: string, token?: string) => Promise<void>;
  loadRun: (runId: string, token?: string) => Promise<void>;
  refreshRunEvents: (runId: string, token?: string) => Promise<void>;
  controlRun: (runId: string, action: "pause" | "resume" | "cancel", token?: string) => Promise<void>;
  applyEvent: (event: WritingEvent) => void;
  /** 研究综述：以 GET 为准重建进度与待确认 gate（刷新/断线重连后调用）。 */
  refreshResearch: (runId: string, token?: string) => Promise<void>;
  /** 用一次 GET gate 覆盖本地 gate 状态（确认 202 后轮询走这里）。 */
  applyGateView: (gate: GateView) => void;
  resetRuntime: () => void;
}

export const useWritingRuntimeStore = create<WritingRuntimeProjection & WritingRuntimeActions>((set, get) => ({
  ...initialWritingRuntimeProjection,
  loading: false,
  error: null,
  loadDocument: async (documentId, token) => {
    set({ loading: true, error: null });
    try {
      const [document, versionPage, quality, auditReport] = await Promise.all([
        writingRequest<DocumentRecord>(`/api/v2/documents/${encodeURIComponent(documentId)}`, { headers: token ? { Authorization: `Bearer ${token}` } : {} }),
        writingRequest<{ versions: StoredDocumentVersion[] }>(`/api/v2/documents/${encodeURIComponent(documentId)}/versions`, { headers: token ? { Authorization: `Bearer ${token}` } : {} }),
        writingRequest<UserQualitySummary>(`/api/v2/documents/${encodeURIComponent(documentId)}/quality`, { headers: token ? { Authorization: `Bearer ${token}` } : {} }).catch(() => null),
        writingRequest<AuditQualityReport>(`/api/v2/documents/${encodeURIComponent(documentId)}/audit-report`, { headers: token ? { Authorization: `Bearer ${token}` } : {} }).catch(() => null),
      ]);
      set({ document, versions: versionPage.versions ?? [], quality, auditReport, committedVersionId: document.current_version_id ?? null, loading: false });
    } catch (error) {
      set({ error: error instanceof Error ? error.message : "无法加载写作文档", loading: false });
    }
  },
  loadRun: async (runId, token) => {
    set({ loading: true, error: null });
    try {
      // mock 研究运行：不请求真实 /runs/{id}，由 research-api 提供投影。
      const run = isMockResearchRunId(runId)
        ? await fetchRunShim(runId)
        : await writingRequest<RuntimeRun>(`/api/v2/runs/${encodeURIComponent(runId)}`, { headers: token ? { Authorization: `Bearer ${token}` } : {} });
      set({ run, lastSequence: 0, events: [], nodeStatuses: {}, artifacts: [], provisionalDeltas: {}, research: { ...initialResearchSlice, runId: isMockResearchRunId(runId) ? runId : null }, loading: false });
      if (isMockResearchRunId(runId)) await get().refreshResearch(runId, token);
    } catch (error) {
      set({ error: error instanceof Error ? error.message : "无法加载运行", loading: false });
    }
  },
  refreshRunEvents: async (runId, token) => {
    try {
      const page = await writingRequest<{ events: WritingEvent[]; next_sequence: number }>(
        `/api/v2/runs/${encodeURIComponent(runId)}/events?after=${get().lastSequence}`,
        { headers: token ? { Authorization: `Bearer ${token}` } : {} },
      );
      for (const event of page.events ?? []) set((state) => projectWritingEvent(state, event));
    } catch (error) {
      set({ error: error instanceof Error ? error.message : "无法同步运行事件" });
    }
  },
  controlRun: async (runId, action, token) => {
    set({ error: null });
    try {
      const run = await writingRequest<RuntimeRun>(`/api/v2/runs/${encodeURIComponent(runId)}/${action}`, {
        method: "POST",
        headers: {
          "Idempotency-Key": `ui-${action}-${runId}-${Date.now()}`,
          ...(token ? { Authorization: `Bearer ${token}` } : {}),
        },
      });
      set({ run });
    } catch (error) {
      set({ error: error instanceof Error ? error.message : "无法控制运行" });
    }
  },
  applyEvent: (event) => set((state) => projectWritingEvent(state, event)),
  refreshResearch: async (runId, token) => {
    try {
      const progress = await fetchResearchProgress(runId, token);
      set((state) => {
        const research = mergeResearchProgress(state.research, progress);
        const activeGate = progress.active_gate;
        const gates = activeGate
          ? mergeGateView(research.gates, activeGate)
          : research.gates;
        // 恢复待确认页面：从 GET 投影重建，无需等待事件流。
        return { research: { ...research, runId, gates, restoredFromGetAt: Date.now(), restoredFromGet: research.restoredFromGet + 1 } };
      });
    } catch {
      // 非研究运行或暂时不可用时保持现状：研究切片只是没有数据，不是页面错误。
    }
  },
  applyGateView: (gate) => set((state) => ({ research: { ...state.research, gates: mergeGateView(state.research.gates, gate) } })),
  resetRuntime: () => set({ ...initialWritingRuntimeProjection, loading: false, error: null }),
}));

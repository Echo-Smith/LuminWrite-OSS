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
      const run = await writingRequest<RuntimeRun>(`/api/v2/runs/${encodeURIComponent(runId)}`, { headers: token ? { Authorization: `Bearer ${token}` } : {} });
      set({ run, lastSequence: 0, events: [], nodeStatuses: {}, artifacts: [], provisionalDeltas: {}, loading: false });
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
  resetRuntime: () => set({ ...initialWritingRuntimeProjection, loading: false, error: null }),
}));

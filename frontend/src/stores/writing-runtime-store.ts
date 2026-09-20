/**
 * Writing Runtime Store — governed REST+SSE runtime + session management
 *
 * Combines:
 * 1. Governed runtime state (documents, runs, events, quality, research)
 * 2. Session management (migrated from legacy agent-store)
 * 3. Streaming text state (SSE content deltas)
 * 4. Temporary WebSocket connection (for workflow/editorial page compatibility)
 */
import { create } from "zustand";
import type {
  AgentStartPayload,
  AgentStepName,
  AgentStepStatus,
  AgentResult,
  AuditQualityReport,
  ChatMessage,
  CompactionPart,
  DataPart,
  DocumentRecord,
  MessagePart,
  OutlineData,
  ReasoningPart,
  RuntimeRun,
  SessionFolder,
  StoredDocumentVersion,
  TextPart,
  ToolCallPart,
  UserQualitySummary,
  WritingArtifact,
  WritingArtifactEventPayload,
  WritingEvent,
  WritingSession,
} from "../lib/writing-runtime-types.ts";
import type { GateView, ResearchProgressView } from "../lib/research-api.ts";
import { fetchResearchProgress, fetchRunShim, isMockResearchRunId, mockNodeStatuses } from "../lib/research-api.ts";
import { applyResearchEvent, initialResearchSlice, mergeGateView, mergeResearchProgress, type ResearchSlice } from "./research-slice.ts";
import { startWritingRun } from "../lib/writing-api.ts";
import { useSettingsStore } from "./settings-store.ts";
import { useAuthStore } from "./auth-store.ts";
import { useAuthModal } from "./auth-modal-store.ts";
import { useBillingStore } from "./billing-store.ts";
import { useEditorialStore } from "./editorial-store.ts";
import { useMemoryStore, type MemoryEntry } from "./memory-store.ts";
import { useWorkflowStore, type AgentConfig as WFAgentConfig, type WorkflowSpec as WFWorkflowSpec } from "./workflow-store.ts";
import { resolveConversationId } from "../lib/conversation-session.ts";
import { markSessionRead } from "../lib/session-read-state.ts";

// ─── ID generators ────────────────────────────────────────

function genId(): string {
  return `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
}

let msgIdCounter = 0;
function genMsgId(): string {
  return `msg-${++msgIdCounter}`;
}

// ─── Governed Runtime Projection ──────────────────────────

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

// ─── Governed Event Projection ────────────────────────────

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

// ─── REST helper ──────────────────────────────────────────

interface APIEnvelope<T> { success?: boolean; data?: T }

async function writingRequest<T>(path: string, init: RequestInit = {}): Promise<T> {
  if (!path.startsWith("/api/v2/")) throw new Error("governed writing store only accepts /api/v2 resources");
  const response = await fetch(path, { ...init, headers: { Accept: "application/json", ...init.headers } });
  const body = await response.json() as APIEnvelope<T> & T;
  if (!response.ok) throw new Error(`writing API request failed (${response.status})`);
  return (body.data ?? body) as T;
}

// ─── Store Interface ──────────────────────────────────────

interface WritingRuntimeState extends WritingRuntimeProjection {
  // Governed runtime UI state
  loading: boolean;
  error: string | null;

  // Streaming state (SSE content deltas)
  streamingText: string;
  reasoningText: string;
  nodeProgress: Record<string, { stepName: string; status: string }>;

  // Active run (linked to current session)
  activeRunId: string | null;

  // Session management (migrated from agent-store)
  sessions: WritingSession[];
  activeSessionId: string | null;
  sessionsLoaded: boolean;
  sessionsTotal: number;
  sessionsPage: number;
  folders: SessionFolder[];
  resumedTraceId: string | null;

  // Temporary WebSocket (workflow/editorial page compatibility)
  ws: WebSocket | null;
  wsConnected: boolean;

  // ── Governed runtime actions ──
  loadDocument: (documentId: string, token?: string) => Promise<void>;
  loadRun: (runId: string, token?: string) => Promise<void>;
  refreshRunEvents: (runId: string, token?: string) => Promise<void>;
  controlRun: (runId: string, action: "pause" | "resume" | "cancel", token?: string) => Promise<void>;
  applyEvent: (event: WritingEvent) => void;
  refreshResearch: (runId: string, token?: string) => Promise<void>;
  applyGateView: (gate: GateView) => void;
  resetRuntime: () => void;

  // ── Session management actions ──
  createSession: () => string;
  switchSession: (id: string) => void;
  deleteSession: (id: string) => Promise<void>;
  loadSessions: (page?: number, append?: boolean) => Promise<void>;
  loadMoreSessions: () => Promise<void>;
  loadArchivedSessions: () => Promise<void>;
  loadFolders: () => Promise<void>;
  createFolder: (name: string) => Promise<boolean>;
  renameFolder: (folderId: string, name: string) => Promise<boolean>;
  deleteFolder: (folderId: string) => Promise<void>;
  batchSessions: (action: "delete" | "archive" | "unarchive" | "move", traceIds: string[], folderId?: string) => Promise<number>;
  duplicateSession: (id: string) => Promise<boolean>;
  loadSessionDetail: (traceId: string) => Promise<void>;
  loadSessionArtifacts: (traceId: string) => Promise<void>;

  // ── Writing control actions ──
  startWriting: (payload: AgentStartPayload) => void;
  pauseWriting: () => void;
  resumeWriting: () => void;
  cancelWriting: () => void;
  confirmOutline: (data: OutlineData | null) => void;
  regenerateOutline: () => void;
  resumeSession: (traceId: string) => void;
  markFeedbackSubmitted: (traceId: string) => void;
  saveArticleEdit: (traceId: string, article: string, articleTitle?: string, versionNote?: string) => Promise<boolean>;
  renameSession: (traceId: string, title: string) => Promise<boolean>;
  loadArticleVersion: (traceId: string, versionId: string) => Promise<boolean>;

  // ── WebSocket (temporary, for workflow page) ──
  connectWS: () => void;
  sendWS: (type: string, payload: Record<string, unknown>) => void;
  handleServerMessage: (msg: { type: string; payload: Record<string, unknown> }) => void;

  // ── Internal helpers ──
  _getActiveSession: () => WritingSession | null;
  _updateActiveSession: (updater: (s: WritingSession) => WritingSession) => void;
  _updateLastAssistantMessage: (updater: (m: ChatMessage) => ChatMessage) => void;
  _restoreEditorialWorkflow: (traceId: string, session: WritingSession) => void;
}

// ─── Store Implementation ─────────────────────────────────

export const useWritingRuntimeStore = create<WritingRuntimeState>((set, get) => ({
  // ── Governed runtime initial state ──
  ...initialWritingRuntimeProjection,
  loading: false,
  error: null,

  // ── Streaming initial state ──
  streamingText: "",
  reasoningText: "",
  nodeProgress: {},

  // ── Active run ──
  activeRunId: null,

  // ── Session management initial state ──
  sessions: [],
  activeSessionId: null,
  sessionsLoaded: false,
  sessionsTotal: 0,
  sessionsPage: 0,
  folders: [],
  resumedTraceId: null,

  // ── WebSocket initial state (temporary) ──
  ws: null,
  wsConnected: false,

  // ═══════════════════════════════════════════════════════
  // Governed Runtime Actions (preserved from original)
  // ═══════════════════════════════════════════════════════

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
      const run = isMockResearchRunId(runId)
        ? await fetchRunShim(runId)
        : await writingRequest<RuntimeRun>(`/api/v2/runs/${encodeURIComponent(runId)}`, { headers: token ? { Authorization: `Bearer ${token}` } : {} });
      const mockStatuses = isMockResearchRunId(runId) ? mockNodeStatuses(runId) : {};
      set({
        run,
        lastSequence: 0,
        events: [],
        nodeStatuses: Object.keys(mockStatuses).length > 0 ? mockStatuses : {},
        artifacts: [],
        provisionalDeltas: {},
        research: { ...initialResearchSlice, runId: isMockResearchRunId(runId) ? runId : null },
        loading: false,
      });
      if (isMockResearchRunId(runId)) await get().refreshResearch(runId, token);
    } catch (error) {
      set({ error: error instanceof Error ? error.message : "无法加载运行", loading: false });
    }
  },

  refreshRunEvents: async (runId, token) => {
    if (isMockResearchRunId(runId)) return;
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

  applyEvent: (event) => {
    // First, apply governed runtime projection
    set((state) => projectWritingEvent(state, event));

    // Then, handle new streaming event types for session messages
    const p = event.payload as Record<string, unknown>;

    if (event.type === "writing.content.delta") {
      const delta = p.delta as string;
      if (delta) {
        set((s) => ({ streamingText: s.streamingText + delta }));
        get()._updateLastAssistantMessage((m) => {
          const parts = [...m.parts];
          let lastTextIdx = -1;
          for (let i = parts.length - 1; i >= 0; i--) {
            if (parts[i].type === "text" && (parts[i] as TextPart).streaming) {
              lastTextIdx = i;
              break;
            }
          }
          if (lastTextIdx >= 0) {
            const textPart = parts[lastTextIdx] as TextPart;
            parts[lastTextIdx] = { ...textPart, text: textPart.text + delta };
          } else {
            parts.push({ type: "text", text: delta, streaming: true });
          }
          return { ...m, parts };
        });
      }
    }

    if (event.type === "writing.content.done") {
      const fullText = p.full_text as string | undefined;
      get()._updateLastAssistantMessage((m) => ({
        ...m,
        parts: m.parts.map((part) => {
          if (part.type === "text" && part.streaming) {
            return { ...part, streaming: false, text: fullText ?? part.text };
          }
          return part;
        }),
      }));
    }

    if (event.type === "writing.reasoning.delta") {
      const delta = p.delta as string;
      if (delta) {
        set((s) => ({ reasoningText: s.reasoningText + delta }));
        get()._updateLastAssistantMessage((m) => {
          const parts = [...m.parts];
          let lastReasoningIdx = -1;
          for (let i = parts.length - 1; i >= 0; i--) {
            if (parts[i].type === "reasoning") {
              lastReasoningIdx = i;
              break;
            }
          }
          if (lastReasoningIdx >= 0) {
            const reasoningPart = parts[lastReasoningIdx] as ReasoningPart;
            if (reasoningPart.completed) {
              parts.push({ type: "reasoning", text: delta });
            } else {
              parts[lastReasoningIdx] = { ...reasoningPart, text: reasoningPart.text + delta };
            }
          } else {
            parts.push({ type: "reasoning", text: delta });
          }
          return { ...m, parts };
        });
      }
    }

    if (event.type === "writing.node.progress") {
      const nodeId = p.node_id as string;
      const stepName = p.step_name as string;
      const status = p.status as string;
      if (nodeId) {
        set((s) => ({
          nodeProgress: {
            ...s.nodeProgress,
            [nodeId]: { stepName: stepName ?? s.nodeProgress[nodeId]?.stepName ?? "", status: status ?? "running" },
          },
        }));
      }
    }
  },

  refreshResearch: async (runId, token) => {
    try {
      const progress = await fetchResearchProgress(runId, token);
      set((state) => {
        const research = mergeResearchProgress(state.research, progress);
        const activeGate = progress.active_gate;
        const gates = activeGate
          ? mergeGateView(research.gates, activeGate)
          : research.gates;
        return { research: { ...research, runId, gates, restoredFromGetAt: Date.now(), restoredFromGet: research.restoredFromGet + 1 } };
      });
    } catch {
      // Non-research run or temporarily unavailable
    }
  },

  applyGateView: (gate) => set((state) => ({ research: { ...state.research, gates: mergeGateView(state.research.gates, gate) } })),

  resetRuntime: () => set({ ...initialWritingRuntimeProjection, loading: false, error: null, streamingText: "", reasoningText: "", nodeProgress: {} }),

  // ═══════════════════════════════════════════════════════
  // Session Management (migrated from agent-store)
  // ═══════════════════════════════════════════════════════

  createSession: () => {
    const id = genId();
    const session: WritingSession = {
      id,
      title: "新写作",
      messages: [],
      traceId: null,
      conversationId: null,
      status: "idle",
      style: useSettingsStore.getState().lastStyle || "yinyue",
      mode: "auto",
      createdAt: Date.now(),
      folderId: null,
      archived: false,
      awaitInputAt: null,
      kbEnabled: true,
    };
    set((state) => ({
      sessions: [session, ...state.sessions],
      activeSessionId: id,
    }));
    return id;
  },

  switchSession: (id) => {
    set({ activeSessionId: id });
    const session = get().sessions.find((s) => s.id === id);
    if (!session) return;
    markSessionRead(session);
    // Resume running/paused sessions via WebSocket (temporary)
    const wsState = get().ws?.readyState;
    if (
      session.traceId &&
      (wsState === WebSocket.OPEN) &&
      (session.status === "running" || session.status === "paused") &&
      get().resumedTraceId !== session.traceId
    ) {
      set({ resumedTraceId: session.traceId });
      get().resumeSession(session.traceId);
    }
    if (session.traceId && session.messages.length === 0) {
      get().loadSessionDetail(session.traceId);
    } else if (session.mode === "editorial" && session.traceId) {
      get()._restoreEditorialWorkflow(session.traceId, session);
    }
  },

  deleteSession: async (id) => {
    const session = get().sessions.find((s) => s.id === id);
    set((state) => {
      const remaining = state.sessions.filter((s) => s.id !== id);
      return {
        sessions: remaining,
        activeSessionId: state.activeSessionId === id ? remaining[0]?.id ?? null : state.activeSessionId,
      };
    });
    if (!session?.traceId) return;
    try {
      await get().batchSessions("delete", [session.traceId]);
    } catch (e) {
      console.error("Failed to delete session on server:", e);
    }
  },

  loadSessions: async (page = 1, append = false) => {
    try {
      const res = await fetch(`/api/v2/sessions?page=${page}&page_size=50`);
      const json = await res.json();
      if (!json.success || !json.data?.sessions) return;

      const dbSessions = json.data.sessions as Array<{
        trace_id: string;
        status: string;
        current_step: string;
        user_input: string;
        style_slug?: string;
        mode: string;
        created_at: string;
        updated_at?: string;
        folder_id?: string;
        archived_at?: string;
        completed_at?: string;
        duration_ms?: number;
        article_title?: string;
        task_name?: string;
        custom_title?: string;
      }>;

      const dbSessionsMapped: WritingSession[] = dbSessions.map((t) => ({
        id: t.trace_id,
        title: t.custom_title || t.article_title || t.task_name || t.user_input?.slice(0, 30) || "历史会话",
        articleTitle: t.article_title || null,
        customTitle: t.custom_title || null,
        messages: [],
        traceId: t.trace_id,
        conversationId: t.trace_id,
        status: (t.status === "completed" ? "completed" :
                t.status === "failed" ? "error" :
                t.status === "running" ? "running" :
                t.status === "paused" ? "paused" : "idle") as WritingSession["status"],
        style: t.style_slug || "yinyue",
        mode: t.mode || "auto",
        createdAt: new Date(t.created_at).getTime(),
        updatedAt: t.updated_at ? new Date(t.updated_at).getTime() : new Date(t.created_at).getTime(),
        folderId: t.folder_id || null,
        archived: Boolean(t.archived_at),
        awaitInputAt: null,
        kbEnabled: true,
      }));

      set((state) => {
        const dbTraceIds = new Set(dbSessionsMapped.map((s) => s.traceId));
        const localOnly = state.sessions.filter(
          (s) => s.traceId && !dbTraceIds.has(s.traceId)
        );
        const localTemp = state.sessions.filter((s) => !s.traceId);
        const base = append ? state.sessions : [...localTemp, ...localOnly];
        return {
          sessions: append ? [...base, ...dbSessionsMapped] : [...base, ...dbSessionsMapped],
          sessionsLoaded: true,
          sessionsTotal: json.data.total ?? base.length,
          sessionsPage: page,
        };
      });
    } catch (e) {
      console.error("Failed to load sessions from DB:", e);
    }
  },

  loadMoreSessions: async () => {
    const { sessionsPage, sessionsTotal, sessions } = get();
    if (sessions.length >= sessionsTotal) return;
    await get().loadSessions(sessionsPage + 1, true);
  },

  loadArchivedSessions: async () => {
    try {
      const res = await fetch("/api/v2/sessions?page=1&page_size=50&archived=true");
      const json = await res.json();
      if (!json.success || !json.data?.sessions) return;
      const rows = json.data.sessions as Array<Record<string, unknown>>;
      const archivedMapped: WritingSession[] = rows.map((t) => ({
        id: t.trace_id as string,
        title: (t.article_title as string) || (t.task_name as string) || (t.user_input as string)?.slice(0, 30) || "历史会话",
        articleTitle: (t.article_title as string) || null,
        messages: [],
        traceId: t.trace_id as string,
        conversationId: t.trace_id as string,
        status: "completed" as WritingSession["status"],
        style: (t.style_slug as string) || "yinyue",
        mode: (t.mode as string) || "auto",
        createdAt: new Date(t.created_at as string).getTime(),
        updatedAt: t.updated_at ? new Date(t.updated_at as string).getTime() : new Date(t.created_at as string).getTime(),
        folderId: (t.folder_id as string) || null,
        archived: true,
        awaitInputAt: null,
        kbEnabled: true,
      }));
      set((state) => {
        const archivedIds = new Set(archivedMapped.map((s) => s.id));
        return {
          sessions: [...state.sessions.filter((s) => !archivedIds.has(s.id)), ...archivedMapped],
        };
      });
    } catch (e) {
      console.error("Failed to load archived sessions:", e);
    }
  },

  // ── Session Folders ──

  loadFolders: async () => {
    try {
      const res = await fetch("/api/v2/session-folders");
      const json = await res.json();
      if (json.success && json.data?.folders) {
        set({ folders: json.data.folders as SessionFolder[] });
      }
    } catch {
      // silent
    }
  },

  createFolder: async (name) => {
    try {
      const res = await fetch("/api/v2/session-folders", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name }),
      });
      const json = await res.json();
      if (!json.success) throw new Error(json.error?.message || "创建文件夹失败");
      await get().loadFolders();
      return true;
    } catch (e) {
      console.error("Failed to create folder:", e);
      return false;
    }
  },

  renameFolder: async (folderId, name) => {
    try {
      const res = await fetch(`/api/v2/session-folders/${folderId}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name }),
      });
      const json = await res.json();
      if (!json.success) throw new Error(json.error?.message || "重命名失败");
      await get().loadFolders();
      return true;
    } catch (e) {
      console.error("Failed to rename folder:", e);
      return false;
    }
  },

  deleteFolder: async (folderId) => {
    try {
      await fetch(`/api/v2/session-folders/${folderId}`, { method: "DELETE" });
      await get().loadFolders();
      await get().loadSessions(1, false);
    } catch (e) {
      console.error("Failed to delete folder:", e);
    }
  },

  batchSessions: async (action, traceIds, folderId) => {
    const res = await fetch("/api/v2/sessions/batch", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ action, trace_ids: traceIds, folder_id: folderId ?? "" }),
    });
    const json = await res.json();
    if (!json.success) throw new Error(json.error?.message || "批量操作失败");
    const idSet = new Set(traceIds);
    set((state) => ({
      sessions: state.sessions.map((s) => {
        if (!s.traceId || !idSet.has(s.traceId)) return s;
        switch (action) {
          case "delete":
            return s;
          case "archive":
            return { ...s, archived: true };
          case "unarchive":
            return { ...s, archived: false };
          case "move":
            return { ...s, folderId: folderId ?? null };
          default:
            return s;
        }
      }).filter((s) => !(action === "delete" && s.traceId && idSet.has(s.traceId))),
    }));
    return json.data?.affected ?? 0;
  },

  duplicateSession: async (id) => {
    const session = get().sessions.find((s) => s.id === id);
    if (!session?.traceId) return false;
    try {
      const res = await fetch(`/api/v2/sessions/${session.traceId}/duplicate`, {
        method: "POST",
      });
      const json = await res.json();
      if (!json.success) throw new Error(json.error?.message || "复制失败");
      await get().loadSessions(1, false);
      return true;
    } catch (e) {
      console.error("Failed to duplicate session:", e);
      return false;
    }
  },

  // ── Session Detail ──

  loadSessionDetail: async (traceId) => {
    const existing = get().sessions.find((s) => s.traceId === traceId);
    if (existing && existing.messages.length > 0) return;

    try {
      const res = await fetch(`/api/v2/sessions/${traceId}`);
      const json = await res.json();
      if (!json.success || !json.data) return;

      const d = json.data as {
        trace_id: string;
        status: string;
        user_input: string;
        style_slug?: string;
        mode: string;
        article?: string;
        article_title?: string;
        task_name?: string;
        custom_title?: string;
        step_history?: Array<{ step: string; status: string; startedAt?: string; completedAt?: string; durationMs?: number; result?: unknown; error?: string }>;
        review?: unknown;
        token_usage?: unknown;
        reasoning_content?: string;
        created_at: string;
        completed_at?: string;
        error?: string;
        has_feedback?: boolean;
      };

      const messages: ChatMessage[] = [];

      if (d.user_input) {
        messages.push({
          id: genMsgId(),
          role: "user",
          parts: [{ type: "text", text: d.user_input }],
          createdAt: new Date(d.created_at).getTime(),
        });
      }

      const assistantParts: MessagePart[] = [];

      if (d.step_history && Array.isArray(d.step_history)) {
        for (const step of d.step_history) {
          assistantParts.push({
            type: "tool-call",
            toolName: step.step as AgentStepName,
            status: step.status === "running" ? "running" : "complete",
            startedAt: step.startedAt ? new Date(step.startedAt).getTime() : undefined,
            completedAt: step.completedAt ? new Date(step.completedAt).getTime() : undefined,
            durationMs: step.durationMs,
            result: step.result,
            error: step.error,
          });
        }
      }

      if (d.reasoning_content) {
        assistantParts.push({ type: "reasoning", text: d.reasoning_content });
      }

      if (d.article) {
        assistantParts.push({ type: "text", text: d.article });
      }

      if (d.review) {
        assistantParts.push({ type: "data", dataType: "review", data: d.review });
      }

      const dIntent = (d.token_usage as { intent?: string })?.intent;
      const isArticle = d.article && dIntent !== "chat" && (d.article_title || dIntent);
      if (isArticle) {
        assistantParts.push({ type: "data", dataType: "feedback", data: { article: d.article!, has_feedback: d.has_feedback } });
      }

      if (d.error) {
        assistantParts.push({ type: "text", text: `❌ 错误：${d.error}` });
      }

      if (assistantParts.length > 0) {
        messages.push({
          id: genMsgId(),
          role: "assistant",
          parts: assistantParts,
          createdAt: new Date(d.created_at).getTime() + 100,
          status: d.status === "failed" ? "error" : "complete",
          articleTitle: d.article_title,
        });
      }

      set((state) => ({
        sessions: state.sessions.map((s) =>
          s.traceId === traceId
            ? {
                ...s,
                title: d.custom_title || d.article_title || d.task_name || d.user_input?.slice(0, 30) || s.title,
                customTitle: d.custom_title || s.customTitle || null,
                messages,
                status: (d.status === "completed" ? "completed" :
                        d.status === "failed" ? "error" :
                        d.status === "running" ? "running" :
                        d.status === "paused" ? "paused" : "idle") as WritingSession["status"],
                style: d.style_slug || s.style,
                mode: d.mode || s.mode,
                intent: dIntent || s.intent || null,
              }
            : s
        ),
      }));

      // Restore editorial workflow state
      if (d.mode === "editorial") {
        const wfStore = useWorkflowStore.getState();
        wfStore.reset();
        wfStore.setUserInput(d.user_input || "");
        wfStore.setTaskId(traceId);
        if (d.article) {
          useWorkflowStore.setState({
            finalArticle: {
              title: d.article_title || "",
              content: d.article,
              word_count: d.article.length,
            },
            finalArticleLoading: false,
            runStatus: "completed",
          });
        } else {
          useWorkflowStore.setState({
            finalArticle: null,
            finalArticleLoading: false,
            runStatus: "completed",
          });
        }
        wfStore.loadPlan(traceId);
      }
    } catch (e) {
      console.error("Failed to load session detail:", e);
    }

    get().loadSessionArtifacts(traceId);
  },

  loadSessionArtifacts: async (traceId) => {
    try {
      const token = useAuthStore.getState().token;
      const res = await fetch(`/api/v2/sessions/${traceId}/artifacts`, {
        headers: token ? { Authorization: `Bearer ${token}` } : {},
      });
      const json = await res.json();
      if (!json.success || !json.data) return;

      const artifacts = (json.data.artifacts || []) as WritingArtifact[];
      set((state) => ({
        sessions: state.sessions.map((s) =>
          s.traceId === traceId ? { ...s, artifacts } : s
        ),
      }));
    } catch (e) {
      console.error("Failed to load session artifacts:", e);
    }
  },

  // ═══════════════════════════════════════════════════════
  // Writing Control Actions
  // ═══════════════════════════════════════════════════════

  startWriting: async (payload) => {
    const state = get();
    let sessionId = state.activeSessionId;

    if (!sessionId) {
      sessionId = state.createSession();
    }

    const userMessage: ChatMessage = {
      id: genMsgId(),
      role: "user",
      parts: [{ type: "text", text: payload.message }],
      createdAt: Date.now(),
    };

    const assistantMessage: ChatMessage = {
      id: genMsgId(),
      role: "assistant",
      parts: [],
      createdAt: Date.now(),
      status: "running",
    };

    set((s) => ({
      sessions: s.sessions.map((sess) =>
        sess.id === sessionId
          ? {
              ...sess,
              title: payload.message.slice(0, 30) || "新写作",
              style: payload.style || sess.style,
              mode: payload.mode || sess.mode,
              status: "running",
              messages: [...sess.messages, userMessage, assistantMessage],
              injectedMaterials: payload.material_refs?.map((m) => m.title || m.material_id) ?? payload.user_materials,
            }
          : sess
      ),
      streamingText: "",
    }));

    try {
      // REST chain: document → contract → confirm → compile → run
      const { run_id } = await startWritingRun(payload);

      // Link run to session
      set((s) => ({
        activeRunId: run_id,
        sessions: s.sessions.map((sess) =>
          sess.id === sessionId ? { ...sess, traceId: run_id } : sess
        ),
      }));
    } catch (error) {
      const msg = error instanceof Error ? error.message : "写作服务暂不可用";
      set((s) => ({
        sessions: s.sessions.map((sess) =>
          sess.id === sessionId
            ? {
                ...sess,
                status: "idle",
                messages: sess.messages.map((m) =>
                  m.status === "running"
                    ? { ...m, status: "error" as const, parts: [{ type: "text" as const, text: `❌ ${msg}` }] }
                    : m
                ),
              }
            : sess
        ),
      }));
    }
  },

  pauseWriting: async () => {
    const runId = get().activeRunId;
    if (!runId) return;
    try {
      await get().controlRun(runId, "pause");
    } catch { /* ignore */ }
  },

  resumeWriting: async () => {
    const runId = get().activeRunId;
    if (!runId) return;
    try {
      await get().controlRun(runId, "resume");
    } catch { /* ignore */ }
  },

  cancelWriting: async () => {
    const runId = get().activeRunId;
    if (runId) {
      try {
        await get().controlRun(runId, "cancel");
      } catch { /* ignore */ }
    }
    get()._updateActiveSession((s) => ({ ...s, status: "idle", awaitInputAt: null }));
  },

  confirmOutline: async (data) => {
    const runId = get().activeRunId;
    if (!runId) return;
    // In governed runtime, outline confirmation goes through the gate system
    // For now, just clear the await state; the gate decision API will be used
    // when the run reaches a human gate node
    get()._updateActiveSession((s) => ({ ...s, awaitInputAt: null }));
  },

  regenerateOutline: async () => {
    const runId = get().activeRunId;
    if (!runId) return;
    get()._updateActiveSession((s) => ({ ...s, status: "running", awaitInputAt: null }));
  },

  resumeSession: async (traceId) => {
    // In governed runtime, resume goes through REST controlRun
    try {
      await get().controlRun(traceId, "resume");
    } catch { /* ignore */ }
  },

  markFeedbackSubmitted: (traceId) => {
    set((state) => ({
      sessions: state.sessions.map((s) => {
        if (s.traceId !== traceId) return s;
        const messages = s.messages.map((m) => {
          if (m.role !== "assistant") return m;
          const parts = m.parts.map((part) => {
            if (part.type === "data" && part.dataType === "feedback") {
              const data = part.data as { article?: string; has_feedback?: boolean };
              return { ...part, data: { ...data, has_feedback: true } };
            }
            return part;
          });
          return { ...m, parts };
        });
        return { ...s, messages };
      }),
    }));
  },

  renameSession: async (traceId, title) => {
    const trimmed = title.trim();
    if (!trimmed) return false;
    try {
      const res = await fetch(`/api/v2/sessions/${traceId}/title`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ title: trimmed }),
      });
      const json = await res.json();
      if (!json.success) return false;
      set((state) => ({
        sessions: state.sessions.map((s) =>
          s.traceId === traceId ? { ...s, customTitle: trimmed, title: trimmed } : s
        ),
      }));
      return true;
    } catch {
      return false;
    }
  },

  saveArticleEdit: async (traceId, article, articleTitle, versionNote) => {
    try {
      const res = await fetch(`/api/v2/sessions/${traceId}/article`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          article,
          article_title: articleTitle,
          version_note: versionNote,
        }),
      });
      const json = await res.json();
      if (json.success) {
        set((state) => ({
          sessions: state.sessions.map((s) => {
            if (s.traceId !== traceId) return s;
            const messages = s.messages.map((m) => {
              if (m.role !== "assistant") return m;
              const parts = m.parts.map((part) => {
                if (part.type === "text") {
                  return { ...part, text: article };
                }
                return part;
              });
              return { ...m, parts };
            });
            return {
              ...s,
              messages,
              articleTitle: articleTitle ?? s.articleTitle,
            };
          }),
        }));
        return true;
      }
      return false;
    } catch (e) {
      console.error("Failed to save article edit:", e);
      return false;
    }
  },

  loadArticleVersion: async (traceId, versionId) => {
    try {
      const res = await fetch(`/api/v2/sessions/${traceId}/versions/${versionId}`);
      const json = await res.json();
      if (json.success && json.data?.article) {
        const article = json.data.article as string;
        const articleTitle = json.data.article_title as string | undefined;
        set((state) => ({
          sessions: state.sessions.map((s) => {
            if (s.traceId !== traceId) return s;
            const messages = s.messages.map((m) => {
              if (m.role !== "assistant") return m;
              const parts = m.parts.map((part) => {
                if (part.type === "text") {
                  return { ...part, text: article };
                }
                return part;
              });
              return { ...m, parts, articleTitle: articleTitle ?? m.articleTitle };
            });
            return {
              ...s,
              messages,
              articleTitle: articleTitle ?? s.articleTitle,
            };
          }),
        }));
        return true;
      }
      return false;
    } catch (e) {
      console.error("Failed to load article version:", e);
      return false;
    }
  },

  // ═══════════════════════════════════════════════════════
  // WebSocket (temporary, for workflow/editorial page)
  // ═══════════════════════════════════════════════════════

  connectWS: () => {
    const { ws } = get();
    if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) return;

    const token = useAuthStore.getState().token;
    const baseUrl = `${window.location.protocol === "https:" ? "wss:" : "ws:"}//${window.location.host}/api/v2/ws/agent`;
    const wsUrl = token ? `${baseUrl}?token=${encodeURIComponent(token)}` : baseUrl;
    const socket = new WebSocket(wsUrl);

    socket.onopen = () => {
      set({ wsConnected: true });
      const session = get()._getActiveSession();
      if (session?.traceId) {
        get().resumeSession(session.traceId);
      }
    };

    socket.onclose = () => {
      set({ wsConnected: false, ws: null });
    };

    socket.onmessage = (event) => {
      try {
        const msg = JSON.parse(event.data) as { type: string; payload: Record<string, unknown> };
        get().handleServerMessage(msg);
      } catch (e) {
        console.error("Failed to parse WS message:", e);
      }
    };

    socket.onerror = (e) => {
      console.error("WebSocket error:", e);
    };

    set({ ws: socket });
  },

  sendWS: (type, payload) => {
    const { ws } = get();
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({ type, payload }));
    }
  },

  handleServerMessage: (msg) => {
    const { type, payload } = msg;
    const p = payload;

    switch (type) {
      case "agent.created": {
        const traceId = p.trace_id as string;
        get()._updateActiveSession((s) => ({
          ...s,
          traceId,
          conversationId: s.conversationId || traceId,
          status: "running",
        }));
        break;
      }

      case "agent.step.start": {
        const step = p.step as AgentStepName;
        const part: ToolCallPart = {
          type: "tool-call",
          toolName: step,
          status: "running",
          startedAt: Date.now(),
        };
        get()._updateLastAssistantMessage((m) => {
          const parts = [...m.parts];
          for (let i = 0; i < parts.length; i++) {
            if (parts[i].type === "reasoning") {
              parts[i] = { ...(parts[i] as ReasoningPart), completed: true };
            }
          }
          parts.push(part);
          return { ...m, parts };
        });
        break;
      }

      case "agent.step.complete": {
        const step = p.step as AgentStepName;
        const result = p.result as Record<string, unknown> | undefined;
        const durationMs = p.duration_ms as number | undefined;
        const isDegraded = result?.degraded === true;
        get()._updateLastAssistantMessage((m) => ({
          ...m,
          parts: m.parts.map((part, i) => {
            if (
              part.type === "tool-call" &&
              part.toolName === step &&
              part.status === "running"
            ) {
              const isLast = m.parts.slice(i + 1).every((p2) => p2.type !== "tool-call" || p2.toolName !== step || p2.status !== "running");
              if (isLast) {
                return {
                  ...part,
                  status: (isDegraded ? "degraded" : "complete") as AgentStepStatus,
                  result,
                  durationMs,
                  completedAt: Date.now(),
                  error: isDegraded ? (result?.error as string) : undefined,
                };
              }
            }
            return part;
          }),
        }));
        break;
      }

      case "agent.stream": {
        const delta = p.delta as string;
        set((s) => ({ streamingText: s.streamingText + delta }));
        get()._updateLastAssistantMessage((m) => {
          const parts = [...m.parts];
          let lastTextIdx = -1;
          for (let i = parts.length - 1; i >= 0; i--) {
            const part = parts[i];
            if (part.type === "text" && (part as TextPart).streaming) {
              lastTextIdx = i;
              break;
            }
          }
          if (lastTextIdx >= 0) {
            const textPart = parts[lastTextIdx] as TextPart;
            parts[lastTextIdx] = { ...textPart, text: textPart.text + delta };
          } else {
            parts.push({ type: "text", text: delta, streaming: true });
          }
          return { ...m, parts };
        });
        break;
      }

      case "agent.stream.reset": {
        set({ streamingText: "" });
        get()._updateLastAssistantMessage((m) => {
          const parts = [...m.parts];
          const filtered = parts.filter(
            (part) => part.type !== "text"
          );
          return { ...m, parts: filtered };
        });
        break;
      }

      case "agent.reasoning": {
        const delta = p.delta as string;
        get()._updateLastAssistantMessage((m) => {
          const parts = [...m.parts];
          let lastReasoningIdx = -1;
          for (let i = parts.length - 1; i >= 0; i--) {
            if (parts[i].type === "reasoning") {
              lastReasoningIdx = i;
              break;
            }
          }
          if (lastReasoningIdx >= 0) {
            const reasoningPart = parts[lastReasoningIdx] as ReasoningPart;
            if (reasoningPart.completed) {
              parts.push({ type: "reasoning", text: delta });
            } else {
              parts[lastReasoningIdx] = { ...reasoningPart, text: reasoningPart.text + delta };
            }
          } else {
            parts.push({ type: "reasoning", text: delta });
          }
          return { ...m, parts };
        });
        break;
      }

      case "agent.stream.done": {
        const fullText = p.full_text as string | undefined;
        get()._updateLastAssistantMessage((m) => ({
          ...m,
          parts: m.parts.map((part) => {
            if (part.type === "text" && part.streaming) {
              return { ...part, streaming: false, text: fullText ?? part.text };
            }
            return part;
          }),
        }));
        break;
      }

      case "agent.article_title": {
        const title = p.title as string;
        if (title) {
          get()._updateLastAssistantMessage((m) => ({
            ...m,
            articleTitle: title,
          }));
        }
        break;
      }

      case "agent.await_input": {
        const step = p.step as string;
        const data = p.data;
        if (step === "outline") {
          get()._updateLastAssistantMessage((m) => {
            const filteredParts = m.parts.filter(
              (part) => !(part.type === "data" && (part as DataPart).dataType === "outline")
            );
            return {
              ...m,
              parts: [
                ...filteredParts,
                {
                  type: "data",
                  dataType: "outline" as const,
                  data: data ?? (p as unknown),
                  attempt: p.attempt as number | undefined,
                  maxAttempts: p.max_attempts as number | undefined,
                },
              ],
            };
          });
        }
        get()._updateActiveSession((s) => ({ ...s, status: "running", awaitInputAt: Date.now() }));
        break;
      }

      case "agent.paused": {
        const reason = p.reason as string | undefined;
        if (reason === "disconnect") {
          get()._updateLastAssistantMessage((m) => ({
            ...m,
            parts: [
              ...m.parts.map((part) =>
                part.type === "text" && part.streaming ? { ...part, streaming: false } : part
              ),
              { type: "text", text: "📡 连接已断开，重连后可继续写作" },
            ],
          }));
        }
        get()._updateActiveSession((s) => ({ ...s, status: "paused" }));
        break;
      }

      case "agent.resumed": {
        get()._updateActiveSession((s) => ({ ...s, status: "running" }));
        break;
      }

      case "agent.completed": {
        const article = p.article as string;
        const articleTitle = p.article_title as string | undefined;
        const review = p.review;
        const intent = (p.token_usage as { intent?: string })?.intent;
        const tokenUsage = p.token_usage;
        const pointsUsed = p.points_used as number | undefined;
        const result: AgentResult = { article, review: review as AgentResult["review"], token_usage: tokenUsage as AgentResult["token_usage"], points_used: pointsUsed };

        const editorialTraceId = p.trace_id as string | undefined;
        if (editorialTraceId && !get()._getActiveSession()) {
          const userInput = useWorkflowStore.getState().userInput || articleTitle || "工作台模式写作";
          const newSession: WritingSession = {
            id: editorialTraceId,
            title: articleTitle || userInput.slice(0, 30) || "工作台模式写作",
            messages: [
              { id: genMsgId(), role: "user", parts: [{ type: "text", text: userInput }], createdAt: Date.now() - 1000 },
              { id: genMsgId(), role: "assistant", parts: [], createdAt: Date.now(), status: "running" },
            ],
            traceId: editorialTraceId,
            conversationId: editorialTraceId,
            status: "completed",
            style: useSettingsStore.getState().lastStyle || "yinyue",
            mode: "editorial",
            createdAt: Date.now(),
            folderId: null,
            archived: false,
            awaitInputAt: null,
            kbEnabled: true,
            articleTitle: articleTitle || null,
          };
          set((state) => ({
            sessions: [newSession, ...state.sessions.filter((s) => s.traceId !== editorialTraceId)],
            activeSessionId: editorialTraceId,
          }));
        }

        get()._updateLastAssistantMessage((m) => {
          const nonTextParts = m.parts.filter((part) => part.type !== "text");
          const finalParts: MessagePart[] = [];
          if (article) {
            finalParts.push({ type: "text", text: article, streaming: false });
          } else {
            for (const part of m.parts) {
              if (part.type === "text") {
                finalParts.push({ ...part, streaming: false });
              }
            }
          }
          const parts = [...nonTextParts, ...finalParts];
          if (review) {
            parts.push({ type: "data", dataType: "review" as const, data: result.review });
          }
          if (article && intent !== "chat") {
            parts.push({ type: "data", dataType: "feedback" as const, data: { article } });
          }
          return { ...m, parts, status: "complete" as const, articleTitle: articleTitle || m.articleTitle, pointsUsed };
        });

        get()._updateActiveSession((s) => ({
          ...s,
          status: "completed",
          title: articleTitle || s.title,
          articleTitle: articleTitle || s.articleTitle,
          intent: intent ?? s.intent ?? null,
        }));

        if (pointsUsed && pointsUsed > 0) {
          useBillingStore.getState().loadBalance();
        }
        break;
      }

      case "agent.error": {
        const errorMsg = p.message as string;
        const errorCode = p.code as string;

        const ERROR_MESSAGES: Record<string, string> = {
          timeout: "⏱️ 写作超时，请简化选题后重试",
          budget_exceeded: "💰 Token 预算已用尽，请稍后重试",
          circuit_breaker: "🔌 AI 服务暂时不可用，请稍后重试",
          quota_exceeded: "💳 AI 模型服务额度不足，请联系管理员充值后重试",
          insufficient_balance: "积分余额不足，请充值后继续使用",
          server_busy: "🔧 服务器繁忙，请稍后重试",
          step_failed: `❌ 步骤执行失败：${errorMsg}`,
          panic: `❌ 内部错误：${errorMsg}`,
        };
        let friendlyMsg = ERROR_MESSAGES[errorCode] ?? `❌ 错误：${errorMsg}`;

        if (errorCode === "concurrent_limit") {
          const limit = p.limit as number | undefined;
          const active = p.active_count as number | undefined;
          if (limit != null && active != null) {
            friendlyMsg = `⏳ 当前已有 ${active} 个写作任务进行中（最多同时 ${limit} 个），请等待完成或取消后再试`;
          } else {
            friendlyMsg = "⏳ 已有写作任务进行中，请等待完成或取消后再试";
          }
        }

        if (errorCode === "guest_limit_reached") {
          const token = useAuthStore.getState().token;
          useAuthModal.getState().openAuth({
            guestToken: token ?? undefined,
            defaultTab: "register",
          });
        }

        get()._updateLastAssistantMessage((m) => ({
          ...m,
          status: "error" as const,
          parts: [
            ...m.parts.map((part) =>
              part.type === "text" && part.streaming ? { ...part, streaming: false } : part
            ),
            { type: "text", text: friendlyMsg },
          ],
        }));
        get()._updateActiveSession((s) => ({ ...s, status: "error" }));
        break;
      }

      case "agent.cancelled": {
        get()._updateActiveSession((s) => ({ ...s, status: "idle" }));
        get()._updateLastAssistantMessage((m) => ({
          ...m,
          status: "complete" as const,
          parts: m.parts.map((part) =>
            part.type === "text" && part.streaming ? { ...part, streaming: false } : part
          ),
        }));
        break;
      }

      case "agent.compaction": {
        const originalMessages = p.original_messages as number;
        const compactedMessages = (p.compacted_messages as number) ?? 1;
        const savedTokens = p.saved_tokens as number;
        const summaryPreview = p.summary_preview as string | undefined;
        const historyVersion = p.history_version as number | undefined;
        const triggerReason = p.trigger_reason as string | undefined;
        get()._updateLastAssistantMessage((m) => ({
          ...m,
          parts: [
            ...m.parts.filter((part) => part.type !== "compaction"),
            {
              type: "compaction" as const,
              originalMessages,
              compactedMessages,
              savedTokens,
              summaryPreview,
              historyVersion,
              triggerReason,
            },
          ],
        }));
        break;
      }

      case "task_name.updated": {
        const traceId = p.trace_id as string;
        const taskName = p.task_name as string;
        if (!traceId || !taskName) break;
        set((state) => ({
          sessions: state.sessions.map((s) => {
            if (s.traceId !== traceId) return s;
            if (s.articleTitle) return s;
            return { ...s, title: taskName };
          }),
        }));
        break;
      }

      case "session.resumed": {
        const traceId = p.trace_id as string;
        const status = p.status as string;
        const article = p.article as string | undefined;
        const articleTitle = p.article_title as string | undefined;
        const taskName = p.task_name as string | undefined;
        const outline = p.outline;
        const review = p.review;
        const stepHistory = p.step_history as Array<Record<string, unknown>> | undefined;
        const reasoningContent = p.reasoning_content as string | undefined;
        const conversationId = p.conversation_id as string | undefined;
        const userInput = p.user_input as string | undefined;
        const style = p.style as string | undefined;
        const mode = p.mode as string | undefined;

        if (status === "not_found") {
          console.warn("Session resume failed:", p.message);
          get()._updateActiveSession((s) => ({
            ...s,
            traceId: null,
            conversationId: null,
            status: "idle",
          }));
          get()._updateLastAssistantMessage((m) => ({
            ...m,
            parts: m.parts.map((part) =>
              part.type === "text" && part.streaming ? { ...part, streaming: false } : part
            ),
          }));
          break;
        }

        get()._updateActiveSession((s) => ({
          ...s,
          traceId,
          conversationId: conversationId || s.conversationId || traceId,
          status: status as WritingSession["status"],
          style: style || s.style,
          mode: mode || s.mode,
          articleTitle: articleTitle || s.articleTitle,
          title: articleTitle || taskName || s.title,
        }));

        const assistantParts: MessagePart[] = [];

        if (stepHistory && Array.isArray(stepHistory)) {
          for (const step of stepHistory) {
            assistantParts.push({
              type: "tool-call",
              toolName: step.step as AgentStepName,
              status: (step.status === "running" ? "running" : "complete") as AgentStepStatus,
              startedAt: step.startedAt ? new Date(step.started_at as string).getTime() : undefined,
              completedAt: step.completedAt ? new Date(step.completed_at as string).getTime() : undefined,
              durationMs: step.duration_ms as number | undefined,
              result: step.result,
              error: step.error as string | undefined,
            });
          }
        }

        if (reasoningContent) {
          assistantParts.push({ type: "reasoning", text: reasoningContent });
        }

        if (article) {
          assistantParts.push({ type: "text", text: article, streaming: status === "running" });
        }

        if (outline) {
          assistantParts.push({ type: "data", dataType: "outline" as const, data: outline });
        }

        if (review) {
          assistantParts.push({ type: "data", dataType: "review" as const, data: review });
        }

        if (article && (articleTitle || taskName)) {
          assistantParts.push({ type: "data", dataType: "feedback" as const, data: { article } });
        }

        get()._updateLastAssistantMessage((m) => ({
          ...m,
          articleTitle: articleTitle || m.articleTitle,
          parts: assistantParts.length > 0 ? assistantParts : m.parts,
        }));

        if (userInput) {
          get()._updateActiveSession((s) => {
            const hasUserMsg = s.messages.some((m) => m.role === "user" && m.parts.some((p) => p.type === "text" && p.text === userInput));
            if (hasUserMsg) return s;
            const userMsg: ChatMessage = {
              id: `msg-user-${traceId}`,
              role: "user",
              parts: [{ type: "text", text: userInput }],
              createdAt: Date.now(),
            };
            return { ...s, messages: [userMsg, ...s.messages] };
          });
        }

        break;
      }

      case "memory.used": {
        const memCtx = p as unknown as { injected: unknown[]; review_guard: unknown[]; dismissed: string[] };
        useMemoryStore.getState().setContext({
          injected: (memCtx.injected as MemoryEntry[]) ?? [],
          review_guard: (memCtx.review_guard as MemoryEntry[]) ?? [],
          dismissed: memCtx.dismissed ?? [],
        });
        break;
      }

      case "editorial.event": {
        const evt = p as unknown as { type: string; task_id: string; payload: Record<string, unknown>; timestamp: string };
        useEditorialStore.getState().pushEvent(evt);
        break;
      }

      // Workflow DAG messages (forwarded to workflow-store)
      case "workflow.created":
      case "workflow.started":
      case "workflow.completed":
      case "workflow.failed":
      case "workflow.paused":
      case "workflow.resumed":
      case "node.started":
      case "node.stream.delta":
      case "node.stream.reset":
      case "node.reasoning.delta":
      case "node.step_start":
      case "node.step_complete":
      case "node.completed":
      case "node.failed":
      case "node.error": {
        const ws = useWorkflowStore.getState();
        const payload = p;

        switch (type) {
          case "workflow.created":
            ws.setPlan({
              agents: (payload.agents as WFAgentConfig[]) || [],
              workflow: (payload.workflow as WFWorkflowSpec) || ({} as WFWorkflowSpec),
              rationale: (payload.rationale as string) || "",
            });
            if (payload.task_id) {
              ws.setTaskId(payload.task_id as string);
            }
            break;
          case "workflow.started":
            ws.setRunStatus("running");
            break;
          case "node.started":
            ws.setNodeStarted(payload.node_id as string, payload.agent_name as string);
            break;
          case "node.stream.delta":
            ws.appendNodeStream(payload.node_id as string, payload.delta as string);
            break;
          case "node.stream.reset":
            ws.resetNodeStream(payload.node_id as string);
            break;
          case "node.error":
            console.warn(`[DAG] Node error: ${payload.node_id}`, payload.message);
            break;
          case "node.completed":
            ws.setNodeCompleted(
              payload.node_id as string,
              payload.artifact_id as string,
              payload.artifact_type as string,
              payload.tokens_used as number,
              payload.duration_ms as number
            );
            break;
          case "node.failed":
            ws.setNodeFailed(
              payload.node_id as string,
              payload.error as string,
              payload.duration_ms as number
            );
            break;
          case "workflow.completed":
            ws.setWorkflowCompleted(payload.total_tokens as number);
            break;
          case "workflow.failed":
            ws.setWorkflowFailed(payload.error as string);
            break;
        }
        break;
      }

      case "ping": {
        break;
      }

      default: {
        if (import.meta.env.DEV) {
          console.debug("[WS] unknown message type:", type);
        }
        break;
      }
    }
  },

  // ═══════════════════════════════════════════════════════
  // Internal Helpers
  // ═══════════════════════════════════════════════════════

  _getActiveSession: () => {
    const { sessions, activeSessionId } = get();
    return sessions.find((s) => s.id === activeSessionId) ?? null;
  },

  _updateActiveSession: (updater) => {
    set((state) => ({
      sessions: state.sessions.map((s) =>
        s.id === state.activeSessionId ? updater(s) : s
      ),
    }));
  },

  _updateLastAssistantMessage: (updater) => {
    get()._updateActiveSession((s) => {
      const messages = [...s.messages];
      for (let i = messages.length - 1; i >= 0; i--) {
        if (messages[i].role === "assistant") {
          messages[i] = updater(messages[i]);
          break;
        }
      }
      return { ...s, messages };
    });
  },

  _restoreEditorialWorkflow: (traceId, session) => {
    const wfStore = useWorkflowStore.getState();
    wfStore.reset();
    wfStore.setUserInput(session.title || "");
    wfStore.setTaskId(traceId);
    const assistantMsg = session.messages.find((m) => m.role === "assistant");
    const textPart = assistantMsg?.parts.find((p) => p.type === "text");
    if (textPart && textPart.type === "text") {
      useWorkflowStore.setState({
        finalArticle: {
          title: session.articleTitle || "",
          content: textPart.text,
          word_count: textPart.text.length,
        },
        finalArticleLoading: false,
        runStatus: "completed",
      });
    } else {
      useWorkflowStore.setState({
        finalArticle: null,
        finalArticleLoading: false,
        runStatus: "completed",
      });
    }
    wfStore.loadPlan(traceId);
  },
}));

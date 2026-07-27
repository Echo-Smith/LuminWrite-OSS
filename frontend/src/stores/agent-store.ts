/**
 * Agent Store — 基于 Zustand 的全局写作状态管理
 *
 * 管理写作会话的消息流，将 WebSocket 事件映射为 UI 消息。
 * 每个"消息"包含多个 content parts（text / tool-call / data），
 * 对齐 assistantUI 的消息模型。
 */
import { create } from "zustand";
import type {
  AgentStepName,
  AgentStepStatus,
  AgentStartPayload,
  AgentResult,
  OutlineData,
  SearchResult,
  WSServerMessage,
} from "@/lib/types";
import { useAuthStore } from "@/stores/auth-store";
import type { MemoryEntry } from "@/stores/memory-store";

// ─── 消息 Part 模型 ──────────────────────────────────────

export type MessagePartType = "text" | "tool-call" | "data" | "reasoning";

export interface ToolCallPart {
  type: "tool-call";
  toolName: AgentStepName;
  status: AgentStepStatus;
  args?: Record<string, unknown>;
  result?: unknown;
  startedAt?: number;
  completedAt?: number;
  durationMs?: number;
  error?: string;
}

export interface TextPart {
  type: "text";
  text: string;
  streaming?: boolean;
}

export interface DataPart {
  type: "data";
  dataType: "outline" | "review" | "search_results" | "feedback";
  data: unknown;
  attempt?: number;
  maxAttempts?: number;
}

export interface ReasoningPart {
  type: "reasoning";
  text: string;
}

export type MessagePart = ToolCallPart | TextPart | DataPart | ReasoningPart;

// ─── 消息模型 ────────────────────────────────────────────

export type MessageRole = "user" | "assistant" | "system";

export interface ChatMessage {
  id: string;
  role: MessageRole;
  parts: MessagePart[];
  createdAt: number;
  status?: "running" | "complete" | "error";
  articleTitle?: string;
}

// ─── 会话模型 ────────────────────────────────────────────

export interface WritingSession {
  id: string;
  title: string;
  messages: ChatMessage[];
  traceId: string | null;
  status: "idle" | "running" | "paused" | "completed" | "error";
  style: string;
  mode: string;
  createdAt: number;
  awaitInputAt: number | null; // timestamp when await_input was received (for timeout countdown)
}

// ─── Store 定义 ──────────────────────────────────────────

interface AgentStore {
  sessions: WritingSession[];
  activeSessionId: string | null;
  sessionsLoaded: boolean;

  // WebSocket 连接
  ws: WebSocket | null;
  wsConnected: boolean;

  // 当前写作状态
  streamingText: string;

  // Actions
  createSession: () => string;
  switchSession: (id: string) => void;
  deleteSession: (id: string) => void;
  loadSessions: () => Promise<void>;
  loadSessionDetail: (traceId: string) => Promise<void>;

  connectWS: () => void;
  sendWS: (type: string, payload: Record<string, unknown>) => void;
  startWriting: (payload: AgentStartPayload) => void;
  pauseWriting: () => void;
  resumeWriting: () => void;
  cancelWriting: () => void;
  confirmOutline: (data: OutlineData | null) => void;
  regenerateOutline: () => void;

  handleServerMessage: (msg: WSServerMessage) => void;
  resumeSession: (traceId: string) => void;
  markFeedbackSubmitted: (traceId: string) => void;

  // 内部 helpers
  _getActiveSession: () => WritingSession | null;
  _updateActiveSession: (updater: (s: WritingSession) => WritingSession) => void;
  _updateLastAssistantMessage: (updater: (m: ChatMessage) => ChatMessage) => void;
}

function genId(): string {
  return `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
}

let msgIdCounter = 0;
function genMsgId(): string {
  return `msg-${++msgIdCounter}`;
}

export const useAgentStore = create<AgentStore>((set, get) => ({
  sessions: [],
  activeSessionId: null,
  sessionsLoaded: false,
  ws: null,
  wsConnected: false,
  streamingText: "",

  createSession: () => {
    const id = genId();
    const session: WritingSession = {
      id,
      title: "新写作",
      messages: [],
      traceId: null,
      status: "idle",
      style: "yinyue",
      mode: "auto",
      createdAt: Date.now(),
      awaitInputAt: null,
    };
    set((state) => ({
      sessions: [session, ...state.sessions],
      activeSessionId: id,
    }));
    return id;
  },

  switchSession: (id) => {
    set({ activeSessionId: id });
    // 延迟加载会话详情
    const session = get().sessions.find((s) => s.id === id);
    if (session?.traceId && session.messages.length === 0) {
      get().loadSessionDetail(session.traceId);
    }
  },

  deleteSession: (id) => {
    // 先从本地 state 移除（即时反馈）
    const session = get().sessions.find((s) => s.id === id);
    set((state) => {
      const remaining = state.sessions.filter((s) => s.id !== id);
      return {
        sessions: remaining,
        activeSessionId: state.activeSessionId === id ? remaining[0]?.id ?? null : state.activeSessionId,
      };
    });
    // 如果有 traceId，调后端 soft-delete（否则刷新后会重新出现）
    if (session?.traceId) {
      const token = useAuthStore.getState().token;
      fetch(`/api/v2/sessions/${session.traceId}`, {
        method: "DELETE",
        headers: token ? { Authorization: `Bearer ${token}` } : {},
      }).catch((e) => console.error("Failed to delete session on server:", e));
    }
  },

  // ─── 从数据库加载会话列表 ────────────────────────────────
  loadSessions: async () => {
    if (get().sessionsLoaded) return;

    try {
      const res = await fetch("/api/v2/sessions?page=1&page_size=50");
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
        completed_at?: string;
        duration_ms?: number;
      }>;

      // 将 DB 记录转换为 WritingSession（仅列表摘要，不加载完整消息）
      const dbSessionsMapped: WritingSession[] = dbSessions.map((t) => ({
        id: t.trace_id,
        title: t.user_input?.slice(0, 30) || "历史会话",
        messages: [], // 延迟加载，点击时通过 loadSessionDetail 获取
        traceId: t.trace_id,
        status: (t.status === "completed" ? "completed" :
                t.status === "failed" ? "error" :
                t.status === "running" ? "running" : "idle") as WritingSession["status"],
        style: t.style_slug || "yinyue",
        mode: t.mode || "auto",
        createdAt: new Date(t.created_at).getTime(),
        awaitInputAt: null,
      }));

      // 合并 DB 会话与本地已有会话（保留本地进行中的会话，避免丢失）
      set((state) => {
        const dbTraceIds = new Set(dbSessionsMapped.map((s) => s.traceId));
        const localOnly = state.sessions.filter(
          (s) => s.traceId && !dbTraceIds.has(s.traceId)
        );
        // 本地无 traceId 的临时会话也保留
        const localTemp = state.sessions.filter((s) => !s.traceId);
        return {
          sessions: [...localTemp, ...localOnly, ...dbSessionsMapped],
          sessionsLoaded: true,
        };
      });
    } catch (e) {
      console.error("Failed to load sessions from DB:", e);
    }
  },

  // ─── 从数据库加载单个会话详情 ────────────────────────────
  loadSessionDetail: async (traceId) => {
    // 先检查是否已加载过消息
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
step_history?: Array<{ step: string; status: string; startedAt?: string; completedAt?: string; durationMs?: number; result?: unknown; error?: string }>;
review?: unknown;
reasoning_content?: string;
created_at: string;
completed_at?: string;
error?: string;
has_feedback?: boolean;
};

      // 重建消息列表
      const messages: ChatMessage[] = [];

      // 用户消息
      if (d.user_input) {
        messages.push({
          id: genMsgId(),
          role: "user",
          parts: [{ type: "text", text: d.user_input }],
          createdAt: new Date(d.created_at).getTime(),
        });
      }

      // 助手消息（从 step_history + article 重建）
      const assistantParts: MessagePart[] = [];

      // 从 step_history 重建 tool-call parts
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

      // 思考过程（从数据库恢复）
      if (d.reasoning_content) {
        assistantParts.push({ type: "reasoning", text: d.reasoning_content });
      }

      // 文章内容
      if (d.article) {
        assistantParts.push({ type: "text", text: d.article });
      }

      // 评价结果
      if (d.review) {
        assistantParts.push({ type: "data", dataType: "review", data: d.review });
        assistantParts.push({ type: "data", dataType: "feedback", data: { article: d.article, has_feedback: d.has_feedback } });
      }

      // 错误信息
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

      // 更新对应会话
      set((state) => ({
        sessions: state.sessions.map((s) =>
          s.traceId === traceId
            ? {
                ...s,
                messages,
                status: (d.status === "completed" ? "completed" :
                        d.status === "failed" ? "error" :
                        d.status === "running" ? "running" : "idle") as WritingSession["status"],
                style: d.style_slug || s.style,
                mode: d.mode || s.mode,
              }
            : s
        ),
      }));
    } catch (e) {
      console.error("Failed to load session detail:", e);
    }
  },

  connectWS: () => {
    const { ws } = get();
    if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) return;

    // Get auth token for WS authentication
    const token = useAuthStore.getState().token;

    // Use same-origin WebSocket URL so that the Vite dev proxy (ws: true)
    // or the Nginx reverse proxy handles forwarding to the backend.
    const baseUrl = `${window.location.protocol === "https:" ? "wss:" : "ws:"}//${window.location.host}/api/v2/ws/agent`;
    const wsUrl = token ? `${baseUrl}?token=${encodeURIComponent(token)}` : baseUrl;
    const socket = new WebSocket(wsUrl);

    socket.onopen = () => {
      set({ wsConnected: true });
      // If we have an active session with a trace ID, try to resume it
      const session = get()._getActiveSession();
      if (session?.traceId) {
        get().resumeSession(session.traceId);
      }
    };

    socket.onclose = () => {
      set({ wsConnected: false, ws: null });
      // 自动重连（3秒后）
      setTimeout(() => {
        if (get().activeSessionId) get().connectWS();
      }, 3000);
    };

    socket.onmessage = (event) => {
      try {
        const msg = JSON.parse(event.data) as WSServerMessage;
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

  startWriting: (payload) => {
    const state = get();
    let sessionId = state.activeSessionId;

    // 如果没有活跃会话，创建一个
    if (!sessionId) {
      sessionId = state.createSession();
    }

    // 添加用户消息
    const userMessage: ChatMessage = {
      id: genMsgId(),
      role: "user",
      parts: [{ type: "text", text: payload.message }],
      createdAt: Date.now(),
    };

    // 预创建 assistant 消息（等待 agent.created 后填充）
    const assistantMessage: ChatMessage = {
      id: genMsgId(),
      role: "assistant",
      parts: [],
      createdAt: Date.now(),
      status: "running",
    };

    // 更新会话
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
            }
          : sess
      ),
      streamingText: "",
    }));

    // 连接 WebSocket 并发送 start
    get().connectWS();

    // 等待连接后发送
    const { ws } = get();
    if (ws && ws.readyState === WebSocket.OPEN) {
      get().sendWS("agent.start", payload as unknown as Record<string, unknown>);
    } else {
      // 等待连接
      const interval = setInterval(() => {
        const current = get().ws;
        if (current && current.readyState === WebSocket.OPEN) {
          clearInterval(interval);
          get().sendWS("agent.start", payload as unknown as Record<string, unknown>);
        }
      }, 100);
      // 10秒超时
      setTimeout(() => clearInterval(interval), 10000);
    }
  },

  pauseWriting: () => {
    const session = get()._getActiveSession();
    if (session?.traceId) {
      get().sendWS("agent.pause", { trace_id: session.traceId });
    }
  },

  resumeWriting: () => {
    const session = get()._getActiveSession();
    if (session?.traceId) {
      get().sendWS("agent.resume", { trace_id: session.traceId });
    }
  },

  cancelWriting: () => {
    const session = get()._getActiveSession();
    if (session?.traceId) {
      get().sendWS("agent.cancel", { trace_id: session.traceId });
    }
    get()._updateActiveSession((s) => ({ ...s, status: "idle" }));
  },

  confirmOutline: (data) => {
    const session = get()._getActiveSession();
    if (session?.traceId) {
      get().sendWS("agent.confirm", {
        trace_id: session.traceId,
        step: "outline",
        data,
      });
    }
  },

  regenerateOutline: () => {
    const session = get()._getActiveSession();
    if (session?.traceId) {
      get().sendWS("agent.confirm", {
        trace_id: session.traceId,
        step: "outline",
        data: { action: "regenerate" },
      });
      // Mark session as running while the outline is being regenerated
      get()._updateActiveSession((s) => ({ ...s, status: "running" }));
    }
  },

  handleServerMessage: (msg) => {
    const { type, payload } = msg;
    const p = payload as Record<string, unknown>;

    switch (type) {
      case "agent.created": {
        const traceId = p.trace_id as string;
        get()._updateActiveSession((s) => ({ ...s, traceId, status: "running" }));
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
        get()._updateLastAssistantMessage((m) => ({
          ...m,
          parts: [...m.parts, part],
        }));
        break;
      }

      case "agent.step.complete": {
        const step = p.step as AgentStepName;
        const result = p.result as Record<string, unknown> | undefined;
        const durationMs = p.duration_ms as number | undefined;
        // Check if the step was degraded (non-critical step failed and was skipped)
        const isDegraded = result?.degraded === true;
        get()._updateLastAssistantMessage((m) => ({
          ...m,
          parts: m.parts.map((part, i) => {
            // 找到最后一个匹配的 tool-call part 并更新
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

        // 更新最后一条 assistant 消息的 text part
        get()._updateLastAssistantMessage((m) => {
          const parts = [...m.parts];
          // 查找最后一个 streaming text part
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
            // 创建新的 streaming text part
            parts.push({ type: "text", text: delta, streaming: true });
          }
          return { ...m, parts };
        });
        break;
      }

      case "agent.stream.reset": {
        // 后端检测到中间 tool-call 轮次产生了被乐观推送的 content，
        // 通知前端丢弃所有已流式输出的 text part 内容。
        set({ streamingText: "" });
        get()._updateLastAssistantMessage((m) => {
          const parts = [...m.parts];
          // 移除所有 streaming text parts
          const filtered = parts.filter(
            (part) => !(part.type === "text" && (part as TextPart).streaming)
          );
          return { ...m, parts: filtered };
        });
        break;
      }

      case "agent.reasoning": {
        const delta = p.delta as string;
        // 更新最后一条 assistant 消息的 reasoning part
        get()._updateLastAssistantMessage((m) => {
          const parts = [...m.parts];
          // 查找最后一个 reasoning part
          let lastReasoningIdx = -1;
          for (let i = parts.length - 1; i >= 0; i--) {
            if (parts[i].type === "reasoning") {
              lastReasoningIdx = i;
              break;
            }
          }
          if (lastReasoningIdx >= 0) {
            const reasoningPart = parts[lastReasoningIdx] as ReasoningPart;
            parts[lastReasoningIdx] = { ...reasoningPart, text: reasoningPart.text + delta };
          } else {
            // 创建新的 reasoning part
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
            // Remove any previous outline data parts before adding the new one
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
        get()._updateActiveSession((s) => ({ ...s, status: "paused", awaitInputAt: Date.now() }));
        break;
      }

case "agent.paused": {
  // Check if this is a disconnect-induced pause
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
        const tokenUsage = p.token_usage;
        const result: AgentResult = { article, review: review as AgentResult["review"], token_usage: tokenUsage as AgentResult["token_usage"] };

        get()._updateLastAssistantMessage((m) => {
          // 确保最后的 text part 不再 streaming
          const parts = m.parts.map((part) => {
            if (part.type === "text" && part.streaming) {
              return { ...part, streaming: false };
            }
            return part;
          });
          // 添加 review data part（仅写作模式有评分）
          if (review) {
            parts.push({ type: "data", dataType: "review" as const, data: result.review });
            // 添加 feedback data part（触发 FeedbackBar 渲染）
            // 仅当有评分时才显示文章评价，chat 模式不显示
            if (article) {
              parts.push({ type: "data", dataType: "feedback" as const, data: { article } });
            }
          }
          return { ...m, parts, status: "complete" as const, articleTitle: articleTitle || m.articleTitle };
        });

        get()._updateActiveSession((s) => ({ ...s, status: "completed" }));
        break;
      }

      case "agent.error": {
        const errorMsg = p.message as string;
        const errorCode = p.code as string;

        // ── Friendly error messages for exit mechanism codes ──
        const ERROR_MESSAGES: Record<string, string> = {
          timeout: "⏱️ 写作超时，请简化选题后重试",
          budget_exceeded: "💰 Token 预算已用尽，请稍后重试",
          circuit_breaker: "🔌 AI 服务暂时不可用，请稍后重试",
          concurrent_limit: "⏳ 已有写作任务进行中，请等待完成或取消后再试",
          server_busy: "🔧 服务器繁忙，请稍后重试",
          step_failed: `❌ 步骤执行失败：${errorMsg}`,
          panic: `❌ 内部错误：${errorMsg}`,
        };
        const friendlyMsg = ERROR_MESSAGES[errorCode] ?? `❌ 错误：${errorMsg}`;

        // Guest limit reached — auto-open register modal
        if (errorCode === "guest_limit_reached") {
          // Use dynamic import to avoid circular dependency
          import("./auth-modal-store").then(({ useAuthModal }) => {
            const token = useAuthStore.getState().token;
            useAuthModal.getState().openAuth({
              guestToken: token ?? undefined,
              defaultTab: "register",
            });
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

      case "session.resumed": {
        const traceId = p.trace_id as string;
        const status = p.status as string;
        const article = p.article as string | undefined;
        const outline = p.outline;
        const review = p.review;

        if (status === "not_found") {
          console.warn("Session resume failed:", p.message);
          // Clear the stale trace ID and reset session status so user can start fresh
          get()._updateActiveSession((s) => ({
            ...s,
            traceId: null,
            status: "idle",
          }));
          // Stop any streaming indicator
          get()._updateLastAssistantMessage((m) => ({
            ...m,
            parts: m.parts.map((part) =>
              part.type === "text" && part.streaming ? { ...part, streaming: false } : part
            ),
          }));
          break;
        }

        // Restore the session state
        get()._updateActiveSession((s) => ({
          ...s,
          traceId,
          status: status as WritingSession["status"],
        }));

        // If there's a partial article, restore it as a text part
        if (article) {
          const articleTitle = p.article_title as string | undefined;
          get()._updateLastAssistantMessage((m) => ({
            ...m,
            articleTitle: articleTitle || m.articleTitle,
            parts: [
              ...m.parts.filter((part) => part.type !== "text"),
              { type: "text", text: article, streaming: status === "running" },
            ],
          }));
        }

        // If there's an outline awaiting confirmation
        if (outline) {
          get()._updateLastAssistantMessage((m) => ({
            ...m,
            parts: [
              ...m.parts.filter((part) => !(part.type === "data" && (part as DataPart).dataType === "outline")),
              { type: "data", dataType: "outline" as const, data: outline },
            ],
          }));
        }

        // If there's a review result
        if (review) {
          get()._updateLastAssistantMessage((m) => ({
            ...m,
            parts: [
              ...m.parts.filter((part) => !(part.type === "data" && (part as DataPart).dataType === "review")),
              { type: "data", dataType: "review" as const, data: review },
            ],
          }));
        }

        break;
      }

      case "memory.used": {
        // Store the memory context for this writing session
        const memCtx = p as unknown as { injected: unknown[]; review_guard: unknown[]; dismissed: string[] };
        import("./memory-store").then(({ useMemoryStore }) => {
          useMemoryStore.getState().setContext({
            injected: (memCtx.injected as MemoryEntry[]) ?? [],
            review_guard: (memCtx.review_guard as MemoryEntry[]) ?? [],
            dismissed: memCtx.dismissed ?? [],
          });
        });
        break;
      }

      case "editorial.event": {
        // Forward editorial events to the editorial store for auto-refresh
        const evt = p as unknown as { type: string; task_id: string; payload: Record<string, unknown>; timestamp: string };
        import("./editorial-store").then(({ useEditorialStore }) => {
          useEditorialStore.getState().pushEvent(evt);
        });
        break;
      }
    }
  },

  resumeSession: (traceId) => {
    get().sendWS("session.resume", { trace_id: traceId });
  },

  markFeedbackSubmitted: (traceId) => {
    // Update the feedback data part's has_feedback flag for the session
    // matching this traceId, so switching sessions preserves the "thank you" state.
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
      // 从后往前找最后一条 assistant 消息
      for (let i = messages.length - 1; i >= 0; i--) {
        if (messages[i].role === "assistant") {
          messages[i] = updater(messages[i]);
          break;
        }
      }
      return { ...s, messages };
    });
  },
}));

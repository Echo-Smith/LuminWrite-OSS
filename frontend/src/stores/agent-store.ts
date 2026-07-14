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

  // WebSocket 连接
  ws: WebSocket | null;
  wsConnected: boolean;

  // 当前写作状态
  streamingText: string;

  // Actions
  createSession: () => string;
  switchSession: (id: string) => void;
  deleteSession: (id: string) => void;

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
  },

  deleteSession: (id) => {
    set((state) => {
      const remaining = state.sessions.filter((s) => s.id !== id);
      return {
        sessions: remaining,
        activeSessionId: state.activeSessionId === id ? remaining[0]?.id ?? null : state.activeSessionId,
      };
    });
  },

  connectWS: () => {
    const { ws } = get();
    if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) return;

    // Get auth token for WS authentication
    const token = useAuthStore.getState().token;

    // In dev mode, connect directly to backend to avoid Vite WS proxy issues
    const isDev = import.meta.env.DEV;
    const baseUrl = isDev
      ? `ws://localhost:8080/api/v2/ws/agent`
      : `${window.location.protocol === "https:" ? "wss:" : "ws:"}//${window.location.host}/api/v2/ws/agent`;
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
        const result = p.result;
        const durationMs = p.duration_ms as number | undefined;
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
                  status: "complete" as AgentStepStatus,
                  result,
                  durationMs,
                  completedAt: Date.now(),
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
        get()._updateActiveSession((s) => ({ ...s, status: "paused" }));
        break;
      }

      case "agent.resumed": {
        get()._updateActiveSession((s) => ({ ...s, status: "running" }));
        break;
      }

      case "agent.completed": {
        const article = p.article as string;
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
          // 添加 review data part
          if (review) {
            parts.push({ type: "data", dataType: "review" as const, data: result.review });
          }
          // 添加 feedback data part（触发 FeedbackBar 渲染）
          if (article) {
            parts.push({ type: "data", dataType: "feedback" as const, data: { article } });
          }
          return { ...m, parts, status: "complete" as const };
        });

        get()._updateActiveSession((s) => ({ ...s, status: "completed" }));
        break;
      }

      case "agent.error": {
        const errorMsg = p.message as string;
        const errorCode = p.code as string;

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
            { type: "text", text: `❌ 错误：${errorMsg}` },
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
          get()._updateLastAssistantMessage((m) => ({
            ...m,
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
    }
  },

  resumeSession: (traceId) => {
    get().sendWS("session.resume", { trace_id: traceId });
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

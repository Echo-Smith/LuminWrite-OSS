/**
 * ─── 风格助手会话状态 ───────────────────────────────────────
 *
 * 对话式风格助手的 store。链路复用后端已有的 style-builder 会话
 * （POST /api/v2/style-builder/sessions[/id/messages|/commit]），
 * 前端在响应到达后以打字机节奏渐进 reveal，配合 Lumi 动画呈现
 * 流式感；phase 字段直接驱动助手形象（idle/thinking/writing/done）。
 */
import { create } from "zustand";
import { useAuthStore } from "@/stores/auth-store";
import { useStyleListStore } from "@/stores/style-list-store";

export type StyleChatPhase = "idle" | "thinking" | "writing" | "done";

export interface StyleChatMessage {
  id: string;
  role: "user" | "assistant";
  content: string;
  createdAt: number;
  /** assistant 消息是否仍在 reveal 中 */
  revealing?: boolean;
}

export interface StyleChatProfileInfo {
  name: string | null;
  description: string | null;
}

const GREETING =
  "你好，我是 Lumi，你的风格助手。聊聊你想要的写作风格吧——比如「冷静克制、短句多、不堆形容词」，或者把一段你喜欢的文字发给我，我来帮你提炼成可复用的风格。";

let nextId = 0;
const genId = () => `style-chat-${Date.now()}-${nextId++}`;

interface StyleChatState {
  open: boolean;
  sessionId: string | null;
  messages: StyleChatMessage[];
  phase: StyleChatPhase;
  /** AI 认为风格信息完整，可以保存 */
  ready: boolean;
  profile: StyleChatProfileInfo | null;
  /** ready 后未保存且对话框关闭时点亮 composer 入口角标 */
  unappliedReady: boolean;
  /** 底部输入行内容（受控在 store，便于跨开关保留草稿） */
  input: string;
  error: string | null;
  setOpen: (open: boolean) => void;
  setInput: (input: string) => void;
  reset: () => void;
  send: (text: string) => Promise<void>;
  commit: () => Promise<boolean>;
}

/** reveal 期间把完整回复切成小块渐进追加（确定性节奏，标点后停顿更长） */
function chunkText(text: string): Array<{ text: string; delayMs: number }> {
  const chunks: Array<{ text: string; delayMs: number }> = [];
  let i = 0;
  while (i < text.length) {
    const size = 2 + (i % 3);
    const piece = text.slice(i, i + size);
    const pause = /[。！？.!?，,；;：:\n]/.test(piece) ? 90 : 22;
    chunks.push({ text: piece, delayMs: pause });
    i += size;
  }
  return chunks;
}

/** 从尾部找最后一个平衡 JSON 对象的起点（后端 stripTrailingJSON 同款算法） */
function trailingJSONObjectStart(text: string): number {
  let depth = 0;
  let inString = false;
  for (let i = text.length - 1; i >= 0; i--) {
    const ch = text[i];
    if (ch === '"' && (i === 0 || text[i - 1] !== "\\")) inString = !inString;
    if (inString) continue;
    if (ch === "}") depth++;
    else if (ch === "{") {
      depth--;
      if (depth === 0) return i;
    }
  }
  return -1;
}

/**
 * 对话流展示净化：Lumi 气泡只呈现对话散文。后端为兼容 my-styles 页的
 * 可折叠 JSON 卡片仍返回含配置 JSON 的完整回复（style_builder.go 注释所
 * 述），这里在展示层剥掉它——markdown 围栏包裹的 JSON、裸 JSON 对象、
 * 残缺的孤立围栏，以及常见 markdown 修饰标记，都不得出现在打字机 reveal 中。
 */
function sanitizeReplyForChat(text: string): string {
  let out = text;
  // 1) 成对代码围栏整体剥除（任何语言标签；对话气泡不展示代码块）
  out = out.replace(/```[a-zA-Z]*\s*\n?[\s\S]*?\n?```/g, "");
  // 2) 尾部裸 JSON：仅当 } 是全文最后一个非空白字符时剥（配置直出形态）；
  //    解析出的对象须像风格配置，防止误删对话里的普通 JSON 举例。
  const trimmedEnd = out.replace(/\s+$/, "");
  if (trimmedEnd.endsWith("}")) {
    const start = trailingJSONObjectStart(trimmedEnd);
    if (start >= 0) {
      const body = trimmedEnd.slice(start);
      try {
        const parsed = JSON.parse(body) as Record<string, unknown>;
        if (parsed.slug !== undefined || parsed.name !== undefined || parsed.system_prompt !== undefined) {
          out = trimmedEnd.slice(0, start);
        }
      } catch {
        // 不是合法 JSON，保留原文
      }
    }
  }
  // 3) 残缺尾部：孤立的 ``` 或 ```json 开栏（LLM 截断时常见）
  out = out.replace(/```(?:json)?\s*$/g, "");
  // 4) markdown 修饰转纯文本：气泡是纯文本渲染，宁可丢样式也不露符号
  out = out
    .replace(/^#{1,6}\s+/gm, "") // 标题井号
    .replace(/(\*\*|__)(.*?)\1/g, "$2") // 加粗
    .replace(/`([^`\n]+)`/g, "$1"); // 行内代码
  return out.replace(/\n{3,}/g, "\n\n").trim();
}

export const useStyleChatStore = create<StyleChatState>((set, get) => ({
  open: false,
  sessionId: null,
  messages: [{ id: genId(), role: "assistant", content: GREETING, createdAt: Date.now() }],
  phase: "idle",
  ready: false,
  profile: null,
  unappliedReady: false,
  input: "",
  error: null,

  setOpen: (open) => {
    set({ open, ...(open ? { unappliedReady: false, error: null } : {}) });
  },

  setInput: (input) => set({ input }),

  reset: () =>
    set({
      sessionId: null,
      messages: [{ id: genId(), role: "assistant", content: GREETING, createdAt: Date.now() }],
      phase: "idle",
      ready: false,
      profile: null,
      unappliedReady: false,
      input: "",
      error: null,
    }),

  send: async (text) => {
    const trimmed = text.trim();
    const state = get();
    if (!trimmed || state.phase === "thinking" || state.phase === "writing") return;

    const token = useAuthStore.getState().token;
    set((s) => ({
      input: "",
      error: null,
      phase: "thinking",
      messages: [...s.messages, { id: genId(), role: "user", content: trimmed, createdAt: Date.now() }],
    }));

    try {
      // 确保会话存在（首次发消息时懒创建）
      let sessionId = get().sessionId;
      if (!sessionId) {
        const res = await fetch("/api/v2/style-builder/sessions", {
          method: "POST",
          headers: { Authorization: `Bearer ${token}` },
        });
        const json = await res.json();
        if (!json.success) throw new Error(json.error?.message ?? "创建会话失败");
        sessionId = json.data.session_id as string;
        set({ sessionId });
      }

      const res = await fetch(`/api/v2/style-builder/sessions/${sessionId}/messages`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${token}`,
        },
        body: JSON.stringify({ message: trimmed }),
      });
      const json = await res.json();
      if (!json.success) throw new Error(json.error?.message ?? "请求失败");

      const ready = Boolean(json.data.ready);
      const profile = (json.data.profile ?? null) as Record<string, unknown> | null;
      // 展示层剥 JSON/markdown；模型只直出配置 JSON 时退化为一句对话文本
      const reply =
        sanitizeReplyForChat((json.data.message as string) ?? "") ||
        (ready ? "风格已经提炼好了，可以直接保存。" : "");
      const profileInfo: StyleChatProfileInfo | null = profile
        ? {
            name: (profile.name as string) ?? null,
            description: (profile.description as string) ?? null,
          }
        : null;

      // 进入 writing：先占位一条 reveal 中的 assistant 消息。
      // ready/profile 延迟到 reveal 完成再置位，保存卡等回复完整显示后出现。
      const msgId = genId();
      set((s) => ({
        phase: "writing",
        messages: [...s.messages, { id: msgId, role: "assistant", content: "", createdAt: Date.now(), revealing: true }],
      }));

      // 打字机 reveal；期间再次 send 会被 phase 守卫拦下
      const chunks = chunkText(reply);
      for (const chunk of chunks) {
        await new Promise((r) => setTimeout(r, chunk.delayMs));
        // 对话框可能已被 reset（关开会话丢弃），此时停止写入
        if (!get().messages.some((m) => m.id === msgId)) return;
        set((s) => ({
          messages: s.messages.map((m) => (m.id === msgId ? { ...m, content: m.content + chunk.text } : m)),
        }));
      }
      set((s) => ({
        messages: s.messages.map((m) => (m.id === msgId ? { ...m, revealing: false } : m)),
        ready,
        profile: profileInfo,
        phase: ready ? "done" : "idle",
        unappliedReady: ready && !get().open ? true : get().unappliedReady,
      }));
    } catch (e) {
      const message = e instanceof Error ? e.message : "网络错误，请重试";
      set((s) => ({
        phase: "idle",
        error: message,
        messages: [...s.messages, { id: genId(), role: "assistant", content: `抱歉，出了点问题：${message}`, createdAt: Date.now() }],
      }));
    }
  },

  commit: async () => {
    const { sessionId } = get();
    if (!sessionId) return false;
    const token = useAuthStore.getState().token;
    try {
      const res = await fetch(`/api/v2/style-builder/sessions/${sessionId}/commit`, {
        method: "POST",
        headers: { Authorization: `Bearer ${token}` },
      });
      const json = await res.json();
      if (!json.success) throw new Error(json.error?.message ?? "保存失败");
      set({ ready: false, profile: null, unappliedReady: false });
      // 通知已挂载的风格列表（composer 选择器等）重新拉取
      useStyleListStore.getState().requestRefresh();
      const msgId = genId();
      set((s) => ({ messages: [...s.messages, { id: msgId, role: "assistant", content: "已保存到「我的风格」，下次写作时可以直接选用。", createdAt: Date.now(), revealing: true }] }));
      // 短 reveal 后收尾
      set((s) => ({ messages: s.messages.map((m) => (m.id === msgId ? { ...m, revealing: false } : m)), phase: "idle" }));
      return true;
    } catch (e) {
      const message = e instanceof Error ? e.message : "网络错误，请重试";
      set({ error: message });
      return false;
    }
  },
}));

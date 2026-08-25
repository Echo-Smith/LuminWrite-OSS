/**
 * Settings Store — 用户偏好设置（云端同步）
 *
 * 管理用户偏好，通过后端 API /preferences 持久化到数据库。
 * 写作功能本身依赖在线 WebSocket，因此设置跟随用户账号而非本地设备。
 *
 * 目前管理：
 * - agentMode: "harness" | "pipeline" | "editorial" — 编排模式选择
 * - enableEditorial: boolean — 是否在侧栏显示工作台入口（实验功能）
 * - lastStyle: string — 上次写作使用的风格 slug（新建会话时作为默认值）
 *
 * 注意：本 store 不 import auth-store，避免循环依赖。
 * token 从 localStorage 直接读取（与 auth-store 的 STORAGE_KEY 一致）。
 */
import { create } from "zustand";

export type AgentMode = "harness" | "pipeline" | "editorial";

const AUTH_STORAGE_KEY = "luminbuddy_auth";

/** 从 localStorage 读取 token（不依赖 auth-store） */
function getToken(): string | null {
  try {
    const raw = localStorage.getItem(AUTH_STORAGE_KEY);
    if (!raw) return null;
    const stored = JSON.parse(raw);
    return stored.token ?? null;
  } catch {
    return null;
  }
}

interface SettingsState {
  agentMode: AgentMode;
  enableEditorial: boolean;  // 是否显示工作台入口（实验功能）
  lastStyle: string;        // 上次写作使用的风格 slug
  loaded: boolean;          // 是否已从后端加载
  setAgentMode: (mode: AgentMode) => void;
  setEnableEditorial: (enabled: boolean) => void;
  setLastStyle: (style: string) => void;
  loadFromServer: () => Promise<void>;
  syncToServer: (prefs: Record<string, unknown>) => Promise<void>;
}

export const useSettingsStore = create<SettingsState>((set, get) => ({
  agentMode: "harness",
  enableEditorial: false,
  lastStyle: "yinyue",
  loaded: false,

  setAgentMode: (mode) => {
    set({ agentMode: mode });
    // Fire-and-forget sync to server
    get().syncToServer({ agent_mode: mode });
  },

  setEnableEditorial: (enabled) => {
    set({ enableEditorial: enabled });
    get().syncToServer({ enable_editorial: enabled });
  },

  setLastStyle: (style) => {
    set({ lastStyle: style });
    get().syncToServer({ last_style: style });
  },

  loadFromServer: async () => {
    if (get().loaded) return;
    const token = getToken();
    try {
      const res = await fetch("/api/v2/preferences", {
        headers: token ? { Authorization: `Bearer ${token}` } : {},
      });
      const json = await res.json();
      if (json.success && json.data) {
        const mode = json.data.agent_mode as AgentMode | undefined;
        if (mode === "harness" || mode === "pipeline" || mode === "editorial") {
          set({ agentMode: mode });
        }
        const enableEditorial = json.data.enable_editorial;
        if (typeof enableEditorial === "boolean") {
          set({ enableEditorial });
        }
        const lastStyle = json.data.last_style;
        if (typeof lastStyle === "string" && lastStyle) {
          set({ lastStyle });
        }
      }
    } catch {
      // Network error — keep default
    } finally {
      set({ loaded: true });
    }
  },

  // Internal: push preferences to server
  syncToServer: async (prefs: Record<string, unknown>) => {
    const token = getToken();
    if (!token) return;
    try {
      await fetch("/api/v2/preferences", {
        method: "PUT",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${token}`,
        },
        body: JSON.stringify(prefs),
      });
    } catch {
      // Silent fail — not critical
    }
  },
}));

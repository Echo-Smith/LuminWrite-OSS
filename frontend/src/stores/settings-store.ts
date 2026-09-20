/**
 * Settings Store — 用户偏好设置（云端同步）
 *
 * 管理用户偏好，通过后端 API /preferences 持久化到数据库。
 * 写作功能本身依赖在线 WebSocket，因此设置跟随用户账号而非本地设备。
 *
 * 目前管理：
 * - agentMode: "harness" | "pipeline" | "editorial" — 编排模式选择
 * - enableEditorial: boolean — 是否在侧栏显示工作台入口（实验功能）
 * - enableResearchReview: boolean — 是否开启研究综述写作路径（实验功能，
 *   用户在实验室功能勾选后 composer 出现「研究综述」mode）
 * - enablePaperMode: boolean — 稿纸模式（A4 纸面视觉）。默认关闭：
 *   暂停该特性，正文以普通流式文档呈现；用户可在实验室功能重新打开。
 *   加载时对遗留的 enable_paper_mode=true 做一次性重置（以
 *   paper_mode_pause_reset 标记防止覆盖用户之后的主动开启）
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
  enableResearchReview: boolean;  // 是否开启研究综述路径（实验功能）
  enablePaperMode: boolean;  // 稿纸模式（A4 纸面视觉），默认关闭
  lastStyle: string;        // 上次写作使用的风格 slug
  loaded: boolean;          // 是否已从后端加载
  setAgentMode: (mode: AgentMode) => void;
  setEnableEditorial: (enabled: boolean) => void;
  setEnableResearchReview: (enabled: boolean) => void;
  setEnablePaperMode: (enabled: boolean) => void;
  setLastStyle: (style: string) => void;
  loadFromServer: () => Promise<void>;
  syncToServer: (prefs: Record<string, unknown>) => Promise<void>;
}

export const useSettingsStore = create<SettingsState>((set, get) => ({
  agentMode: "harness",
  enableEditorial: false,
  enableResearchReview: false,
  enablePaperMode: false,
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

  setEnableResearchReview: (enabled) => {
    set({ enableResearchReview: enabled });
    get().syncToServer({ enable_research_review: enabled });
  },

  setEnablePaperMode: (enabled) => {
    set({ enablePaperMode: enabled });
    get().syncToServer({ enable_paper_mode: enabled });
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
        const enableResearchReview = json.data.enable_research_review;
        if (typeof enableResearchReview === "boolean") {
          set({ enableResearchReview });
        }
        const enablePaperMode = json.data.enable_paper_mode;
        // 稿纸模式已暂停并改为默认关闭：账号里遗留的开启值只重置一次（打标记），
        // 之后用户在实验室功能的重新开启照常云端持久化。
        if (enablePaperMode === true && json.data.paper_mode_pause_reset !== true) {
          set({ enablePaperMode: false });
          void get().syncToServer({ enable_paper_mode: false, paper_mode_pause_reset: true });
        } else if (typeof enablePaperMode === "boolean") {
          set({ enablePaperMode: enablePaperMode });
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

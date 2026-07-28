/**
 * Settings Store — 用户偏好设置（云端同步）
 *
 * 管理用户偏好，通过后端 API /preferences 持久化到数据库。
 * 写作功能本身依赖在线 WebSocket，因此设置跟随用户账号而非本地设备。
 *
 * 目前管理：
 * - agentMode: "unified" | "pipeline" — 编排模式选择
 */
import { create } from "zustand";
import { useAuthStore } from "@/stores/auth-store";

export type AgentMode = "unified" | "pipeline";

interface SettingsState {
  agentMode: AgentMode;
  loaded: boolean;          // 是否已从后端加载
  setAgentMode: (mode: AgentMode) => void;
  loadFromServer: () => Promise<void>;
  syncToServer: (prefs: Record<string, unknown>) => Promise<void>;
}

export const useSettingsStore = create<SettingsState>((set, get) => ({
  agentMode: "unified",
  loaded: false,

  setAgentMode: (mode) => {
    set({ agentMode: mode });
    // Fire-and-forget sync to server
    get().syncToServer({ agent_mode: mode });
  },

  loadFromServer: async () => {
    if (get().loaded) return;
    const token = useAuthStore.getState().token;
    try {
      const res = await fetch("/api/v2/preferences", {
        headers: token ? { Authorization: `Bearer ${token}` } : {},
      });
      const json = await res.json();
      if (json.success && json.data) {
        const mode = json.data.agent_mode as AgentMode | undefined;
        if (mode === "unified" || mode === "pipeline") {
          set({ agentMode: mode });
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
    const token = useAuthStore.getState().token;
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

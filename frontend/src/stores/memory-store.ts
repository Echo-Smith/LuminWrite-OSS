/**
 * Memory Store — 用户记忆管理
 *
 * 管理用户记忆列表、memory.used 事件状态、dismiss 操作
 */
import { create } from "zustand";
import { useAuthStore } from "@/stores/auth-store";

// ─── 辅助：获取认证 header ───────────────────────────────────

function authHeaders(): Record<string, string> {
  const token = useAuthStore.getState().token;
  return token ? { Authorization: `Bearer ${token}` } : {};
}

// ─── 类型定义 ──────────────────────────────────────────────

export interface MemoryEntry {
  id: string;
  tier: "hard" | "pattern" | "feedback";
  category: string;
  value: string;
  confidence: number;
  dismissible: boolean;
}

export interface MemoryContext {
  injected: MemoryEntry[];
  review_guard: MemoryEntry[];
  dismissed: string[];
}

export interface UserMemory {
  id: string;
  user_id: string;
  tier: "hard" | "pattern" | "feedback";
  category: string;
  key: string;
  value: string;
  confidence: number;
  occurrences: number;
  status: "candidate" | "active" | "superseded" | "dismissed" | "archived";
  quality_source?: string;
  first_seen: string;
  last_seen: string;
  created_at: string;
  updated_at: string;
}

// ─── Store ─────────────────────────────────────────────────

interface MemoryState {
  // 本次写作的记忆上下文
  currentContext: MemoryContext | null;

  // 设置页记忆列表
  memories: UserMemory[];
  loading: boolean;

  // Actions
  setContext: (ctx: MemoryContext | null) => void;
  clearContext: () => void;
  fetchMemories: () => Promise<void>;
  createMemory: (category: string, key: string, value: string) => Promise<boolean>;
  deleteMemory: (id: string) => Promise<boolean>;
  dismissMemory: (memoryId: string, sessionId: string) => Promise<boolean>;
}

const API_BASE = "/api/v2";

export const useMemoryStore = create<MemoryState>((set, get) => ({
  currentContext: null,
  memories: [],
  loading: false,

  setContext: (ctx) => set({ currentContext: ctx }),
  clearContext: () => set({ currentContext: null }),

  fetchMemories: async () => {
    set({ loading: true });
    try {
      const res = await fetch(`${API_BASE}/memories?limit=100`, {
        headers: authHeaders(),
      });
      if (!res.ok) return;
      const json = await res.json();
      if (json.success) {
        set({ memories: json.data?.memories ?? [] });
      }
    } catch {
      // silent fail
    } finally {
      set({ loading: false });
    }
  },

  createMemory: async (category, key, value) => {
    try {
      const res = await fetch(`${API_BASE}/memories`, {
        method: "POST",
        headers: { "Content-Type": "application/json", ...authHeaders() },
        body: JSON.stringify({ category, key, value }),
      });
      if (!res.ok) return false;
      const json = await res.json();
      if (json.success) {
        get().fetchMemories();
        return true;
      }
      return false;
    } catch {
      return false;
    }
  },

  deleteMemory: async (id) => {
    try {
      const res = await fetch(`${API_BASE}/memories/${id}`, {
        method: "DELETE",
        headers: authHeaders(),
      });
      if (!res.ok) return false;
      const json = await res.json();
      if (json.success) {
        set((s) => ({ memories: s.memories.filter((m) => m.id !== id) }));
        return true;
      }
      return false;
    } catch {
      return false;
    }
  },

  dismissMemory: async (memoryId, sessionId) => {
    try {
      const res = await fetch(`${API_BASE}/memories/${memoryId}/dismiss`, {
        method: "POST",
        headers: { "Content-Type": "application/json", ...authHeaders() },
        body: JSON.stringify({ session_id: sessionId }),
      });
      if (!res.ok) return false;
      const json = await res.json();
      return json.success === true;
    } catch {
      return false;
    }
  },
}));

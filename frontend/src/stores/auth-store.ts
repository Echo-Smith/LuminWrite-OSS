/**
 * Auth Store — 登录态持久化与全局认证管理
 *
 * 基于 Zustand + localStorage 实现：
 * - token 存储与自动恢复
 * - 登录/登出
 * - token 自动刷新（过期前 5 分钟）
 * - 用户信息管理
 */
import { create } from "zustand";

// ─── 类型定义 ──────────────────────────────────────────────

export interface AuthUser {
  userId: string;
  role: "guest" | "user" | "admin";
}

/**
 * 结构化认证结果
 * - ok=true: 操作成功
 * - ok=false: 操作失败，code 为后端错误码，message 为可展示文案
 */
export type AuthResult =
  | { ok: true }
  | { ok: false; code: string; message: string };

interface AuthState {
  token: string | null;
  user: AuthUser | null;
  expiresAt: number | null; // unix seconds
  initialized: boolean;

  // Actions
  init: () => Promise<void>;
  login: (token: string, userId: string, role: string, expiresIn: number) => void;
  logout: () => void;
  refreshToken: () => Promise<boolean>;
  isAuthenticated: () => boolean;
  isGuest: () => boolean;
  getAuthHeaders: () => Record<string, string>;
}

// ─── 存储键 ────────────────────────────────────────────────

const STORAGE_KEY = "luminbuddy_auth";

interface StoredAuth {
  token: string;
  userId: string;
  role: string;
  expiresAt: number;
}

// ─── 工具函数 ──────────────────────────────────────────────

function loadFromStorage(): { token: string; user: AuthUser; expiresAt: number } | null {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return null;

    const stored = JSON.parse(raw) as StoredAuth;
    if (!stored.token || !stored.expiresAt) return null;

    // 检查是否过期
    const now = Math.floor(Date.now() / 1000);
    if (now >= stored.expiresAt) {
      localStorage.removeItem(STORAGE_KEY);
      return null;
    }

    return {
      token: stored.token,
      user: { userId: stored.userId, role: stored.role as "user" | "admin" },
      expiresAt: stored.expiresAt,
    };
  } catch {
    return null;
  }
}

function saveToStorage(token: string, userId: string, role: string, expiresAt: number) {
  const data: StoredAuth = { token, userId, role, expiresAt };
  localStorage.setItem(STORAGE_KEY, JSON.stringify(data));
}

function clearStorage() {
  localStorage.removeItem(STORAGE_KEY);
}

// ─── API 调用 ──────────────────────────────────────────────

async function callRefreshAPI(currentToken: string): Promise<{
  token: string;
  user_id: string;
  role: string;
  expires_in: number;
} | null> {
  try {
    const res = await fetch("/api/v2/auth/refresh", {
      method: "POST",
      headers: {
        "Authorization": `Bearer ${currentToken}`,
        "Content-Type": "application/json",
      },
    });
    if (!res.ok) return null;
    const json = await res.json();
    if (!json.success || !json.data?.token) return null;
    return json.data;
  } catch {
    return null;
  }
}

async function callLoginAPI(body: Record<string, unknown>): Promise<{
  data: { token: string; user_id: string; role: string; expires_in: number };
} | { error: { code: string; message: string } }> {
  try {
    const res = await fetch("/api/v2/auth/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    const json = await res.json();
    if (!res.ok || !json.success) {
      const code = json.error?.code || "login_failed";
      const message = json.error?.message || "登录失败，请重试";
      return { error: { code, message } };
    }
    return { data: json.data };
  } catch {
    return { error: { code: "network_error", message: "网络错误，请检查连接" } };
  }
}

async function callGuestAPI(): Promise<{
  token: string;
  user_id: string;
  role: string;
  expires_in: number;
} | null> {
  try {
    const res = await fetch("/api/v2/auth/guest", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
    });
    if (!res.ok) return null;
    const json = await res.json();
    if (!json.success || !json.data?.token) return null;
    return json.data;
  } catch {
    return null;
  }
}

async function callRegisterAPI(body: Record<string, unknown>): Promise<{
  data: { token: string; user_id: string; role: string; expires_in: number };
} | { error: { code: string; message: string } }> {
  try {
    const res = await fetch("/api/v2/auth/register", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    const json = await res.json();
    if (!res.ok || !json.success) {
      const code = json.error?.code || "register_failed";
      const message = json.error?.message || "注册失败，请重试";
      return { error: { code, message } };
    }
    return { data: json.data };
  } catch {
    return { error: { code: "network_error", message: "网络错误，请检查连接" } };
  }
}

// ─── Store ─────────────────────────────────────────────────

export const useAuthStore = create<AuthState>((set, get) => ({
  token: null,
  user: null,
  expiresAt: null,
  initialized: false,

  init: async () => {
    if (get().initialized) return;

    const stored = loadFromStorage();
    if (stored) {
      set({
        token: stored.token,
        user: stored.user,
        expiresAt: stored.expiresAt,
        initialized: true,
      });

      // 如果 token 即将过期（5 分钟内），尝试刷新
      const now = Math.floor(Date.now() / 1000);
      const remaining = (stored.expiresAt ?? 0) - now;
      if (remaining < 300 && remaining > 0) {
        get().refreshToken();
      }

      // 设置定时器，在过期前 5 分钟自动刷新
      scheduleAutoRefresh(stored.expiresAt, () => get().refreshToken());
      return;
    }

    // No stored token — auto-create guest session
    const guestResult = await callGuestAPI();
    if (guestResult) {
      const expiresAt = Math.floor(Date.now() / 1000) + guestResult.expires_in;
      const user: AuthUser = {
        userId: guestResult.user_id,
        role: guestResult.role as AuthUser["role"],
      };
      saveToStorage(guestResult.token, guestResult.user_id, guestResult.role, expiresAt);
      set({ token: guestResult.token, user, expiresAt, initialized: true });
      scheduleAutoRefresh(expiresAt, () => get().refreshToken());
    } else {
      // Fallback: mark as initialized even if guest creation fails
      set({ initialized: true });
    }
  },

  login: (token, userId, role, expiresIn) => {
    const expiresAt = Math.floor(Date.now() / 1000) + expiresIn;
    const user: AuthUser = { userId, role: role as AuthUser["role"] };

    saveToStorage(token, userId, role, expiresAt);

    set({ token, user, expiresAt, initialized: true });

    // 设置自动刷新
    scheduleAutoRefresh(expiresAt, () => get().refreshToken());
  },

  logout: () => {
    clearStorage();
    set({ token: null, user: null, expiresAt: null });
    // 清理选题缓存，避免下一个用户看到上一个用户的数据
    import("@/stores/topic-cache-store").then((m) => m.useTopicCacheStore.getState().clearCache());
  },

  refreshToken: async () => {
    const { token } = get();
    if (!token) return false;

    const result = await callRefreshAPI(token);
    if (!result) {
      // 刷新失败，登出
      get().logout();
      return false;
    }

    const expiresAt = Math.floor(Date.now() / 1000) + result.expires_in;
    saveToStorage(result.token, result.user_id, result.role, expiresAt);

    set({
      token: result.token,
      user: { userId: result.user_id, role: result.role as AuthUser["role"] },
      expiresAt,
    });

    // 重新调度下次自动刷新
    scheduleAutoRefresh(expiresAt, () => get().refreshToken());
    return true;
  },

  isAuthenticated: () => {
    const { token, expiresAt } = get();
    if (!token || !expiresAt) return false;
    return Math.floor(Date.now() / 1000) < expiresAt;
  },

  isGuest: () => {
    const { user } = get();
    return user?.role === "guest";
  },

  getAuthHeaders: () => {
    const { token } = get();
    if (!token) return {} as Record<string, string>;
    return { Authorization: `Bearer ${token}` };
  },
}));

// ─── 静态方法：用于非组件场景 ─────────────────────────────

/**
 * 错误码 → 中文文案映射
 * 后端返回英文 message 用于日志，前端统一映射为中文展示
 */
const ERROR_MESSAGES_ZH: Record<string, string> = {
  invalid_credentials: "用户名或密码错误",
  invalid_api_key: "API Key 无效",
  username_taken: "用户名已被占用",
  not_guest: "当前账号不是游客，无需升级",
  not_found: "用户不存在",
  weak_password: "密码至少 6 位",
  bad_request: "请求参数有误",
  network_error: "网络错误，请检查连接",
};

function localizeError(code: string, fallback: string): string {
  return ERROR_MESSAGES_ZH[code] || fallback;
}

export const authStore = {
  login: async (body: Record<string, unknown>): Promise<AuthResult> => {
    const result = await callLoginAPI(body);
    if ("error" in result) {
      return { ok: false, code: result.error.code, message: localizeError(result.error.code, result.error.message) };
    }
    useAuthStore.getState().login(result.data.token, result.data.user_id, result.data.role, result.data.expires_in);
    return { ok: true };
  },

  register: async (body: Record<string, unknown>): Promise<AuthResult> => {
    const result = await callRegisterAPI(body);
    if ("error" in result) {
      return { ok: false, code: result.error.code, message: localizeError(result.error.code, result.error.message) };
    }
    useAuthStore.getState().login(result.data.token, result.data.user_id, result.data.role, result.data.expires_in);
    return { ok: true };
  },

  logout: () => useAuthStore.getState().logout(),

  getToken: () => useAuthStore.getState().token,

  getAuthHeaders: () => useAuthStore.getState().getAuthHeaders(),

  isAuthenticated: () => useAuthStore.getState().isAuthenticated(),

  isGuest: () => useAuthStore.getState().isGuest(),
};

// ─── 自动刷新调度 ──────────────────────────────────────────

let refreshTimer: ReturnType<typeof setTimeout> | null = null;

function scheduleAutoRefresh(expiresAt: number, refreshFn: () => void) {
  if (refreshTimer) {
    clearTimeout(refreshTimer);
    refreshTimer = null;
  }

  const now = Math.floor(Date.now() / 1000);
  const refreshAt = expiresAt - 300; // 过期前 5 分钟
  const delay = (refreshAt - now) * 1000;

  if (delay > 0) {
    refreshTimer = setTimeout(() => {
      refreshFn();
    }, Math.min(delay, 2_147_483_000)); // 最大 ~24 天
  }
}

// ─── fetch 拦截器：自动附加 Auth Header ────────────────────

const originalFetch = window.fetch;
window.fetch = function (input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  const headers = new Headers(init?.headers);

  // 如果没有手动设置 Authorization，且有 token，则自动附加
  if (!headers.has("Authorization")) {
    const authHeaders = authStore.getAuthHeaders();
    if (authHeaders.Authorization) {
      headers.set("Authorization", authHeaders.Authorization);
    }
  }

  // admin token fallback
  const adminToken = localStorage.getItem("admin_token");
  if (!headers.has("Authorization") && adminToken) {
    headers.set("Authorization", `Bearer ${adminToken}`);
  }

  return originalFetch.call(this, input, { ...init, headers });
};

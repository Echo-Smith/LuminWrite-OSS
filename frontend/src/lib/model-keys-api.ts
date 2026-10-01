/**
 * Model Keys API — BYOK 模型服务（个人中心）
 *
 * 用户自带模型端点与密钥的 CRUD、连通测试、模型发现，以及个人用量聚合。
 * 错误处理对齐 memory-store 的轻量风格：失败抛错由调用方 toast/降级。
 */
import { useAuthStore } from "@/stores/auth-store";

function authHeaders(): Record<string, string> {
  const token = useAuthStore.getState().token;
  return token ? { Authorization: `Bearer ${token}` } : {};
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      ...authHeaders(),
      ...(init?.headers ?? {}),
    },
  });
  const body = await res.json().catch(() => ({}));
  if (!res.ok || body?.success === false) {
    const code = body?.error?.code ?? `http_${res.status}`;
    const message = body?.error?.message ?? "请求失败";
    const err = new Error(message) as Error & { code: string };
    err.code = code;
    throw err;
  }
  return body?.data as T;
}

// ─── 类型 ──────────────────────────────────────────────────

export interface UserModelKey {
  id: string;
  name: string;
  provider: string;
  model_name: string;
  base_url: string;
  has_api_key: boolean;
  api_key_masked?: string;
  max_tokens: number;
  temperature: number;
  reasoning_effort: string;
  purpose: string;
  is_default: boolean;
  is_active: boolean;
  custom_headers?: Record<string, string>;
  created_at: string;
  updated_at: string;
}

export interface UserModelKeyInput {
  name?: string;
  provider: string;
  model_name: string;
  base_url?: string;
  api_key?: string; // 留空 = 保留已存密钥（更新时）
  max_tokens?: number;
  temperature?: number;
  reasoning_effort?: string;
  is_default?: boolean;
  custom_headers?: Record<string, string>;
}

export interface ProbeResult {
  ok: boolean;
  models?: string[];
  total?: number;
  error_code?: string;
  message?: string;
  latency_ms?: number;
  base_url?: string;
}

export interface UserUsageStats {
  total_traces: number;
  total_tokens: number;
  daily: { date: string; traces: number; tokens: number }[];
}

// ─── 模型服务 CRUD ─────────────────────────────────────────

export async function listModelKeys(): Promise<UserModelKey[]> {
  const data = await request<{ model_keys: UserModelKey[] }>("/api/v2/model-keys");
  return data.model_keys ?? [];
}

export async function createModelKey(input: UserModelKeyInput): Promise<UserModelKey> {
  return request<UserModelKey>("/api/v2/model-keys", {
    method: "POST",
    body: JSON.stringify(input),
  });
}

export async function updateModelKey(id: string, input: UserModelKeyInput): Promise<UserModelKey> {
  return request<UserModelKey>(`/api/v2/model-keys/${id}`, {
    method: "PUT",
    body: JSON.stringify(input),
  });
}

export async function deleteModelKey(id: string): Promise<void> {
  await request(`/api/v2/model-keys/${id}`, { method: "DELETE" });
}

export async function setDefaultModelKey(id: string): Promise<void> {
  await request(`/api/v2/model-keys/${id}/default`, { method: "PUT" });
}

export async function testModelKey(id: string): Promise<ProbeResult> {
  return request<ProbeResult>(`/api/v2/model-keys/${id}/test`, { method: "POST" });
}

export async function discoverModels(input: {
  provider: string;
  base_url?: string;
  api_key: string;
  custom_headers?: Record<string, string>;
}): Promise<ProbeResult> {
  return request<ProbeResult>("/api/v2/model-keys/discover", {
    method: "POST",
    body: JSON.stringify(input),
  });
}

// ─── 个人用量 ──────────────────────────────────────────────

export async function getMyUsage(days = 30): Promise<UserUsageStats> {
  const data = await request<{ days: number; usage: UserUsageStats }>(`/api/v2/usage?days=${days}`);
  return data.usage;
}

// ─── 错误码文案 ────────────────────────────────────────────

export const PROBE_ERROR_LABELS: Record<string, string> = {
  PROVIDER_AUTH_REJECTED: "密钥被服务商拒绝（401/403）",
  PROVIDER_RATE_LIMITED: "触发服务商限流（429）",
  PROVIDER_TIMEOUT: "服务商响应超时",
  PROVIDER_MALFORMED_RESPONSE: "服务商响应格式异常",
  PROVIDER_UNAVAILABLE: "无法连接服务商",
  byok_unavailable: "本部署未配置密钥加密（API_KEY_ENCRYPTION_KEY），BYOK 不可用",
  model_key_exists: "该模型已有一条配置",
  registration_disabled: "本部署已关闭注册",
};

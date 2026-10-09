/**
 * RSS 订阅 API — 素材中心
 *
 * 用户级订阅源管理：订阅/编辑/删除/立即更新。条目由后端周期性抓取后
 * 以素材（source_type=rss）落入目标文件夹。
 */
function authHeaders(): Record<string, string> {
  const token = localStorage.getItem("token");
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

// ─── 类型 ──────────────────────────────────────────────

export interface RSSSubscription {
  id: string;
  feed_url: string;
  title: string;
  site_url?: string;
  description?: string;
  target_folder_id?: string;
  max_items_per_tick: number;
  last_fetched_at?: string;
  last_item_at?: string;
  fail_count: number;
  last_error?: string;
  is_active: boolean;
  created_at: string;
  updated_at: string;
}

export interface RSSRefreshResult {
  subscription_id: string;
  fetched: number;
  imported: number;
  skipped: number;
  not_modified: boolean;
  error?: string;
}

// ─── API ──────────────────────────────────────────────

export async function listRSSSubscriptions(): Promise<RSSSubscription[]> {
  const data = await request<{ subscriptions: RSSSubscription[] }>("/api/v2/rss/subscriptions");
  return data.subscriptions ?? [];
}

export async function createRSSSubscription(input: {
  feed_url: string;
  target_folder_id?: string;
  max_items_per_tick?: number;
}): Promise<RSSSubscription> {
  return request<RSSSubscription>("/api/v2/rss/subscriptions", {
    method: "POST",
    body: JSON.stringify(input),
  });
}

export async function updateRSSSubscription(
  id: string,
  input: { title?: string; target_folder_id?: string; max_items_per_tick?: number; is_active?: boolean },
): Promise<RSSSubscription> {
  return request<RSSSubscription>(`/api/v2/rss/subscriptions/${id}`, {
    method: "PUT",
    body: JSON.stringify(input),
  });
}

export async function deleteRSSSubscription(id: string): Promise<void> {
  await request(`/api/v2/rss/subscriptions/${id}`, { method: "DELETE" });
}

export async function refreshRSSSubscription(id: string): Promise<RSSRefreshResult> {
  return request<RSSRefreshResult>(`/api/v2/rss/subscriptions/${id}/refresh`, { method: "POST" });
}

export const RSS_ERROR_LABELS: Record<string, string> = {
  subscription_exists: "你已经订阅过这个源",
  feed_unreachable: "无法读取订阅源",
};

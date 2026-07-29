/**
 * WeKnora API Service — 封装所有 WeKnora 后端 API 调用
 * 路径前缀: /api/weknora
 */

// ─── Types ───────────────────────────────────────────────

export interface WeKnoraKnowledge {
  id: string;
  title: string;
  content?: string;
  source?: string;
  status?: string;
  created_at?: string;
  updated_at?: string;
}

export interface WeKnoraKB {
  id: string;
  name: string;
  description?: string;
  document_count?: number;
  created_at?: string;
}

export interface WeKnoraSearchResult {
  title: string;
  snippet: string;
  url?: string;
  source: string;
  score: number;
}

interface APIResponse<T> {
  success: boolean;
  message?: string;
  data?: T;
}

// ─── API Functions ──────────────────────────────────────

const BASE = "/api/weknora";

async function fetchJSON<T>(url: string, init?: RequestInit): Promise<APIResponse<T>> {
  const res = await fetch(url, init);
  return res.json();
}

async function postJSON<T>(url: string, body: unknown): Promise<APIResponse<T>> {
  return fetchJSON<T>(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
}

/** 检查 WeKnora 是否已配置（返回知识库列表表示已连接） */
export async function checkConfig(): Promise<{ configured: boolean; kbs: WeKnoraKB[] }> {
  try {
    const json = await fetchJSON<{ knowledge_bases: WeKnoraKB[] }>(`${BASE}/kbs`);
    return { configured: json.success, kbs: json.data?.knowledge_bases ?? [] };
  } catch {
    return { configured: false, kbs: [] };
  }
}

/** 混合检索（BM25 + Dense + GraphRAG） */
export async function search(query: string, limit = 10): Promise<WeKnoraSearchResult[]> {
  const json = await postJSON<{ results: WeKnoraSearchResult[] }>(`${BASE}/search`, { query, limit });
  return json.data?.results ?? [];
}

/** 列出知识条目（分页） */
export async function listKnowledge(
  page: number,
  pageSize = 20,
): Promise<{ entries: WeKnoraKnowledge[]; total: number }> {
  const json = await fetchJSON<{ entries: WeKnoraKnowledge[]; total: number }>(
    `${BASE}/knowledge?page=${page}&page_size=${pageSize}`,
  );
  return { entries: json.data?.entries ?? [], total: json.data?.total ?? 0 };
}

/** 从文本/Markdown 创建知识 */
export async function createKnowledge(title: string, content: string): Promise<string> {
  const json = await postJSON<{ id: string }>(`${BASE}/knowledge`, { title, content });
  return json.data?.id ?? "";
}

/** 从 URL 导入知识 */
export async function createKnowledgeFromURL(url: string, title?: string): Promise<string> {
  const json = await postJSON<{ id: string }>(`${BASE}/knowledge/url`, { url, title });
  return json.data?.id ?? "";
}

/** 上传文件创建知识 */
export async function uploadFile(file: File, title?: string): Promise<string> {
  const fd = new FormData();
  fd.append("file", file);
  if (title) fd.append("title", title);
  const res = await fetch(`${BASE}/knowledge/upload`, { method: "POST", body: fd });
  const json = await res.json();
  return json.data?.id ?? "";
}

/** 删除知识条目 */
export async function deleteKnowledge(id: string): Promise<void> {
  await fetch(`${BASE}/knowledge/${id}`, { method: "DELETE" });
}

/**
 * User Material API Service — 个人素材库 API 调用
 * 路径前缀: /api/v2/materials
 */

// ─── Types ───────────────────────────────────────────────

export interface UserMaterial {
  id: string;
  user_id: string;
  title: string;
  content_preview: string;
  source_type: "text" | "file" | "url" | "auto";
  source_url?: string;
  file_name?: string;
  file_size?: number;
  doc_id?: string;
  chunk_count?: number;
  metadata?: Record<string, unknown>;
  status: string;
  created_at: string;
  updated_at: string;
}

export interface MaterialSearchResult {
  doc_id?: string;
  chunk_id?: string;
  id: string;
  content: string;
  score: number;
  bm25_score?: number;
  dense_score?: number;
  title: string;
  source: string;
  knowledge_id?: string;
}

export interface TopicMaterialAssociation {
  id: string;
  topic_id: string;
  material_id: string;
  user_id: string;
  association_type: "manual" | "auto";
  relevance_score?: number;
  created_at: string;
  material?: UserMaterial | null;
}

interface APIResponse<T> {
  success: boolean;
  message?: string;
  data?: T;
}

// ─── API Functions ──────────────────────────────────────

const BASE = "/api/v2";

async function fetchWithAuth(url: string, init?: RequestInit): Promise<Response> {
  const token = localStorage.getItem("token");
  const headers: Record<string, string> = { ...(init?.headers as Record<string, string>) };
  if (token) headers["Authorization"] = `Bearer ${token}`;
  return fetch(url, { ...init, headers });
}

async function fetchJSON<T>(url: string, init?: RequestInit): Promise<APIResponse<T>> {
  const res = await fetchWithAuth(url, init);
  return res.json();
}

async function postJSON<T>(url: string, body: unknown): Promise<APIResponse<T>> {
  return fetchJSON<T>(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
}

/** 列出当前用户的素材 */
export async function listMaterials(
  page = 1,
  pageSize = 20,
): Promise<{ materials: UserMaterial[]; total: number }> {
  const json = await fetchJSON<{ materials: UserMaterial[]; total: number }>(
    `${BASE}/materials?page=${page}&page_size=${pageSize}`,
  );
  return {
    materials: json.data?.materials ?? [],
    total: json.data?.total ?? 0,
  };
}

/** 从文本/Markdown 创建素材 */
export async function createMaterial(title: string, content: string): Promise<string> {
  const json = await postJSON<{ id: string }>(`${BASE}/materials`, { title, content });
  return json.data?.id ?? "";
}

/** 上传文件创建素材 */
export async function uploadMaterial(file: File, title?: string): Promise<string> {
  const fd = new FormData();
  fd.append("file", file);
  if (title) fd.append("title", title);
  const res = await fetchWithAuth(`${BASE}/materials/upload`, { method: "POST", body: fd });
  const json = await res.json();
  return json.data?.id ?? "";
}

/** 删除素材 */
export async function deleteMaterial(id: string): Promise<void> {
  await fetchWithAuth(`${BASE}/materials/${id}`, { method: "DELETE" });
}

/** 混合检索用户素材库 */
export async function searchMaterials(
  query: string,
  limit = 10,
): Promise<MaterialSearchResult[]> {
  const json = await postJSON<{ results: MaterialSearchResult[] }>(`${BASE}/materials/search`, {
    query,
    limit,
  });
  return json.data?.results ?? [];
}

// ─── Topic-Material Association ─────────────────────────

/** 列出选题关联的素材 */
export async function listTopicMaterials(
  topicId: string,
): Promise<{ associations: TopicMaterialAssociation[]; total: number }> {
  const json = await fetchJSON<{ associations: TopicMaterialAssociation[]; total: number }>(
    `${BASE}/topics/${topicId}/materials`,
  );
  return {
    associations: json.data?.associations ?? [],
    total: json.data?.total ?? 0,
  };
}

/** 手动关联素材到选题 */
export async function associateMaterial(
  topicId: string,
  materialId: string,
): Promise<void> {
  await postJSON(`${BASE}/topics/${topicId}/materials/${materialId}`, {});
}

/** 取消素材与选题的关联 */
export async function removeMaterialAssociation(
  topicId: string,
  materialId: string,
): Promise<void> {
  await fetchWithAuth(`${BASE}/topics/${topicId}/materials/${materialId}`, { method: "DELETE" });
}

/** 自动关联（用选题标题在用户素材库中搜索并关联） */
export async function autoAssociateMaterials(
  topicId: string,
  query?: string,
  limit = 5,
): Promise<{ associated: number; results: MaterialSearchResult[] }> {
  const json = await postJSON<{ associated: number; results: MaterialSearchResult[] }>(
    `${BASE}/topics/${topicId}/materials/auto`,
    { query, limit },
  );
  return {
    associated: json.data?.associated ?? 0,
    results: json.data?.results ?? [],
  };
}

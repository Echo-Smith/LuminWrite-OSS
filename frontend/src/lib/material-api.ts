/**
 * User Material API Service — 个人素材库 API 调用
 * 路径前缀: /api/v2/materials, /api/v2/material-folders
 *
 * 注意：不再手动从 localStorage 读取 token。
 * 全局 fetch 拦截器（auth-store.ts）会自动附加 Authorization header。
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
  folder_id?: string;
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

export interface MaterialFolder {
  id: string;
  user_id: string;
  name: string;
  parent_id?: string;
  sort_order: number;
  description?: string;
  material_count: number;
  created_at: string;
  updated_at: string;
}

interface APIResponse<T> {
  success: boolean;
  message?: string;
  data?: T;
}

// ─── API Functions ──────────────────────────────────────

const BASE = "/api/v2";

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

async function putJSON<T>(url: string, body?: unknown): Promise<APIResponse<T>> {
  return fetchJSON<T>(url, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: body ? JSON.stringify(body) : undefined,
  });
}

/** 列出当前用户的素材（支持文件夹筛选） */
export async function listMaterials(
  page = 1,
  pageSize = 20,
  folderId?: string,
): Promise<{ materials: UserMaterial[]; total: number }> {
  let url = `${BASE}/materials?page=${page}&page_size=${pageSize}`;
  if (folderId !== undefined) {
    url += `&folder_id=${encodeURIComponent(folderId)}`;
  }
  const json = await fetchJSON<{ materials: UserMaterial[]; total: number }>(url);
  return {
    materials: json.data?.materials ?? [],
    total: json.data?.total ?? 0,
  };
}

/** 从文本/Markdown 创建素材 */
export async function createMaterial(
  title: string,
  content: string,
  folderId?: string,
): Promise<string> {
  const json = await postJSON<{ id: string }>(`${BASE}/materials`, {
    title,
    content,
    folder_id: folderId ?? "",
  });
  return json.data?.id ?? "";
}

/** 上传文件创建素材 */
export async function uploadMaterial(
  file: File,
  title?: string,
  folderId?: string,
): Promise<string> {
  const fd = new FormData();
  fd.append("file", file);
  if (title) fd.append("title", title);
  if (folderId) fd.append("folder_id", folderId);
  const res = await fetch(`${BASE}/materials/upload`, { method: "POST", body: fd });
  const json = await res.json();
  return json.data?.id ?? "";
}

/** 删除素材 */
export async function deleteMaterial(id: string): Promise<void> {
  await fetch(`${BASE}/materials/${id}`, { method: "DELETE" });
}

/** 获取单个素材的完整内容 */
export async function getMaterialContent(id: string): Promise<UserMaterial> {
  const res = await fetch(`${BASE}/materials/${id}`);
  const data = await res.json();
  return (data.data ?? data) as UserMaterial;
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

/** 移动素材到文件夹 */
export async function moveMaterial(materialId: string, folderId: string): Promise<void> {
  await putJSON(`${BASE}/materials/${materialId}/move`, { folder_id: folderId });
}

// ─── Folder API ─────────────────────────────────────────

/** 列出所有素材文件夹 */
export async function listFolders(): Promise<MaterialFolder[]> {
  const json = await fetchJSON<{ folders: MaterialFolder[]; total: number }>(
    `${BASE}/material-folders`,
  );
  return json.data?.folders ?? [];
}

/** 创建文件夹 */
export async function createFolder(
  name: string,
  parentId?: string,
  description?: string,
): Promise<MaterialFolder | null> {
  const json = await postJSON<MaterialFolder>(`${BASE}/material-folders`, {
    name,
    parent_id: parentId ?? "",
    description: description ?? "",
  });
  return json.data ?? null;
}

/** 更新文件夹 */
export async function updateFolder(
  id: string,
  name?: string,
  description?: string,
): Promise<MaterialFolder | null> {
  const json = await putJSON<MaterialFolder>(`${BASE}/material-folders/${id}`, {
    name: name ?? "",
    description: description ?? "",
  });
  return json.data ?? null;
}

/** 删除文件夹（素材会移到根目录） */
export async function deleteFolder(id: string): Promise<void> {
  await fetch(`${BASE}/material-folders/${id}`, { method: "DELETE" });
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
  await fetch(`${BASE}/topics/${topicId}/materials/${materialId}`, { method: "DELETE" });
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

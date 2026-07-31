/**
 * Knowledge Base API Service — 本地知识库 API 调用
 * 路径前缀: /api/v2/kb
 */

// ─── Types ───────────────────────────────────────────────

/** 知识文档 */
export interface KnowledgeDoc {
  id: string;
  title: string;
  content?: string;
  source?: string;
  source_type?: string;    // text | file | url
  status?: string;         // active | processing | failed
  chunk_count?: number;
  file_name?: string;
  file_size?: number;
  created_at?: string;
  updated_at?: string;
}

/** 知识库（多 KB 支持） */
export interface KBInfo {
  id: string;
  name: string;
  description?: string;
  document_count?: number;
  chunk_count?: number;
  created_at?: string;
  updated_at?: string;
}

/** 混合检索结果（chunk 级） */
export interface KBSearchResult {
  doc_id?: string;
  chunk_id?: string;
  title: string;
  content: string;
  score: number;
  bm25_score?: number;
  dense_score?: number;
  source?: string;
  url?: string;
  snippet?: string;        // backward compat
}

interface APIResponse<T> {
  success: boolean;
  message?: string;
  data?: T;
}

// ─── API Functions ──────────────────────────────────────

const BASE = "/api/v2/kb";

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

/** 检查知识库是否已配置 */
export async function checkConfig(): Promise<{ configured: boolean; kbs: KBInfo[] }> {
  try {
    const json = await fetchJSON<{ knowledge_bases: KBInfo[] }>(`${BASE}/kbs`);
    return { configured: json.success, kbs: json.data?.knowledge_bases ?? [] };
  } catch {
    return { configured: false, kbs: [] };
  }
}

/** 混合检索（BM25 + Dense + RRF） */
export async function search(query: string, limit = 10): Promise<KBSearchResult[]> {
  const json = await postJSON<{ results: KBSearchResult[] }>(`${BASE}/search`, { query, limit });
  return json.data?.results ?? [];
}

/** 检索模式 */
export type SearchMode = "hybrid" | "bm25" | "dense";

/** 带模式参数的混合检索 */
export async function searchWithMode(
  query: string,
  opts?: {
    limit?: number;
    mode?: SearchMode;
    bm25Weight?: number;
    denseWeight?: number;
    kbId?: string;
  },
): Promise<{ results: KBSearchResult[]; mode: string }> {
  const body = {
    query,
    limit: opts?.limit ?? 10,
    mode: opts?.mode ?? "hybrid",
    bm25_weight: opts?.bm25Weight ?? 0,
    dense_weight: opts?.denseWeight ?? 0,
    kb_id: opts?.kbId ?? "default",
  };
  const json = await postJSON<{ results: KBSearchResult[]; mode: string }>(`${BASE}/search`, body);
  return {
    results: json.data?.results ?? [],
    mode: json.data?.mode ?? "hybrid",
  };
}

/** 列出知识条目（分页，按 KB 隔离） */
export async function listKnowledge(
  page: number,
  pageSize = 20,
  kbId?: string,
): Promise<{ entries: KnowledgeDoc[]; total: number }> {
  const params = new URLSearchParams({ page: String(page), page_size: String(pageSize) });
  if (kbId) params.set("kb_id", kbId);
  const json = await fetchJSON<{ entries: KnowledgeDoc[]; total: number }>(
    `${BASE}/knowledge?${params}`,
  );
  return { entries: json.data?.entries ?? [], total: json.data?.total ?? 0 };
}

/** 从文本/Markdown 创建知识（可指定 KB） */
export async function createKnowledge(title: string, content: string, kbId?: string): Promise<string> {
  const json = await postJSON<{ id: string }>(`${BASE}/knowledge`, { title, content, kb_id: kbId ?? "default" });
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

/** 获取知识库状态 */
export async function getStatus(): Promise<{
  enabled: boolean;
  local_kb: boolean;
}> {
  try {
    const json = await fetchJSON<{ enabled: boolean; local_kb: boolean }>(`${BASE}/status`);
    return {
      enabled: json.data?.enabled ?? false,
      local_kb: json.data?.local_kb ?? false,
    };
  } catch {
    return { enabled: false, local_kb: false };
  }
}

// ─── Stats & Document Detail ────────────────────────────

/** 知识库统计 */
export interface KBStats {
  doc_count: number;
  chunk_count: number;
  chunk_with_embedding: number;
  entity_count: number;
  relation_count: number;
  source_breakdown: Record<string, number>;
}

/** 知识分块 */
export interface KBChunk {
  id: string;
  doc_id: string;
  user_id?: string;
  chunk_index: number;
  title?: string;
  content: string;
  has_embedding: boolean;
  created_at?: string;
}

/** 实体 */
export interface KBEntity {
  id: string;
  doc_id: string;
  chunk_id?: string;
  entity_name: string;
  entity_type: string;
  attributes?: Record<string, unknown>;
}

/** 关系 */
export interface KBRelation {
  id: string;
  doc_id: string;
  source_entity: string;
  target_entity: string;
  relation_type: string;
  attributes?: Record<string, unknown>;
}

/** 获取知识库统计（按 KB 隔离） */
export async function getStats(kbId?: string): Promise<KBStats> {
  const params = kbId ? `?kb_id=${kbId}` : "";
  const json = await fetchJSON<KBStats>(`${BASE}/stats${params}`);
  return json.data ?? {
    doc_count: 0,
    chunk_count: 0,
    chunk_with_embedding: 0,
    entity_count: 0,
    relation_count: 0,
    source_breakdown: {},
  };
}

/** 获取文档分块列表 */
export async function getDocumentChunks(docId: string): Promise<KBChunk[]> {
  const json = await fetchJSON<{ chunks: KBChunk[] }>(`${BASE}/documents/${docId}/chunks`);
  return json.data?.chunks ?? [];
}

/** 获取文档关联实体和关系 */
export async function getDocumentEntities(docId: string): Promise<{
  entities: KBEntity[];
  relations: KBRelation[];
}> {
  const json = await fetchJSON<{
    entities: KBEntity[];
    relations: KBRelation[];
  }>(`${BASE}/documents/${docId}/entities`);
  return {
    entities: json.data?.entities ?? [],
    relations: json.data?.relations ?? [],
  };
}

// ─── Entity Graph ───────────────────────────────────────

/** 图谱节点 */
export interface GraphNode {
  id: string;
  entity_name: string;
  entity_type: string;
  attributes?: Record<string, unknown>;
}

/** 图谱边 */
export interface GraphEdge {
  source_entity: string;  // node id
  target_entity: string;  // node id
  relation_type: string;
}

/** 获取全局实体关系图谱 */
export async function getGraph(limit = 50): Promise<{
  nodes: GraphNode[];
  edges: GraphEdge[];
}> {
  const json = await fetchJSON<{
    nodes: GraphNode[];
    edges: GraphEdge[];
  }>(`${BASE}/graph?limit=${limit}`);
  return {
    nodes: json.data?.nodes ?? [],
    edges: json.data?.edges ?? [],
  };
}

// ─── KB Management (Multi-KB CRUD) ──────────────────────

/** 创建知识库 */
export async function createKB(name: string, description?: string, id?: string): Promise<KBInfo> {
  const json = await postJSON<KBInfo>(`${BASE}/manage`, { name, description: description ?? "", id: id ?? "" });
  return json.data ?? { id: "", name };
}

/** 列出所有知识库 */
export async function listKBs(): Promise<KBInfo[]> {
  const json = await fetchJSON<{ knowledge_bases: KBInfo[] }>(`${BASE}/kbs`);
  return json.data?.knowledge_bases ?? [];
}

/** 更新知识库 */
export async function updateKB(id: string, name: string, description?: string): Promise<KBInfo> {
  const res = await fetch(`${BASE}/manage/${id}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name, description: description ?? "" }),
  });
  const json = await res.json();
  return json.data ?? { id, name };
}

/** 删除知识库（不能删除 default） */
export async function deleteKB(id: string): Promise<void> {
  await fetch(`${BASE}/manage/${id}`, { method: "DELETE" });
}

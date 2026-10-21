export interface LwClient {
  health(): Promise<{ ok: boolean }>;
  createDocument(input: {
    title: string;
    genre?: string;
  }): Promise<{ id: string; versionId: string; sessionId: string }>;
  listDocuments(): Promise<Array<{ id: string; title: string; savedAt: string }>>;
  saveDocument(id: string): Promise<{ savedAt: string; already: boolean }>;
  restoreDocument(id: string, versionId: string): Promise<{ ok: boolean; versionId?: string }>;
  discardDocument(id: string): Promise<{ discardedAt: string }>;
  undoDiscard(id: string): Promise<{ discardedAt: null }>;
}

export function createClient(
  baseUrl: string,
  fetchImpl: typeof fetch = fetch,
  options?: { token?: string },
): LwClient {
  const root = baseUrl.replace(/\/$/, "");
  return {
    async health() {
      const response = await fetchImpl(`${root}/health`);
      return (await response.json()) as { ok: boolean };
    },
    async createDocument(input) {
      const headers: Record<string, string> = { "content-type": "application/json" };
      if (options?.token) headers.authorization = `Bearer ${options.token}`;
      const response = await fetchImpl(`${root}/api/v1/documents`, {
        method: "POST",
        headers,
        body: JSON.stringify(input),
      });
      if (!response.ok) throw new Error(`创建文档失败 ${response.status}`);
      return (await response.json()) as { id: string; versionId: string; sessionId: string };
    },
    async listDocuments() {
      const response = await fetchImpl(`${root}/api/v1/documents`, {
        headers: authHeaders(options),
      });
      if (!response.ok) throw new Error(`列出文档失败 ${response.status}`);
      return (await response.json()) as Array<{ id: string; title: string; savedAt: string }>;
    },
    async saveDocument(id) {
      const response = await fetchImpl(`${root}/api/v1/documents/${id}/save`, {
        method: "POST",
        headers: authHeaders(options),
      });
      if (!response.ok) throw new Error(`保存文档失败 ${response.status}`);
      return (await response.json()) as { savedAt: string; already: boolean };
    },
    async restoreDocument(id, versionId) {
      const response = await fetchImpl(`${root}/api/v1/documents/${id}/restore`, {
        method: "POST",
        headers: { ...authHeaders(options), "content-type": "application/json" },
        body: JSON.stringify({ versionId }),
      });
      if (!response.ok) throw new Error(`回滚文档失败 ${response.status}`);
      return (await response.json()) as { ok: boolean; versionId?: string };
    },
    async discardDocument(id) {
      const response = await fetchImpl(`${root}/api/v1/documents/${id}/discard`, {
        method: "POST",
        headers: authHeaders(options),
      });
      if (!response.ok) throw new Error(`丢弃文档失败 ${response.status}`);
      return (await response.json()) as { discardedAt: string };
    },
    async undoDiscard(id) {
      const response = await fetchImpl(`${root}/api/v1/documents/${id}/discard/undo`, {
        method: "POST",
        headers: authHeaders(options),
      });
      if (!response.ok) throw new Error(`撤销丢弃失败 ${response.status}`);
      return (await response.json()) as { discardedAt: null };
    },
  };
}

function authHeaders(options?: { token?: string }): Record<string, string> {
  return options?.token ? { authorization: `Bearer ${options.token}` } : {};
}

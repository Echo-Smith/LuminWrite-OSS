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
  uploadSource(input: {
    filename: string;
    dataBase64: string;
    mime?: string;
    title?: string;
  }): Promise<{ sourceId: string; status: "indexed" | "duplicate" }>;
  searchSources(query: string): Promise<{ note: string; hits: Array<{ sourceId: string }> }>;
  submitTask(body: Record<string, unknown>, idempotencyKey: string): Promise<ClientTask>;
  getTask(id: string): Promise<ClientTask>;
  answerTask(id: string, message: string): Promise<ClientTask>;
  cancelTask(id: string): Promise<ClientTask>;
  taskEvents(id: string, after?: number): Promise<string>;
}

export interface ClientTask {
  id: string;
  state: string;
  phase?: string;
  documentId?: string;
  resultVersionId?: string;
  proposalId?: string;
  usage: { inputTokens: number; outputTokens: number; costUsd: number };
  error?: { code: string; message: string; retryable: boolean };
  createdAt: string;
  updatedAt: string;
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
    async uploadSource(input) {
      const response = await fetchImpl(`${root}/api/v1/sources`, {
        method: "POST",
        headers: { ...authHeaders(options), "content-type": "application/json" },
        body: JSON.stringify(input),
      });
      if (!response.ok) throw new Error(`上传资料失败 ${response.status}`);
      return (await response.json()) as { sourceId: string; status: "indexed" | "duplicate" };
    },
    async searchSources(query) {
      const response = await fetchImpl(
        `${root}/api/v1/sources/search?q=${encodeURIComponent(query)}`,
        { headers: authHeaders(options) },
      );
      if (!response.ok) throw new Error(`检索资料失败 ${response.status}`);
      return (await response.json()) as { note: string; hits: Array<{ sourceId: string }> };
    },
    async submitTask(body, idempotencyKey) {
      const response = await fetchImpl(`${root}/api/v1/tasks`, {
        method: "POST",
        headers: {
          ...authHeaders(options),
          "content-type": "application/json",
          "idempotency-key": idempotencyKey,
        },
        body: JSON.stringify(body),
      });
      if (!response.ok) throw new Error(`提交任务失败 ${response.status}`);
      return (await response.json()) as ClientTask;
    },
    async getTask(id) {
      const response = await fetchImpl(`${root}/api/v1/tasks/${id}`, {
        headers: authHeaders(options),
      });
      if (!response.ok) throw new Error(`读取任务失败 ${response.status}`);
      return (await response.json()) as ClientTask;
    },
    async answerTask(id, message) {
      const response = await fetchImpl(`${root}/api/v1/tasks/${id}/messages`, {
        method: "POST",
        headers: { ...authHeaders(options), "content-type": "application/json" },
        body: JSON.stringify({ message }),
      });
      if (!response.ok) throw new Error(`回复任务失败 ${response.status}`);
      return (await response.json()) as ClientTask;
    },
    async cancelTask(id) {
      const response = await fetchImpl(`${root}/api/v1/tasks/${id}/cancel`, {
        method: "POST",
        headers: authHeaders(options),
      });
      if (!response.ok) throw new Error(`取消任务失败 ${response.status}`);
      return (await response.json()) as ClientTask;
    },
    async taskEvents(id, after) {
      const headers = authHeaders(options);
      if (after !== undefined) headers["last-event-id"] = String(after);
      const response = await fetchImpl(`${root}/api/v1/tasks/${id}/events`, { headers });
      if (!response.ok) throw new Error(`读取任务事件失败 ${response.status}`);
      return response.text();
    },
  };
}

function authHeaders(options?: { token?: string }): Record<string, string> {
  return options?.token ? { authorization: `Bearer ${options.token}` } : {};
}

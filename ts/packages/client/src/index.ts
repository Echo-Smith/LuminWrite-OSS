export interface LwClient {
  health(): Promise<{ ok: boolean }>;
  createDocument(input: {
    title: string;
    genre?: string;
  }): Promise<{ id: string; versionId: string; sessionId: string }>;
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
  };
}

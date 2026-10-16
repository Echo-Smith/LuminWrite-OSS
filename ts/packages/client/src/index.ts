export interface LwClient {
  health(): Promise<{ ok: boolean }>;
  createDocument(input: {
    title: string;
    ownerId: string;
  }): Promise<{ id: string; versionId: string }>;
}

export function createClient(baseUrl: string, fetchImpl: typeof fetch = fetch): LwClient {
  const root = baseUrl.replace(/\/$/, "");
  return {
    async health() {
      const response = await fetchImpl(`${root}/health`);
      return (await response.json()) as { ok: boolean };
    },
    async createDocument(input) {
      const response = await fetchImpl(`${root}/documents`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(input),
      });
      return (await response.json()) as { id: string; versionId: string };
    },
  };
}

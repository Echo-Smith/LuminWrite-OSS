import { createHash } from "node:crypto";

/** pgvector 的 HNSW 索引要求列维度固定。默认 1024，对应本地 BGE-M3。 */
export const EMBED_DIM = 1024;
export const FAKE_EMBED_MODEL = "fake/hash";

export interface EmbeddingPort {
  model: string;
  dim: number;
  embed(texts: string[], ctx: { userId: string; requestId: string }): Promise<number[][]>;
}

/** 确定性假嵌入。CI 用它走向量那一路，不访问网络。 */
export function createFakeEmbedder(dim = EMBED_DIM): EmbeddingPort {
  return {
    model: FAKE_EMBED_MODEL,
    dim,
    async embed(texts) {
      return texts.map((text) => hashEmbedding(text, dim));
    },
  };
}

export function hashEmbedding(text: string, dim = EMBED_DIM): number[] {
  const values = new Array<number>(dim);
  let sum = 0;
  for (let index = 0; index < dim; index += 1) {
    const digest = createHash("sha256").update(`${index}\0${text}`).digest();
    const value = digest.readInt32BE(0) / 0x7fffffff;
    values[index] = value;
    sum += value * value;
  }
  const norm = Math.sqrt(sum) || 1;
  return values.map((value) => value / norm);
}

/**
 * pi-ai 没有嵌入接口。真实嵌入走 OpenAI 兼容的 `/embeddings`，
 * 密钥来自绑在 `kb.embed` 槽上的 BYOK 凭据。
 */
export function createOpenAiEmbedder(input: {
  model: string;
  dim?: number;
  apiKey: string;
  baseUrl: string;
  fetchImpl?: typeof fetch;
}): EmbeddingPort {
  const dim = input.dim ?? EMBED_DIM;
  const root = input.baseUrl.replace(/\/$/, "");
  const fetchImpl = input.fetchImpl ?? fetch;
  return {
    model: input.model,
    dim,
    async embed(texts, ctx) {
      const response = await fetchImpl(`${root}/embeddings`, {
        method: "POST",
        headers: {
          authorization: `Bearer ${input.apiKey}`,
          "content-type": "application/json",
        },
        body: JSON.stringify({ model: input.model, input: texts, user: ctx.userId }),
      });
      if (!response.ok) throw new Error(`嵌入请求失败 ${response.status}`);
      const body = (await response.json()) as {
        data?: Array<{ index?: number; embedding?: number[] }>;
      };
      const ordered = new Array<number[]>(texts.length);
      for (const item of body.data ?? []) {
        if (item.index == null || !item.embedding) continue;
        ordered[item.index] = item.embedding;
      }
      if (ordered.some((item) => !item || item.length !== dim)) {
        throw new Error(`嵌入维度不是 ${dim}`);
      }
      return ordered;
    },
  };
}

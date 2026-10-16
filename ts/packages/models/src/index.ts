import type { UsageEvent, UsageSink } from "@luminwrite/edition";

export type ModelTier = "fast" | "balanced" | "strong";

export interface ModelRequest {
  tier: ModelTier;
  prompt: string;
  requestId: string;
  system?: string;
}

export interface ModelResponse {
  text: string;
  model: string;
  inputTokens: number;
  outputTokens: number;
}

export interface ModelProvider {
  id: string;
  complete(request: ModelRequest): Promise<ModelResponse>;
}

export interface ModelPort {
  complete(request: ModelRequest): Promise<ModelResponse>;
}

export class RetryableModelError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "RetryableModelError";
  }
}

export function createModelRouter(options: {
  usage: UsageSink;
  providers: ModelProvider[];
  routes: Record<ModelTier, string[]>;
  isRetryable?: (error: unknown) => boolean;
}): ModelPort {
  const byId = new Map(options.providers.map((provider) => [provider.id, provider]));
  const retryable =
    options.isRetryable ?? ((error: unknown) => error instanceof RetryableModelError);
  return {
    async complete(request) {
      const chain = options.routes[request.tier];
      let last: unknown;
      for (let index = 0; index < chain.length; index += 1) {
        const id = chain[index];
        const provider = id ? byId.get(id) : undefined;
        if (!provider) throw new Error(`档位 ${request.tier} 没有可用模型 ${id ?? ""}`);
        try {
          const result = await provider.complete(request);
          const event: UsageEvent = {
            requestId: request.requestId,
            model: result.model,
            inputTokens: result.inputTokens,
            outputTokens: result.outputTokens,
            costMicros: 0,
          };
          await options.usage.record(event);
          return result;
        } catch (error) {
          last = error;
          if (!retryable(error) || index === chain.length - 1) throw error;
        }
      }
      throw last instanceof Error ? last : new Error("模型路由失败");
    },
  };
}

export function fakeProvider(id = "fake"): ModelProvider {
  return {
    id,
    complete(request) {
      return Promise.resolve({
        text: `【${request.tier}】${request.prompt.slice(0, 80)}`,
        model: id,
        inputTokens: 1,
        outputTokens: 1,
      });
    },
  };
}

export function createOpenAICompatibleProvider(options: {
  id: string;
  apiKey: string;
  baseUrl: string;
  model: string;
  fetchImpl?: typeof fetch;
}): ModelProvider {
  const fetchImpl = options.fetchImpl ?? fetch;
  return {
    id: options.id,
    async complete(request) {
      const response = await fetchImpl(`${options.baseUrl.replace(/\/$/, "")}/chat/completions`, {
        method: "POST",
        headers: {
          authorization: `Bearer ${options.apiKey}`,
          "content-type": "application/json",
        },
        body: JSON.stringify({
          model: options.model,
          messages: [
            ...(request.system ? [{ role: "system", content: request.system }] : []),
            { role: "user", content: request.prompt },
          ],
        }),
      });
      if (response.status === 429 || response.status >= 500) {
        throw new RetryableModelError(`模型服务暂时失败：${response.status}`);
      }
      if (!response.ok) throw new Error(`模型服务拒绝了请求：${response.status}`);
      const body = (await response.json()) as {
        choices?: Array<{ message?: { content?: string } }>;
        usage?: { prompt_tokens?: number; completion_tokens?: number };
      };
      return {
        text: body.choices?.[0]?.message?.content ?? "",
        model: options.model,
        inputTokens: body.usage?.prompt_tokens ?? 0,
        outputTokens: body.usage?.completion_tokens ?? 0,
      };
    },
  };
}

/** 只有显式打开并且提供了密钥时才构造真实供应商。测试和 CI 不会走到这里。 */
export function providerFromEnv(env: NodeJS.ProcessEnv = process.env): ModelProvider | undefined {
  if (env.LW_LLM_ENABLE !== "1") return undefined;
  const apiKey = env.LW_LLM_API_KEY ?? env.OPENAI_API_KEY;
  if (!apiKey) return undefined;
  const baseUrl = env.LW_LLM_BASE_URL ?? "https://api.openai.com/v1";
  const model = env.LW_LLM_MODEL ?? "gpt-4.1-mini";
  return createOpenAICompatibleProvider({ id: "env", apiKey, baseUrl, model });
}

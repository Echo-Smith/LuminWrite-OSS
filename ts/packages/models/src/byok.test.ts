import {
  createAssistantMessageEventStream,
  createProvider,
  type Model,
} from "@earendil-works/pi-ai";
import { googleGenerativeAIApi } from "@earendil-works/pi-ai/api/google-generative-ai.lazy";
import { describe, expect, it } from "vitest";

import {
  byokFromBase,
  closedAuthContext,
  createClosedModels,
  GEMINI_NATIVE_BASE,
  geminiModelId,
  geminiOpenAiEndpoint,
} from "./byok.js";

describe("byok provider", () => {
  it("把 stream 委托回内置 provider，并把 model.provider 改回去；环境变量读不到 key", async () => {
    const secret = "lwtest_byok_delegate";
    let seenProvider = "";
    let seenKey = "";
    const baseId = "stub";
    const model: Model<"openai-completions"> = {
      id: "demo",
      name: "demo",
      api: "openai-completions",
      provider: baseId,
      baseUrl: "http://127.0.0.1",
      input: ["text"],
      cost: { input: 1, output: 1, cacheRead: 0, cacheWrite: 0 },
      reasoning: false,
      contextWindow: 1024,
      maxTokens: 16,
    };
    const base = createProvider({
      id: baseId,
      name: "Stub",
      baseUrl: model.baseUrl,
      auth: {
        apiKey: {
          name: "stub",
          resolve: async () => ({ auth: { apiKey: "from-base" }, source: "base" }),
        },
      },
      models: [model],
      api: {
        stream(requestModel, _context, options) {
          seenProvider = requestModel.provider;
          seenKey = options?.apiKey ?? "";
          const stream = createAssistantMessageEventStream();
          stream.push({
            type: "done",
            reason: "stop",
            message: {
              role: "assistant",
              content: [{ type: "text", text: "ok" }],
              api: "openai-completions",
              provider: baseId,
              model: requestModel.id,
              stopReason: "stop",
              timestamp: Date.now(),
              usage: {
                input: 1,
                output: 1,
                cacheRead: 0,
                cacheWrite: 0,
                totalTokens: 2,
                cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 },
              },
            },
          });
          stream.end();
          return stream;
        },
        streamSimple(requestModel, context, options) {
          return this.stream(requestModel, context, options);
        },
      },
    });
    const wrapped = byokFromBase(
      base,
      { id: "cred_delegate", label: "委托", baseUrl: null },
      async () => ({
        reveal: () => secret,
      }),
    );
    const models = createClosedModels();
    models.setProvider(wrapped);
    const previous = process.env.OPENAI_API_KEY;
    process.env.OPENAI_API_KEY = "lwtest_ambient_should_not_win";
    try {
      const resolved = models.getModel("byok_cred_delegate", "demo");
      if (!resolved) throw new Error("没有注册");
      const message = await models.completeSimple(resolved, {
        messages: [{ role: "user", content: "ping" }],
      });
      expect(message.stopReason).toBe("stop");
      expect(seenProvider).toBe(baseId);
      expect(seenKey).toBe(secret);
      expect(JSON.stringify(message)).not.toContain(secret);
      await expect(closedAuthContext().env("OPENAI_API_KEY")).resolves.toBeUndefined();
    } finally {
      if (previous === undefined) delete process.env.OPENAI_API_KEY;
      else process.env.OPENAI_API_KEY = previous;
    }
  });
});

describe("Gemini", () => {
  it("OpenAI 兼容端点去掉 store，路径仍落在 /openai/", () => {
    const endpoint = geminiOpenAiEndpoint(
      "https://generativelanguage.googleapis.com/v1beta/openai",
    );
    expect(endpoint.baseUrl.endsWith("/openai/")).toBe(true);
    expect(endpoint.compat).toEqual({ supportsStore: false });
    expect(geminiOpenAiEndpoint("https://api.openai.com/v1").compat).toBeUndefined();
    expect(geminiModelId("models/gemini-4-argon")).toBe("gemini-4-argon");
  });

  it("原生 generateContent 把密钥放在头里，工具写成 functionDeclarations", async () => {
    const key = "gemini-test-key-value";
    const seen: { url: string; header: string | null; body: string } = {
      url: "",
      header: null,
      body: "",
    };
    const original = globalThis.fetch;
    globalThis.fetch = async (input, init) => {
      seen.url = String(input);
      seen.header = new Headers(init?.headers).get("x-goog-api-key");
      seen.body = typeof init?.body === "string" ? init.body : "";
      return new Response(JSON.stringify({ error: { message: "停在测试" } }), { status: 400 });
    };
    try {
      const api = googleGenerativeAIApi() as {
        stream: (model: unknown, context: unknown, options: unknown) => AsyncIterable<unknown>;
      };
      const stream = api.stream(
        {
          id: "gemini-4-argon",
          name: "gemini-4-argon",
          api: "google-generative-ai",
          provider: "byok_test",
          baseUrl: GEMINI_NATIVE_BASE,
          input: ["text"],
          cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
          reasoning: false,
          contextWindow: 128000,
          maxTokens: 64,
        },
        {
          messages: [
            {
              role: "system",
              content: "只通过工具改稿。",
              toolsAdded: [
                {
                  name: "propose_edits",
                  description: "提交修订",
                  parameters: { type: "object", properties: {} },
                },
              ],
              timestamp: 0,
            },
            { role: "user", content: "写一段", timestamp: 1 },
          ],
        },
        { apiKey: key },
      );
      for await (const _event of stream) {
        // 等到请求结束。
      }
    } finally {
      globalThis.fetch = original;
    }
    const url = new URL(seen.url);
    expect(url.pathname).toBe("/v1beta/models/gemini-4-argon:streamGenerateContent");
    expect(url.search).not.toContain(key);
    expect(seen.header).toBe(key);
    const body = JSON.parse(seen.body) as {
      contents?: unknown[];
      systemInstruction?: { parts?: Array<{ text?: string }> };
      tools?: Array<{ functionDeclarations?: Array<{ name?: string }> }>;
    };
    expect(body.contents?.length).toBeGreaterThan(0);
    expect(body.systemInstruction?.parts?.[0]?.text).toContain("只通过工具改稿");
    expect(body.tools?.[0]?.functionDeclarations?.[0]?.name).toBe("propose_edits");
  });
});

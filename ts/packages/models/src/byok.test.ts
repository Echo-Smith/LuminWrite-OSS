import {
  createAssistantMessageEventStream,
  createProvider,
  type Model,
} from "@earendil-works/pi-ai";
import { describe, expect, it } from "vitest";

import { byokFromBase, closedAuthContext, createClosedModels } from "./byok.js";

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

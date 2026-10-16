import { describe, expect, it, vi } from "vitest";

import {
  createModelRouter,
  createOpenAICompatibleProvider,
  fakeProvider,
  providerFromEnv,
  RetryableModelError,
} from "./index.js";

describe("模型路由", () => {
  it("可重试的失败会落到下一家，用量只记成功的一次", async () => {
    const usage: Array<string> = [];
    let calls = 0;
    const flaky = {
      id: "flaky",
      complete() {
        calls += 1;
        return Promise.reject(new RetryableModelError("忙"));
      },
    };
    const router = createModelRouter({
      usage: {
        record(event) {
          usage.push(event.model);
          return Promise.resolve();
        },
      },
      providers: [flaky, fakeProvider("backup")],
      routes: { fast: ["flaky", "backup"], balanced: ["backup"], strong: ["backup"] },
    });
    const result = await router.complete({ tier: "fast", prompt: "显卡时租", requestId: "r1" });
    expect(result.model).toBe("backup");
    expect(result.text.startsWith("【fast】")).toBe(true);
    expect(calls).toBe(1);
    expect(usage).toEqual(["backup"]);
  });

  it("不可重试的错误不会继续", async () => {
    const backup = vi.fn();
    const router = createModelRouter({
      usage: { record: () => Promise.resolve() },
      providers: [
        {
          id: "bad",
          complete: () => Promise.reject(new Error("配置错误")),
        },
        { id: "backup", complete: backup },
      ],
      routes: { fast: ["bad", "backup"], balanced: ["backup"], strong: ["backup"] },
    });
    await expect(router.complete({ tier: "fast", prompt: "x", requestId: "r" })).rejects.toThrow(
      "配置错误",
    );
    expect(backup).not.toHaveBeenCalled();
  });

  it("默认不启用环境变量里的真实供应商", () => {
    expect(providerFromEnv({})).toBeUndefined();
    expect(providerFromEnv({ OPENAI_API_KEY: "present-but-disabled" })).toBeUndefined();
  });

  it("兼容接口只在注入的 fetch 上解析", async () => {
    const provider = createOpenAICompatibleProvider({
      id: "openai",
      apiKey: "test-key",
      baseUrl: "https://example.invalid/v1",
      model: "demo-model",
      fetchImpl: async () =>
        new Response(
          JSON.stringify({
            choices: [{ message: { content: "批大小从 1 提到 8。" } }],
            usage: { prompt_tokens: 4, completion_tokens: 2 },
          }),
          { status: 200 },
        ),
    });
    const result = await provider.complete({ tier: "balanced", prompt: "写一句", requestId: "r2" });
    expect(result).toMatchObject({
      text: "批大小从 1 提到 8。",
      model: "demo-model",
      inputTokens: 4,
    });
  });
});

import assert from "node:assert/strict";
import test from "node:test";

// —— node 环境的浏览器全局桩（auth-store 在模块加载期引用 window/localStorage）——
const memoryStorage: Record<string, string> = {};
const globalAny = globalThis as unknown as Record<string, unknown>;
globalAny.window = globalThis;
globalAny.localStorage = {
  getItem: (k: string) => memoryStorage[k] ?? null,
  setItem: (k: string, v: string) => { memoryStorage[k] = String(v); },
  removeItem: (k: string) => { delete memoryStorage[k]; },
  clear: () => { for (const k of Object.keys(memoryStorage)) delete memoryStorage[k]; },
};
if (!globalAny.navigator) globalAny.navigator = { userAgent: "node-test" };

// 全局桩就绪后再加载被测模块（ESM 静态 import 会先于桩执行）
const { useStyleChatStore } = await import("../src/stores/style-chat-store.ts");
const { useStyleListStore } = await import("../src/stores/style-list-store.ts");

/** node --test 环境无 fetch：注入最小 mock（按 URL 分流会话创建/发消息） */
const originalFetch = globalThis.fetch;
const sentBodies: string[] = [];

// @ts-expect-error 测试 stub 不完整实现 Response
const mockFetch: typeof fetch = async (input: Request | string, init?: { body?: string }) => {
  const url = typeof input === "string" ? input : input.url;
  if (url.endsWith("/style-builder/sessions")) {
    return { ok: true, json: async () => ({ success: true, data: { session_id: "s-1" } }) };
  }
  if (url.includes("/style-builder/sessions/s-1/messages")) {
    sentBodies.push(init?.body ?? "");
    return {
      ok: true,
      json: async () => ({ success: true, data: { message: "回复内容。", ready: true, profile: { name: "风格", description: "d" } } }),
    };
  }
  if (url.includes("/style-builder/sessions/s-1/commit")) {
    return { ok: true, json: async () => ({ success: true, data: {} }) };
  }
  throw new Error("unexpected fetch: " + url);
};

test.before(() => {
  globalThis.fetch = mockFetch;
});

test.after(() => {
  globalThis.fetch = originalFetch;
});

test("ready 延迟到 reveal 完成后才置位，保存卡与回复同步出现", async () => {
  const s = useStyleChatStore.getState();
  s.reset();
  useStyleChatStore.setState({ open: true });

  await s.send("想要克制的风格");

  const mid = useStyleChatStore.getState();
  assert.equal(mid.phase, "done");
  assert.equal(mid.ready, true, "reveal 完成后 ready 置位");
  const reply = mid.messages.find((m) => m.role === "assistant" && m.content.includes("回复内容"));
  assert.ok(reply, "回复消息已写入");
  assert.equal(reply?.revealing, false, "reveal 已结束");
});

test("commit 成功后发出风格列表刷新信号", async () => {
  // 测试执行顺序无关：确保会话存在（前序测试可能 reset 过）
  useStyleChatStore.setState({ sessionId: "s-1" });
  const before = useStyleListStore.getState().version;
  const ok = await useStyleChatStore.getState().commit();
  assert.equal(ok, true);
  assert.equal(useStyleListStore.getState().version, before + 1, "刷新信号已 bump");
});

test("send 进行中重复发送被守卫拦截", async () => {
  const s = useStyleChatStore.getState();
  s.reset();
  // 不等待完成，立刻二次发送应被拒绝（phase 守卫）
  const first = s.send("第一条");
  const countBefore = sentBodies.length;
  await s.send("第二条");
  assert.equal(sentBodies.length, countBefore, "busy 期间第二次 send 未发出网络请求");
  await first;
});

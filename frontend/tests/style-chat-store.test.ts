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

// —— 展示层净化：气泡不得出现 JSON / markdown 痕迹 ——

/** 可编程回复的 mock：注入一次后自动还原 */
async function withReplyOnce(message: string, run: () => Promise<void>) {
  const original = mockFetch;
  globalThis.fetch = async (input: Request | string, init?: { body?: string }) => {
    const url = typeof input === "string" ? input : input.url;
    if (url.includes("/style-builder/sessions/s-1/messages")) {
      return { ok: true, json: async () => ({ success: true, data: { message, ready: true, profile: { name: "n", description: "d" } } }) };
    }
    return original(input as Parameters<typeof fetch>[0], init as never);
  };
  try {
    await run();
  } finally {
    globalThis.fetch = original;
  }
}

test("裸 JSON 配置直出时气泡只保留对话前缀，无 JSON 痕迹", async () => {
  const s = useStyleChatStore.getState();
  s.reset();
  useStyleChatStore.setState({ sessionId: "s-1" });
  await withReplyOnce(
    '好的，风格已提炼完成：冷静克制、短句为主。\n\n{"slug":"calm","name":"冷静克制","system_prompt":"x","word_range":{"min":800,"max":1500}}',
    async () => {
      await s.send("帮我提炼");
    },
  );
  const reply = useStyleChatStore.getState().messages.filter((m) => m.role === "assistant").at(-1);
  assert.ok(reply);
  assert.ok(reply.content.includes("冷静克制、短句为主"), "对话前缀保留");
  assert.ok(!reply.content.includes("{"), "无 JSON 花括号");
  assert.ok(!reply.content.includes('"slug"'), "无 JSON 字段");
});

test("markdown 围栏 JSON、标题井号、加粗与行内代码不露出修饰符号", async () => {
  const s = useStyleChatStore.getState();
  s.reset();
  useStyleChatStore.setState({ sessionId: "s-1" });
  await withReplyOnce(
    '## 风格要点\n\n**短句**为主，用词`克制`。\n\n```json\n{"slug":"a","name":"A","system_prompt":"x"}\n```\n',
    async () => {
      await s.send("继续");
    },
  );
  const reply = useStyleChatStore.getState().messages.filter((m) => m.role === "assistant").at(-1);
  assert.ok(reply);
  assert.ok(!reply.content.includes("#"), "无标题井号");
  assert.ok(!reply.content.includes("**"), "无加粗星号");
  assert.ok(!reply.content.includes("`"), "无反引号");
  assert.ok(!reply.content.includes("```"), "无代码围栏");
  assert.ok(reply.content.includes("短句为主"), "正文保留（加粗已剥）");
  assert.ok(!reply.content.includes('"slug"'), "围栏内 JSON 已剥");
});

test("对话中的普通 JSON 举例（非风格配置）不被误删", async () => {
  const s = useStyleChatStore.getState();
  s.reset();
  useStyleChatStore.setState({ sessionId: "s-1" });
  const plain = '比如字数范围可以写成 {"min": 800, "max": 1500} 这样的结构，你觉得如何？';
  await withReplyOnce(plain, async () => {
    await s.send("字数怎么定");
  });
  const reply = useStyleChatStore.getState().messages.filter((m) => m.role === "assistant").at(-1);
  assert.ok(reply);
  assert.ok(reply.content.includes('{"min": 800, "max": 1500}'), "普通 JSON 举例保留");
});

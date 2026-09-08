/**
 * 实验室功能「研究综述」开关的设置存储测试：
 * - 默认关闭；
 * - 勾选后 PUT /api/v2/preferences 同步 enable_research_review；
 * - loadFromServer 能读回云端状态（跟随账号，换设备一致）。
 */
import assert from "node:assert/strict";
import test from "node:test";
import { useSettingsStore } from "../src/stores/settings-store.ts";

// settings-store 直接读 localStorage 里的 token（node 无该全局对象，注入最小 stub）。
const storage: Record<string, string> = {
  luminbuddy_auth: JSON.stringify({ token: "test-token" }),
};
(globalThis as unknown as { localStorage: unknown }).localStorage = {
  getItem: (key: string) => storage[key] ?? null,
  setItem: (key: string, value: string) => { storage[key] = value; },
  removeItem: (key: string) => { delete storage[key]; },
};

interface CapturedRequest {
  method: string;
  path: string;
  body: Record<string, unknown> | null;
}

function installPreferencesApi(respond: (req: CapturedRequest) => unknown, capture: CapturedRequest[]): void {
  (globalThis as unknown as { fetch: unknown }).fetch = (async (
    input: string | URL | Request,
    init?: { method?: string; body?: string },
  ) => {
    const path = typeof input === "string" ? input : input.toString();
    const method = init?.method ?? "GET";
    const body = init?.body ? (JSON.parse(init.body) as Record<string, unknown>) : null;
    capture.push({ method, path, body });
    const payload = respond({ method, path, body });
    return { ok: true, json: async () => payload } as Response;
  }) as typeof fetch;
}

function resetStore(): void {
  useSettingsStore.setState({ enableResearchReview: false, loaded: false });
}

const tick = () => new Promise((resolve) => setTimeout(resolve, 0));

test("research review lab toggle defaults to off", () => {
  resetStore();
  assert.equal(useSettingsStore.getState().enableResearchReview, false);
});

test("checking the lab toggle syncs enable_research_review to /preferences", async () => {
  resetStore();
  const capture: CapturedRequest[] = [];
  installPreferencesApi(() => ({ success: true, data: { saved: true } }), capture);

  useSettingsStore.getState().setEnableResearchReview(true);
  await tick();

  assert.equal(useSettingsStore.getState().enableResearchReview, true);
  const put = capture.find((request) => request.method === "PUT" && request.path === "/api/v2/preferences");
  assert.ok(put, "expected a PUT /api/v2/preferences");
  assert.equal(put.body?.enable_research_review, true);
});

test("loadFromServer restores the lab toggle from the cloud", async () => {
  resetStore();
  const capture: CapturedRequest[] = [];
  installPreferencesApi(() => ({ success: true, data: { enable_research_review: true, enable_editorial: false } }), capture);

  await useSettingsStore.getState().loadFromServer();

  assert.equal(useSettingsStore.getState().enableResearchReview, true);
  assert.equal(useSettingsStore.getState().enableEditorial, false);
  const get = capture.find((request) => request.method === "GET" && request.path === "/api/v2/preferences");
  assert.ok(get, "expected a GET /api/v2/preferences");
});

/**
 * packages-api 客户端测试（纯逻辑，stub fetch）：
 * - 信封解析：success 时取 data，失败时抛出带 code 的 Error；
 * - install 请求体序列化（builtin 带 slug / url 带 url）；
 * - uninstall 用 DELETE；
 * - 已安装列表按 kind 过滤拼 query。
 */
import assert from "node:assert/strict";
import test from "node:test";

interface Captured {
  path: string;
  method: string;
  body: Record<string, unknown> | null;
  headers: Record<string, string>;
}

const captured: Captured[] = [];
let nextResponse: unknown = { success: true, data: { ok: true } };

(globalThis as unknown as { fetch: unknown }).fetch = (async (
  input: string | URL | Request,
  init?: { method?: string; body?: string; headers?: Record<string, string> },
) => {
  const path = typeof input === "string" ? input : input.toString();
  captured.push({
    path,
    method: init?.method ?? "GET",
    body: init?.body ? (JSON.parse(init.body) as Record<string, unknown>) : null,
    headers: init?.headers ?? {},
  });
  return {
    ok: true,
    json: async () => nextResponse,
  } as Response;
}) as typeof fetch;

// auth-store 持有 token；直接设置 store 状态（node 无浏览器环境）。
const { useAuthStore } = await import("../src/stores/auth-store.ts");
useAuthStore.setState({ token: "pkg-token" });

const {
  listCatalog, listInstalledPackages, installPackage, uninstallPackage,
} = await import("../src/lib/packages-api.ts");

function last(): Captured {
  return captured[captured.length - 1];
}

test("listCatalog unwraps the data envelope", async () => {
  nextResponse = { success: true, data: { packages: [{ kind: "style", slug: "writer-starter", version: "1.0.0", title: "t", description: "d" }] } };
  const catalog = await listCatalog();
  assert.equal(catalog.length, 1);
  assert.equal(catalog[0].slug, "writer-starter");
  assert.equal(last().path, "/api/v2/packages/catalog");
  assert.equal(last().method, "GET");
});

test("installPackage posts the builtin body and unwraps the result", async () => {
  nextResponse = {
    success: true,
    data: { package: { id: "p1", slug: "writer-starter" }, style_profile_id: "s1" },
  };
  const result = await installPackage({ source: "builtin", slug: "writer-starter" });
  assert.equal(result.style_profile_id, "s1");
  assert.equal(last().path, "/api/v2/packages/install");
  assert.equal(last().method, "POST");
  assert.equal(last().body?.source, "builtin");
  assert.equal(last().body?.slug, "writer-starter");
  assert.equal(last().headers.Authorization, "Bearer pkg-token");
});

test("installPackage maps a failed envelope to a coded error", async () => {
  nextResponse = { success: false, error: { code: "already_installed", message: "已安装" } };
  await assert.rejects(
    () => installPackage({ source: "url", url: "https://example.com/p.zip" }),
    (err: Error & { code?: string }) => {
      assert.equal(err.code, "already_installed");
      assert.equal(err.message, "已安装");
      return true;
    },
  );
});

test("listInstalledPackages filters by kind", async () => {
  nextResponse = { success: true, data: { packages: [], total: 0 } };
  await listInstalledPackages("skill");
  assert.equal(last().path, "/api/v2/packages?kind=skill");
});

test("uninstallPackage issues DELETE", async () => {
  nextResponse = { success: true, data: { ok: true } };
  await uninstallPackage("pkg-1");
  assert.equal(last().path, "/api/v2/packages/pkg-1");
  assert.equal(last().method, "DELETE");
});

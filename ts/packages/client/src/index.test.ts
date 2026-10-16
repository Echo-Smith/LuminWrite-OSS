import { describe, expect, it } from "vitest";

import { createClient } from "./index.js";

describe("客户端", () => {
  it("把健康检查发给给定的 fetch", async () => {
    const client = createClient(
      "http://127.0.0.1:8787",
      async () => new Response(JSON.stringify({ ok: true })),
    );
    expect(await client.health()).toEqual({ ok: true });
  });

  it("建文档走 /api/v1，并带上所有者令牌", async () => {
    const seen: Array<{ url: string; authorization: string | null; body: string }> = [];
    const client = createClient(
      "http://127.0.0.1:8787",
      async (input, init) => {
        seen.push({
          url: String(input),
          authorization: new Headers(init?.headers).get("authorization"),
          body: String(init?.body ?? ""),
        });
        return new Response(
          JSON.stringify({ id: "doc_1", versionId: "ver_1", sessionId: "ses_1" }),
        );
      },
      { token: "lwo_test" },
    );
    await client.createDocument({ title: "一篇" });
    expect(seen[0]?.url).toBe("http://127.0.0.1:8787/api/v1/documents");
    expect(seen[0]?.authorization).toBe("Bearer lwo_test");
    expect(seen[0]?.body).not.toContain("ownerId");
    expect(seen[0]?.body).not.toContain("actor");
  });

  it("保存和回滚走文档接口", async () => {
    const seen: string[] = [];
    const client = createClient("http://127.0.0.1:8787", async (input, init) => {
      seen.push(`${init?.method ?? "GET"} ${String(input)}`);
      return new Response(
        JSON.stringify({ savedAt: "t", already: false, ok: true, discardedAt: "t" }),
      );
    });
    await client.saveDocument("doc_1");
    await client.restoreDocument("doc_1", "ver_1");
    await client.discardDocument("doc_1");
    await client.undoDiscard("doc_1");
    await client.uploadSource({ filename: "a.md", dataBase64: "YQ==" });
    await client.searchSources("春茶");
    expect(seen).toEqual([
      "POST http://127.0.0.1:8787/api/v1/documents/doc_1/save",
      "POST http://127.0.0.1:8787/api/v1/documents/doc_1/restore",
      "POST http://127.0.0.1:8787/api/v1/documents/doc_1/discard",
      "POST http://127.0.0.1:8787/api/v1/documents/doc_1/discard/undo",
      "POST http://127.0.0.1:8787/api/v1/sources",
      "GET http://127.0.0.1:8787/api/v1/sources/search?q=%E6%98%A5%E8%8C%B6",
    ]);
  });
});

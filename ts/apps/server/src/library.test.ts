import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createApiKey, ensureOwner, openMemoryDatabase, readOwnerToken } from "@luminwrite/db";
import type { WritingRuntime } from "@luminwrite/harness";
import { describe, expect, it } from "vitest";

import { requiredScope } from "./api-access.js";
import { createApp } from "./app.js";
import { createOssAuth } from "./auth.js";

function stubRuntime(): WritingRuntime {
  return {
    async submit() {
      return { submissionId: "sub" };
    },
    async abort() {},
    async resume() {},
    async *watch() {},
    async close() {},
    async events() {
      return [];
    },
    async readRelatedDossier() {
      throw new Error("未实现");
    },
    async submitTurn() {
      return { turnId: "trn_stub", status: "applied", submissionId: "sub" };
    },
  };
}

async function boot() {
  const dir = await mkdtemp(join(tmpdir(), "lw-library-"));
  const dataDir = join(dir, "data");
  const db = await openMemoryDatabase();
  const owner = await ensureOwner(db, { dataDir });
  const token = await readOwnerToken(dataDir);
  const runtime = stubRuntime();
  const app = createApp({ db, runtime, auth: createOssAuth(db), env: {} });
  const request = (path: string, init?: RequestInit, bearer: string | undefined = token) => {
    const headers = new Headers(init?.headers);
    if (bearer) headers.set("authorization", `Bearer ${bearer}`);
    if (init?.body && !headers.has("content-type")) headers.set("content-type", "application/json");
    return app.request(path, { ...init, headers });
  };
  return {
    db,
    owner,
    request,
    async close() {
      await runtime.close();
      await db.close();
      await rm(dir, { recursive: true, force: true });
    },
  };
}

describe("文章库 API", () => {
  it("列表、搜索、标签和归档都走已有的丢弃语义", async () => {
    expect(requiredScope("GET", "/api/v1/library/articles")).toBe("library:read");
    expect(requiredScope("POST", "/api/v1/library/articles/doc_1/tags")).toBe("documents:write");
    expect(requiredScope("POST", "/api/v1/library/purge")).toBe("owner-only");

    const ctx = await boot();
    try {
      const created = await ctx.request("/api/v1/documents", {
        method: "POST",
        body: JSON.stringify({ title: "时租笔记", genre: "评论" }),
      });
      expect(created.status).toBe(201);
      const documentId = ((await created.json()) as { id: string }).id;
      const beforeSave = await ctx.request("/api/v1/library/articles");
      expect(await beforeSave.json()).toEqual([]);
      const unsavedTag = await ctx.request(`/api/v1/library/articles/${documentId}/tags`, {
        method: "POST",
        body: JSON.stringify({ name: "时租" }),
      });
      expect(unsavedTag.status).toBe(409);

      const saved = await ctx.request(`/api/v1/documents/${documentId}/save`, { method: "POST" });
      expect(saved.status).toBe(200);
      const seeded = await ctx.request(`/api/v1/documents/${documentId}/ops`, {
        method: "POST",
        body: JSON.stringify({
          ops: [
            {
              op: "insert_block",
              afterBlockId: null,
              newMarkdown: "一台 A10 按小时计费。",
              reason: "起稿",
            },
          ],
        }),
      });
      expect(seeded.status).toBe(200);

      const other = await ctx.request("/api/v1/documents", {
        method: "POST",
        body: JSON.stringify({ title: "另一篇", genre: "报告" }),
      });
      const otherId = ((await other.json()) as { id: string }).id;
      await ctx.request(`/api/v1/documents/${otherId}/save`, { method: "POST" });

      const tagged = await ctx.request(`/api/v1/library/articles/${documentId}/tags`, {
        method: "POST",
        body: JSON.stringify({ name: " 时租 " }),
      });
      expect(tagged.status).toBe(200);
      expect(await tagged.json()).toEqual({ tags: ["时租"] });
      const again = await ctx.request(`/api/v1/library/articles/${documentId}/tags`, {
        method: "POST",
        body: JSON.stringify({ name: "成本" }),
      });
      expect(await again.json()).toEqual({ tags: ["成本", "时租"] });

      const listed = await ctx.request(
        `/api/v1/library/articles?tag=时租&tag=成本&q=${encodeURIComponent("按小时")}`,
      );
      const articles = (await listed.json()) as Array<{ id: string; status: string }>;
      expect(articles.map((item) => item.id)).toEqual([documentId]);
      expect(articles[0]?.status).toBe("active");

      const byGenre = await ctx.request(
        `/api/v1/library/articles?genre=${encodeURIComponent("报告")}`,
      );
      expect(((await byGenre.json()) as Array<{ id: string }>).map((item) => item.id)).toEqual([
        otherId,
      ]);

      const archived = await ctx.request(`/api/v1/library/articles/${documentId}/archive`, {
        method: "POST",
      });
      expect(archived.status).toBe(200);
      expect(await archived.json()).toMatchObject({ status: "archived" });
      const active = (await (await ctx.request("/api/v1/library/articles")).json()) as Array<{
        id: string;
      }>;
      expect(active.map((item) => item.id)).toEqual([otherId]);
      const onlyArchived = (await (
        await ctx.request("/api/v1/library/articles?archived=archived")
      ).json()) as Array<{ id: string; tags: string[] }>;
      expect(onlyArchived.map((item) => item.id)).toEqual([documentId]);
      expect(onlyArchived[0]?.tags).toEqual(["成本", "时租"]);

      const undone = await ctx.request(`/api/v1/library/articles/${documentId}/archive/undo`, {
        method: "POST",
      });
      expect(await undone.json()).toMatchObject({ status: "active", discardedAt: null });
      await ctx.request(`/api/v1/library/articles/${documentId}/archive`, { method: "POST" });
      const purged = await ctx.request("/api/v1/library/purge", {
        method: "POST",
        body: JSON.stringify({ retentionDays: 0 }),
      });
      expect(await purged.json()).toEqual({ purged: [documentId] });
      expect((await ctx.request(`/api/v1/library/articles/${documentId}`)).status).toBe(404);
      expect(await ctx.request("/api/v1/library/tags").then((response) => response.json())).toEqual(
        [],
      );

      const reader = await createApiKey(ctx.db, {
        userId: ctx.owner.userId,
        name: "只读文库",
        scopes: ["library:read"],
      });
      const readList = await ctx.request("/api/v1/library/articles", undefined, reader.token);
      expect(readList.status).toBe(200);
      const denied = await ctx.request(
        `/api/v1/library/articles/${otherId}/tags`,
        { method: "POST", body: JSON.stringify({ name: "不能写" }) },
        reader.token,
      );
      expect(denied.status).toBe(403);
      const writer = await createApiKey(ctx.db, {
        userId: ctx.owner.userId,
        name: "不能看文库",
        scopes: ["documents:write"],
      });
      const hidden = await ctx.request("/api/v1/library/articles", undefined, writer.token);
      expect(hidden.status).toBe(403);
      const keyPurge = await ctx.request(
        "/api/v1/library/purge",
        { method: "POST", body: JSON.stringify({ retentionDays: 0 }) },
        reader.token,
      );
      expect(keyPurge.status).toBe(403);
    } finally {
      await ctx.close();
    }
  });
});

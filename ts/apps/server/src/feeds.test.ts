import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createApiKey, ensureOwner, openMemoryDatabase, readOwnerToken } from "@luminwrite/db";
import type { WritingRuntime } from "@luminwrite/harness";
import { createKnowledgeBase } from "@luminwrite/kb";
import { createFeedService, type PageFetch } from "@luminwrite/sources";
import { describe, expect, it } from "vitest";

import { createApp } from "./app.js";
import { createOssAuth } from "./auth.js";

const FEED = `<?xml version="1.0"?><rss version="2.0"><channel><title>示例</title><item><title>条目</title><guid>a</guid><description>订阅正文独有句子</description></item></channel></rss>`;

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

describe("订阅和网址导入", () => {
  it("所有者可以订阅和导入，默认 API key 只能读来源", async () => {
    const dir = await mkdtemp(join(tmpdir(), "lw-feeds-http-"));
    const dataDir = join(dir, "data");
    const db = await openMemoryDatabase();
    const owner = await ensureOwner(db, { dataDir });
    const token = await readOwnerToken(dataDir);
    const runtime = stubRuntime();
    const fetchPage: PageFetch = async (url) => {
      if (url.endsWith(".xml")) {
        return { status: 200, body: FEED, finalUrl: url, etag: "v1" };
      }
      return {
        status: 200,
        body: "<html><head><title>导入页</title></head><body><article><p>导入正文独有句子</p></article></body></html>",
        finalUrl: url,
        contentType: "text/html",
      };
    };
    const kb = createKnowledgeBase({ db });
    const feeds = createFeedService({ db, kb, fetch: fetchPage });
    const app = createApp({
      db,
      runtime,
      auth: createOssAuth(db),
      kb,
      feeds,
      pageFetch: fetchPage,
    });
    const request = (path: string, init?: RequestInit, bearer = token) => {
      const headers = new Headers(init?.headers);
      if (bearer) headers.set("authorization", `Bearer ${bearer}`);
      if (init?.body && !headers.has("content-type")) {
        headers.set("content-type", "application/json");
      }
      return app.request(path, { ...init, headers });
    };
    try {
      const subscribed = await request("/api/v1/feeds", {
        method: "POST",
        body: JSON.stringify({
          url: "https://example.com/feed.xml",
          isHotspot: true,
          ownerId: "user_spoof",
        }),
      });
      expect(subscribed.status).toBe(201);
      const created = (await subscribed.json()) as {
        added: number;
        feed: { ownerId: string; isHotspot: boolean };
      };
      expect(created.added).toBe(1);
      expect(created.feed.ownerId).toBe(owner.userId);
      expect(created.feed.isHotspot).toBe(true);

      const key = await createApiKey(db, { userId: owner.userId, name: "只读来源" });
      const deniedFeed = await request(
        "/api/v1/feeds",
        { method: "POST", body: JSON.stringify({ url: "https://example.com/other.xml" }) },
        key.token,
      );
      expect(deniedFeed.status).toBe(403);
      const deniedUrl = await request(
        "/api/v1/sources/url",
        { method: "POST", body: JSON.stringify({ url: "https://example.com/page" }) },
        key.token,
      );
      expect(deniedUrl.status).toBe(403);
      const found = await request(
        "/api/v1/sources/search?q=订阅正文独有句子",
        { method: "GET" },
        key.token,
      );
      expect(found.status).toBe(200);
      const hits = (await found.json()) as { hits: Array<{ kind: string }> };
      expect(hits.hits.some((hit) => hit.kind === "rss_item")).toBe(true);

      const imported = await request("/api/v1/sources/url", {
        method: "POST",
        body: JSON.stringify({ url: "https://example.com/page" }),
      });
      expect(imported.status).toBe(201);
      const exported = await request("/api/v1/feeds/opml");
      expect(exported.status).toBe(200);
      expect((await exported.json()) as { xml: string }).toMatchObject({
        xml: expect.stringContaining("https://example.com/feed.xml"),
      });
    } finally {
      await runtime.close();
      await db.close();
      await rm(dir, { recursive: true, force: true });
    }
  });
});

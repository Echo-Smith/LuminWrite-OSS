import { newId, openMemoryDatabase } from "@luminwrite/db";
import { createInlineJobs } from "@luminwrite/jobs";
import { createKnowledgeBase } from "@luminwrite/kb";
import { describe, expect, it } from "vitest";

import { canonicalUrl, createFeedService, importUrl, type PageFetch } from "./index.js";

function rss(items: string): string {
  return `<?xml version="1.0"?><rss version="2.0"><channel><title>示例</title>${items}</channel></rss>`;
}

function item(input: {
  title: string;
  guid: string;
  body: string;
  when?: string;
  link?: string;
}): string {
  return `<item><title>${input.title}</title><guid>${input.guid}</guid><description>${input.body}</description>${
    input.link ? `<link>${input.link}</link>` : ""
  }${input.when ? `<pubDate>${input.when}</pubDate>` : ""}</item>`;
}

function page(
  body: string,
  extra?: { status?: number; etag?: string },
): Awaited<ReturnType<PageFetch>> {
  return {
    status: extra?.status ?? 200,
    body: extra?.status === 304 ? null : body,
    finalUrl: "https://example.com/feed.xml",
    ...(extra?.etag ? { etag: extra.etag } : {}),
  };
}

describe("RSS", () => {
  it("同一 guid 不重复，304 不重新入库，内容变了才升修订", async () => {
    const db = await openMemoryDatabase();
    try {
      const kb = createKnowledgeBase({ db });
      let calls = 0;
      const fetch: PageFetch = async (_url, options) => {
        calls += 1;
        if (calls === 2 && options?.headers?.["If-None-Match"] === "v1") {
          return page("", { status: 304, etag: "v1" });
        }
        if (calls >= 3) {
          return page(rss(item({ title: "甲", guid: "a", body: "改过的正文" })), { etag: "v2" });
        }
        return page(rss(item({ title: "甲", guid: "a", body: "原来的正文" })), { etag: "v1" });
      };
      const feeds = createFeedService({ db, kb, fetch });
      const feed = await feeds.subscribe("user_1", { url: "https://example.com/feed.xml" });
      const first = await feeds.refresh(feed.id);
      expect(first).toMatchObject({ added: 1, notModified: false });
      const second = await feeds.refresh(feed.id);
      expect(second).toEqual({ added: 0, skipped: 0, notModified: true });
      const third = await feeds.refresh(feed.id);
      expect(third.added).toBe(0);
      expect(third.notModified).toBe(false);
      const rows = await db.query<{ revision: number | string; body: string }>(
        `SELECT revision, body FROM sources WHERE feed_id = $1`,
        [feed.id],
      );
      expect(rows).toHaveLength(1);
      expect(Number(rows[0]?.revision)).toBe(2);
      expect(rows[0]?.body).toContain("改过的正文");
      const hits = await kb.search({ ownerId: "user_1", text: "改过的正文", limit: 5 });
      expect(hits[0]?.kind).toBe("rss_item");
      expect(hits[0]?.text).toContain("改过的正文");
    } finally {
      await db.close();
    }
  });

  it("每次只收录上限内的新条目", async () => {
    const db = await openMemoryDatabase();
    try {
      const kb = createKnowledgeBase({ db });
      const xml = rss(
        ["一", "二", "三", "四", "五"]
          .map((title) => item({ title, guid: title, body: `${title}的正文` }))
          .join(""),
      );
      const feeds = createFeedService({
        db,
        kb,
        globalCap: 4,
        fetch: async () => page(xml),
      });
      const first = await feeds.subscribe("user_1", { url: "https://example.com/a.xml" });
      const second = await feeds.subscribe("user_1", { url: "https://example.com/b.xml" });
      const tick = new Map<string, number>();
      const left = await feeds.refresh(first.id, tick);
      const right = await feeds.refresh(second.id, tick);
      expect(left.added).toBe(3);
      expect(right.added).toBe(1);
      const rows = await db.query<{ id: string }>(`SELECT id FROM sources WHERE kind = 'rss_item'`);
      expect(rows).toHaveLength(4);
    } finally {
      await db.close();
    }
  });

  it("过期且没有引用的条目会删，被引用的留下", async () => {
    const db = await openMemoryDatabase();
    try {
      const kb = createKnowledgeBase({ db });
      const xml = rss(
        [
          item({
            title: "旧的",
            guid: "old",
            body: "很久以前的正文",
            when: "Mon, 01 Jan 2001 00:00:00 GMT",
          }),
          item({
            title: "被引用",
            guid: "cited",
            body: "被引用的旧正文",
            when: "Tue, 02 Jan 2001 00:00:00 GMT",
          }),
          item({
            title: "新的",
            guid: "new",
            body: "今天的正文",
            when: "Wed, 14 Oct 2026 00:00:00 GMT",
          }),
        ].join(""),
      );
      const feeds = createFeedService({ db, kb, fetch: async () => page(xml) });
      const feed = await feeds.subscribe("user_1", { url: "https://example.com/feed.xml" });
      await db.query(
        `UPDATE feeds SET max_items_per_tick = 10, retain_days = 100000 WHERE id = $1`,
        [feed.id],
      );
      expect((await feeds.refresh(feed.id)).added).toBe(3);
      const cited = await db.query<{ id: string }>(
        `SELECT id FROM sources WHERE feed_guid = 'cited'`,
      );
      if (!cited[0]) throw new Error("没有被引用的条目");
      await db.query(
        `INSERT INTO evidence (
           id, owner_id, citing_document_id, source_id, source_revision, quote, locator, authority
         ) VALUES ($1, $2, 'doc_x', $3, 1, '被引用的旧正文', '{"charStart":0,"charEnd":4}'::jsonb, 2)`,
        [newId("evd"), "user_1", cited[0].id],
      );
      await db.query(`UPDATE feeds SET retain_days = 30 WHERE id = $1`, [feed.id]);
      await feeds.refresh(feed.id);
      const titles = await db.query<{ title: string }>(
        `SELECT title FROM sources WHERE feed_id = $1 ORDER BY title`,
        [feed.id],
      );
      expect(titles.map((row) => row.title)).toEqual(["新的", "被引用"]);
    } finally {
      await db.close();
    }
  });

  it("OPML 往返后地址一致，热点标记只存列", async () => {
    const db = await openMemoryDatabase();
    try {
      const kb = createKnowledgeBase({ db });
      const feeds = createFeedService({
        db,
        kb,
        fetch: async () => page(rss(item({ title: "甲", guid: "a", body: "正文" }))),
      });
      const xml = `<?xml version="1.0"?><opml version="2.0"><body><outline text="外层"><outline text="示例" xmlUrl="https://example.com/feed.xml" /></outline></body></opml>`;
      const imported = await feeds.importOpml("user_1", xml);
      expect(imported.added).toBe(1);
      expect(imported.failed).toEqual([]);
      const again = await feeds.importOpml("user_1", await feeds.exportOpml("user_1"));
      expect(again.added).toBe(0);
      const listed = await feeds.list("user_1");
      expect(listed.map((feed) => feed.url)).toEqual(["https://example.com/feed.xml"]);
      const hot = await feeds.subscribe("user_1", {
        url: "https://example.com/hot.xml",
        isHotspot: true,
      });
      expect(hot.isHotspot).toBe(true);
      expect(canonicalUrl("HTTPS://Example.com/a/#x")).toBe("https://example.com/a");
    } finally {
      await db.close();
    }
  });

  it("全文抓取失败时退回摘要，调度会拉全部订阅", async () => {
    const db = await openMemoryDatabase();
    try {
      const kb = createKnowledgeBase({ db });
      const jobs = createInlineJobs();
      const fetch: PageFetch = async (url) => {
        if (url.endsWith("/article")) throw new Error("原文失败");
        return page(
          rss(
            item({
              title: "甲",
              guid: "a",
              body: "只有摘要",
              link: "https://example.com/article",
            }),
          ),
        );
      };
      const feeds = createFeedService({ db, kb, fetch, jobs });
      await feeds.start();
      const feed = await feeds.subscribe("user_1", {
        url: "https://example.com/feed.xml",
        fullText: true,
      });
      await jobs.advance(15 * 60 * 1000);
      const rows = await db.query<{ body: string }>(`SELECT body FROM sources WHERE feed_id = $1`, [
        feed.id,
      ]);
      expect(rows[0]?.body).toContain("只有摘要");
      const other = await importUrl({
        kb,
        ownerId: "user_1",
        url: "https://example.com/page",
        fetch: async () => ({
          status: 200,
          body: "<html><head><title>导入</title></head><body><article><p>网页正文在这里。</p></article></body></html>",
          finalUrl: "https://example.com/page",
          contentType: "text/html",
        }),
      });
      expect(other.status).toBe("indexed");
      const found = await kb.readSource("user_1", other.sourceId);
      expect(found.body).toContain("网页正文");
      await jobs.close();
    } finally {
      await db.close();
    }
  });
});

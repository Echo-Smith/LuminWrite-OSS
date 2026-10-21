import type { LwDatabase } from "@luminwrite/db";
import { newId } from "@luminwrite/db";
import type { JobPort } from "@luminwrite/jobs";
import { KbInputError, type KnowledgeBase } from "@luminwrite/kb";
import { generateOpml, parseFeed, parseOpml } from "feedsmith";

import { extractArticle, looksLikeHtml, stripTags } from "./article.js";
import type { SafeFetchResult } from "./safe-fetch.js";
import { SafeFetchError } from "./safe-fetch.js";

const RSS_FETCH = "rss.fetch";
const SCAN_LIMIT = 50;
const DEFAULT_GLOBAL_CAP = 20;

export class FeedError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "FeedError";
  }
}

export interface Feed {
  id: string;
  ownerId: string;
  url: string;
  folderId: string;
  title: string | null;
  fullText: boolean;
  isHotspot: boolean;
  maxItemsPerTick: number;
  retainDays: number;
  etag: string | null;
  lastModified: string | null;
  failCount: number;
  lastError: string | null;
  lastFetchedAt: string | null;
}

export interface RefreshResult {
  added: number;
  skipped: number;
  notModified: boolean;
}

export type PageFetch = (
  url: string,
  options?: { headers?: Record<string, string> },
) => Promise<SafeFetchResult>;

export interface FeedService {
  subscribe(
    ownerId: string,
    input: { url: string; folderId?: string; fullText?: boolean; isHotspot?: boolean },
  ): Promise<Feed>;
  refresh(feedId: string, tick?: Map<string, number>): Promise<RefreshResult>;
  importOpml(ownerId: string, xml: string): Promise<{ added: number; failed: string[] }>;
  exportOpml(ownerId: string): Promise<string>;
  unsubscribe(feedId: string, opts: { keepItems: boolean; ownerId?: string }): Promise<void>;
  list(ownerId: string): Promise<Feed[]>;
  get(feedId: string): Promise<Feed | undefined>;
  start(): Promise<void>;
}

interface FeedRow {
  id: string;
  owner_id: string;
  url: string;
  folder_id: string;
  title: string | null;
  full_text: boolean;
  is_hotspot: boolean;
  max_items_per_tick: number | string;
  retain_days: number | string;
  etag: string | null;
  last_modified: string | null;
  fail_count: number | string;
  last_error: string | null;
  last_fetched_at: string | Date | null;
}

interface NormalizedItem {
  guid?: string;
  link?: string;
  title: string;
  summary: string;
  publishedAt?: string;
  author?: string;
}

function num(value: number | string): number {
  return typeof value === "number" ? value : Number(value);
}

function mapFeed(row: FeedRow): Feed {
  const fetched = row.last_fetched_at;
  return {
    id: row.id,
    ownerId: row.owner_id,
    url: row.url,
    folderId: row.folder_id,
    title: row.title,
    fullText: row.full_text === true,
    isHotspot: row.is_hotspot === true,
    maxItemsPerTick: num(row.max_items_per_tick),
    retainDays: num(row.retain_days),
    etag: row.etag,
    lastModified: row.last_modified,
    failCount: num(row.fail_count),
    lastError: row.last_error,
    lastFetchedAt: fetched ? new Date(fetched).toISOString() : null,
  };
}

function plainText(value: unknown): string {
  if (typeof value === "string") return value;
  if (value && typeof value === "object" && "value" in value) {
    const inner = (value as { value?: unknown }).value;
    return typeof inner === "string" ? inner : "";
  }
  return "";
}

function dateOf(value: unknown): string | undefined {
  if (value instanceof Date && !Number.isNaN(value.getTime())) return value.toISOString();
  if (typeof value === "string" && value.trim()) {
    const parsed = Date.parse(value);
    if (Number.isFinite(parsed)) return new Date(parsed).toISOString();
  }
  return undefined;
}

function personName(people: Array<{ name?: string }> | undefined): string | undefined {
  const name = people?.[0]?.name?.trim();
  return name || undefined;
}

export function canonicalUrl(raw: string): string {
  const url = new URL(raw);
  url.hash = "";
  url.hostname = url.hostname.toLowerCase();
  if (url.pathname.length > 1) url.pathname = url.pathname.replace(/\/+$/, "");
  return url.toString();
}

function assertHttpUrl(raw: string): string {
  const url = raw.trim();
  let parsed: URL;
  try {
    parsed = new URL(url);
  } catch {
    throw new FeedError("只接受 http 和 https");
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
    throw new FeedError("只接受 http 和 https");
  }
  return url;
}

function folderName(url: string, title?: string): string {
  const raw = title?.trim() || new URL(url).hostname;
  return raw.slice(0, 80) || "订阅";
}

function atomLink(links: Array<{ href?: string; rel?: string }> | undefined): string | undefined {
  if (!links || links.length === 0) return undefined;
  const alternate = links.find((link) => !link.rel || link.rel === "alternate");
  return (alternate ?? links[0])?.href;
}

interface LooseItem {
  title?: unknown;
  link?: string;
  description?: string;
  guid?: { value?: string };
  pubDate?: unknown;
  content?: { encoded?: string; value?: string };
  authors?: Array<{ name?: string }>;
  id?: string;
  links?: Array<{ href?: string; rel?: string }>;
  published?: unknown;
  updated?: unknown;
  summary?: unknown;
  url?: string;
  content_html?: string;
  content_text?: string;
  date_published?: unknown;
}

function looseItems(feed: object): LooseItem[] {
  const record = feed as { items?: unknown; entries?: unknown };
  const list = record.items ?? record.entries;
  return Array.isArray(list) ? (list as LooseItem[]) : [];
}

function normalizeItem(item: LooseItem): NormalizedItem {
  const guid = item.guid?.value?.trim() || item.id?.trim();
  const link = item.link || item.url || atomLink(item.links);
  const publishedAt =
    dateOf(item.pubDate) ??
    dateOf(item.published) ??
    dateOf(item.updated) ??
    dateOf(item.date_published);
  const author = personName(item.authors);
  const summary = stripTags(
    item.content?.encoded ||
      plainText(item.content) ||
      item.content_text ||
      item.content_html ||
      item.description ||
      plainText(item.summary) ||
      plainText(item.title),
  );
  return {
    ...(guid ? { guid } : {}),
    ...(link ? { link } : {}),
    title: plainText(item.title) || "未命名",
    summary,
    ...(publishedAt ? { publishedAt } : {}),
    ...(author ? { author } : {}),
  };
}

function normalizeFeed(xml: string): { title?: string; items: NormalizedItem[] } {
  let parsed: ReturnType<typeof parseFeed>;
  try {
    parsed = parseFeed(xml);
  } catch {
    throw new FeedError("订阅内容无法解析");
  }
  const title = plainText(parsed.feed.title).trim();
  return {
    ...(title ? { title } : {}),
    items: looseItems(parsed.feed).map(normalizeItem),
  };
}

function itemGuid(item: NormalizedItem): string | undefined {
  if (item.guid?.trim()) return item.guid.trim();
  if (!item.link) return undefined;
  try {
    return canonicalUrl(item.link);
  } catch {
    return undefined;
  }
}

function sortItems(items: NormalizedItem[]): NormalizedItem[] {
  return items
    .map((item, index) => ({
      item,
      index,
      time: item.publishedAt ? Date.parse(item.publishedAt) : Number.NaN,
    }))
    .sort((left, right) => {
      const leftOk = Number.isFinite(left.time);
      const rightOk = Number.isFinite(right.time);
      if (leftOk && rightOk && left.time !== right.time) return right.time - left.time;
      if (leftOk !== rightOk) return leftOk ? -1 : 1;
      return left.index - right.index;
    })
    .map((entry) => entry.item);
}

async function loadFeed(db: LwDatabase, feedId: string): Promise<Feed | undefined> {
  const rows = await db.query<FeedRow>(`SELECT * FROM feeds WHERE id = $1`, [feedId]);
  const row = rows[0];
  return row ? mapFeed(row) : undefined;
}

export function createFeedService(input: {
  db: LwDatabase;
  kb: KnowledgeBase;
  fetch?: PageFetch;
  jobs?: JobPort;
  globalCap?: number;
}): FeedService {
  const db = input.db;
  const kb = input.kb;
  const jobs = input.jobs;
  const globalCap = input.globalCap ?? DEFAULT_GLOBAL_CAP;
  const fetchPage: PageFetch =
    input.fetch ??
    (async () => {
      throw new SafeFetchError("测试不访问网络");
    });

  async function subscribe(
    ownerId: string,
    spec: { url: string; folderId?: string; fullText?: boolean; isHotspot?: boolean },
  ): Promise<Feed> {
    const url = assertHttpUrl(spec.url);
    const existing = await db.query<FeedRow>(
      `SELECT * FROM feeds WHERE owner_id = $1 AND url = $2 LIMIT 1`,
      [ownerId, url],
    );
    const found = existing[0];
    if (found) return mapFeed(found);
    let folderId = spec.folderId;
    if (folderId) {
      const folder = await db.query<{ id: string }>(
        `SELECT id FROM folders WHERE id = $1 AND owner_id = $2`,
        [folderId, ownerId],
      );
      if (!folder[0]) throw new FeedError("找不到文件夹");
    } else {
      const created = await kb.createFolder({ ownerId, name: folderName(url) });
      folderId = created.id;
    }
    const id = newId("feed");
    await db.query(
      `INSERT INTO feeds (
         id, owner_id, url, folder_id, full_text, is_hotspot
       ) VALUES ($1, $2, $3, $4, $5, $6)`,
      [id, ownerId, url, folderId, spec.fullText === true, spec.isHotspot === true],
    );
    if (jobs) {
      await jobs.send(RSS_FETCH, { feedId: id }, { singletonKey: `rss:${id}` });
    }
    const created = await loadFeed(db, id);
    if (!created) throw new FeedError("找不到订阅");
    return created;
  }

  async function retain(feed: Feed): Promise<void> {
    await db.query(
      `DELETE FROM sources
       WHERE kind = 'rss_item'
         AND owner_id = $1
         AND feed_id = $2
         AND COALESCE(published_at, created_at) < now() - ($3::int * interval '1 day')
         AND NOT EXISTS (SELECT 1 FROM evidence e WHERE e.source_id = sources.id)`,
      [feed.ownerId, feed.id, feed.retainDays],
    );
  }

  async function markSuccess(
    feed: Feed,
    patch: { title?: string; etag: string | null; lastModified: string | null },
  ): Promise<void> {
    await db.query(
      `UPDATE feeds
       SET title = COALESCE($2, title),
           etag = $3,
           last_modified = $4,
           fail_count = 0,
           last_error = NULL,
           last_fetched_at = now()
       WHERE id = $1`,
      [feed.id, patch.title ?? null, patch.etag, patch.lastModified],
    );
  }

  async function markFailure(feedId: string, message: string): Promise<void> {
    await db.query(
      `UPDATE feeds
       SET fail_count = fail_count + 1, last_error = $2, last_fetched_at = now()
       WHERE id = $1`,
      [feedId, message.slice(0, 500)],
    );
  }

  async function refresh(
    feedId: string,
    tick: Map<string, number> = new Map(),
  ): Promise<RefreshResult> {
    const feed = await loadFeed(db, feedId);
    if (!feed) throw new FeedError("找不到订阅");
    try {
      const headers: Record<string, string> = {};
      if (feed.etag) headers["If-None-Match"] = feed.etag;
      if (feed.lastModified) headers["If-Modified-Since"] = feed.lastModified;
      const response = await fetchPage(feed.url, { headers });
      if (response.status === 304) {
        await markSuccess(feed, {
          etag: response.etag ?? feed.etag,
          lastModified: response.lastModified ?? feed.lastModified,
        });
        await retain(feed);
        return { added: 0, skipped: 0, notModified: true };
      }
      if (response.status < 200 || response.status >= 300 || response.body == null) {
        throw new FeedError(`抓取失败（${response.status}）`);
      }
      const parsed = normalizeFeed(response.body);
      const used = tick.get(feed.ownerId) ?? 0;
      const room = Math.max(0, globalCap - used);
      const budget = Math.min(Math.max(0, feed.maxItemsPerTick), room);
      let added = 0;
      let skipped = 0;
      for (const item of sortItems(parsed.items).slice(0, SCAN_LIMIT)) {
        if (added >= budget) break;
        const guid = itemGuid(item);
        if (!guid) {
          skipped += 1;
          continue;
        }
        let body = item.summary;
        if (feed.fullText && item.link) {
          try {
            const article = await fetchPage(item.link);
            if (article.status >= 200 && article.status < 300 && article.body) {
              const text = looksLikeHtml(article.body, article.contentType)
                ? extractArticle(article.body).text
                : article.body.trim();
              if (text) body = text;
            }
          } catch {
            body = item.summary;
          }
        }
        if (!body.trim()) {
          skipped += 1;
          continue;
        }
        try {
          const saved = await kb.saveText({
            ownerId: feed.ownerId,
            kind: "rss_item",
            title: item.title,
            body,
            ...(item.link ? { url: item.link } : {}),
            ...(item.author ? { publisher: item.author } : {}),
            ...(item.publishedAt ? { publishedAt: item.publishedAt } : {}),
            folderId: feed.folderId,
            feedId: feed.id,
            feedGuid: guid,
          });
          if (saved.status === "indexed") added += 1;
          else skipped += 1;
        } catch (error) {
          if (!(error instanceof KbInputError)) throw error;
          skipped += 1;
        }
      }
      tick.set(feed.ownerId, used + added);
      await markSuccess(feed, {
        ...(parsed.title ? { title: parsed.title } : {}),
        etag: response.etag ?? null,
        lastModified: response.lastModified ?? null,
      });
      await retain(feed);
      return { added, skipped, notModified: false };
    } catch (error) {
      const message = error instanceof Error ? error.message : "抓取失败";
      await markFailure(feed.id, message);
      if (error instanceof FeedError || error instanceof SafeFetchError) throw error;
      throw new FeedError(message);
    }
  }

  async function refreshAll(): Promise<void> {
    const rows = await db.query<{ id: string }>(`SELECT id FROM feeds ORDER BY id`);
    const tick = new Map<string, number>();
    for (const row of rows) {
      await refresh(row.id, tick).catch(() => undefined);
    }
  }

  return {
    subscribe,
    refresh,
    async importOpml(ownerId, xml) {
      let document: ReturnType<typeof parseOpml>;
      try {
        document = parseOpml(xml);
      } catch {
        throw new FeedError("OPML 无法解析");
      }
      const urls: string[] = [];
      const seen = new Set<string>();
      const walk = (
        outlines: Array<{ xmlUrl?: string; outlines?: typeof outlines }> | undefined,
      ) => {
        for (const outline of outlines ?? []) {
          const url = outline.xmlUrl?.trim();
          if (url && !seen.has(url)) {
            seen.add(url);
            urls.push(url);
          }
          walk(outline.outlines);
        }
      };
      walk(document.body?.outlines);
      const failed: string[] = [];
      let added = 0;
      const tick = new Map<string, number>();
      for (const url of urls) {
        const before = await db.query<{ id: string }>(
          `SELECT id FROM feeds WHERE owner_id = $1 AND url = $2`,
          [ownerId, url],
        );
        try {
          const feed = await subscribe(ownerId, { url });
          if (!before[0]) {
            added += 1;
            await refresh(feed.id, tick).catch(() => undefined);
          }
        } catch (error) {
          failed.push(`${url}: ${error instanceof Error ? error.message : "导入失败"}`);
        }
      }
      return { added, failed };
    },
    async exportOpml(ownerId) {
      const feeds = await db.query<FeedRow>(
        `SELECT * FROM feeds WHERE owner_id = $1 ORDER BY url`,
        [ownerId],
      );
      return generateOpml({
        head: { title: "LuminWrite" },
        body: {
          outlines: feeds.map((feed) => ({
            text: feed.title || feed.url,
            title: feed.title || feed.url,
            type: "rss",
            xmlUrl: feed.url,
          })),
        },
      });
    },
    async unsubscribe(feedId, opts) {
      const feed = await loadFeed(db, feedId);
      if (!feed || (opts.ownerId && feed.ownerId !== opts.ownerId))
        throw new FeedError("找不到订阅");
      if (!opts.keepItems) {
        await db.query(`DELETE FROM sources WHERE owner_id = $1 AND feed_id = $2`, [
          feed.ownerId,
          feed.id,
        ]);
      }
      await db.query(`DELETE FROM feeds WHERE id = $1 AND owner_id = $2`, [feed.id, feed.ownerId]);
    },
    async list(ownerId) {
      const rows = await db.query<FeedRow>(`SELECT * FROM feeds WHERE owner_id = $1 ORDER BY url`, [
        ownerId,
      ]);
      return rows.map(mapFeed);
    },
    get: (feedId) => loadFeed(db, feedId),
    async start() {
      if (!jobs) return;
      await jobs.work(RSS_FETCH, async (data) => {
        const feedId =
          data && typeof data === "object" && "feedId" in data && typeof data.feedId === "string"
            ? data.feedId
            : undefined;
        if (feedId) {
          await refresh(feedId).catch(() => undefined);
          return;
        }
        await refreshAll();
      });
      await jobs.schedule(RSS_FETCH, "*/15 * * * *");
    },
  };
}

export async function importUrl(input: {
  kb: KnowledgeBase;
  ownerId: string;
  url: string;
  fetch: PageFetch;
  folderId?: string;
  title?: string;
}): Promise<{ sourceId: string; status: "indexed" | "duplicate" | "updated" }> {
  const url = assertHttpUrl(input.url);
  let response: SafeFetchResult;
  try {
    response = await input.fetch(url);
  } catch (error) {
    if (error instanceof SafeFetchError || error instanceof FeedError) throw error;
    throw new FeedError("抓取失败");
  }
  if (response.status < 200 || response.status >= 300 || !response.body) {
    throw new FeedError(`抓取失败（${response.status}）`);
  }
  const article = looksLikeHtml(response.body, response.contentType)
    ? extractArticle(response.body)
    : { title: "", text: response.body.trim() };
  const title = input.title?.trim() || article.title || new URL(url).hostname;
  return input.kb.saveText({
    ownerId: input.ownerId,
    kind: "url",
    title,
    body: article.text,
    url: response.finalUrl || url,
    publisher: new URL(url).hostname,
    ...(input.folderId ? { folderId: input.folderId } : {}),
  });
}

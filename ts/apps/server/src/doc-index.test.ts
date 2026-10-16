import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { EditOp } from "@luminwrite/contracts";
import { ensureOwner, openMemoryDatabase, readOwnerToken } from "@luminwrite/db";
import type { WritingRuntime } from "@luminwrite/harness";
import { createInlineJobs } from "@luminwrite/jobs";
import { createKnowledgeBase } from "@luminwrite/kb";
import { describe, expect, it } from "vitest";

import { createApp } from "./app.js";
import { createOssAuth } from "./auth.js";
import { bindDocumentIndex } from "./doc-index.js";
import { sessionKnowledge } from "./kb-session.js";

const actorOf = (userId: string) => ({ type: "user" as const, userId });

function insert(markdown: string): EditOp {
  return {
    op: "insert_block",
    afterBlockId: null,
    newMarkdown: markdown,
    reason: "起稿",
  };
}

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

describe("文档索引任务", () => {
  it("连续自动保存只索引一次，而且索引到最新正文；回滚后再索引", async () => {
    const now = new Date("2026-06-01T00:00:00.000Z");
    const db = await openMemoryDatabase({ now: () => now });
    const jobs = createInlineJobs();
    const kb = createKnowledgeBase({ db });
    bindDocumentIndex(db, jobs, kb);
    let runs = 0;
    await jobs.work("doc.index", async (data) => {
      runs += 1;
      const documentId = (data as { documentId?: string }).documentId;
      if (documentId) await kb.indexDocument(documentId);
    });
    try {
      const created = await db.createDocument({ title: "手稿", ownerId: "user_1" });
      const actor = actorOf("user_1");
      const seeded = await db.applyManual({
        documentId: created.id,
        actor,
        ops: [insert("麒麟在山南记下只属于这篇的句子。")],
      });
      expect(seeded.ok).toBe(true);
      if (!seeded.ok) throw new Error("起稿失败");
      expect(
        await kb.search({ ownerId: "user_1", text: "麒麟在山南", limit: 5, library: true }),
      ).toEqual([]);
      await db.saveDocument(created.id);
      const saved = await kb.search({
        ownerId: "user_1",
        text: "麒麟在山南",
        limit: 5,
        library: true,
      });
      expect(saved).toHaveLength(1);
      expect(runs).toBe(0);

      const first = await db.blocks(created.id);
      const block = first[0];
      if (!block) throw new Error("没有块");
      expect(
        (
          await db.applyManual({
            documentId: created.id,
            actor,
            ops: [
              {
                op: "replace_text",
                blockId: block.blockId,
                contentHash: block.contentHash,
                expectedOldText: block.text,
                newText: "貔貅在河北记下中间稿。",
                reason: "一改",
              },
            ],
          })
        ).ok,
      ).toBe(true);
      const mid = await db.blocks(created.id);
      const midBlock = mid[0];
      if (!midBlock) throw new Error("没有块");
      expect(
        (
          await db.applyManual({
            documentId: created.id,
            actor,
            ops: [
              {
                op: "replace_text",
                blockId: midBlock.blockId,
                contentHash: midBlock.contentHash,
                expectedOldText: midBlock.text,
                newText: "饕餮在西边记下最新稿。",
                reason: "二改",
              },
            ],
          })
        ).ok,
      ).toBe(true);
      await jobs.advance(59_000);
      expect(runs).toBe(0);
      expect(
        await kb.search({ ownerId: "user_1", text: "饕餮在西边", limit: 5, library: true }),
      ).toEqual([]);
      await jobs.advance(1_000);
      expect(runs).toBe(1);
      expect(
        await kb.search({ ownerId: "user_1", text: "饕餮在西边", limit: 5, library: true }),
      ).toHaveLength(1);
      expect(
        await kb.search({ ownerId: "user_1", text: "麒麟在山南", limit: 5, library: true }),
      ).toEqual([]);

      expect(
        (
          await db.rollback({
            documentId: created.id,
            toVersionId: seeded.versionId,
            actor,
          })
        ).ok,
      ).toBe(true);
      await jobs.advance(60_000);
      expect(runs).toBe(2);
      expect(
        await kb.search({ ownerId: "user_1", text: "麒麟在山南", limit: 5, library: true }),
      ).toHaveLength(1);

      await db.discardDocument(created.id);
      const afterDiscard = await db.query<{ n: number | string }>(
        `SELECT count(*)::int AS n FROM chunks c
         JOIN sources s ON s.id = c.source_id
         WHERE s.document_id = $1`,
        [created.id],
      );
      expect(Number(afterDiscard[0]?.n)).toBe(0);
      await jobs.advance(60_000);
      const stillGone = await db.query<{ n: number | string }>(
        `SELECT count(*)::int AS n FROM chunks c
         JOIN sources s ON s.id = c.source_id
         WHERE s.document_id = $1`,
        [created.id],
      );
      expect(Number(stillGone[0]?.n)).toBe(0);
      await db.undoDiscard(created.id);
      const back = await kb.search({
        ownerId: "user_1",
        text: "麒麟在山南",
        limit: 5,
        library: true,
      });
      expect(back).toHaveLength(1);
    } finally {
      await jobs.close();
      await db.close();
    }
  });

  it("派生稿的检索不把原稿当外部资料", async () => {
    const db = await openMemoryDatabase();
    try {
      const kb = createKnowledgeBase({ db });
      const tools = sessionKnowledge(db, kb);
      const parent = await db.createDocument({ title: "原稿", ownerId: "user_1" });
      const actor = actorOf("user_1");
      expect(
        (
          await db.applyManual({
            documentId: parent.id,
            actor,
            ops: [insert("麒麟在山南记下只属于这篇的句子。")],
          })
        ).ok,
      ).toBe(true);
      await db.saveDocument(parent.id);
      await kb.indexDocument(parent.id);
      const versionId = (await db.getDocument(parent.id))?.headVersionId;
      if (!versionId) throw new Error("没有版本");
      const derived = await db.deriveDocument({
        versionId,
        inheritDossier: false,
        ownerId: "user_1",
        actor,
      });
      await kb.indexDocument(derived.id);
      const other = await db.createDocument({ title: "旁证", ownerId: "user_1" });
      expect(
        (
          await db.applyManual({
            documentId: other.id,
            actor,
            ops: [insert("当康只在东边的田里出现。")],
          })
        ).ok,
      ).toBe(true);
      await db.saveDocument(other.id);
      await kb.indexDocument(other.id);

      const lineage = await tools.search({
        sessionId: derived.sessionId,
        query: "麒麟在山南",
        library: true,
      });
      expect(lineage.hits).toEqual([]);
      const outside = await tools.search({
        sessionId: derived.sessionId,
        query: "当康只在东边",
        library: true,
      });
      expect(outside.hits).toHaveLength(1);
      const fromOther = await tools.search({
        sessionId: other.sessionId,
        query: "麒麟在山南",
        library: true,
      });
      expect(fromOther.hits.length).toBeGreaterThan(0);
    } finally {
      await db.close();
    }
  });

  it("接口可以移出知识库，并钉住引用的版本", async () => {
    const dir = await mkdtemp(join(tmpdir(), "lw-doc-index-"));
    const db = await openMemoryDatabase();
    const runtime = stubRuntime();
    const jobs = createInlineJobs();
    try {
      const owner = await ensureOwner(db, { dataDir: join(dir, "data") });
      const token = await readOwnerToken(join(dir, "data"));
      const kb = createKnowledgeBase({ db });
      bindDocumentIndex(db, jobs, kb);
      const app = createApp({ db, runtime, auth: createOssAuth(db), kb, env: {} });
      const request = (path: string, init?: RequestInit) => {
        const headers = new Headers(init?.headers);
        headers.set("authorization", `Bearer ${token}`);
        if (init?.body && !headers.has("content-type"))
          headers.set("content-type", "application/json");
        return app.request(path, { ...init, headers });
      };
      const created = await db.createDocument({ title: "接口稿", ownerId: owner.userId });
      const quote = "英招在云端记下只属于这篇的句子。";
      expect(
        (
          await db.applyManual({
            documentId: created.id,
            actor: actorOf(owner.userId),
            ops: [insert(quote)],
          })
        ).ok,
      ).toBe(true);
      await db.saveDocument(created.id);
      const indexed = await kb.indexDocument(created.id);
      if (!indexed.sourceId) throw new Error("没有来源");

      const hiddenSearch = await request(
        `/api/v1/sources/search?q=${encodeURIComponent("英招在云端")}`,
      );
      expect(hiddenSearch.status).toBe(200);
      expect(((await hiddenSearch.json()) as { hits: unknown[] }).hits).toEqual([]);
      const opened = await request(
        `/api/v1/sources/search?q=${encodeURIComponent("英招在云端")}&library=1`,
      );
      expect(((await opened.json()) as { hits: { sourceId: string }[] }).hits).toHaveLength(1);

      const excluded = await request(`/api/v1/documents/${created.id}/kb`, {
        method: "POST",
        body: JSON.stringify({ excluded: true, actor: "ignored", ownerId: "nope" }),
      });
      expect(excluded.status).toBe(200);
      expect(await excluded.json()).toEqual({ kbExcluded: true });
      const after = await request(
        `/api/v1/sources/search?q=${encodeURIComponent("英招在云端")}&library=1`,
      );
      expect(((await after.json()) as { hits: unknown[] }).hits).toEqual([]);

      await request(`/api/v1/documents/${created.id}/kb`, {
        method: "POST",
        body: JSON.stringify({ excluded: false }),
      });
      const reopened = await request(
        `/api/v1/sources/search?q=${encodeURIComponent("英招在云端")}&library=1`,
      );
      expect(((await reopened.json()) as { hits: unknown[] }).hits).toHaveLength(1);
      const cited = await request(`/api/v1/documents/${created.id}/citations`, {
        method: "POST",
        body: JSON.stringify({ sourceId: indexed.sourceId, quote }),
      });
      expect(cited.status).toBe(201);
      const body = (await cited.json()) as { id: string; citationStatus: string };
      expect(body.citationStatus).toBe("verified");
      const read = await request(`/api/v1/documents/${created.id}/citations/${body.id}`);
      expect(read.status).toBe(200);
      expect((await read.json()) as { citationStatus: string }).toMatchObject({
        citationStatus: "verified",
        sourceHasNewerVersion: false,
      });
    } finally {
      await jobs.close();
      await runtime.close();
      await db.close();
      await rm(dir, { recursive: true, force: true });
    }
  });
});

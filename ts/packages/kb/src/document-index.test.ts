import type { EditOp } from "@luminwrite/contracts";
import { openMemoryDatabase } from "@luminwrite/db";
import { describe, expect, it } from "vitest";

import { chunkBlocks } from "./chunk.js";
import { createKnowledgeBase } from "./kb.js";

const actor = { type: "user" as const, userId: "user_1" };

function insert(markdown: string): EditOp {
  return {
    op: "insert_block",
    afterBlockId: null,
    newMarkdown: markdown,
    reason: "起稿",
  };
}

describe("文档进知识库", () => {
  it("按块分块，哈希不变不升修订，移出知识库后删掉块", async () => {
    const db = await openMemoryDatabase();
    try {
      const kb = createKnowledgeBase({ db });
      const created = await db.createDocument({ title: "手稿", ownerId: "user_1" });
      expect(
        (
          await db.applyManual({
            documentId: created.id,
            actor,
            ops: [insert("麒麟在山南记下只属于这篇的句子。")],
          })
        ).ok,
      ).toBe(true);
      const firstBlocks = await db.blocks(created.id);
      const first = firstBlocks[0];
      if (!first) throw new Error("没有块");
      expect(
        (
          await db.applyManual({
            documentId: created.id,
            actor,
            ops: [
              {
                op: "insert_block",
                afterBlockId: first.blockId,
                newMarkdown: "貔貅在河北记下另一段。",
                reason: "续写",
              },
            ],
          })
        ).ok,
      ).toBe(true);
      await db.saveDocument(created.id);
      const indexed = await kb.indexDocument(created.id);
      expect(indexed.status).toBe("indexed");
      const again = await kb.indexDocument(created.id);
      expect(again).toEqual({ status: "skipped", sourceId: indexed.sourceId });

      const sources = await db.query<{
        revision: number | string;
        body: string;
        indexed_version_id: string;
      }>(`SELECT revision, body, indexed_version_id FROM sources WHERE id = $1`, [
        indexed.sourceId,
      ]);
      expect(Number(sources[0]?.revision)).toBe(1);
      const chunks = await db.query<{
        text: string;
        char_start: number | string;
        char_end: number | string;
        block_ids: string[] | null;
      }>(
        `SELECT text, char_start, char_end, block_ids FROM chunks WHERE source_id = $1 ORDER BY ord`,
        [indexed.sourceId],
      );
      expect(chunks.length).toBeGreaterThan(0);
      const body = sources[0]?.body ?? "";
      for (const chunk of chunks) {
        const start = Number(chunk.char_start);
        const end = Number(chunk.char_end);
        expect(body.slice(start, end)).toBe(chunk.text);
        expect(chunk.block_ids?.length).toBeGreaterThan(0);
      }
      const blockIds = chunks.flatMap((chunk) => chunk.block_ids ?? []);
      const current = await db.blocks(created.id);
      expect(blockIds).toEqual(expect.arrayContaining(current.map((block) => block.blockId)));

      expect(await kb.search({ ownerId: "user_1", text: "麒麟在山南", limit: 5 })).toEqual([]);
      const opened = await kb.search({
        ownerId: "user_1",
        text: "麒麟在山南",
        limit: 5,
        library: true,
      });
      expect(opened.map((hit) => hit.sourceId)).toEqual([indexed.sourceId]);

      await db.setKbExcluded(created.id, true);
      const hidden = await db.query<{ n: number | string }>(
        `SELECT count(*)::int AS n FROM chunks WHERE source_id = $1`,
        [indexed.sourceId],
      );
      expect(Number(hidden[0]?.n)).toBe(0);
      expect(
        await kb.search({ ownerId: "user_1", text: "麒麟在山南", limit: 5, library: true }),
      ).toEqual([]);
      await db.setKbExcluded(created.id, false);
      await kb.indexDocument(created.id);
      const restored = await kb.search({
        ownerId: "user_1",
        text: "麒麟在山南",
        limit: 5,
        library: true,
      });
      expect(restored.map((hit) => hit.sourceId)).toEqual([indexed.sourceId]);
    } finally {
      await db.close();
    }
  });

  it("引用钉住当时的版本，改稿后仍 verified 并标出有新版本", async () => {
    const db = await openMemoryDatabase();
    try {
      const kb = createKnowledgeBase({ db });
      const created = await db.createDocument({ title: "旧文", ownerId: "user_1" });
      const quote = "饕餮在西边记下只属于旧版的句子。";
      expect(
        (
          await db.applyManual({
            documentId: created.id,
            actor,
            ops: [insert(quote)],
          })
        ).ok,
      ).toBe(true);
      await db.saveDocument(created.id);
      const indexed = await kb.indexDocument(created.id);
      if (!indexed.sourceId) throw new Error("没有来源");
      const cited = await kb.citeDocument({
        ownerId: "user_1",
        citingDocumentId: created.id,
        sourceId: indexed.sourceId,
        quote,
      });
      expect(cited.citationStatus).toBe("verified");
      expect(cited.citedVersionId).toBeTruthy();
      expect(cited.sourceHasNewerVersion).toBe(false);
      expect(cited.locator.blockId).toBeTruthy();

      const blocks = await db.blocks(created.id);
      const block = blocks[0];
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
                newText: "梼杌改成了完全不同的新句子。",
                reason: "改稿",
              },
            ],
          })
        ).ok,
      ).toBe(true);
      await kb.indexDocument(created.id);
      const resolved = await kb.resolveEvidence("user_1", cited.id);
      expect(resolved.citationStatus).toBe("verified");
      expect(resolved.citedVersionId).toBe(cited.citedVersionId);
      expect(resolved.sourceHasNewerVersion).toBe(true);

      await db.discardDocument(created.id);
      await db.purgeDiscarded({ retentionDays: 0 });
      const left = await db.query<{ n: number | string }>(
        `SELECT count(*)::int AS n FROM evidence WHERE id = $1`,
        [cited.id],
      );
      const sources = await db.query<{ n: number | string }>(
        `SELECT count(*)::int AS n FROM sources WHERE document_id = $1`,
        [created.id],
      );
      expect(Number(left[0]?.n)).toBe(0);
      expect(Number(sources[0]?.n)).toBe(0);
      expect(await db.getDocument(created.id)).toBeUndefined();
    } finally {
      await db.close();
    }
  });

  it("短块合成一块，超长块单独成块且偏移对得上正文", () => {
    const packed = chunkBlocks([
      { blockId: "b1", text: "甲" },
      { blockId: "b2", text: "乙" },
    ]);
    expect(packed.chunks).toHaveLength(1);
    expect(packed.chunks[0]?.blockIds).toEqual(["b1", "b2"]);
    expect(packed.body.slice(packed.chunks[0]?.charStart, packed.chunks[0]?.charEnd)).toBe(
      packed.chunks[0]?.text,
    );
    const huge = chunkBlocks([
      { blockId: "big", text: "春".repeat(900) },
      { blockId: "next", text: "茶" },
    ]);
    expect(huge.chunks.length).toBeGreaterThan(1);
    expect(huge.chunks[0]?.blockIds).toEqual(["big"]);
    expect(huge.body.slice(huge.chunks[0]?.charStart, huge.chunks[0]?.charEnd)).toBe(
      huge.chunks[0]?.text,
    );
  });
});

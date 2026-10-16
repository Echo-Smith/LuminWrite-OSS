import type { BlockId, EditOp } from "@luminwrite/contracts";
import { blockHashSync, blockText, listBlocks, type PMJSON } from "@luminwrite/doc-model";
import { describe, expect, it } from "vitest";

import { openMemoryDatabase } from "./database.js";

const user = { type: "user" as const, userId: "user_1" };

function texts(doc: PMJSON): string[] {
  return listBlocks(doc).map((node) => blockText(node));
}

function opOn(doc: PMJSON, index: number, newText: string): EditOp {
  const node = listBlocks(doc)[index];
  if (!node) throw new Error("missing block");
  const blockId = node.attrs?.id;
  if (typeof blockId !== "string") throw new Error("missing id");
  return {
    op: "replace_text",
    blockId: blockId as BlockId,
    contentHash: blockHashSync(node),
    expectedOldText: blockText(node),
    newText,
    reason: "测试",
  };
}

describe("文档版本", () => {
  it("不同块的修订互不冲突，过期锚点被拒绝，回滚追加版本", async () => {
    const db = await openMemoryDatabase();
    const created = await db.createDocument({ title: "推理成本", ownerId: "user_1" });
    const seed = await db.applyManual({
      documentId: created.id,
      actor: user,
      summary: "起稿",
      ops: [
        {
          op: "insert_block",
          afterBlockId: null,
          newMarkdown: "第一段保持不动。\n\n第二段待改。\n\n第三段也保持不动。",
          reason: "起稿",
        },
      ],
    });
    expect(seed.ok).toBe(true);
    const base = await db.getDocument(created.id);
    const baseDoc = base?.head?.content;
    if (!baseDoc) throw new Error("missing head");
    const first = opOn(baseDoc, 0, "第一段已经改过。");
    const second = opOn(baseDoc, 1, "第二段单独改过。");
    const applied = await db.applyManual({ documentId: created.id, actor: user, ops: [first] });
    expect(applied.ok).toBe(true);
    const concurrent = await db.applyManual({ documentId: created.id, actor: user, ops: [second] });
    expect(concurrent.ok).toBe(true);
    const head = await db.getDocument(created.id);
    expect(texts(head?.head?.content ?? { type: "doc" })).toEqual([
      "第一段已经改过。",
      "第二段单独改过。",
      "第三段也保持不动。",
    ]);
    expect(head?.humanTouched).toBe(true);

    const stale = await db.applyManual({ documentId: created.id, actor: user, ops: [first] });
    expect(stale.ok).toBe(false);
    if (!stale.ok) expect(stale.errors[0]?.code).toBe("hash_mismatch");
    const afterStale = await db.getDocument(created.id);
    expect(texts(afterStale?.head?.content ?? { type: "doc" })).toEqual(
      texts(head?.head?.content ?? { type: "doc" }),
    );

    const versions = await db.listVersions(created.id);
    const rolled = await db.rollback({
      documentId: created.id,
      toVersionId: versions[1]?.id ?? "",
      actor: { type: "system" },
    });
    expect(rolled.ok).toBe(true);
    const restored = await db.getDocument(created.id);
    expect(texts(restored?.head?.content ?? { type: "doc" })).toEqual([
      "第一段保持不动。",
      "第二段待改。",
      "第三段也保持不动。",
    ]);
    expect(restored?.head?.origin).toBe("restore");
    const more = await db.listVersions(created.id);
    expect(more.length).toBe(versions.length + 1);
    expect(more[1]?.id).toBe(versions[1]?.id);

    await expect(
      db.exec(`UPDATE doc_versions SET summary = 'x' WHERE id = '${created.versionId}'`),
    ).rejects.toThrow(/append-only/);
    await db.close();
  });

  it("同一任务号只落一版；提案拒绝不改正文", async () => {
    const db = await openMemoryDatabase();
    const created = await db.createDocument({ title: "提案", ownerId: "user_1" });
    const turn = await db.beginTurn({
      documentId: created.id,
      kind: "revise",
      instruction: "改",
      scope: "document",
      delivery: "followup",
      applyMode: "proposal",
      idempotencyKey: "k1",
      actor: user,
    });
    const again = await db.beginTurn({
      documentId: created.id,
      kind: "revise",
      instruction: "改",
      scope: "document",
      delivery: "followup",
      applyMode: "proposal",
      idempotencyKey: "k1",
      actor: user,
    });
    expect(again.turnId).toBe(turn.turnId);
    expect(again.created).toBe(false);

    const proposed = await db.propose({
      documentId: created.id,
      turnId: turn.turnId,
      piTaskId: "task_1",
      applyMode: "proposal",
      actor: { type: "system" },
      summary: "提案",
      ops: [
        {
          op: "insert_block",
          afterBlockId: null,
          newMarkdown: "提案里的一段。",
          reason: "提案",
        },
      ],
    });
    expect(proposed.ok).toBe(true);
    if (proposed.ok) {
      const replay = await db.propose({
        documentId: created.id,
        turnId: turn.turnId,
        piTaskId: "task_1",
        applyMode: "proposal",
        actor: { type: "system" },
        summary: "提案",
        ops: [],
      });
      expect(replay).toMatchObject({ ok: true, already: true, proposalId: proposed.proposalId });
      const before = await db.blocks(created.id);
      expect(before).toHaveLength(0);
      if (proposed.proposalId) await db.rejectProposal(proposed.proposalId);
    }
    expect(await db.blocks(created.id)).toHaveLength(0);
    await db.recordUsage({
      requestId: "k1",
      model: "faux-1",
      inputTokens: 0,
      outputTokens: 0,
      costMicros: 0,
    });
    await db.close();
  });
});

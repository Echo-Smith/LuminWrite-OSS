import type { BlockId, ContentHash, EditOp } from "@luminwrite/contracts";
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

describe("会话与派生文档", () => {
  it("档案只按操作更新，分叉和派生不共享正文，块 id 不碰撞", async () => {
    const db = await openMemoryDatabase();
    const created = await db.createDocument({ title: "推理成本", ownerId: "user_1" });
    const seeded = await db.applyManual({
      documentId: created.id,
      actor: user,
      sessionId: created.sessionId,
      summary: "起稿",
      ops: [
        {
          op: "insert_block",
          afterBlockId: null,
          newMarkdown: "第一段先看时租。一台 A10 按小时计费。\n\n第二段保持不动。这一段不改。",
          reason: "起稿",
        },
      ],
    });
    expect(seeded.ok).toBe(true);
    const dossier = await db.applyDossier({
      documentId: created.id,
      expectedVersion: 1,
      ops: [
        { op: "set_outline", items: ["先看时租"] },
        { op: "add_citation", title: "时租价目", locator: "2026-10" },
        { op: "add_requirement", text: "只写能核对的数字" },
        { op: "add_decision", text: "先不增加机器" },
      ],
    });
    expect(dossier.ok).toBe(true);
    const staleDossier = await db.applyDossier({
      documentId: created.id,
      expectedVersion: 1,
      ops: [{ op: "add_decision", text: "这条不该写上" }],
    });
    expect(staleDossier.ok).toBe(false);
    const kept = await db.readDossier(created.id);
    expect(kept.version).toBe(2);
    expect(kept.content.decisions).toEqual(["先不增加机器"]);

    if (!seeded.ok) throw new Error("seed");
    const head = await db.getDocument(created.id);
    const changed = await db.applyManual({
      documentId: created.id,
      actor: user,
      sessionId: created.sessionId,
      ops: [opOn(head?.head?.content ?? { type: "doc" }, 0, "第一段已经改过。费用按小时计。")],
    });
    expect(changed.ok).toBe(true);
    const main = await db.mainSession(created.id);
    if (!main || !changed.ok) throw new Error("main");
    expect(main.baseVersionId).toBe(changed.versionId);
    expect(JSON.stringify(main)).not.toContain("第一段");

    const fork = await db.openSession({
      documentId: created.id,
      kind: "fork",
      baseVersionId: seeded.versionId,
      actor: { type: "api_client", userId: "user_1", keyId: "key_1" },
    });
    expect(fork.kind).toBe("fork");
    expect(fork.piConversationId).toBeNull();
    expect(fork.baseVersionId).toBe(seeded.versionId);
    const oldBlocks = await db.readBlocksAt(created.id, seeded.versionId);
    const fresh = await db.openSession({
      documentId: created.id,
      kind: "collab",
      actor: user,
    });
    expect(fresh.baseVersionId).toBe(changed.versionId);

    const derived = await db.deriveDocument({
      versionId: seeded.versionId,
      inheritDossier: true,
      ownerId: "user_2",
      actor: user,
    });
    const bare = await db.deriveDocument({
      versionId: seeded.versionId,
      inheritDossier: false,
      ownerId: "user_2",
      title: "空档案派生",
      actor: user,
    });
    const sourceBlocks = await db.blocks(created.id);
    const derivedBlocks = await db.blocks(derived.id);
    expect(derivedBlocks.map((block) => block.text)).toEqual(oldBlocks.map((block) => block.text));
    const sourceIds = new Set(sourceBlocks.map((block) => block.blockId));
    expect(derivedBlocks.every((block) => !sourceIds.has(block.blockId))).toBe(true);
    const inherited = await db.readDossier(derived.id);
    expect(inherited.version).toBe(1);
    expect(inherited.content.outline).toEqual(["先看时租"]);
    expect(inherited.content.citations).toEqual([{ title: "时租价目", locator: "2026-10" }]);
    expect(inherited.content.requirements).toEqual(["只写能核对的数字"]);
    expect(inherited.content.decisions).toEqual([]);
    expect((await db.readDossier(bare.id)).content.outline).toEqual([]);
    const derivedSession = await db.getSession(derived.sessionId);
    expect(derivedSession?.piConversationId).toBeNull();
    expect(derivedSession?.kind).toBe("main");
    expect(await db.listSessions(derived.id)).toHaveLength(1);

    const touched = await db.applyManual({
      documentId: derived.id,
      actor: user,
      sessionId: derived.sessionId,
      ops: [
        opOn(
          (await db.getDocument(derived.id))?.head?.content ?? { type: "doc" },
          0,
          "派生文档自己的第一段。",
        ),
      ],
    });
    expect(touched.ok).toBe(true);
    const sourceAfter = await db.blocks(created.id);
    expect(sourceAfter.map((block) => block.text)).toEqual(sourceBlocks.map((block) => block.text));
    expect(new Set((await db.listDerived(created.id)).map((item) => item.id))).toEqual(
      new Set([derived.id, bare.id]),
    );

    await db.grantRead(main.id, derived.id);
    expect(await db.canRead(main.id, derived.id)).toBe(true);
    expect(await db.canRead(fork.id, derived.id)).toBe(false);
    const foreign = (await db.blocks(derived.id))[0];
    if (!foreign) throw new Error("missing derived block");
    const beforeCount = (await db.listVersions(created.id)).length;
    const missed = await db.applySessionEdits({
      sessionId: main.id,
      documentIds: [created.id],
      actor: user,
      ops: [
        {
          op: "replace_text",
          blockId: foreign.blockId,
          contentHash: foreign.contentHash as ContentHash,
          expectedOldText: foreign.text,
          newText: "不该写上。",
          reason: "误用块 id",
        },
      ],
    });
    expect(missed.ok).toBe(false);
    if (!missed.ok) expect(missed.errors[0]?.code).toBe("block_not_found");
    const mixed = await db.applySessionEdits({
      sessionId: main.id,
      documentIds: [created.id, derived.id],
      actor: user,
      ops: [
        {
          op: "replace_text",
          blockId: sourceAfter[0]?.blockId ?? foreign.blockId,
          contentHash: (sourceAfter[0]?.contentHash ?? foreign.contentHash) as ContentHash,
          expectedOldText: sourceAfter[0]?.text ?? foreign.text,
          newText: "不该写上。",
          reason: "两篇一起改",
        },
      ],
    });
    expect(mixed.ok).toBe(false);
    if (!mixed.ok) expect(mixed.errors[0]?.code).toBe("out_of_scope");
    expect(await db.listVersions(created.id)).toHaveLength(beforeCount);
    const home = sourceAfter[0];
    if (!home) throw new Error("missing home");
    const brought = await db.applySessionEdits({
      sessionId: main.id,
      documentIds: [created.id],
      actor: user,
      ops: [
        {
          op: "replace_text",
          blockId: home.blockId,
          contentHash: home.contentHash as ContentHash,
          expectedOldText: home.text,
          newText: "从派生文档写回的第一段。",
          reason: "写回",
        },
      ],
    });
    expect(brought.ok).toBe(true);
    expect((await db.blocks(created.id))[0]?.text).toBe("从派生文档写回的第一段。");
    expect((await db.blocks(derived.id))[0]?.text).toBe("派生文档自己的第一段。");
    await db.closeSession(fresh.id);
    await expect(
      db.beginTurn({
        documentId: created.id,
        sessionId: fresh.id,
        kind: "revise",
        instruction: "改",
        scope: "document",
        delivery: "followup",
        applyMode: "auto",
        idempotencyKey: "closed",
        actor: user,
      }),
    ).rejects.toThrow(/会话已关闭/);
    await db.close();
  });
});

import type { BlockId, EditOp } from "@luminwrite/contracts";
import { blockHashSync, blockText, listBlocks, type PMJSON } from "@luminwrite/doc-model";
import { describe, expect, it } from "vitest";

import {
  AUTOSAVE_WINDOW_MS,
  discardRetentionDays,
  documentVisibleToKb,
  openMemoryDatabase,
} from "./database.js";

const user = { type: "user" as const, userId: "user_1" };
const keyA = { type: "api_client" as const, userId: "user_1", keyId: "key_a" };
const keyB = { type: "api_client" as const, userId: "user_1", keyId: "key_b" };

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

const seedOp: EditOp = {
  op: "insert_block",
  afterBlockId: null,
  newMarkdown: "第一段。",
  reason: "起稿",
};

describe("保存、自动保存、丢弃", () => {
  it("未保存不进列表；保存后 30 秒内的人工修订合成一版", async () => {
    let now = new Date("2026-01-01T00:00:00.000Z");
    const db = await openMemoryDatabase({ now: () => now });
    const created = await db.createDocument({ title: "草稿", ownerId: "user_1" });
    expect(await db.listDocuments("user_1")).toEqual([]);
    expect(documentVisibleToKb({ savedAt: null, discardedAt: null })).toBe(false);

    const seeded = await db.applyManual({
      documentId: created.id,
      actor: user,
      ops: [seedOp],
    });
    const again = await db.applyManual({
      documentId: created.id,
      actor: user,
      ops: [
        opOn((await db.getDocument(created.id))?.head?.content ?? { type: "doc" }, 0, "保存前。"),
      ],
    });
    expect(seeded.ok && again.ok).toBe(true);
    if (!seeded.ok || !again.ok) throw new Error("seed");
    expect(again.versionId).not.toBe(seeded.versionId);
    expect(await db.listDocuments("user_1")).toEqual([]);

    const saved = await db.saveDocument(created.id);
    expect(saved.already).toBe(false);
    expect((await db.saveDocument(created.id)).already).toBe(true);
    expect((await db.listDocuments("user_1")).map((item) => item.id)).toEqual([created.id]);

    now = new Date(now.getTime() + 1_000);
    const first = await db.applyManual({
      documentId: created.id,
      actor: user,
      summary: "第一下",
      ops: [
        opOn((await db.getDocument(created.id))?.head?.content ?? { type: "doc" }, 0, "第一下。"),
      ],
    });
    now = new Date(now.getTime() + 10_000);
    const second = await db.applyManual({
      documentId: created.id,
      actor: user,
      summary: "第二下",
      ops: [
        opOn((await db.getDocument(created.id))?.head?.content ?? { type: "doc" }, 0, "第二下。"),
      ],
    });
    expect(first.ok && second.ok).toBe(true);
    if (!first.ok || !second.ok) throw new Error("autosave");
    expect(second.versionId).toBe(first.versionId);
    expect(second.seq).toBe(first.seq);
    const folded = await db.getVersion(first.versionId);
    expect(folded?.summary).toBe("第二下");
    expect(folded?.ops).toHaveLength(2);
    expect(folded?.createdAt).toBe(new Date("2026-01-01T00:00:01.000Z").toISOString());
    const beforeWindow = await db.listVersions(created.id);

    now = new Date(now.getTime() + AUTOSAVE_WINDOW_MS);
    const third = await db.applyManual({
      documentId: created.id,
      actor: user,
      ops: [
        opOn((await db.getDocument(created.id))?.head?.content ?? { type: "doc" }, 0, "窗口外。"),
      ],
    });
    expect(third.ok).toBe(true);
    if (!third.ok) throw new Error("window");
    expect(third.versionId).not.toBe(first.versionId);

    now = new Date(now.getTime() + 1_000);
    const otherKey = await db.applyManual({
      documentId: created.id,
      actor: keyA,
      ops: [
        opOn((await db.getDocument(created.id))?.head?.content ?? { type: "doc" }, 0, "密钥甲。"),
      ],
    });
    now = new Date(now.getTime() + 1_000);
    const otherActor = await db.applyManual({
      documentId: created.id,
      actor: keyB,
      ops: [
        opOn((await db.getDocument(created.id))?.head?.content ?? { type: "doc" }, 0, "密钥乙。"),
      ],
    });
    expect(otherKey.ok && otherActor.ok).toBe(true);
    if (!otherKey.ok || !otherActor.ok) throw new Error("keys");
    expect(otherActor.versionId).not.toBe(otherKey.versionId);
    expect((await db.getVersion(otherKey.versionId))?.apiKeyId).toBe("key_a");

    now = new Date(now.getTime() + 1_000);
    const sameKey = await db.applyManual({
      documentId: created.id,
      actor: keyB,
      ops: [
        opOn(
          (await db.getDocument(created.id))?.head?.content ?? { type: "doc" },
          0,
          "密钥乙再改。",
        ),
      ],
    });
    expect(sameKey.ok).toBe(true);
    if (!sameKey.ok) throw new Error("same key");
    expect(sameKey.versionId).toBe(otherActor.versionId);

    const turn = await db.beginTurn({
      documentId: created.id,
      kind: "revise",
      instruction: "改",
      scope: "document",
      delivery: "followup",
      applyMode: "auto",
      idempotencyKey: "turn-1",
      actor: { type: "system" },
    });
    now = new Date(now.getTime() + 1_000);
    const applied = await db.propose({
      documentId: created.id,
      turnId: turn.turnId,
      piTaskId: "task_save_1",
      applyMode: "auto",
      actor: { type: "system" },
      summary: "回合",
      ops: [
        opOn((await db.getDocument(created.id))?.head?.content ?? { type: "doc" }, 0, "回合改过。"),
      ],
    });
    expect(applied).toMatchObject({ ok: true, status: "applied" });
    now = new Date(now.getTime() + 1_000);
    const afterTurn = await db.applyManual({
      documentId: created.id,
      actor: keyB,
      ops: [
        opOn((await db.getDocument(created.id))?.head?.content ?? { type: "doc" }, 0, "回合之后。"),
      ],
    });
    expect(afterTurn.ok).toBe(true);
    if (!afterTurn.ok || !applied.ok || applied.status !== "applied") throw new Error("turn");
    expect(afterTurn.versionId).not.toBe(applied.versionId);
    expect(afterTurn.versionId).not.toBe(sameKey.versionId);

    const rolled = await db.rollback({
      documentId: created.id,
      toVersionId: first.versionId,
      actor: keyA,
    });
    expect(rolled.ok).toBe(true);
    const head = await db.getDocument(created.id);
    expect(head?.head?.origin).toBe("restore");
    expect(
      head?.head?.content && listBlocks(head.head.content).map((node) => blockText(node)),
    ).toEqual(["第二下。"]);
    const afterRollback = await db.listVersions(created.id);
    expect(afterRollback.length).toBeGreaterThan(beforeWindow.length);
    expect(afterRollback.find((version) => version.id === first.versionId)?.summary).toBe("第二下");
    const diff = await db.diffVersions(created.id, first.versionId, head?.headVersionId ?? "");
    expect(diff.some((item) => item.kind === "unchanged" || item.kind === "modified")).toBe(true);

    await expect(
      db.exec(`UPDATE doc_versions SET summary = 'x' WHERE id = '${first.versionId}'`),
    ).rejects.toThrow(/append-only/);
    await db.close();
  });

  it("外部创建、导入和派生一开始就算已保存", async () => {
    const db = await openMemoryDatabase();
    const imported = await db.createDocument({
      title: "导入",
      ownerId: "user_1",
      createdVia: "import",
    });
    const viaKey = await db.createDocument({
      title: "密钥",
      ownerId: "user_1",
      actor: keyA,
    });
    const parent = await db.createDocument({ title: "源", ownerId: "user_1" });
    const derived = await db.deriveDocument({
      versionId: parent.versionId,
      inheritDossier: false,
      ownerId: "user_1",
      actor: user,
    });
    const ids = (await db.listDocuments("user_1")).map((item) => item.id).sort();
    expect(ids).toEqual([imported.id, viaKey.id, derived.id].sort());
    expect((await db.getDocument(imported.id))?.savedAt).toBeTruthy();
    expect((await db.getDocument(parent.id))?.savedAt).toBeNull();
    expect((await db.getDocument(viaKey.id))?.createdVia).toBe("ui");
    await db.close();
  });

  it("丢弃后从列表消失，保留期内可撤销，过期才清退版本", async () => {
    let now = new Date("2026-03-01T00:00:00.000Z");
    const db = await openMemoryDatabase({ now: () => now });
    const created = await db.createDocument({ title: "要丢", ownerId: "user_1" });
    await db.saveDocument(created.id);
    const kept = await db.createDocument({
      title: "留下",
      ownerId: "user_1",
      createdVia: "import",
    });
    const discarded = await db.discardDocument(created.id);
    expect(
      documentVisibleToKb({ savedAt: discarded.discardedAt, discardedAt: discarded.discardedAt }),
    ).toBe(false);
    expect((await db.listDocuments("user_1")).map((item) => item.id)).toEqual([kept.id]);
    expect((await db.getDocument(created.id))?.discardedAt).toBe(discarded.discardedAt);

    now = new Date(now.getTime() + 29 * 24 * 60 * 60 * 1000);
    expect((await db.purgeDiscarded({ retentionDays: 30, now })).purged).toEqual([]);
    expect(await db.getDocument(created.id)).toBeTruthy();

    await db.undoDiscard(created.id);
    now = new Date("2026-06-01T00:00:00.000Z");
    expect((await db.purgeDiscarded({ retentionDays: 30, now })).purged).toEqual([]);
    expect((await db.listDocuments("user_1")).map((item) => item.id).sort()).toEqual(
      [created.id, kept.id].sort(),
    );

    await db.discardDocument(created.id);
    now = new Date(now.getTime() + 31 * 24 * 60 * 60 * 1000);
    expect((await db.purgeDiscarded({ retentionDays: 30, now })).purged).toEqual([created.id]);
    expect(await db.getDocument(created.id)).toBeUndefined();
    expect(await db.getVersion(created.versionId)).toBeUndefined();
    expect((await db.listDocuments("user_1")).map((item) => item.id)).toEqual([kept.id]);
    await expect(db.exec(`delete from documents where id = '${kept.id}'`)).rejects.toThrow(
      /append-only/,
    );
    expect(discardRetentionDays({})).toBe(30);
    expect(discardRetentionDays({ LW_DISCARD_RETENTION_DAYS: "7" })).toBe(7);
    expect(discardRetentionDays({ LW_DISCARD_RETENTION_DAYS: "nope" })).toBe(30);
    await db.close();
  });

  it("回滚追加 restore，旧版本的块哈希还在", async () => {
    const db = await openMemoryDatabase();
    const created = await db.createDocument({
      title: "回滚",
      ownerId: "user_1",
      createdVia: "import",
    });
    const written = await db.applyManual({
      documentId: created.id,
      actor: user,
      ops: [seedOp],
    });
    expect(written.ok).toBe(true);
    const restored = await db.rollback({
      documentId: created.id,
      toVersionId: created.versionId,
      actor: keyA,
    });
    expect(restored.ok).toBe(true);
    const versions = await db.listVersions(created.id);
    expect(versions.map((version) => version.origin)).toEqual(["import", "manual", "restore"]);
    expect(versions[0]?.id).toBe(created.versionId);
    expect(versions[2]?.apiKeyId).toBe("key_a");
    const diff = await db.diffVersions(created.id, versions[1]?.id ?? "", versions[2]?.id ?? "");
    expect(diff.some((item) => item.kind === "removed")).toBe(true);
    const hash = versions[1]?.content;
    expect(hash).toBeTruthy();
    expect(
      blockText(listBlocks(versions[1]?.content ?? { type: "doc" })[0] ?? { type: "paragraph" }),
    ).toBe("第一段。");
    await db.close();
  });
});

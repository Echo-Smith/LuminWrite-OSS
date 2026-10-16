import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { openMemoryDatabase } from "@luminwrite/db";
import { afterEach, describe, expect, it } from "vitest";

import { writingStoreFromDatabase } from "./from-db.js";
import { createWritingRuntime } from "./runtime.js";

const dirs: string[] = [];

afterEach(async () => {
  await Promise.all(dirs.splice(0).map((dir) => rm(dir, { recursive: true, force: true })));
});

describe("read_related_dossier", () => {
  it("工具按血缘读到源文档的当前决定，无关文档读不到，决定也不会写进派生档案", async () => {
    const dir = await mkdtemp(join(tmpdir(), "lw-dossier-"));
    dirs.push(dir);
    const db = await openMemoryDatabase();
    const source = await db.createDocument({ title: "源", ownerId: "user_1" });
    await db.applyDossier({
      documentId: source.id,
      expectedVersion: 1,
      ops: [{ op: "add_decision", text: "先不增加机器" }],
    });
    const derived = await db.deriveDocument({
      versionId: source.versionId,
      inheritDossier: true,
      ownerId: "user_1",
      actor: { type: "user", userId: "user_1" },
    });
    await db.applyDossier({
      documentId: source.id,
      expectedVersion: 2,
      ops: [{ op: "add_decision", text: "改用更便宜的显卡时租" }],
    });
    const base = writingStoreFromDatabase(db);
    const seen: string[][] = [];
    const runtime = await createWritingRuntime({
      sqlitePath: join(dir, "pi.sqlite"),
      store: {
        ...base,
        async readRelatedDossier(sessionId, documentId) {
          const dossier = await base.readRelatedDossier(sessionId, documentId);
          seen.push([...dossier.content.decisions]);
          return dossier;
        },
      },
    });
    const actor = { type: "user" as const, userId: "user_1" };
    await runtime.submitTurn({
      documentId: derived.id,
      sessionId: derived.sessionId,
      kind: "revise",
      instruction: `读取关联档案 ${source.id}`,
      scope: "document",
      delivery: "followup",
      applyMode: "auto",
      idempotencyKey: "read-source",
      actor,
    });
    expect(seen).toEqual([["先不增加机器", "改用更便宜的显卡时租"]]);
    expect((await db.readDossier(derived.id)).content.decisions).toEqual([]);
    const stranger = await db.createDocument({ title: "无关", ownerId: "user_3" });
    await expect(runtime.readRelatedDossier(derived.sessionId, stranger.id)).rejects.toThrow(
      /不能读这篇文档的档案/,
    );
    await runtime.close();
    await db.close();
  });
});

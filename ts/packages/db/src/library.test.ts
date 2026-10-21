import type { EditOp } from "@luminwrite/contracts";
import { describe, expect, it } from "vitest";

import { openMemoryDatabase } from "./database.js";

const user = { type: "user" as const, userId: "user_1" };

const insert = (markdown: string): EditOp => ({
  op: "insert_block",
  afterBlockId: null,
  newMarkdown: markdown,
  reason: "起稿",
});

describe("文章库", () => {
  it("只列出已保存文档，能按标签、正文和归档过滤，清退后标签一起消失", async () => {
    const db = await openMemoryDatabase();
    const draft = await db.createDocument({ title: "还没保存", ownerId: "user_1" });
    const essay = await db.createDocument({
      title: "时租笔记",
      ownerId: "user_1",
      genre: "评论",
    });
    await db.saveDocument(essay.id);
    const other = await db.createDocument({
      title: "另一篇",
      ownerId: "user_1",
      genre: "报告",
      createdVia: "import",
    });
    const foreign = await db.createDocument({
      title: "别人的时租笔记",
      ownerId: "user_2",
      createdVia: "import",
    });
    const written = await db.applyManual({
      documentId: essay.id,
      actor: user,
      ops: [insert("一台 A10 按小时计费。")],
    });
    expect(written.ok).toBe(true);

    const active = await db.listArticles("user_1", { tags: [], archived: "active", limit: 50 });
    expect(active.map((item) => item.id).sort()).toEqual([essay.id, other.id].sort());
    expect(active.find((item) => item.id === draft.id)).toBeUndefined();
    expect(active.find((item) => item.id === foreign.id)).toBeUndefined();

    expect(await db.addDocumentTag(essay.id, "user_1", "  时租  ")).toEqual(["时租"]);
    expect(await db.addDocumentTag(essay.id, "user_1", "时租")).toEqual(["时租"]);
    expect(await db.addDocumentTag(essay.id, "user_1", "成本")).toEqual(["成本", "时租"]);
    await db.addDocumentTag(other.id, "user_1", "时租");
    await expect(db.addDocumentTag(foreign.id, "user_1", "时租")).rejects.toThrow(/找不到文档/);

    const tagged = await db.listArticles("user_1", {
      tags: ["时租", "成本"],
      archived: "active",
      limit: 50,
    });
    expect(tagged.map((item) => item.id)).toEqual([essay.id]);

    const found = await db.listArticles("user_1", {
      text: "按小时计费",
      tags: [],
      archived: "active",
      limit: 50,
    });
    expect(found.map((item) => item.id)).toEqual([essay.id]);
    const byGenre = await db.listArticles("user_1", {
      genre: "报告",
      tags: [],
      archived: "active",
      limit: 50,
    });
    expect(byGenre.map((item) => item.id)).toEqual([other.id]);
    expect(byGenre[0]?.createdVia).toBe("import");

    await db.setKbExcluded(essay.id, true);
    const excluded = await db.listArticles("user_1", {
      tags: [],
      archived: "active",
      kbExcluded: true,
      limit: 50,
    });
    expect(excluded.map((item) => item.id)).toEqual([essay.id]);

    await db.discardDocument(essay.id);
    const still = await db.listArticles("user_1", { tags: [], archived: "active", limit: 50 });
    expect(still.map((item) => item.id)).toEqual([other.id]);
    const archived = await db.listArticles("user_1", { tags: [], archived: "archived", limit: 50 });
    expect(archived.map((item) => item.status)).toEqual(["archived"]);
    expect(archived[0]?.id).toBe(essay.id);
    expect(archived[0]?.tags).toEqual(["成本", "时租"]);

    expect(await db.removeDocumentTag(other.id, "user_1", "时租")).toEqual([]);
    expect(await db.listTags("user_1")).toEqual(["成本", "时租"]);
    expect((await db.purgeDiscarded({ retentionDays: 0 })).purged).toEqual([essay.id]);
    expect(await db.getArticle("user_1", essay.id)).toBeUndefined();
    expect(await db.listTags("user_1")).toEqual([]);
    expect(await db.getDocument(other.id)).toBeTruthy();
    await db.close();
  });
});

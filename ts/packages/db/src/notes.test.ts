import { describe, expect, it } from "vitest";

import { openMemoryDatabase } from "./database.js";

const user = { type: "user", id: "user_1" };
const marker = "氯茴香醚注记以后少写";

async function textsContaining(db: Awaited<ReturnType<typeof openMemoryDatabase>>, needle: string) {
  const columns = await db.query<{ table_name: string; column_name: string }>(
    `SELECT table_name, column_name
     FROM information_schema.columns
     WHERE table_schema = 'public'
       AND data_type IN ('text', 'jsonb', 'character varying')`,
  );
  const hits: string[] = [];
  for (const column of columns) {
    if (!/^[a-z_]+$/.test(column.table_name) || !/^[a-z_]+$/.test(column.column_name)) continue;
    const rows = await db.query<{ found: number }>(
      `SELECT 1 AS found FROM ${column.table_name}
       WHERE ${column.column_name}::text LIKE $1 LIMIT 1`,
      [`%${needle}%`],
    );
    if (rows.length > 0) hits.push(`${column.table_name}.${column.column_name}`);
  }
  return hits;
}

describe("写作笔记与会话清退", () => {
  it("删除后库里和审计原文都找不到这句话，改措辞会关掉旧条", async () => {
    const db = await openMemoryDatabase();
    const notes = db.notes();
    const written = await notes.write(
      "user_1",
      { text: marker, kind: "habit", scope: { level: "global" } },
      user,
    );
    expect(await textsContaining(db, marker)).toEqual(
      expect.arrayContaining(["preferences.statement", "preference_audit.after_text"]),
    );
    const corrected = await notes.correct(
      "user_1",
      written.preference.id,
      1,
      `${marker}改短`,
      user,
    );
    expect(corrected.id).not.toBe(written.preference.id);
    const listed = await notes.list("user_1");
    expect(listed.map((item) => item.statement)).toEqual([`${marker}改短`]);
    const closed = await notes.list("user_1", { status: ["superseded"] });
    expect(closed.map((item) => item.id)).toEqual([written.preference.id]);
    await notes.forget("user_1", corrected.id, user);
    await notes.forget("user_1", written.preference.id, user);
    expect(await textsContaining(db, marker)).toEqual([]);
    const trail = await notes.explain("user_1", written.preference.id);
    expect(trail.every((item) => item.before === "" && item.after === "")).toBe(true);
    expect(await notes.list("user_2")).toEqual([]);
    await db.close();
  });

  it("会话先标记丢弃，过了保留期才从库里删掉", async () => {
    let now = new Date("2026-03-01T00:00:00.000Z");
    const db = await openMemoryDatabase({ now: () => now });
    const created = await db.createDocument({ title: "会话", ownerId: "user_1" });
    const session = await db.mainSession(created.id);
    if (!session) throw new Error("没有主会话");
    await db.setSessionConversation(session.id, "41");
    const marked = await db.discardSession(session.id);
    expect((await db.discardSession(session.id)).discardedAt).toBe(marked.discardedAt);
    expect((await db.getSession(session.id))?.status).toBe("closed");
    now = new Date(now.getTime() + 29 * 24 * 60 * 60 * 1000);
    expect((await db.purgeDiscarded({ retentionDays: 30, now })).purgedSessions).toEqual([]);
    await db.undoDiscardSession(session.id);
    expect((await db.getSession(session.id))?.discardedAt).toBeNull();
    await db.discardSession(session.id);
    now = new Date(now.getTime() + 31 * 24 * 60 * 60 * 1000);
    const purged = await db.purgeDiscarded({ retentionDays: 30, now });
    expect(purged.purged).toEqual([]);
    expect(purged.purgedSessions).toEqual([
      { id: session.id, documentId: created.id, piConversationId: "41" },
    ]);
    expect(await db.getSession(session.id)).toBeUndefined();
    expect(await db.getDocument(created.id)).toBeTruthy();
    await db.close();
  });
});

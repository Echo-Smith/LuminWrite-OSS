import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { DatabaseSync } from "node:sqlite";
import { describe, expect, it } from "vitest";

import { MemoryWritingStore } from "./memory-store.js";
import { purgePiConversations } from "./pi-purge.js";
import { createWritingRuntime } from "./runtime.js";
import type { SessionSnapshot } from "./store.js";

const user = { type: "user" as const, userId: "user_1" };
const markerA = "氯茴香醚注记甲";
const markerB = "氯茴香醚注记乙";
const markerFork = "氯茴香醚注记叉";
const markerChild = "氯茴香醚注记子";

describe("写作笔记注入与 pi 会话清退", () => {
  it("active 笔记进提示词，api 会话不提供提议工具，清退后原文从 sqlite 消失", async () => {
    const dir = await mkdtemp(join(tmpdir(), "lw-notes-"));
    const store = new MemoryWritingStore();
    const created = store.createDocument({ title: "甲", ownerId: "user_1" });
    const other = store.createDocument({ title: "乙", ownerId: "user_1" });
    const apiSession = store.openApi(created.id);
    const proposed: string[] = [];
    const runtime = await createWritingRuntime({
      sqlitePath: join(dir, "pi.sqlite"),
      store,
      notes: {
        async proseFor(session) {
          if (session.id === created.sessionId) return markerA;
          if (session.id === other.sessionId) return markerB;
          return undefined;
        },
        async read(session: SessionSnapshot) {
          return [
            {
              id: "prf_visible",
              kind: "taboo",
              statement: "赋能不要出现。",
              plainConflict: true,
              scope: { level: "global" },
            },
          ].filter(() => session.kind !== "api" || true);
        },
        async propose(session, input) {
          if (session.kind === "api") return { ok: false, reason: "这个会话不能提写作笔记" };
          proposed.push(input.text);
          return { ok: true, id: "prf_new" };
        },
      },
    });
    try {
      const outlined = await runtime.submitTurn({
        documentId: created.id,
        kind: "outline",
        instruction: "先写大纲",
        scope: "document",
        delivery: "followup",
        applyMode: "auto",
        idempotencyKey: "outline-a",
        actor: user,
      });
      expect(outlined.status).toBe("applied");
      const apiTurn = await runtime.submitTurn({
        documentId: created.id,
        sessionId: apiSession,
        kind: "outline",
        instruction: "记下写作笔记 以后句子要短",
        scope: "document",
        delivery: "followup",
        applyMode: "auto",
        idempotencyKey: "api-note",
        actor: user,
      });
      expect(apiTurn.status).toBe("failed");
      expect(proposed).toEqual([]);
      const forced = await runtime.submitTurn({
        documentId: created.id,
        sessionId: apiSession,
        kind: "outline",
        instruction: "强行记下写作笔记 不要记下来",
        scope: "document",
        delivery: "followup",
        applyMode: "auto",
        idempotencyKey: "api-force",
        actor: user,
      });
      expect(forced.status).toBe("failed");
      expect(proposed).toEqual([]);
      const mainNote = await runtime.submitTurn({
        documentId: other.id,
        kind: "outline",
        instruction: "记下写作笔记 段尾不要再总结",
        scope: "document",
        delivery: "followup",
        applyMode: "auto",
        idempotencyKey: "main-note",
        actor: user,
      });
      expect(mainNote.status).toBe("failed");
      expect(proposed).toEqual(["段尾不要再总结"]);
      const read = await runtime.submitTurn({
        documentId: other.id,
        kind: "outline",
        instruction: "读取写作笔记",
        scope: "document",
        delivery: "followup",
        applyMode: "auto",
        idempotencyKey: "read-note",
        actor: user,
      });
      expect(read.status).toBe("failed");
    } finally {
      await runtime.close();
    }

    const sessionA = await store.getSession(created.sessionId);
    const sessionB = await store.getSession(other.sessionId);
    const sessionApi = await store.getSession(apiSession);
    if (
      !sessionA?.piConversationId ||
      !sessionB?.piConversationId ||
      !sessionApi?.piConversationId
    ) {
      throw new Error("会话没有 pi 对话");
    }
    const before = await readFile(join(dir, "pi.sqlite"));
    expect(before.includes(markerA)).toBe(true);
    expect(before.includes(markerB)).toBe(true);
    const apiRows = entryRecords(join(dir, "pi.sqlite"), Number(sessionApi.piConversationId));
    const quiet = apiRows.find((record) => record.includes("以后句子要短"));
    expect(quiet).toBeTruthy();
    expect(quiet).not.toContain("propose_writing_note");
    const mainText = entriesText(join(dir, "pi.sqlite"), Number(sessionB.piConversationId));
    expect(mainText).toContain("propose_writing_note");
    expect(mainText).toContain("plainConflict");

    const database = new DatabaseSync(join(dir, "pi.sqlite"));
    const nextId = (
      database.prepare("SELECT next_id FROM durable_metadata").get() as { next_id: string }
    ).next_id;
    database
      .prepare(
        "INSERT INTO conversations (id, owner_conversation_id, owner_task_id, record) VALUES (?, NULL, NULL, ?)",
      )
      .run(
        900001,
        JSON.stringify({
          id: 900001,
          parent: { conversationId: Number(sessionA.piConversationId), at: 1 },
        }),
      );
    database
      .prepare(
        "INSERT INTO entries (id, conversation_id, head, commit_seq, record) VALUES (?, ?, ?, 1, ?)",
      )
      .run(900002, 900001, 900002, JSON.stringify({ text: markerFork }));
    database
      .prepare(
        "INSERT INTO conversations (id, owner_conversation_id, owner_task_id, record) VALUES (?, ?, NULL, ?)",
      )
      .run(900003, Number(sessionA.piConversationId), JSON.stringify({ id: 900003 }));
    database
      .prepare(
        "INSERT INTO entries (id, conversation_id, head, commit_seq, record) VALUES (?, ?, ?, 1, ?)",
      )
      .run(900004, 900003, 900004, JSON.stringify({ text: markerChild }));
    database.close();

    const report = purgePiConversations(join(dir, "pi.sqlite"), [sessionA.piConversationId]);
    expect(report.deletedConversationIds).toContain(sessionA.piConversationId);
    expect(report.deletedConversationIds).toContain("900003");
    expect(report.detachedForkIds).toEqual(["900001"]);
    expect(report.vacuumed).toBe(true);
    const after = await readFile(join(dir, "pi.sqlite"));
    expect(after.includes(markerA)).toBe(false);
    expect(after.includes(markerChild)).toBe(false);
    expect(after.includes(markerB)).toBe(true);
    expect(after.includes(markerFork)).toBe(true);
    const kept = new DatabaseSync(join(dir, "pi.sqlite"));
    const fork = kept.prepare("SELECT record FROM conversations WHERE id = 900001").get() as {
      record: string;
    };
    expect(JSON.parse(fork.record).parent).toBeUndefined();
    const survivor = kept
      .prepare("SELECT id FROM conversations WHERE id = ?")
      .get(Number(sessionB.piConversationId));
    expect(survivor).toBeTruthy();
    const metadata = kept.prepare("SELECT next_id FROM durable_metadata").get() as {
      next_id: string;
    };
    expect(metadata.next_id).toBe(nextId);
    kept.close();
    await rm(dir, { recursive: true, force: true });
  });
});

function entriesText(path: string, conversationId: number): string {
  return entryRecords(path, conversationId).join("\n");
}

function entryRecords(path: string, conversationId: number): string[] {
  const database = new DatabaseSync(path);
  try {
    const rows = database
      .prepare("SELECT record FROM entries WHERE conversation_id = ?")
      .all(conversationId) as Array<{ record: string }>;
    return rows.map((row) => row.record);
  } finally {
    database.close();
  }
}

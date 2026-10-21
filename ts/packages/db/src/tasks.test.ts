import { describe, expect, it } from "vitest";

import { openMemoryDatabase } from "./database.js";

const input = {
  kind: "write" as const,
  sources: { kb: true, rss: true, library: false, web: false, urls: [] as string[] },
  review: { outlineApproval: false },
};

describe("api tasks", () => {
  it("同一把密钥的幂等键只插入一行，事件可以按序号续上", async () => {
    const db = await openMemoryDatabase();
    try {
      const first = await db.claimApiTask({
        userId: "usr_1",
        apiKeyId: "key_1",
        idempotencyKey: "k1",
        kind: "write",
        state: "submitted",
        documentId: "doc_1",
        sessionId: "ses_1",
        input,
        needsInput: null,
        maxConcurrent: 2,
      });
      expect(first.status).toBe("created");
      const again = await db.claimApiTask({
        userId: "usr_1",
        apiKeyId: "key_1",
        idempotencyKey: "k1",
        kind: "write",
        state: "submitted",
        documentId: "doc_2",
        sessionId: "ses_2",
        input,
        needsInput: null,
        maxConcurrent: 2,
      });
      expect(again.status).toBe("replay");
      if (again.status !== "replay" || first.status !== "created") return;
      expect(again.task.id).toBe(first.task.id);
      expect(again.task.documentId).toBe("doc_1");
      const events = await db.taskEventsAfter(first.task.id, 0);
      expect(events.map((event) => event.type)).toEqual(["task.submitted"]);
      const after = await db.taskEventsAfter(first.task.id, events[0]?.seq ?? 0);
      expect(after).toEqual([]);
    } finally {
      await db.close();
    }
  });

  it("未结束的任务占着并发名额，取消之后完成写不回去", async () => {
    const db = await openMemoryDatabase();
    try {
      const claimed = await db.claimApiTask({
        userId: "usr_1",
        apiKeyId: "key_1",
        idempotencyKey: "k1",
        kind: "write",
        state: "submitted",
        documentId: "doc_1",
        sessionId: "ses_1",
        input,
        needsInput: null,
        maxConcurrent: 1,
      });
      expect(claimed.status).toBe("created");
      const blocked = await db.claimApiTask({
        userId: "usr_1",
        apiKeyId: "key_1",
        idempotencyKey: "k2",
        kind: "write",
        state: "submitted",
        documentId: "doc_2",
        sessionId: "ses_2",
        input,
        needsInput: null,
        maxConcurrent: 1,
      });
      expect(blocked.status).toBe("limited");
      if (claimed.status !== "created") return;
      const canceled = await db.transitionApiTask(
        claimed.task.id,
        ["submitted", "working"],
        { state: "canceled" },
        { type: "task.canceled", payload: { state: "canceled" } },
      );
      expect(canceled?.state).toBe("canceled");
      const late = await db.transitionApiTask(
        claimed.task.id,
        ["working"],
        { state: "completed", resultVersionId: "ver_2" },
        { type: "task.completed", payload: { state: "completed" } },
      );
      expect(late).toBeUndefined();
      expect((await db.getApiTask(claimed.task.id))?.state).toBe("canceled");
      const events = await db.taskEventsAfter(claimed.task.id, 0);
      expect(events.map((event) => event.seq)).toEqual([1, 2]);
      expect(events[1]?.type).toBe("task.canceled");
    } finally {
      await db.close();
    }
  });
});

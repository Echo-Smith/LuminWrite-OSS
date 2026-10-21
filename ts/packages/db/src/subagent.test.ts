import { describe, expect, it } from "vitest";

import { openMemoryDatabase } from "./database.js";

describe("子代理运行", () => {
  it("按文档列出来，同一任务重复写入不另起一行，清除文档时一起删掉", async () => {
    const db = await openMemoryDatabase();
    try {
      const created = await db.createDocument({ title: "审校", ownerId: "user_1" });
      const begun = await db.beginTurn({
        documentId: created.id,
        kind: "review",
        instruction: "委托 reviewer 看一眼",
        scope: "document",
        delivery: "followup",
        applyMode: "auto",
        idempotencyKey: "delegate-1",
        actor: { type: "user", userId: "user_1" },
      });
      const first = await db.saveSubagentRun({
        turnId: begun.turnId,
        sessionId: created.sessionId,
        piTaskId: "task_1",
        agentRef: "reviewer",
        slot: "role.reviewer",
        modelRef: "test/faux",
        input: { question: "看一眼", blockIds: ["blk_aaaaaaaa"] },
        output: null,
        status: "succeeded",
      });
      const again = await db.saveSubagentRun({
        turnId: begun.turnId,
        sessionId: created.sessionId,
        piTaskId: "task_1",
        piConversationId: "7",
        agentRef: "reviewer",
        slot: "role.reviewer",
        modelRef: "test/faux",
        input: { question: "看一眼", blockIds: ["blk_aaaaaaaa"] },
        output: { text: "已看过。" },
        status: "succeeded",
      });
      expect(again.id).toBe(first.id);
      const listed = await db.listSubagentRuns(created.id);
      expect(listed).toHaveLength(1);
      expect(listed[0]).toMatchObject({
        agentRef: "reviewer",
        slot: "role.reviewer",
        modelRef: "test/faux",
        status: "succeeded",
        piConversationId: "7",
      });
      await db.discardDocument(created.id);
      await db.purgeDiscarded({ retentionDays: 0 });
      expect(await db.query("SELECT id FROM subagent_runs")).toEqual([]);
    } finally {
      await db.close();
    }
  });
});

import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { ensureOwner, openMemoryDatabase, readOwnerToken } from "@luminwrite/db";
import type { WritingRuntime } from "@luminwrite/harness";
import { describe, expect, it } from "vitest";

import { createApp } from "./app.js";
import { createOssAuth } from "./auth.js";

function stubRuntime(): WritingRuntime {
  return {
    async submit() {
      return { submissionId: "sub" };
    },
    async abort() {},
    async resume() {},
    async *watch() {},
    async close() {},
    async events() {
      return [];
    },
    async readRelatedDossier() {
      throw new Error("未实现");
    },
    async submitTurn() {
      return { turnId: "trn_stub", status: "applied", submissionId: "sub" };
    },
  };
}

describe("子代理运行", () => {
  it("按文档列出 delegate 记下的运行", async () => {
    const dir = await mkdtemp(join(tmpdir(), "lw-roles-http-"));
    const db = await openMemoryDatabase();
    const owner = await ensureOwner(db, { dataDir: join(dir, "data") });
    const token = await readOwnerToken(join(dir, "data"));
    const app = createApp({ db, runtime: stubRuntime(), auth: createOssAuth(db) });
    try {
      const created = await db.createDocument({ title: "审校", ownerId: owner.userId });
      const begun = await db.beginTurn({
        documentId: created.id,
        kind: "review",
        instruction: "委托 reviewer 看一眼",
        scope: "document",
        delivery: "followup",
        applyMode: "auto",
        idempotencyKey: "delegate-http",
        actor: { type: "user", userId: owner.userId },
      });
      await db.saveSubagentRun({
        turnId: begun.turnId,
        sessionId: created.sessionId,
        piTaskId: "task_http",
        agentRef: "reviewer",
        slot: "role.reviewer",
        modelRef: "test/faux",
        input: { question: "看一眼", blockIds: [] },
        output: { code: "ok" },
        status: "succeeded",
      });
      const response = await app.request(`/api/v1/documents/${created.id}/subagent-runs`, {
        headers: { authorization: `Bearer ${token}` },
      });
      expect(response.status).toBe(200);
      const body = (await response.json()) as Array<{ agentRef: string; slot: string }>;
      expect(body).toHaveLength(1);
      expect(body[0]).toMatchObject({ agentRef: "reviewer", slot: "role.reviewer" });
    } finally {
      await db.close();
      await rm(dir, { recursive: true, force: true });
    }
  });
});

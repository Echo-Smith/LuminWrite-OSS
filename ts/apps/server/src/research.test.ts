import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { EditOp } from "@luminwrite/contracts";
import { ensureOwner, openMemoryDatabase, readOwnerToken } from "@luminwrite/db";
import { createWritingRuntime, writingStoreFromDatabase } from "@luminwrite/harness";
import { createKnowledgeBase } from "@luminwrite/kb";
import { describe, expect, it } from "vitest";

import { createApp } from "./app.js";
import { createOssAuth } from "./auth.js";
import { sessionKnowledge } from "./kb-session.js";

function insert(markdown: string): EditOp {
  return {
    op: "insert_block",
    afterBlockId: null,
    newMarkdown: markdown,
    reason: "起稿",
  };
}

describe("检索计划", () => {
  it("faux 回合落库，接口能读回，没有引用的断言被标出", async () => {
    const dir = await mkdtemp(join(tmpdir(), "lw-research-"));
    const db = await openMemoryDatabase();
    const owner = await ensureOwner(db, { dataDir: join(dir, "data") });
    const token = await readOwnerToken(join(dir, "data"));
    const kb = createKnowledgeBase({ db });
    const runtime = await createWritingRuntime({
      sqlitePath: join(dir, "pi.sqlite"),
      store: writingStoreFromDatabase(db),
      knowledge: sessionKnowledge(db, kb),
      loadEvidence: async (documentId) => {
        const document = await db.getDocument(documentId);
        if (!document) return [];
        return kb.listEvidence(document.ownerId, documentId);
      },
    });
    const app = createApp({ db, runtime, auth: createOssAuth(db), kb });
    const request = (path: string, init?: RequestInit) => {
      const headers = new Headers(init?.headers);
      headers.set("authorization", `Bearer ${token}`);
      if (init?.body && !headers.has("content-type")) {
        headers.set("content-type", "application/json");
      }
      return app.request(path, { ...init, headers });
    };
    try {
      const created = await db.createDocument({ title: "研究稿", ownerId: owner.userId });
      expect(
        (
          await db.applyManual({
            documentId: created.id,
            actor: { type: "user", userId: owner.userId },
            ops: [insert("因此可以断定成本下降了 30%。")],
          })
        ).ok,
      ).toBe(true);
      const turn = await request(`/api/v1/documents/${created.id}/turns`, {
        method: "POST",
        body: JSON.stringify({
          kind: "research",
          instruction: "查显卡时租",
          scope: "document",
          delivery: "followup",
          applyMode: "auto",
          idempotencyKey: "research-1",
        }),
      });
      expect(turn.status).toBe(200);
      expect(await turn.json()).toMatchObject({ status: "applied" });
      const plans = await request(`/api/v1/documents/${created.id}/plans`);
      expect(plans.status).toBe(200);
      const body = (await plans.json()) as Array<{
        plan: { questions: Array<{ id: string }> };
        gaps: string[];
      }>;
      expect(body).toHaveLength(1);
      expect(body[0]?.plan.questions[0]?.id).toBe("q1");
      expect(body[0]?.gaps).toEqual(["q1: 尚未有通过校验的证据"]);
      const events = await request(`/api/v1/documents/${created.id}/events`);
      const listed = (await events.json()) as Array<{ type: string }>;
      expect(listed.some((event) => event.type === "research.plan")).toBe(true);
      expect(listed.some((event) => event.type === "claim.flags")).toBe(true);
    } finally {
      await runtime.close();
      await db.close();
      await rm(dir, { recursive: true, force: true });
    }
  });
});

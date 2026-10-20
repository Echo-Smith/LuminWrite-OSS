import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createApiKey, ensureOwner, openMemoryDatabase, readOwnerToken } from "@luminwrite/db";
import type { WritingRuntime } from "@luminwrite/harness";
import { describe, expect, it } from "vitest";

import { requiredScope } from "./api-access.js";
import { createApp } from "./app.js";
import { createOssAuth } from "./auth.js";

const marker = "氯茴香醚注记接口";

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

async function boot() {
  const dir = await mkdtemp(join(tmpdir(), "lw-notes-http-"));
  const dataDir = join(dir, "data");
  const db = await openMemoryDatabase();
  const owner = await ensureOwner(db, { dataDir });
  const token = await readOwnerToken(dataDir);
  const app = createApp({ db, runtime: stubRuntime(), auth: createOssAuth(db), env: {} });
  const request = (path: string, init?: RequestInit, bearer: string | undefined = token) => {
    const headers = new Headers(init?.headers);
    if (bearer) headers.set("authorization", `Bearer ${bearer}`);
    if (init?.body && !headers.has("content-type")) headers.set("content-type", "application/json");
    return Promise.resolve(app.request(path, { ...init, headers }));
  };
  return {
    db,
    owner,
    request,
    async close() {
      await db.close();
      await rm(dir, { recursive: true, force: true });
    },
  };
}

describe("写作笔记接口", () => {
  it("所有者可以确认、改写、导出和删除；密钥不能碰", async () => {
    const ctx = await boot();
    try {
      expect(requiredScope("POST", "/api/v1/me/writing-notes")).toBe("owner-only");
      expect(requiredScope("POST", "/api/v1/documents/doc_1/sessions/ses_1/discard")).toBe(
        "owner-only",
      );
      const key = await createApiKey(ctx.db, { userId: ctx.owner.userId, name: "外部" });
      const denied = await ctx.request(
        "/api/v1/me/writing-notes",
        {
          method: "POST",
          body: JSON.stringify({ text: marker, kind: "habit", scopeLevel: "global" }),
        },
        key.token,
      );
      expect(denied.status).toBe(403);

      const created = await ctx.request("/api/v1/documents", {
        method: "POST",
        body: JSON.stringify({ title: "笔记文档" }),
      });
      const document = (await created.json()) as { id: string; sessionId?: string };
      const doc = await ctx.db.getDocument(document.id);
      const session = await ctx.db.mainSession(document.id);
      if (!doc || !session) throw new Error("没有文档");

      const hollow = await ctx.request("/api/v1/me/writing-notes", {
        method: "POST",
        body: JSON.stringify({ text: "赋能不要出现。", kind: "taboo", scopeLevel: "global" }),
      });
      expect(hollow.status).toBe(201);
      const hollowBody = (await hollow.json()) as {
        id: string;
        plainConflict: boolean;
        status: string;
      };
      expect(hollowBody.plainConflict).toBe(true);
      expect(hollowBody.status).toBe("active");

      const noted = await ctx.request("/api/v1/me/writing-notes", {
        method: "POST",
        body: JSON.stringify({
          text: marker,
          kind: "habit",
          scopeLevel: "document",
          scopeRef: doc.id,
        }),
      });
      expect(noted.status).toBe(201);
      const note = (await noted.json()) as { id: string; version: number; factHint: boolean };
      expect(note.factHint).toBe(false);

      const candidate = await ctx.db.notes().propose(ctx.owner.userId, {
        text: "段尾不要再总结一遍。",
        kind: "structure",
        scope: { level: "global" },
        sessionId: session.id,
      });
      const accepted = await ctx.request(`/api/v1/me/writing-notes/${candidate.id}/review`, {
        method: "POST",
        body: JSON.stringify({ action: "accept" }),
      });
      expect(accepted.status).toBe(200);
      expect(((await accepted.json()) as { status: string }).status).toBe("active");

      const overlay = await ctx.db.notes().compileOverlay(ctx.owner.userId, { documentId: doc.id });
      expect(overlay.prose).toContain(marker);
      expect(overlay.prose).toContain("段尾不要再总结一遍。");
      expect(overlay.prose).not.toContain("赋能");

      const corrected = await ctx.request(`/api/v1/me/writing-notes/${note.id}/correct`, {
        method: "POST",
        body: JSON.stringify({ expectedVersion: note.version, text: `${marker}改短` }),
      });
      expect(corrected.status).toBe(200);
      const next = (await corrected.json()) as { id: string; statement: string };
      expect(next.id).not.toBe(note.id);
      const listed = await ctx.request("/api/v1/me/writing-notes");
      const body = (await listed.json()) as { notes: Array<{ id: string; statement: string }> };
      expect(body.notes.map((item) => item.id)).not.toContain(note.id);
      expect(body.notes.some((item) => item.statement === `${marker}改短`)).toBe(true);

      const exported = await ctx.request("/api/v1/me/writing-notes/export");
      expect(exported.status).toBe(200);
      expect(JSON.stringify(await exported.json())).toContain(`${marker}改短`);

      const removed = await ctx.request(`/api/v1/me/writing-notes/${next.id}`, {
        method: "DELETE",
      });
      expect(removed.status).toBe(200);
      const receipt = (await removed.json()) as { status: string; notice: string };
      expect(receipt.status).toBe("verified");
      expect(receipt.notice).toContain("聊天记录");
      const removedOld = await ctx.request(`/api/v1/me/writing-notes/${note.id}`, {
        method: "DELETE",
      });
      expect(removedOld.status).toBe(200);
      const gone = JSON.stringify(
        await (await ctx.request("/api/v1/me/writing-notes/export")).json(),
      );
      expect(gone).not.toContain(marker);

      const keyDiscard = await ctx.request(
        `/api/v1/documents/${doc.id}/sessions/${session.id}/discard`,
        { method: "POST" },
        key.token,
      );
      expect(keyDiscard.status).toBe(403);
      const discarded = await ctx.request(
        `/api/v1/documents/${doc.id}/sessions/${session.id}/discard`,
        { method: "POST" },
      );
      expect(discarded.status).toBe(200);
      expect((await ctx.db.getSession(session.id))?.discardedAt).toBeTruthy();
    } finally {
      await ctx.close();
    }
  });
});

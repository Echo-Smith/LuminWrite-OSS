import { randomBytes } from "node:crypto";
import { mkdtemp, readdir, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
  createApiKey,
  ensureOwner,
  openDatabase,
  openMemoryDatabase,
  readOwnerToken,
} from "@luminwrite/db";
import type { WritingRuntime } from "@luminwrite/harness";
import { createWritingRuntime, writingStoreFromDatabase } from "@luminwrite/harness";
import { describe, expect, it } from "vitest";

import { requiredScope } from "./api-access.js";
import { createApp } from "./app.js";
import { createOssAuth } from "./auth.js";

const notes = {
  async proseFor() {
    return undefined;
  },
  async read() {
    return [];
  },
  async propose() {
    return { ok: true as const, id: "prf_cleanup" };
  },
};

async function filesContaining(dir: string, needle: string): Promise<string[]> {
  const hits: string[] = [];
  const encoded = Buffer.from(needle);
  async function walk(current: string): Promise<void> {
    const entries = await readdir(current, { withFileTypes: true }).catch(() => undefined);
    if (!entries) return;
    for (const entry of entries) {
      const path = join(current, entry.name);
      if (entry.isDirectory()) {
        await walk(path);
        continue;
      }
      const bytes = await readFile(path);
      if (bytes.includes(encoded)) hits.push(path.slice(dir.length));
    }
  }
  await walk(dir);
  return hits;
}

describe("M2-00 清理", () => {
  it("删除会看工作是否还在跑，失败回到已归档且不重试", async () => {
    expect(requiredScope("DELETE", "/api/v1/sessions/ses_1")).toBe("owner-only");
    expect(requiredScope("DELETE", "/api/v1/documents/doc_1")).toBe("owner-only");
    expect(requiredScope("POST", "/api/v1/sessions/ses_1/archive")).toBe("owner-only");
    expect(requiredScope("GET", "/api/v1/turns/trn_1")).toBe("tasks:read");

    const dir = await mkdtemp(join(tmpdir(), "lw-del-"));
    const dataDir = join(dir, "data");
    const db = await openMemoryDatabase();
    const owner = await ensureOwner(db, { dataDir });
    const token = await readOwnerToken(dataDir);
    const runtime = await createWritingRuntime({
      sqlitePath: join(dir, "pi.sqlite"),
      store: writingStoreFromDatabase(db),
      notes,
    });
    let purgeCalls = 0;
    const guarded: WritingRuntime = {
      ...runtime,
      async purgePiConversations() {
        purgeCalls += 1;
        throw new Error("清退 pi 失败：磁盘忙");
      },
    };
    const app = createApp({ db, runtime: guarded, auth: createOssAuth(db) });
    const request = (path: string, init?: RequestInit, bearer: string | undefined = token) => {
      const headers = new Headers(init?.headers);
      if (bearer) headers.set("authorization", `Bearer ${bearer}`);
      if (init?.body && !headers.has("content-type"))
        headers.set("content-type", "application/json");
      return app.request(path, { ...init, headers });
    };
    try {
      const settings = await request("/api/v1/me/settings");
      expect(await settings.json()).toEqual({ autoPurgeDiscarded: false });

      const createdRes = await request("/api/v1/documents", {
        method: "POST",
        body: JSON.stringify({ title: "要删的会话" }),
      });
      const created = (await createdRes.json()) as { id: string };
      const listed = (await (
        await request(`/api/v1/documents/${created.id}/sessions`)
      ).json()) as Array<{ id: string }>;
      const sessionId = listed[0]?.id;
      if (!sessionId) throw new Error("没有会话");

      const early = await request(`/api/v1/sessions/${sessionId}`, { method: "DELETE" });
      expect(early.status).toBe(409);
      expect(await early.json()).toEqual({ error: "先归档，再永久删除" });

      const begun = await db.beginTurn({
        documentId: created.id,
        sessionId,
        kind: "outline",
        instruction: "还在写",
        scope: "document",
        delivery: "followup",
        applyMode: "auto",
        idempotencyKey: "busy-turn",
        actor: { type: "user", userId: owner.userId },
      });
      await db.markTurn(begun.turnId, { status: "running" });
      await db.discardSession(sessionId);
      const busy = await request(`/api/v1/sessions/${sessionId}`, { method: "DELETE" });
      expect(busy.status).toBe(409);
      expect(await busy.json()).toEqual({
        error: "还有 1 个回合在生成，等它结束或先取消",
      });
      expect((await db.getSession(sessionId))?.status).toBe("closed");
      const skipped = await db.purgeDiscarded({ retentionDays: 0 });
      expect(skipped.purgedSessions.map((row) => row.id)).not.toContain(sessionId);
      expect(skipped.skipped.some((row) => row.id === sessionId)).toBe(true);

      await db.markTurn(begun.turnId, { status: "cancelled" });
      const claimed = await db.claimPermanentSessionDelete(sessionId);
      if (!claimed.ok) throw new Error(claimed.error);
      const during = await request(`/api/v1/documents/${created.id}/turns`, {
        method: "POST",
        body: JSON.stringify({
          sessionId,
          kind: "outline",
          instruction: "删除过程中不要开始",
          scope: "document",
          delivery: "followup",
          applyMode: "auto",
          idempotencyKey: "during-delete",
        }),
      });
      expect(during.status).toBe(409);
      expect(((await during.json()) as { error: string }).error).toContain("正在删除");
      await db.releasePermanentSessionDelete(sessionId);

      await db.setSessionConversation(sessionId, "77");
      const failed = await request(`/api/v1/sessions/${sessionId}`, { method: "DELETE" });
      expect(failed.status).toBe(500);
      expect(await failed.json()).toEqual({ error: "清退 pi 失败：磁盘忙" });
      expect(purgeCalls).toBe(1);
      const restored = await db.getSession(sessionId);
      expect(restored?.status).toBe("closed");
      expect(restored?.discardedAt).toBeTruthy();

      const key = await createApiKey(db, { userId: owner.userId, name: "外部" });
      const denied = await request(
        `/api/v1/sessions/${sessionId}`,
        { method: "DELETE" },
        key.token,
      );
      expect(denied.status).toBe(403);

      const doc = await request("/api/v1/documents", {
        method: "POST",
        body: JSON.stringify({ title: "要删的文档" }),
      });
      const documentId = ((await doc.json()) as { id: string }).id;
      const docSessions = (await (
        await request(`/api/v1/documents/${documentId}/sessions`)
      ).json()) as Array<{ id: string }>;
      const docSession = docSessions[0]?.id;
      if (!docSession) throw new Error("文档没有会话");
      await request(`/api/v1/documents/${documentId}/discard`, { method: "POST" });
      const docTurn = await db.beginTurn({
        documentId,
        sessionId: docSession,
        kind: "outline",
        instruction: "文档上还有回合",
        scope: "document",
        delivery: "followup",
        applyMode: "auto",
        idempotencyKey: "doc-busy",
        actor: { type: "user", userId: owner.userId },
      });
      await db.markTurn(docTurn.turnId, { status: "running" });
      const docBusy = await request(`/api/v1/documents/${documentId}`, { method: "DELETE" });
      expect(docBusy.status).toBe(409);
      expect(((await docBusy.json()) as { error: string }).error).toContain("1 个回合在生成");
      expect(await db.getDocument(documentId)).toBeTruthy();
    } finally {
      await runtime.close();
      await db.close();
      await rm(dir, { recursive: true, force: true });
    }
  }, 30_000);

  it("只记笔记的回合是 no_change，失败回合的原因能从接口读到", async () => {
    const dir = await mkdtemp(join(tmpdir(), "lw-turn-"));
    const dataDir = join(dir, "data");
    const db = await openMemoryDatabase();
    await ensureOwner(db, { dataDir });
    const token = await readOwnerToken(dataDir);
    const runtime = await createWritingRuntime({
      sqlitePath: join(dir, "pi.sqlite"),
      store: writingStoreFromDatabase(db),
      notes,
    });
    const app = createApp({ db, runtime, auth: createOssAuth(db) });
    const request = (path: string, init?: RequestInit) => {
      const headers = new Headers(init?.headers);
      headers.set("authorization", `Bearer ${token}`);
      if (init?.body) headers.set("content-type", "application/json");
      return app.request(path, { ...init, headers });
    };
    try {
      const created = (await (
        await request("/api/v1/documents", {
          method: "POST",
          body: JSON.stringify({ title: "笔记回合" }),
        })
      ).json()) as { id: string };
      const noted = await request(`/api/v1/documents/${created.id}/turns`, {
        method: "POST",
        body: JSON.stringify({
          kind: "outline",
          instruction: "记下写作笔记 段尾不要再总结",
          scope: "document",
          delivery: "followup",
          applyMode: "auto",
          idempotencyKey: "note-only",
        }),
      });
      expect(noted.status).toBe(200);
      const noteBody = (await noted.json()) as {
        status: string;
        error: string | null;
        turnId: string;
      };
      expect(noteBody.status).toBe("no_change");
      expect(noteBody.error).toBeNull();
      const review = await request(`/api/v1/documents/${created.id}/turns`, {
        method: "POST",
        body: JSON.stringify({
          kind: "review",
          instruction: "审一审",
          scope: "document",
          delivery: "followup",
          applyMode: "auto",
          idempotencyKey: "review-1",
        }),
      });
      const reviewBody = (await review.json()) as { status: string; error: string; turnId: string };
      expect(reviewBody.status).toBe("failed");
      expect(reviewBody.error).toBe("尚未实现");
      const fetched = await request(`/api/v1/turns/${reviewBody.turnId}`);
      expect(fetched.status).toBe(200);
      expect(await fetched.json()).toMatchObject({
        id: reviewBody.turnId,
        status: "failed",
        error: "尚未实现",
      });
    } finally {
      await runtime.close();
      await db.close();
      await rm(dir, { recursive: true, force: true });
    }
  }, 30_000);

  it("Harness 不关、另一个会话开着时，清掉的原文不再留在数据目录里", async () => {
    const dir = await mkdtemp(join(tmpdir(), "lw-purge-"));
    const dataDir = join(dir, "data");
    const db = await openDatabase({ kind: "pglite", dataDir });
    const owner = await ensureOwner(db, { dataDir });
    const token = await readOwnerToken(dataDir);
    const runtime = await createWritingRuntime({
      sqlitePath: join(dataDir, "pi.sqlite"),
      store: writingStoreFromDatabase(db),
      notes,
    });
    const app = createApp({ db, runtime, auth: createOssAuth(db) });
    const request = (path: string, init?: RequestInit) => {
      const headers = new Headers(init?.headers);
      headers.set("authorization", `Bearer ${token}`);
      if (init?.body) headers.set("content-type", "application/json");
      return app.request(path, { ...init, headers });
    };
    const marker = `PROBE${randomBytes(1800).toString("hex")}`;
    const kept = `KEEP${randomBytes(16).toString("hex")}`;
    try {
      const first = await db.createDocument({ title: "被删会话", ownerId: owner.userId });
      const second = await db.createDocument({ title: "还开着", ownerId: owner.userId });
      const actor = { type: "user" as const, userId: owner.userId };
      await runtime.submitTurn({
        documentId: first.id,
        sessionId: first.sessionId,
        kind: "outline",
        instruction: marker,
        scope: "document",
        delivery: "followup",
        applyMode: "auto",
        idempotencyKey: "purge-marker",
        actor,
      });
      await runtime.submitTurn({
        documentId: second.id,
        sessionId: second.sessionId,
        kind: "outline",
        instruction: kept,
        scope: "document",
        delivery: "followup",
        applyMode: "auto",
        idempotencyKey: "purge-kept",
        actor,
      });
      expect(await filesContaining(dataDir, marker)).not.toEqual([]);
      await db.discardSession(first.sessionId);
      const deleted = await request(`/api/v1/sessions/${first.sessionId}`, { method: "DELETE" });
      expect(deleted.status).toBe(200);
      const body = (await deleted.json()) as { deleted: boolean; residue: string; detail: string };
      expect(body.deleted).toBe(true);
      const left = await filesContaining(dataDir, marker);
      expect(body.residue, left.join("\n")).toBe("cleared");
      expect(left).toEqual([]);
      expect(body.detail).toBe("已删除");
      expect(await filesContaining(dataDir, kept)).not.toEqual([]);
      expect(await db.getSession(first.sessionId)).toBeUndefined();
      expect(await db.getSession(second.sessionId)).toBeTruthy();
      expect(await db.getDocument(first.id)).toBeTruthy();
    } finally {
      await runtime.close();
      await db.close();
      await rm(dir, { recursive: true, force: true });
    }
  }, 60_000);
});

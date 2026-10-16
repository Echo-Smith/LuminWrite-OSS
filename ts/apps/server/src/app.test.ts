import { mkdtemp, rm, stat } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { BlockId, EditOp } from "@luminwrite/contracts";
import {
  ensureOwner,
  hashOwnerToken,
  openMemoryDatabase,
  readOwnerToken,
  rotateOwnerToken,
} from "@luminwrite/db";
import { blockHashSync, blockText, listBlocks, type PMJSON } from "@luminwrite/doc-model";
import { createWritingRuntime, writingStoreFromDatabase } from "@luminwrite/harness";
import { describe, expect, it } from "vitest";

import { createApp } from "./app.js";
import { createOssAuth } from "./auth.js";
import { resolveListen } from "./listen.js";
import { startServer } from "./serve.js";

function replaceFirst(doc: PMJSON, newText: string): EditOp {
  const node = listBlocks(doc)[0];
  if (!node) throw new Error("没有块");
  return {
    op: "replace_text",
    blockId: node.attrs?.id as BlockId,
    contentHash: blockHashSync(node),
    expectedOldText: blockText(node),
    newText,
    reason: "手改",
  };
}

async function boot() {
  const dir = await mkdtemp(join(tmpdir(), "lw-http-"));
  const dataDir = join(dir, "data");
  const db = await openMemoryDatabase();
  const owner = await ensureOwner(db, { dataDir });
  const token = await readOwnerToken(dataDir);
  const runtime = await createWritingRuntime({
    sqlitePath: join(dir, "pi.sqlite"),
    store: writingStoreFromDatabase(db),
    usage: { record: (event) => db.recordUsage(event) },
  });
  const app = createApp({ db, runtime, auth: createOssAuth(db) });
  const request = (path: string, init?: RequestInit, bearer: string | undefined = token) => {
    const headers = new Headers(init?.headers);
    if (bearer) headers.set("authorization", `Bearer ${bearer}`);
    if (init?.body && !headers.has("content-type")) headers.set("content-type", "application/json");
    return app.request(path, { ...init, headers });
  };
  return {
    dir,
    dataDir,
    db,
    runtime,
    app,
    token,
    owner,
    request,
    async close() {
      await runtime.close();
      await db.close();
      await rm(dir, { recursive: true, force: true });
    },
  };
}

describe("HTTP", () => {
  it("建文档、出大纲、写初稿、只改一段、回滚", async () => {
    const ctx = await boot();
    const health = await ctx.app.request("/health");
    expect(await health.json()).toMatchObject({ ok: true });

    const createdRes = await ctx.request("/api/v1/documents", {
      method: "POST",
      body: JSON.stringify({ title: "推理成本", ownerId: "user_1" }),
    });
    expect(createdRes.status).toBe(201);
    const created = (await createdRes.json()) as { id: string };
    const stored = await ctx.db.getDocument(created.id);
    expect(stored?.ownerId).toBe(ctx.owner.userId);
    const turn = async (kind: string, key: string, instruction: string) => {
      const response = await ctx.request(`/api/v1/documents/${created.id}/turns`, {
        method: "POST",
        body: JSON.stringify({
          kind,
          instruction,
          scope: kind === "revise" ? "selection" : "document",
          delivery: "followup",
          applyMode: "auto",
          idempotencyKey: key,
          actor: { type: "user", userId: "intruder" },
        }),
      });
      expect(response.status).toBe(200);
      return (await response.json()) as { status: string; versionId?: string };
    };

    const outline = await turn("outline", "outline-1", "先写大纲");
    expect(outline.status).toBe("applied");
    const draft = await turn("draft", "draft-1", "按大纲写初稿");
    expect(draft.status).toBe("applied");
    const before = await ctx.request(`/api/v1/documents/${created.id}`);
    const drafted = (await before.json()) as { blocks: Array<{ text: string; blockId: string }> };
    const revised = await turn("revise", "revise-1", "只改第2段，写短一点");
    expect(revised.status).toBe("applied");
    const afterRes = await ctx.request(`/api/v1/documents/${created.id}`);
    const after = (await afterRes.json()) as { blocks: Array<{ text: string; blockId: string }> };
    expect(after.blocks[0]?.text).toBe(drafted.blocks[0]?.text);
    expect(after.blocks[2]?.text).toBe(drafted.blocks[2]?.text);
    expect(after.blocks[1]?.text).not.toBe(drafted.blocks[1]?.text);
    expect(after.blocks[0]?.blockId).toBe(drafted.blocks[0]?.blockId);
    expect(after.blocks[2]?.blockId).toBe(drafted.blocks[2]?.blockId);

    const versions = (await (
      await ctx.request(`/api/v1/documents/${created.id}/versions`)
    ).json()) as Array<{ id: string; actorType: string; actorId: string }>;
    expect(versions.every((version) => version.actorId === ctx.owner.userId)).toBe(true);
    expect(versions.some((version) => version.actorId === "intruder")).toBe(false);
    const rolled = await ctx.request(`/api/v1/documents/${created.id}/rollback`, {
      method: "POST",
      body: JSON.stringify({ versionId: versions[1]?.id, actor: { type: "system" } }),
    });
    expect(rolled.status).toBe(200);
    const restored = (await (await ctx.request(`/api/v1/documents/${created.id}`)).json()) as {
      blocks: Array<{ text: string }>;
    };
    expect(restored.blocks.map((block) => block.text)).toEqual([
      "推理成本先看三件事：显卡时租、显存占用、批大小。",
      "批大小从 1 提到 8 时，单次请求分到的固定开销会下降。",
      "建议先量出当前批大小和每小时费用，再决定加机器还是调批大小。",
    ]);

    const events = await ctx.request(`/api/v1/documents/${created.id}/events?after=0`);
    expect((await events.json()) as unknown[]).not.toHaveLength(0);
    const stream = await ctx.request(`/api/v1/documents/${created.id}/events/stream`);
    expect(await stream.text()).toContain("id: ");

    const head = await ctx.db.getDocument(created.id);
    const manual = await ctx.request(`/api/v1/documents/${created.id}/ops`, {
      method: "POST",
      body: JSON.stringify({
        actor: { type: "api_client", userId: "intruder", keyId: "key_x" },
        ops: [replaceFirst(head?.head?.content ?? { type: "doc" }, "手改第一段。")],
      }),
    });
    expect(manual.status).toBe(200);
    const afterManual = (await (
      await ctx.request(`/api/v1/documents/${created.id}/versions`)
    ).json()) as Array<{ actorType: string; actorId: string }>;
    expect(afterManual.at(-1)).toMatchObject({ actorType: "user", actorId: ctx.owner.userId });
    const stale = await ctx.request(`/api/v1/documents/${created.id}/ops`, {
      method: "POST",
      body: JSON.stringify({
        ops: [replaceFirst(head?.head?.content ?? { type: "doc" }, "再改一次但锚点是旧的。")],
      }),
    });
    expect(stale.status).toBe(409);
    await ctx.close();
  });

  it("没有令牌是 401，换令牌后旧令牌也是 401，库里只有哈希", async () => {
    const ctx = await boot();
    const missing = await ctx.request("/api/v1/documents", { method: "POST", body: "{}" }, "");
    expect(missing.status).toBe(401);
    const health = await ctx.app.request("/health");
    expect(health.status).toBe(200);

    const fileMode = (await stat(join(ctx.dataDir, "owner.token"))).mode & 0o777;
    expect(fileMode).toBe(0o600);
    const row = await ctx.db.getOwner();
    expect(row?.tokenHash).toBe(hashOwnerToken(ctx.token));
    expect(row?.tokenHash).not.toContain(ctx.token);
    expect(JSON.stringify(row)).not.toContain(ctx.token);

    await rotateOwnerToken(ctx.db, { dataDir: ctx.dataDir });
    const expired = await ctx.request("/api/v1/documents", {
      method: "POST",
      body: JSON.stringify({ title: "旧令牌" }),
    });
    expect(expired.status).toBe(401);
    const next = await readOwnerToken(ctx.dataDir);
    expect(next).not.toBe(ctx.token);
    const created = await ctx.request(
      "/api/v1/documents",
      { method: "POST", body: JSON.stringify({ title: "新令牌" }) },
      next,
    );
    expect(created.status).toBe(201);
    const rotated = await ctx.db.getOwner();
    expect(rotated?.tokenHash).toBe(hashOwnerToken(next));
    expect(JSON.stringify(rotated)).not.toContain(next);
    await ctx.close();
  });

  it("文档级授权走 EditAuthorizer", async () => {
    const ctx = await boot();
    const foreign = await ctx.db.createDocument({ title: "别人的", ownerId: "user_other" });
    const denied = await ctx.request(`/api/v1/documents/${foreign.id}`);
    expect(denied.status).toBe(403);
    const missing = await ctx.request("/api/v1/documents/doc_missing");
    expect(missing.status).toBe(404);

    const blocked = createApp({
      db: ctx.db,
      runtime: ctx.runtime,
      auth: createOssAuth(ctx.db),
      authorizer: {
        async canEdit() {
          return false;
        },
      },
    });
    const created = await ctx.request("/api/v1/documents", {
      method: "POST",
      body: JSON.stringify({ title: "自己的" }),
    });
    const id = ((await created.json()) as { id: string }).id;
    const hidden = await blocked.request(`/api/v1/documents/${id}`, {
      headers: { authorization: `Bearer ${ctx.token}` },
    });
    expect(hidden.status).toBe(403);
    await ctx.close();
  });
});

describe("监听", () => {
  it("默认绑回环，外网地址带警告", () => {
    expect(resolveListen({})).toEqual({ host: "127.0.0.1", port: 8787 });
    expect(resolveListen({ LW_HOST: "localhost", LW_PORT: "9" })).toEqual({
      host: "localhost",
      port: 9,
    });
    expect(resolveListen({ LW_HOST: "0.0.0.0" }).warning).toContain("所有者令牌");
    expect(() => resolveListen({ LW_PORT: "nope" })).toThrow(/LW_PORT/);
  });

  it("进程默认用 PGlite，未带令牌的 /api/v1 返回 401", async () => {
    const dir = await mkdtemp(join(tmpdir(), "lw-serve-"));
    const running = await startServer({
      LW_DATA_DIR: dir,
      LW_HOST: "127.0.0.1",
      LW_PORT: "0",
    });
    try {
      expect(running.url.startsWith("http://127.0.0.1:")).toBe(true);
      expect(running.warning).toBeUndefined();
      const health = await fetch(`${running.url}/health`);
      expect(health.status).toBe(200);
      const denied = await fetch(`${running.url}/api/v1/documents`);
      expect(denied.status).toBe(401);
      const token = await readOwnerToken(dir);
      const created = await fetch(`${running.url}/api/v1/documents`, {
        method: "POST",
        headers: {
          authorization: `Bearer ${token}`,
          "content-type": "application/json",
        },
        body: JSON.stringify({ title: "本机", actor: { type: "user", userId: "nope" } }),
      });
      expect(created.status).toBe(201);
    } finally {
      await running.close();
      await rm(dir, { recursive: true, force: true });
    }
  });
});

import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { BlockId, EditOp } from "@luminwrite/contracts";
import { createApiKey, ensureOwner, openMemoryDatabase, readOwnerToken } from "@luminwrite/db";
import { blockHashSync, blockText, listBlocks, type PMJSON } from "@luminwrite/doc-model";
import type { WritingRuntime } from "@luminwrite/harness";
import { describe, expect, it } from "vitest";

import { requiredScope } from "./api-access.js";
import { createApp } from "./app.js";
import { createOssAuth } from "./auth.js";

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

const insert: EditOp = {
  op: "insert_block",
  afterBlockId: null,
  newMarkdown: "原文。",
  reason: "起稿",
};

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
  const dir = await mkdtemp(join(tmpdir(), "lw-save-"));
  const dataDir = join(dir, "data");
  const db = await openMemoryDatabase();
  const owner = await ensureOwner(db, { dataDir });
  const token = await readOwnerToken(dataDir);
  const runtime = stubRuntime();
  const app = createApp({ db, runtime, auth: createOssAuth(db), env: {} });
  const request = (path: string, init?: RequestInit, bearer: string | undefined = token) => {
    const headers = new Headers(init?.headers);
    if (bearer) headers.set("authorization", `Bearer ${bearer}`);
    if (init?.body && !headers.has("content-type")) headers.set("content-type", "application/json");
    return app.request(path, { ...init, headers });
  };
  return {
    db,
    owner,
    request,
    async close() {
      await runtime.close();
      await db.close();
      await rm(dir, { recursive: true, force: true });
    },
  };
}

describe("文档保存", () => {
  it("首次手动保存后才进列表，连续修订合并，回滚追加版本", async () => {
    const ctx = await boot();
    try {
      const createdRes = await ctx.request("/api/v1/documents", {
        method: "POST",
        body: JSON.stringify({ title: "草稿" }),
      });
      expect(createdRes.status).toBe(201);
      const created = (await createdRes.json()) as { id: string; versionId: string };
      const hidden = (await (await ctx.request("/api/v1/documents")).json()) as Array<{
        id: string;
      }>;
      expect(hidden).toEqual([]);

      const seeded = await ctx.request(`/api/v1/documents/${created.id}/ops`, {
        method: "POST",
        body: JSON.stringify({ ops: [insert] }),
      });
      expect(seeded.status).toBe(200);
      const saved = await ctx.request(`/api/v1/documents/${created.id}/save`, { method: "POST" });
      expect(saved.status).toBe(200);
      expect(((await saved.json()) as { already: boolean }).already).toBe(false);
      const listed = (await (await ctx.request("/api/v1/documents")).json()) as Array<{
        id: string;
      }>;
      expect(listed.map((item) => item.id)).toEqual([created.id]);

      const doc = (await (await ctx.request(`/api/v1/documents/${created.id}`)).json()) as {
        head: { content: PMJSON };
      };
      const first = await ctx.request(`/api/v1/documents/${created.id}/ops`, {
        method: "POST",
        body: JSON.stringify({ ops: [replaceFirst(doc.head.content, "第一下。")] }),
      });
      const firstBody = (await first.json()) as { ok: boolean; versionId: string };
      const mid = (await (await ctx.request(`/api/v1/documents/${created.id}`)).json()) as {
        head: { content: PMJSON };
      };
      const second = await ctx.request(`/api/v1/documents/${created.id}/ops`, {
        method: "POST",
        body: JSON.stringify({ ops: [replaceFirst(mid.head.content, "第二下。")] }),
      });
      const secondBody = (await second.json()) as { versionId: string; seq: number };
      expect(secondBody.versionId).toBe(firstBody.versionId);

      const versions = (await (
        await ctx.request(`/api/v1/documents/${created.id}/versions`)
      ).json()) as Array<{ id: string; origin: string }>;
      const restored = await ctx.request(`/api/v1/documents/${created.id}/restore`, {
        method: "POST",
        body: JSON.stringify({ versionId: versions[0]?.id }),
      });
      expect(restored.status).toBe(200);
      const after = (await (await ctx.request(`/api/v1/documents/${created.id}`)).json()) as {
        head: { origin: string; id: string };
        headVersionId: string;
      };
      expect(after.head.origin).toBe("restore");
      const more = (await (
        await ctx.request(`/api/v1/documents/${created.id}/versions`)
      ).json()) as Array<{ id: string }>;
      expect(more.length).toBe(versions.length + 1);
      expect(more[0]?.id).toBe(versions[0]?.id);

      const version = await ctx.request(`/api/v1/versions/${after.headVersionId}`);
      expect(version.status).toBe(200);
      const diff = await ctx.request(
        `/api/v1/documents/${created.id}/diff?from=${versions[1]?.id}&to=${after.headVersionId}`,
      );
      expect(diff.status).toBe(200);
      const changes = (await diff.json()) as Array<{ kind: string }>;
      expect(changes.some((item) => item.kind === "modified" || item.kind === "removed")).toBe(
        true,
      );

      const missing = await ctx.request(`/api/v1/documents/${created.id}/diff`);
      expect(missing.status).toBe(400);
    } finally {
      await ctx.close();
    }
  });

  it("密钥建文档即已保存，并能回滚；不能清退", async () => {
    const ctx = await boot();
    try {
      const key = await createApiKey(ctx.db, {
        userId: ctx.owner.userId,
        name: "写手",
        scopes: ["documents:read", "documents:write"],
      });
      const createdRes = await ctx.request(
        "/api/v1/documents",
        { method: "POST", body: JSON.stringify({ title: "外部" }) },
        key.token,
      );
      expect(createdRes.status).toBe(201);
      const created = (await createdRes.json()) as { id: string; versionId: string };
      const listed = (await (
        await ctx.request("/api/v1/documents", { method: "GET" }, key.token)
      ).json()) as Array<{
        id: string;
        createdVia: string;
      }>;
      expect(listed).toEqual([expect.objectContaining({ id: created.id, createdVia: "api" })]);

      const seeded = await ctx.request(
        `/api/v1/documents/${created.id}/ops`,
        { method: "POST", body: JSON.stringify({ ops: [insert] }) },
        key.token,
      );
      expect(seeded.status).toBe(200);
      const rolled = await ctx.request(
        `/api/v1/documents/${created.id}/rollback`,
        { method: "POST", body: JSON.stringify({ versionId: created.versionId }) },
        key.token,
      );
      expect(rolled.status).toBe(200);
      const head = (await (await ctx.request(`/api/v1/documents/${created.id}`)).json()) as {
        head: { origin: string; apiKeyId: string | null };
      };
      expect(head.head.origin).toBe("restore");
      expect(head.head.apiKeyId).toBe(key.id);

      const purged = await ctx.request(
        "/api/v1/admin/purge-discarded",
        { method: "POST", body: JSON.stringify({}) },
        key.token,
      );
      expect(purged.status).toBe(403);
      expect(requiredScope("GET", "/api/v1/versions/ver_1")).toBe("documents:read");
      expect(requiredScope("POST", "/api/v1/admin/purge-discarded")).toBe("owner-only");
    } finally {
      await ctx.close();
    }
  });

  it("丢弃后列表看不到，撤销后回来，过期清退才删掉", async () => {
    const ctx = await boot();
    try {
      const createdRes = await ctx.request("/api/v1/documents", {
        method: "POST",
        body: JSON.stringify({ title: "要丢" }),
      });
      const created = (await createdRes.json()) as { id: string; versionId: string };
      await ctx.request(`/api/v1/documents/${created.id}/save`, { method: "POST" });
      const discarded = await ctx.request(`/api/v1/documents/${created.id}/discard`, {
        method: "POST",
      });
      expect(discarded.status).toBe(200);
      expect(await (await ctx.request("/api/v1/documents")).json()).toEqual([]);
      const hidden = (await (await ctx.request(`/api/v1/documents/${created.id}`)).json()) as {
        discardedAt: string | null;
      };
      expect(hidden.discardedAt).toBeTruthy();

      const undone = await ctx.request(`/api/v1/documents/${created.id}/discard/undo`, {
        method: "POST",
      });
      expect(undone.status).toBe(200);
      expect(
        (await (await ctx.request("/api/v1/documents")).json()) as Array<{ id: string }>,
      ).toEqual([expect.objectContaining({ id: created.id })]);

      await ctx.request(`/api/v1/documents/${created.id}/discard`, { method: "POST" });
      const purged = await ctx.request("/api/v1/admin/purge-discarded", {
        method: "POST",
        body: JSON.stringify({ retentionDays: 0 }),
      });
      expect(purged.status).toBe(200);
      expect(await purged.json()).toEqual({ purged: [created.id] });
      expect((await ctx.request(`/api/v1/documents/${created.id}`)).status).toBe(404);
      expect(await ctx.db.getVersion(created.versionId)).toBeUndefined();
    } finally {
      await ctx.close();
    }
  });
});

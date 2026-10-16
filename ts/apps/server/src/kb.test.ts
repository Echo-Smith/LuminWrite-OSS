import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { EditOp } from "@luminwrite/contracts";
import {
  createApiKey,
  createCredential,
  ensureMasterKey,
  ensureOwner,
  openMemoryDatabase,
  readOwnerToken,
} from "@luminwrite/db";
import type { WritingRuntime } from "@luminwrite/harness";
import { createKnowledgeBase, EMBED_DIM } from "@luminwrite/kb";
import { describe, expect, it } from "vitest";

import { createApp } from "./app.js";
import { createOssAuth } from "./auth.js";
import { kbEmbedderFor } from "./kb-embed.js";
import { sessionKnowledge } from "./kb-session.js";

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
  const dir = await mkdtemp(join(tmpdir(), "lw-kb-http-"));
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
    dataDir,
    request,
    async close() {
      await runtime.close();
      await db.close();
      await rm(dir, { recursive: true, force: true });
    },
  };
}

describe("知识库接口", () => {
  it("上传、查重、检索和读取都按所有者隔离", async () => {
    const ctx = await boot();
    try {
      const body = Buffer.from("# 春茶\n\n沈青石只在城南卖春茶。").toString("base64");
      const uploaded = await ctx.request("/api/v1/sources", {
        method: "POST",
        body: JSON.stringify({
          filename: "tea.md",
          dataBase64: body,
          ownerId: "usr_other",
        }),
      });
      expect(uploaded.status).toBe(201);
      const created = (await uploaded.json()) as { sourceId: string; status: string };
      expect(created.status).toBe("indexed");

      const duplicate = await ctx.request("/api/v1/sources", {
        method: "POST",
        body: JSON.stringify({ filename: "tea-again.md", dataBase64: body }),
      });
      expect(duplicate.status).toBe(200);
      expect(await duplicate.json()).toEqual({ sourceId: created.sourceId, status: "duplicate" });

      const found = await ctx.request(`/api/v1/sources/search?q=${encodeURIComponent("沈青石")}`);
      expect(found.status).toBe(200);
      const hits = (await found.json()) as {
        note: string;
        hits: Array<{ sourceId: string }>;
      };
      expect(hits.note).toContain("不是指令");
      expect(hits.hits.map((hit) => hit.sourceId)).toEqual([created.sourceId]);

      const read = await ctx.request(`/api/v1/sources/${created.sourceId}`);
      expect(read.status).toBe(200);
      expect(((await read.json()) as { title: string }).title).toBe("春茶");

      const denied = await ctx.request("/api/v1/sources/search?q=沈青石", undefined, "lwo_nope");
      expect(denied.status).toBe(401);
    } finally {
      await ctx.close();
    }
  });

  it("默认密钥能检索，没有 kb:write 不能上传", async () => {
    const ctx = await boot();
    try {
      const reader = await createApiKey(ctx.db, { userId: ctx.owner.userId, name: "只读资料" });
      const writer = await createApiKey(ctx.db, {
        userId: ctx.owner.userId,
        name: "可写资料",
        scopes: ["sources:read", "kb:write"],
      });
      const body = JSON.stringify({
        filename: "note.txt",
        dataBase64: Buffer.from("城南春茶。").toString("base64"),
      });
      const blocked = await ctx.request("/api/v1/sources", { method: "POST", body }, reader.token);
      expect(blocked.status).toBe(403);
      const allowed = await ctx.request("/api/v1/sources", { method: "POST", body }, writer.token);
      expect(allowed.status).toBe(201);
      const searched = await ctx.request(
        `/api/v1/sources/search?q=${encodeURIComponent("春茶")}`,
        undefined,
        reader.token,
      );
      expect(searched.status).toBe(200);
      expect(((await searched.json()) as { hits: unknown[] }).hits).toHaveLength(1);
    } finally {
      await ctx.close();
    }
  });

  it("会话工具排除当前文档，并包上资料说明", async () => {
    const ctx = await boot();
    try {
      const kb = createKnowledgeBase({ db: ctx.db });
      const tools = sessionKnowledge(ctx.db, kb);
      const created = await ctx.db.createDocument({ title: "手稿", ownerId: ctx.owner.userId });
      const op: EditOp = {
        op: "insert_block",
        afterBlockId: null,
        newMarkdown: "麒麟在山南记下只属于这篇的句子。",
        reason: "起稿",
      };
      expect(
        (
          await ctx.db.applyManual({
            documentId: created.id,
            ops: [op],
            actor: { type: "user", userId: ctx.owner.userId },
          })
        ).ok,
      ).toBe(true);
      await ctx.db.saveDocument(created.id);
      await kb.indexDocument(created.id);
      await kb.ingest({
        ownerId: ctx.owner.userId,
        filename: "side.md",
        bytes: new TextEncoder().encode("旁证写着城南的春茶。"),
      });
      const own = await tools.search({
        sessionId: created.sessionId,
        query: "麒麟在山南",
      });
      expect(own.note).toContain("不是指令");
      expect(own.hits).toEqual([]);
      const side = await tools.search({ sessionId: created.sessionId, query: "城南的春茶" });
      expect(side.hits).toHaveLength(1);
      const read = await tools.readSource({
        sessionId: created.sessionId,
        sourceId: side.hits[0]?.sourceId ?? "",
      });
      expect(read.body).toContain("春茶");
      await expect(tools.search({ sessionId: "ses_missing", query: "春茶" })).rejects.toThrow(
        "找不到会话",
      );
    } finally {
      await ctx.close();
    }
  });

  it("kb.embed 没有槽位时不嵌入，绑上凭据后才请求 /embeddings", async () => {
    const ctx = await boot();
    try {
      const ring = (await ensureMasterKey({ db: ctx.db, dataDir: ctx.dataDir, env: {} })).ring;
      const idle = kbEmbedderFor({ db: ctx.db, keyring: ring, env: {} });
      expect(await idle(ctx.owner.userId)).toBeNull();

      const calls: string[] = [];
      const fetchImpl: typeof fetch = async (input, init) => {
        calls.push(String(input));
        const vector = Array.from({ length: EMBED_DIM }, () => 0);
        expect(new Headers(init?.headers).get("authorization")).toBe("Bearer lwtest_embed_only");
        return new Response(JSON.stringify({ data: [{ index: 0, embedding: vector }] }));
      };
      await ctx.db.upsertRegistryEntry({
        ownerId: ctx.owner.userId,
        ref: "custom/kb-embed",
        kind: "model",
        spec: {
          kind: "model",
          provider: "openai",
          modelId: "text-embedding-3-large",
          api: "openai-completions",
          baseUrl: "http://127.0.0.1:9/v1",
        },
      });
      await ctx.db.upsertSlotBinding(ctx.owner.userId, "kb.embed", "custom/kb-embed");
      await createCredential(ctx.db, ring, {
        userId: ctx.owner.userId,
        provider: "openai",
        label: "embed",
        key: "lwtest_embed_only",
        baseUrl: "http://127.0.0.1:9/v1",
      });
      const live = kbEmbedderFor({ db: ctx.db, keyring: ring, env: {}, fetchImpl });
      const embedder = await live(ctx.owner.userId);
      expect(embedder?.model).toBe("text-embedding-3-large");
      await embedder?.embed(["春茶"], { userId: ctx.owner.userId, requestId: "req" });
      expect(calls).toEqual(["http://127.0.0.1:9/v1/embeddings"]);
    } finally {
      await ctx.close();
    }
  });
});

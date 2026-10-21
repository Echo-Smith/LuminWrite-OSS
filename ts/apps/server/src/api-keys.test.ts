import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { BlockId, EditOp } from "@luminwrite/contracts";
import { ensureOwner, hashApiKey, openMemoryDatabase, readOwnerToken } from "@luminwrite/db";
import { blockHashSync, blockText, listBlocks, type PMJSON } from "@luminwrite/doc-model";
import {
  createWritingRuntime,
  type WritingRuntime,
  writingStoreFromDatabase,
} from "@luminwrite/harness";
import { describe, expect, it } from "vitest";

import { resolveApiApplyMode } from "./api-access.js";
import { createApp } from "./app.js";
import { createOssAuth } from "./auth.js";
import { TokenBucketLimiter } from "./rate-limit.js";

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

function stubRuntime(submitTurn?: WritingRuntime["submitTurn"]): WritingRuntime {
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
    async submitTurn(req) {
      if (submitTurn) return submitTurn(req);
      return { turnId: "trn_stub", status: "applied", submissionId: "sub" };
    },
  };
}

async function boot(options?: { limiter?: TokenBucketLimiter; runtime?: WritingRuntime }) {
  const dir = await mkdtemp(join(tmpdir(), "lw-keys-http-"));
  const dataDir = join(dir, "data");
  const db = await openMemoryDatabase();
  const owner = await ensureOwner(db, { dataDir });
  const token = await readOwnerToken(dataDir);
  const runtime =
    options?.runtime ??
    (await createWritingRuntime({
      sqlitePath: join(dir, "pi.sqlite"),
      store: writingStoreFromDatabase(db),
      usage: { record: (event) => db.recordUsage(event) },
    }));
  const app = createApp({
    db,
    runtime,
    auth: createOssAuth(db),
    ...(options?.limiter ? { limiter: options.limiter } : {}),
  });
  const request = (path: string, init?: RequestInit, bearer: string | undefined = token) => {
    const headers = new Headers(init?.headers);
    if (bearer) headers.set("authorization", `Bearer ${bearer}`);
    if (init?.body && !headers.has("content-type")) headers.set("content-type", "application/json");
    return app.request(path, { ...init, headers });
  };
  return {
    db,
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

async function issue(
  ctx: Awaited<ReturnType<typeof boot>>,
  body: Record<string, unknown> = { name: "外部" },
) {
  const response = await ctx.request("/api/v1/me/api-keys", {
    method: "POST",
    body: JSON.stringify(body),
  });
  expect(response.status).toBe(201);
  return (await response.json()) as {
    id: string;
    token: string;
    scopes: string[];
    defaultApplyMode: string;
  };
}

describe("API 密钥策略", () => {
  it("明文只出现在创建响应里，库和列表都没有", async () => {
    const ctx = await boot({ runtime: stubRuntime() });
    try {
      const issued = await issue(ctx, { name: "只显示一次", webhookSecret: "lwtest_hook_secret" });
      expect(issued.token).toMatch(/^lw_[0-9A-Za-z]{8}_[0-9A-Za-z]{43}$/);
      expect(issued.scopes).not.toContain("kb:write");
      const listed = await ctx.request("/api/v1/me/api-keys");
      const listBody = JSON.stringify(await listed.json());
      expect(listBody).not.toContain(issued.token);
      expect(listBody).not.toContain(issued.token.split("_")[2]);
      expect(listBody).not.toContain("lwtest_hook_secret");
      const stored = await ctx.db.findApiKeyByHash(hashApiKey(issued.token));
      expect(stored?.hash).toBe(hashApiKey(issued.token));
      expect(JSON.stringify(stored)).not.toContain(issued.token);
      expect(JSON.stringify(stored)).not.toContain(issued.token.split("_")[2]);
    } finally {
      await ctx.close();
    }
  });

  it("越权、凭据、笔记、订阅和签发都是 403，吊销后是 401", async () => {
    const ctx = await boot({ runtime: stubRuntime() });
    try {
      const created = await ctx.request("/api/v1/documents", {
        method: "POST",
        body: JSON.stringify({ title: "稿" }),
      });
      const documentId = ((await created.json()) as { id: string }).id;
      const readOnly = await issue(ctx, { name: "只读", scopes: ["documents:read"] });
      const deniedOps = await ctx.request(
        `/api/v1/documents/${documentId}/ops`,
        { method: "POST", body: JSON.stringify({ ops: [] }) },
        readOnly.token,
      );
      expect(deniedOps.status).toBe(403);
      const deniedTurn = await ctx.request(
        `/api/v1/documents/${documentId}/turns`,
        {
          method: "POST",
          body: JSON.stringify({
            kind: "outline",
            instruction: "写大纲",
            scope: "document",
            delivery: "followup",
            idempotencyKey: "k1",
          }),
        },
        readOnly.token,
      );
      expect(deniedTurn.status).toBe(403);

      const writer = await issue(ctx, { name: "能写" });
      for (const path of [
        "/api/v1/me/api-keys",
        "/api/v1/me/credentials",
        "/api/v1/me/writing-notes",
        "/api/v1/me/model-slots/balanced",
        "/api/v1/feeds",
        "/api/v1/subscriptions",
      ]) {
        const response = await ctx.request(path, { method: "POST", body: "{}" }, writer.token);
        expect(response.status, path).toBe(403);
      }
      const addKb = await ctx.request("/api/v1/kb", { method: "POST", body: "{}" }, writer.token);
      expect(addKb.status).toBe(403);
      const ownerCredentials = await ctx.request("/api/v1/me/credentials");
      expect(ownerCredentials.status).toBe(200);
      expect(await ownerCredentials.json()).toEqual([]);

      expect(
        (await ctx.request("/api/v1/documents", { method: "GET" }, writer.token)).status,
      ).not.toBe(401);
      await ctx.request(`/api/v1/me/api-keys/${writer.id}`, { method: "DELETE" });
      const revoked = await ctx.request("/api/v1/documents", { method: "GET" }, writer.token);
      expect(revoked.status).toBe(401);
    } finally {
      await ctx.close();
    }
  });

  it("超速率返回 429 和 Retry-After", async () => {
    const ctx = await boot({
      runtime: stubRuntime(),
      limiter: new TokenBucketLimiter({ capacity: 1, refillPerSecond: 0 }),
    });
    try {
      const created = await ctx.request("/api/v1/documents", {
        method: "POST",
        body: JSON.stringify({ title: "限流" }),
      });
      const documentId = ((await created.json()) as { id: string }).id;
      const key = await issue(ctx);
      const first = await ctx.request(
        `/api/v1/documents/${documentId}`,
        { method: "GET" },
        key.token,
      );
      expect(first.status).toBe(200);
      const second = await ctx.request(
        `/api/v1/documents/${documentId}`,
        { method: "GET" },
        key.token,
      );
      expect(second.status).toBe(429);
      expect(Number(second.headers.get("retry-after"))).toBeGreaterThanOrEqual(1);
    } finally {
      await ctx.close();
    }
  });

  it("并发上限生效，当日成本到顶也是 429", async () => {
    let release = () => {};
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    let started = 0;
    const ctx = await boot({
      runtime: stubRuntime(async () => {
        started += 1;
        await gate;
        return { turnId: "trn_busy", status: "applied", submissionId: "sub" };
      }),
    });
    try {
      const created = await ctx.request("/api/v1/documents", {
        method: "POST",
        body: JSON.stringify({ title: "并发" }),
      });
      const documentId = ((await created.json()) as { id: string }).id;
      const key = await issue(ctx, { name: "并发", maxConcurrentTasks: 1, dailyCostCapUsd: 1 });
      const turn = (idempotencyKey: string) =>
        ctx.request(
          `/api/v1/documents/${documentId}/turns`,
          {
            method: "POST",
            body: JSON.stringify({
              kind: "outline",
              instruction: "写大纲",
              scope: "document",
              delivery: "followup",
              idempotencyKey,
            }),
          },
          key.token,
        );
      const first = turn("one");
      for (let i = 0; i < 50 && started < 1; i++) {
        await new Promise((resolve) => setTimeout(resolve, 10));
      }
      expect(started).toBe(1);
      const second = await turn("two");
      expect(second.status).toBe(429);
      expect(second.headers.get("retry-after")).toBe("1");
      release();
      expect((await first).status).toBe(200);

      await ctx.db.recordUsage({
        requestId: "cost",
        model: "faux-1",
        inputTokens: 1,
        outputTokens: 1,
        costMicros: 1_000_000,
        apiKeyId: key.id,
      });
      const capped = await ctx.request(
        `/api/v1/documents/${documentId}`,
        { method: "GET" },
        key.token,
      );
      expect(capped.status).toBe(429);
      expect(Number(capped.headers.get("retry-after"))).toBeGreaterThanOrEqual(1);
      expect(await capped.json()).toMatchObject({ error: "已达到当日成本上限" });
    } finally {
      release();
      await ctx.close();
    }
  });

  it("人改过的文档上，外部 Agent 的回合默认挂提案", async () => {
    const seen: Array<"auto" | "proposal"> = [];
    const ctx = await boot({
      runtime: stubRuntime(async (req) => {
        seen.push(req.applyMode);
        return {
          turnId: "trn_mode",
          status: req.applyMode === "proposal" ? "proposed" : "applied",
          submissionId: "sub",
        };
      }),
    });
    try {
      const created = await ctx.request("/api/v1/documents", {
        method: "POST",
        body: JSON.stringify({ title: "手改过" }),
      });
      const documentId = ((await created.json()) as { id: string }).id;
      await ctx.db.exec(`update documents set human_touched = true where id = '${documentId}'`);
      const key = await issue(ctx, { name: "默认提案" });
      const proposed = await ctx.request(
        `/api/v1/documents/${documentId}/turns`,
        {
          method: "POST",
          body: JSON.stringify({
            kind: "revise",
            instruction: "改短一点",
            scope: "document",
            delivery: "followup",
            idempotencyKey: "omit-mode",
          }),
        },
        key.token,
      );
      expect(proposed.status).toBe(200);
      expect(await proposed.json()).toMatchObject({ status: "proposed" });

      const locked = await issue(ctx, { name: "只许提案", defaultApplyMode: "proposal" });
      const forced = await ctx.request(
        `/api/v1/documents/${documentId}/turns`,
        {
          method: "POST",
          body: JSON.stringify({
            kind: "revise",
            instruction: "再改",
            scope: "document",
            delivery: "followup",
            applyMode: "auto",
            idempotencyKey: "forced",
          }),
        },
        locked.token,
      );
      expect(await forced.json()).toMatchObject({ status: "proposed" });
      expect(seen).toEqual(["proposal", "proposal"]);

      const tier = await issue(ctx, { name: "档位", allowedTiers: ["balanced"] });
      const wrongTier = await ctx.request(
        `/api/v1/documents/${documentId}/turns`,
        {
          method: "POST",
          body: JSON.stringify({
            kind: "outline",
            instruction: "写大纲",
            scope: "document",
            delivery: "followup",
            model: "tier/pro",
            idempotencyKey: "tier",
          }),
        },
        tier.token,
      );
      expect(wrongTier.status).toBe(403);
    } finally {
      await ctx.close();
    }
  });

  it("真实回合在 human_touched 文档上走提案", async () => {
    const ctx = await boot();
    try {
      const created = await ctx.request("/api/v1/documents", {
        method: "POST",
        body: JSON.stringify({ title: "推理成本" }),
      });
      const documentId = ((await created.json()) as { id: string }).id;
      const outline = await ctx.request(`/api/v1/documents/${documentId}/turns`, {
        method: "POST",
        body: JSON.stringify({
          kind: "outline",
          instruction: "先写大纲",
          scope: "document",
          delivery: "followup",
          applyMode: "auto",
          idempotencyKey: "outline-1",
        }),
      });
      expect(outline.status).toBe(200);
      const draft = await ctx.request(`/api/v1/documents/${documentId}/turns`, {
        method: "POST",
        body: JSON.stringify({
          kind: "draft",
          instruction: "按大纲写初稿",
          scope: "document",
          delivery: "followup",
          applyMode: "auto",
          idempotencyKey: "draft-1",
        }),
      });
      expect(draft.status).toBe(200);
      const doc = await ctx.db.getDocument(documentId);
      const manual = await ctx.request(`/api/v1/documents/${documentId}/ops`, {
        method: "POST",
        body: JSON.stringify({
          ops: [replaceFirst(doc?.head?.content ?? { type: "doc" }, "手改第一段。")],
        }),
      });
      expect(manual.status).toBe(200);
      const touched = await ctx.db.getDocument(documentId);
      expect(touched?.humanTouched).toBe(true);
      const key = await issue(ctx);
      const turn = await ctx.request(
        `/api/v1/documents/${documentId}/turns`,
        {
          method: "POST",
          body: JSON.stringify({
            kind: "revise",
            instruction: "只改第2段，写短一点",
            scope: "document",
            delivery: "followup",
            idempotencyKey: "agent-revise",
          }),
        },
        key.token,
      );
      expect(turn.status).toBe(200);
      expect(await turn.json()).toMatchObject({ status: "proposed" });
      const after = await ctx.db.getDocument(documentId);
      expect(after?.headVersionId).toBe(touched?.headVersionId);
    } finally {
      await ctx.close();
    }
  });
});

describe("落稿方式", () => {
  it("D10 和策略默认值", () => {
    expect(resolveApiApplyMode({ humanTouched: true, policyDefault: "auto" })).toBe("proposal");
    expect(
      resolveApiApplyMode({ requested: "auto", humanTouched: true, policyDefault: "proposal" }),
    ).toBe("proposal");
    expect(
      resolveApiApplyMode({ requested: "auto", humanTouched: true, policyDefault: "auto" }),
    ).toBe("auto");
    expect(
      resolveApiApplyMode({ requested: "auto", humanTouched: false, policyDefault: "proposal" }),
    ).toBe("proposal");
    expect(resolveApiApplyMode({ humanTouched: false, policyDefault: "auto" })).toBe("auto");
  });
});

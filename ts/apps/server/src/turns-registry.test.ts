import { randomBytes } from "node:crypto";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
  createApiKey,
  createCredential,
  ensureOwner,
  importEnvCredential,
  loadMasterKey,
  openMemoryDatabase,
  readOwnerToken,
} from "@luminwrite/db";
import { createWritingRuntime, writingStoreFromDatabase } from "@luminwrite/harness";
import { createClosedModels, probeResolved } from "@luminwrite/models";
import { describe, expect, it } from "vitest";

import { createApp } from "./app.js";
import { createOssAuth } from "./auth.js";
import { resolveTurnChain, turnSpendFor } from "./model-registry.js";

async function boot(extra: Record<string, string> = {}) {
  const dir = await mkdtemp(join(tmpdir(), "lw-turn-http-"));
  const dataDir = join(dir, "data");
  const db = await openMemoryDatabase();
  const owner = await ensureOwner(db, { dataDir });
  const token = await readOwnerToken(dataDir);
  const env = {
    LW_SECRETS_KEY: randomBytes(32).toString("base64"),
    NODE_ENV: "test",
    OPENAI_API_KEY: "lwtest_ambient_turn_key",
    ...extra,
  };
  const ring = (await loadMasterKey({ dataDir, env })).ring;
  const models = createClosedModels();
  const registry = { db, env, keyring: ring, models };
  const runtime = await createWritingRuntime({
    sqlitePath: join(dir, "pi.sqlite"),
    store: writingStoreFromDatabase(db),
    usage: { record: (event) => db.recordUsage(event) },
    models,
    env,
    resolveTurn: (input) => resolveTurnChain(registry, input),
    turnSpend: (actor) => turnSpendFor(registry, actor),
  });
  const app = createApp({
    db,
    runtime,
    auth: createOssAuth(db),
    keyring: ring,
    env,
    models,
  });
  const request = (path: string, init?: RequestInit, bearer: string | undefined = token) => {
    const headers = new Headers(init?.headers);
    if (bearer) headers.set("authorization", `Bearer ${bearer}`);
    if (init?.body && !headers.has("content-type")) headers.set("content-type", "application/json");
    return app.request(path, { ...init, headers });
  };
  return {
    db,
    owner,
    ring,
    request,
    async close() {
      await runtime.close();
      await db.close();
      await rm(dir, { recursive: true, force: true });
    },
  };
}

function turnBody(key: string) {
  return JSON.stringify({
    kind: "outline",
    instruction: "先写大纲",
    scope: "document",
    delivery: "followup",
    applyMode: "auto",
    idempotencyKey: key,
    actor: { type: "user", userId: "intruder" },
  });
}

describe("服务进程的回合", () => {
  it("没有凭据时用 test/faux，不读取环境里的供应商密钥", async () => {
    const ctx = await boot();
    try {
      const created = await ctx.request("/api/v1/documents", {
        method: "POST",
        body: JSON.stringify({ title: "无凭据" }),
      });
      const document = (await created.json()) as { id: string; sessionId: string };
      const response = await ctx.request(`/api/v1/documents/${document.id}/turns`, {
        method: "POST",
        body: turnBody("bare-1"),
      });
      expect(response.status).toBe(200);
      const body = (await response.json()) as { status: string; turnId: string };
      expect(body.status).toBe("applied");
      const turn = await ctx.db.getTurn(body.turnId);
      expect(turn?.modelRef).toBe("test/faux");
      const session = await ctx.db.getSession(document.sessionId);
      expect(session?.modelPin).toBe("test/faux");
      const usage = await ctx.db.usageForTurn(body.turnId);
      expect(usage[0]).toMatchObject({
        userId: ctx.owner.userId,
        apiKeyId: null,
        credentialId: "local",
        modelRef: "test/faux",
      });
      expect(JSON.stringify(usage)).not.toContain("lwtest_ambient_turn_key");
    } finally {
      await ctx.close();
    }
  });

  it("配了测试凭据就走注册表，换槽后新回合换模型", async () => {
    const ctx = await boot();
    try {
      await createCredential(ctx.db, ctx.ring, {
        userId: ctx.owner.userId,
        provider: "test",
        label: "测试",
        key: "lwtest_registered_turn",
      });
      const saved = await ctx.request("/api/v1/me/registry", {
        method: "PUT",
        body: JSON.stringify({
          ref: "custom/other",
          spec: { kind: "model", provider: "test", modelId: "faux-1" },
        }),
      });
      expect(saved.status).toBe(200);
      const created = await ctx.request("/api/v1/documents", {
        method: "POST",
        body: JSON.stringify({ title: "有凭据" }),
      });
      const document = (await created.json()) as { id: string; sessionId: string };
      const bound = await ctx.request("/api/v1/me/model-slots/writer.main", {
        method: "PUT",
        body: JSON.stringify({ targetRef: "custom/other" }),
      });
      expect(bound.status).toBe(200);
      const first = await ctx.request(`/api/v1/documents/${document.id}/turns`, {
        method: "POST",
        body: turnBody("reg-1"),
      });
      const firstBody = (await first.json()) as { status: string; turnId: string };
      expect(firstBody.status).toBe("applied");
      expect((await ctx.db.getTurn(firstBody.turnId))?.modelRef).toBe("custom/other");
      expect((await ctx.db.getSession(document.sessionId))?.modelPin).toBe("custom/other");
      const usage = await ctx.db.usageForTurn(firstBody.turnId);
      expect(usage[0]?.credentialId).not.toBe("local");
      expect(usage[0]?.userId).toBe(ctx.owner.userId);
      expect(JSON.stringify(usage)).not.toContain("lwtest_registered_turn");

      await ctx.request("/api/v1/me/model-slots/writer.main", {
        method: "PUT",
        body: JSON.stringify({ targetRef: "test/faux" }),
      });
      const second = await ctx.request(`/api/v1/documents/${document.id}/turns`, {
        method: "POST",
        body: turnBody("reg-2"),
      });
      const secondBody = (await second.json()) as { turnId: string; status: string };
      expect(secondBody.status).toBe("applied");
      expect((await ctx.db.getTurn(secondBody.turnId))?.modelRef).toBe("test/faux");
      expect((await ctx.db.getSession(document.sessionId))?.modelPin).toBe("test/faux");
    } finally {
      await ctx.close();
    }
  });

  it("所有者当日上限返回 429，API 密钥回合记下 key id", async () => {
    const ctx = await boot({ LW_DAILY_COST_CAP_USD: "0" });
    try {
      const created = await ctx.request("/api/v1/documents", {
        method: "POST",
        body: JSON.stringify({ title: "上限" }),
      });
      const documentId = ((await created.json()) as { id: string }).id;
      const capped = await ctx.request(`/api/v1/documents/${documentId}/turns`, {
        method: "POST",
        body: turnBody("cap-1"),
      });
      expect(capped.status).toBe(429);
      expect(await capped.json()).toMatchObject({ error: "已到今日费用上限" });
      expect(capped.headers.get("retry-after")).toBeTruthy();
    } finally {
      await ctx.close();
    }

    const open = await boot();
    try {
      const created = await open.request("/api/v1/documents", {
        method: "POST",
        body: JSON.stringify({ title: "密钥" }),
      });
      const documentId = ((await created.json()) as { id: string }).id;
      const issued = await createApiKey(open.db, {
        userId: open.owner.userId,
        name: "外部",
        scopes: ["tasks:write", "documents:read"],
      });
      const response = await open.request(
        `/api/v1/documents/${documentId}/turns`,
        { method: "POST", body: turnBody("key-1") },
        issued.token,
      );
      expect(response.status).toBe(200);
      const body = (await response.json()) as { turnId: string; status: string };
      expect(body.status).toBe("applied");
      const usage = await open.db.usageForTurn(body.turnId);
      expect(usage[0]).toMatchObject({ userId: open.owner.userId, apiKeyId: issued.id });
    } finally {
      await open.close();
    }
  });

  it.skipIf(process.env.LW_LIVE !== "1")("只有 LW_LIVE=1 才探测已导入的真实模型", async () => {
    const key = process.env.LW_LLM_API_KEY ?? process.env.OPENAI_API_KEY ?? "";
    expect(key.length).toBeGreaterThan(0);
    const ctx = await boot({
      LW_LLM_ENABLE: "1",
      LW_LLM_API_KEY: key,
      NODE_ENV: "production",
    });
    try {
      const imported = await importEnvCredential(
        ctx.db,
        ctx.ring,
        {
          LW_LLM_ENABLE: "1",
          LW_LLM_API_KEY: key,
          NODE_ENV: "production",
        },
        ctx.owner.userId,
      );
      expect(imported).not.toBe("skipped");
      const models = createClosedModels();
      const registry = {
        db: ctx.db,
        env: { LW_LLM_ENABLE: "1", NODE_ENV: "production" },
        keyring: ctx.ring,
        models,
      };
      const choice = await resolveTurnChain(registry, {
        actor: { type: "user", userId: ctx.owner.userId },
        slot: "writer.main",
        sessionPin: null,
        now: Date.now(),
        cooldownUntil: new Map(),
      });
      const model = choice.chain[0];
      expect(model).toBeTruthy();
      expect(model?.ref.startsWith("test/")).toBe(false);
      if (!model) return;
      await probeResolved(models, model);
    } finally {
      await ctx.close();
    }
  });
});

import { randomBytes } from "node:crypto";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
  createCredential,
  ensureOwner,
  loadMasterKey,
  openMemoryDatabase,
  readOwnerToken,
  revealCredential,
} from "@luminwrite/db";
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

async function boot() {
  const dir = await mkdtemp(join(tmpdir(), "lw-cred-http-"));
  const dataDir = join(dir, "data");
  const db = await openMemoryDatabase();
  const owner = await ensureOwner(db, { dataDir });
  const token = await readOwnerToken(dataDir);
  const env = { LW_SECRETS_KEY: randomBytes(32).toString("base64") };
  const ring = (await loadMasterKey({ dataDir, env })).ring;
  const runtime = stubRuntime();
  const app = createApp({ db, runtime, auth: createOssAuth(db), keyring: ring, env });
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
    ring,
    request,
    runtime,
    async close() {
      await runtime.close();
      await db.close();
      await rm(dir, { recursive: true, force: true });
    },
  };
}

describe("凭据接口", () => {
  it("响应、错误页和日志里都没有密钥，留空的 PATCH 保留原值，API 密钥是 403", async () => {
    const ctx = await boot();
    const key = `lwtest_${randomBytes(12).toString("hex")}`;
    const logged: string[] = [];
    const original = console.error;
    console.error = (...args: unknown[]) => {
      logged.push(args.map((item) => String(item)).join(" "));
    };
    try {
      const created = await ctx.request("/api/v1/me/credentials", {
        method: "POST",
        body: JSON.stringify({
          provider: "openai",
          label: "主 key",
          key,
          baseUrl: "http://127.0.0.1:11434/v1",
        }),
      });
      expect(created.status).toBe(201);
      const body = (await created.json()) as { id: string; fingerprint: string };
      expect(JSON.stringify(body)).not.toContain(key);
      expect(body.fingerprint).toMatch(/^[0-9a-f]{12}$/);

      const listed = await ctx.request("/api/v1/me/credentials");
      const listText = JSON.stringify(await listed.json());
      expect(listText).not.toContain(key);

      const kept = await ctx.request(`/api/v1/me/credentials/${body.id}`, {
        method: "PATCH",
        body: JSON.stringify({ key: "", label: "改名" }),
      });
      expect(kept.status).toBe(200);
      const keptBody = (await kept.json()) as { label: string; fingerprint: string };
      expect(keptBody.label).toBe("改名");
      expect(keptBody.fingerprint).toBe(body.fingerprint);
      const row = await ctx.db.getCredential(body.id);
      if (!row) throw new Error("没有凭据");
      expect(revealCredential(row, ctx.ring).reveal()).toBe(key);

      const replacedKey = "lwtest_replaced_secret";
      const replaced = await ctx.request(`/api/v1/me/credentials/${body.id}`, {
        method: "PATCH",
        body: JSON.stringify({ key: replacedKey }),
      });
      expect(replaced.status).toBe(200);
      expect(JSON.stringify(await replaced.json())).not.toContain(replacedKey);
      const next = await ctx.db.getCredential(body.id);
      if (!next) throw new Error("没有凭据");
      expect(revealCredential(next, ctx.ring).reveal()).toBe(replacedKey);

      const issued = await ctx.request("/api/v1/me/api-keys", {
        method: "POST",
        body: JSON.stringify({ name: "外部" }),
      });
      const apiKey = ((await issued.json()) as { token: string }).token;
      const denied = await ctx.request(
        "/api/v1/me/credentials",
        { method: "POST", body: JSON.stringify({ provider: "openai", label: "x", key }) },
        apiKey,
      );
      expect(denied.status).toBe(403);
      expect(await denied.text()).not.toContain(key);

      const bad = await ctx.request("/api/v1/me/credentials", {
        method: "POST",
        body: JSON.stringify({
          provider: "openai",
          label: "坏端点",
          key,
          baseUrl: "http://192.168.1.9/v1",
        }),
      });
      expect(bad.status).toBe(400);
      expect(await bad.text()).not.toContain(key);

      ctx.db.insertCredential = () => Promise.reject(new Error(key));
      const blown = await ctx.request("/api/v1/me/credentials", {
        method: "POST",
        body: JSON.stringify({ provider: "openai", label: "炸", key }),
      });
      expect(blown.status).toBe(500);
      const blownText = await blown.text();
      expect(blownText).not.toContain(key);
      expect(blownText).toContain("服务器出错");
      expect(logged.join("\n")).not.toContain(key);
    } finally {
      console.error = original;
      await ctx.close();
    }
  });

  it("验证只确认能解开，不回原文", async () => {
    const ctx = await boot();
    try {
      const key = "lwtest_verify_secret";
      const created = await createCredential(ctx.db, ctx.ring, {
        userId: ctx.owner.userId,
        provider: "anthropic",
        label: "核对",
        key,
      });
      const verified = await ctx.request(`/api/v1/me/credentials/${created.id}/verify`, {
        method: "POST",
      });
      expect(verified.status).toBe(200);
      const body = await verified.json();
      expect(JSON.stringify(body)).not.toContain(key);
      expect(body).toMatchObject({ status: "active", lastErrorCode: null });
    } finally {
      await ctx.close();
    }
  });
});

import { randomBytes } from "node:crypto";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
  createApiKey,
  ensureOwner,
  loadMasterKey,
  openMemoryDatabase,
  readOwnerToken,
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

describe("模型目录接口", () => {
  it("OPENAI_API_KEY 不会让模型变成可用，API 密钥不能改槽位", async () => {
    const dir = await mkdtemp(join(tmpdir(), "lw-models-http-"));
    const dataDir = join(dir, "data");
    const db = await openMemoryDatabase();
    const owner = await ensureOwner(db, { dataDir });
    const token = await readOwnerToken(dataDir);
    const env = {
      LW_SECRETS_KEY: randomBytes(32).toString("base64"),
      OPENAI_API_KEY: "lwtest_ambient_model_key",
      NODE_ENV: "test",
    };
    const ring = (await loadMasterKey({ dataDir, env })).ring;
    const app = createApp({
      db,
      runtime: stubRuntime(),
      auth: createOssAuth(db),
      keyring: ring,
      env,
    });
    const request = (path: string, init?: RequestInit, bearer = token) => {
      const headers = new Headers(init?.headers);
      if (bearer) headers.set("authorization", `Bearer ${bearer}`);
      if (init?.body && !headers.has("content-type"))
        headers.set("content-type", "application/json");
      return app.request(path, { ...init, headers });
    };
    try {
      const listed = await request("/api/v1/models");
      expect(listed.status).toBe(200);
      const body = (await listed.json()) as Array<{ ref: string; usable: boolean; why?: string }>;
      expect(JSON.stringify(body)).not.toContain("lwtest_ambient_model_key");
      expect(body.find((item) => item.ref === "openai/gpt-4.1-mini")).toMatchObject({
        usable: false,
        why: "没有凭据",
      });
      expect(body.find((item) => item.ref === "tier/balanced")?.usable).toBe(false);
      const issued = await createApiKey(db, {
        userId: owner.userId,
        name: "外部",
        scopes: ["documents:read", "documents:write", "tasks:read", "tasks:write"],
      });
      const denied = await request(
        "/api/v1/me/model-slots/writer.main",
        { method: "PUT", body: JSON.stringify({ targetRef: "tier/fast" }) },
        issued.token,
      );
      expect(denied.status).toBe(403);
      expect(JSON.stringify(await denied.json())).not.toContain("lwtest_ambient_model_key");
      const bound = await request("/api/v1/me/model-slots/writer.main", {
        method: "PUT",
        body: JSON.stringify({ targetRef: "tier/fast" }),
      });
      expect(bound.status).toBe(200);
    } finally {
      await db.close();
      await rm(dir, { recursive: true, force: true });
    }
  });
});

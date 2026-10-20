import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createClient } from "@luminwrite/client";
import { ensureOwner, openMemoryDatabase, readOwnerToken } from "@luminwrite/db";
import { runWriteReviseRollback } from "@luminwrite/eval-adapter";
import {
  createWritingRuntime,
  type TurnResult,
  type WritingRuntime,
  writingStoreFromDatabase,
} from "@luminwrite/harness";
import { describe, expect, it } from "vitest";

import { createApp } from "./app.js";
import { createOssAuth } from "./auth.js";

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

async function boot(runtime?: WritingRuntime) {
  const dir = await mkdtemp(join(tmpdir(), "lw-tasks-"));
  const dataDir = join(dir, "data");
  const db = await openMemoryDatabase();
  const owner = await ensureOwner(db, { dataDir });
  const token = await readOwnerToken(dataDir);
  const active =
    runtime ??
    (await createWritingRuntime({
      sqlitePath: join(dir, "pi.sqlite"),
      store: writingStoreFromDatabase(db),
      usage: { record: (event) => db.recordUsage(event) },
    }));
  const app = createApp({ db, runtime: active, auth: createOssAuth(db) });
  const request = (path: string, init?: RequestInit, bearer: string | undefined = token) => {
    const headers = new Headers(init?.headers);
    if (bearer) headers.set("authorization", `Bearer ${bearer}`);
    if (init?.body && !headers.has("content-type")) headers.set("content-type", "application/json");
    return Promise.resolve(app.request(path, { ...init, headers }));
  };
  return {
    db,
    token,
    owner,
    request,
    async close() {
      await active.close();
      await db.close();
      await rm(dir, { recursive: true, force: true });
    },
  };
}

type Boot = Awaited<ReturnType<typeof boot>>;

async function issue(ctx: Boot, body: Record<string, unknown> = { name: "外部" }) {
  const response = await ctx.request("/api/v1/me/api-keys", {
    method: "POST",
    body: JSON.stringify(body),
  });
  expect(response.status).toBe(201);
  return (await response.json()) as { id: string; token: string; scopes: string[] };
}

function clientFor(ctx: Boot, token: string) {
  return createClient(
    "http://127.0.0.1:8787",
    async (input, init) => {
      const url = new URL(String(input));
      return ctx.request(`${url.pathname}${url.search}`, init, "");
    },
    { token },
  );
}

async function waitTask(ctx: Boot, id: string, bearer: string) {
  const started = Date.now();
  for (;;) {
    const response = await ctx.request(`/api/v1/tasks/${id}`, undefined, bearer);
    const body = (await response.json()) as { state: string; documentId?: string; id: string };
    if (body.state !== "submitted" && body.state !== "working") return body;
    if (Date.now() - started > 20_000) throw new Error("任务超时");
    await new Promise((resolve) => setTimeout(resolve, 40));
  }
}

function sseEvents(body: string): Array<{ id: number; type: string }> {
  return body
    .split("\n\n")
    .map((block) => block.trim())
    .filter((block) => block.length > 0)
    .map((block) => {
      const idLine = block.split("\n").find((line) => line.startsWith("id: "));
      const dataLine = block.split("\n").find((line) => line.startsWith("data: "));
      const data = JSON.parse(dataLine?.slice(6) ?? "{}") as { type?: string };
      return { id: Number(idLine?.slice(4) ?? "0"), type: data.type ?? "" };
    });
}

describe("任务 API", () => {
  it("发现文档不需要登录，OpenAPI 需要能读任务的密钥", async () => {
    const ctx = await boot(stubRuntime());
    try {
      const card = await ctx.request("/.well-known/agent-card.json", undefined, "");
      expect(card.status).toBe(200);
      const cardBody = (await card.json()) as {
        url: string;
        supportedInterfaces: Array<{ protocolBinding: string; url: string }>;
        capabilities: { streaming: boolean; pushNotifications: boolean };
      };
      expect(cardBody.url).toBe("http://localhost/api/v1");
      expect(cardBody.supportedInterfaces.map((item) => item.protocolBinding)).toEqual([
        "HTTP+JSON",
      ]);
      expect(JSON.stringify(cardBody)).not.toMatch(/JSON-?RPC/i);
      expect(cardBody.capabilities.pushNotifications).toBe(false);
      expect(cardBody.capabilities.streaming).toBe(true);

      const anonymous = await ctx.request("/api/v1/openapi.json", undefined, "");
      expect(anonymous.status).toBe(401);
      const key = await issue(ctx);
      const spec = await ctx.request("/api/v1/openapi.json", undefined, key.token);
      expect(spec.status).toBe(200);
      const document = (await spec.json()) as {
        paths: Record<string, unknown>;
        components?: { securitySchemes?: Record<string, { scheme?: string }> };
      };
      expect(document.paths["/tasks"]).toBeTruthy();
      expect(document.paths["/tasks/{id}/events"]).toBeTruthy();
      expect(document.components?.securitySchemes?.apiKey?.scheme).toBe("bearer");
      const narrow = await issue(ctx, { name: "只读文档", scopes: ["documents:read"] });
      const denied = await ctx.request("/api/v1/openapi.json", undefined, narrow.token);
      expect(denied.status).toBe(403);
    } finally {
      await ctx.close();
    }
  });

  it("所有者令牌不能提交，档位之外是 403，同一幂等键只建一篇", async () => {
    const ctx = await boot(stubRuntime());
    try {
      const ownerPost = await ctx.request("/api/v1/tasks", {
        method: "POST",
        headers: { "idempotency-key": "owner" },
        body: JSON.stringify({ kind: "write", brief: { topic: "推理成本" } }),
      });
      expect(ownerPost.status).toBe(403);

      const key = await issue(ctx, { name: "档位", allowedTiers: ["balanced"] });
      const missing = await ctx.request(
        "/api/v1/tasks",
        { method: "POST", body: JSON.stringify({ kind: "write", brief: { topic: "推理成本" } }) },
        key.token,
      );
      expect(missing.status).toBe(400);

      const blocked = await ctx.request(
        "/api/v1/tasks",
        {
          method: "POST",
          headers: { "idempotency-key": "tier" },
          body: JSON.stringify({ kind: "write", model: "tier/opus", brief: { topic: "推理成本" } }),
        },
        key.token,
      );
      expect(blocked.status).toBe(403);
      expect(await ctx.db.countActiveApiTasks(key.id)).toBe(0);

      const accepted = await ctx.request(
        "/api/v1/tasks",
        {
          method: "POST",
          headers: { "idempotency-key": "once" },
          body: JSON.stringify({
            kind: "write",
            model: "tier/balanced",
            brief: { topic: "推理成本" },
            callbackUrl: "https://example.com/hook",
            sources: { library: false },
          }),
        },
        key.token,
      );
      expect(accepted.status).toBe(202);
      const created = (await accepted.json()) as { id: string; documentId?: string };
      const done = await waitTask(ctx, created.id, key.token);
      expect(done.state).toBe("completed");
      const replay = await ctx.request(
        "/api/v1/tasks",
        {
          method: "POST",
          headers: { "idempotency-key": "once" },
          body: JSON.stringify({ kind: "write", brief: { topic: "另一篇" } }),
        },
        key.token,
      );
      expect(replay.status).toBe(200);
      const replayed = (await replay.json()) as { id: string; documentId?: string };
      expect(replayed.id).toBe(created.id);
      expect(replayed.documentId).toBe(created.documentId);
      const listed = await ctx.db.listDocuments(ctx.owner.userId);
      expect(listed.filter((item) => item.createdVia === "api")).toHaveLength(1);

      const stream = await ctx.request(`/api/v1/tasks/${created.id}/events`, undefined, key.token);
      expect(stream.headers.get("content-type")).toContain("text/event-stream");
      const events = sseEvents(await stream.text());
      expect(events.map((event) => event.type)).toContain("task.submitted");
      expect(events.map((event) => event.type)).toContain("task.completed");
      const first = events[0];
      expect(first).toBeTruthy();
      const resume = await ctx.request(
        `/api/v1/tasks/${created.id}/events`,
        {
          headers: { "last-event-id": String(first?.id ?? 0) },
        },
        key.token,
      );
      const rest = sseEvents(await resume.text());
      expect(rest.map((event) => event.id)).toEqual(events.slice(1).map((event) => event.id));
      expect(rest.some((event) => event.id === first?.id)).toBe(false);

      const reader = await issue(ctx, { name: "只读任务", scopes: ["tasks:read"] });
      const forbidden = await ctx.request(
        "/api/v1/tasks",
        {
          method: "POST",
          headers: { "idempotency-key": "nope" },
          body: JSON.stringify({ kind: "write", brief: { topic: "推理成本" } }),
        },
        reader.token,
      );
      expect(forbidden.status).toBe(403);
      const visible = await ctx.request(`/api/v1/tasks/${created.id}`, undefined, reader.token);
      expect(visible.status).toBe(200);
    } finally {
      await ctx.close();
    }
  });

  it("并发满了返回 429，取消不会被后到的完成覆盖", async () => {
    let release: (result: TurnResult) => void = () => {};
    const gate = new Promise<TurnResult>((resolve) => {
      release = resolve;
    });
    let entered: () => void = () => {};
    const started = new Promise<void>((resolve) => {
      entered = resolve;
    });
    const ctx = await boot(
      stubRuntime(async () => {
        entered();
        return gate;
      }),
    );
    try {
      const key = await issue(ctx, { name: "并发", maxConcurrentTasks: 1 });
      const first = await ctx.request(
        "/api/v1/tasks",
        {
          method: "POST",
          headers: { "idempotency-key": "a" },
          body: JSON.stringify({ kind: "write", brief: { topic: "推理成本" } }),
        },
        key.token,
      );
      expect(first.status).toBe(202);
      const created = (await first.json()) as { id: string };
      await started;
      const second = await ctx.request(
        "/api/v1/tasks",
        {
          method: "POST",
          headers: { "idempotency-key": "b" },
          body: JSON.stringify({ kind: "write", brief: { topic: "另一篇" } }),
        },
        key.token,
      );
      expect(second.status).toBe(429);
      expect(second.headers.get("retry-after")).toBe("1");

      const pending = ctx
        .request(`/api/v1/tasks/${created.id}/events`, undefined, key.token)
        .then(async (response) => response.text());
      await new Promise((resolve) => setTimeout(resolve, 250));
      const canceled = await ctx.request(
        `/api/v1/tasks/${created.id}/cancel`,
        { method: "POST" },
        key.token,
      );
      expect(canceled.status).toBe(200);
      release({ turnId: "trn_late", status: "applied", submissionId: "sub" });
      const body = sseEvents(await pending);
      expect(body.map((event) => event.type)).toContain("task.canceled");
      const finalTask = await ctx.request(`/api/v1/tasks/${created.id}`, undefined, key.token);
      expect(((await finalTask.json()) as { state: string }).state).toBe("canceled");
    } finally {
      release({ turnId: "trn_late", status: "applied", submissionId: "sub" });
      await ctx.close();
    }
  });

  it("大纲确认之前不跑回合，回复之后才跑", async () => {
    let calls = 0;
    const ctx = await boot(
      stubRuntime(async () => {
        calls += 1;
        return { turnId: "trn_outline", status: "applied", submissionId: "sub" };
      }),
    );
    try {
      const key = await issue(ctx);
      const client = clientFor(ctx, key.token);
      const waiting = await client.submitTask(
        { kind: "write", brief: { topic: "推理成本" }, review: { outlineApproval: true } },
        "outline",
      );
      expect(waiting.state).toBe("input_required");
      expect(waiting.documentId).toBeTruthy();
      expect(calls).toBe(0);
      await client.answerTask(waiting.id, "按这个大纲写");
      const done = await waitTask(ctx, waiting.id, key.token);
      expect(done.state).toBe("completed");
      expect(calls).toBe(1);
    } finally {
      await ctx.close();
    }
  });

  it("faux 下 client 只靠 API Key 走完写、改、回滚", async () => {
    const ctx = await boot();
    try {
      const key = await issue(ctx);
      const client = clientFor(ctx, key.token);
      const result = await runWriteReviseRollback(client);
      const written = await ctx.request(
        `/api/v1/documents/${result.documentId}/versions`,
        undefined,
        key.token,
      );
      const versions = (await written.json()) as Array<{ id: string; seq: number }>;
      expect(versions.some((version) => version.id === result.wroteVersionId)).toBe(true);
      expect(versions.some((version) => version.id === result.revisedVersionId)).toBe(true);
      const wroteVersion = await ctx.request(
        `/api/v1/versions/${result.wroteVersionId}`,
        undefined,
        key.token,
      );
      const revisedVersion = await ctx.request(
        `/api/v1/versions/${result.revisedVersionId}`,
        undefined,
        key.token,
      );
      const wroteBody = (await wroteVersion.json()) as { content: unknown };
      const revisedBody = (await revisedVersion.json()) as { content: unknown };
      expect(JSON.stringify(wroteBody.content)).not.toBe(JSON.stringify(revisedBody.content));
      const head = await ctx.request(
        `/api/v1/documents/${result.documentId}`,
        undefined,
        key.token,
      );
      const headDoc = (await head.json()) as {
        blocks: Array<{ text: string; blockId: string; contentHash: string }>;
        headVersionId: string;
      };
      expect(headDoc.headVersionId).not.toBe(result.revisedVersionId);
      expect(headDoc.blocks[0]?.text).toContain("显存决定能放多大的批");

      const writeTask = await ctx.db.findApiTaskByIdempotency(key.id, "eval-write");
      const reviseTask = await ctx.db.findApiTaskByIdempotency(key.id, "eval-revise");
      expect(writeTask?.sessionId).toBeTruthy();
      expect(reviseTask?.sessionId).toBe(writeTask?.sessionId);
      expect(writeTask?.input.sources.library).toBe(false);
      expect(writeTask?.input.model).toBeUndefined();

      const replay = await client.submitTask(
        { kind: "write", brief: { topic: "不该再写一篇" } },
        "eval-write",
      );
      expect(replay.id).toBe(writeTask?.id);
      expect(await ctx.db.listDocuments(ctx.owner.userId)).toHaveLength(1);

      const second = await client.submitTask(
        { kind: "write", brief: { topic: "另一篇评论" }, instruction: "再写一篇" },
        "eval-proposal-doc",
      );
      const drafted = await waitTask(ctx, second.id, key.token);
      expect(drafted.state).toBe("completed");
      expect(drafted.documentId).toBeTruthy();
      const fresh = await ctx.request(
        `/api/v1/documents/${drafted.documentId}`,
        undefined,
        key.token,
      );
      const freshDoc = (await fresh.json()) as {
        blocks: Array<{ text: string; blockId: string; contentHash: string }>;
      };
      const untouched = freshDoc.blocks[0];
      const edited = freshDoc.blocks[1];
      expect(untouched && edited).toBeTruthy();
      if (!untouched || !edited) return;
      const touched = await ctx.request(`/api/v1/documents/${drafted.documentId}/ops`, {
        method: "POST",
        body: JSON.stringify({
          ops: [
            {
              op: "replace_text",
              blockId: edited.blockId,
              contentHash: edited.contentHash,
              expectedOldText: edited.text,
              newText: "人手改过这一段。额外的尾巴还在。",
              reason: "手改",
            },
          ],
        }),
      });
      expect(touched.status).toBe(200);
      const proposalTask = await client.submitTask(
        {
          kind: "revise",
          documentId: drafted.documentId,
          instruction: "把第1段写短",
        },
        "eval-proposal",
      );
      const finished = await waitTask(ctx, proposalTask.id, key.token);
      expect(finished.state).toBe("completed");
      const stored = await ctx.db.getApiTask(proposalTask.id);
      expect(stored?.proposalId).toBeTruthy();
      const still = await ctx.request(
        `/api/v1/documents/${drafted.documentId}`,
        undefined,
        key.token,
      );
      const stillDoc = (await still.json()) as { blocks: Array<{ text: string }> };
      expect(stillDoc.blocks[0]?.text).toBe(untouched.text);
      expect(stillDoc.blocks[1]?.text).toBe("人手改过这一段。额外的尾巴还在。");
    } finally {
      await ctx.close();
    }
  });
});

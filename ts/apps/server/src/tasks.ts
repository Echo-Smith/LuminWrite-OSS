import type { Actor, ApiTask, ApiTaskPhase, NormalizedCreateTask } from "@luminwrite/contracts";
import {
  CreateTaskBodySchema,
  normalizeCreateTask,
  TaskMessageBodySchema,
  taskInstruction,
} from "@luminwrite/contracts";
import type { ApiKeyView, ApiTaskRecord, LwDatabase } from "@luminwrite/db";
import { readActiveApiKey } from "@luminwrite/db";
import type { EditAuthorizer } from "@luminwrite/edition";
import type { WritingRuntime } from "@luminwrite/harness";
import type { Context, Hono } from "hono";

import { modelWithinAllowedTiers, resolveApiApplyMode } from "./api-access.js";
import { openApiDocument } from "./openapi.js";

type Env = { Variables: { actor: Actor; apiKey?: ApiKeyView } };
type TaskContext = Context<Env>;

export interface TaskDeps {
  db: LwDatabase;
  runtime: WritingRuntime;
}

const STREAM_LIMIT_MS = 25_000;

export function registerTaskRoutes(
  api: Hono<Env>,
  deps: TaskDeps,
  authorizer: EditAuthorizer,
): void {
  api.get("/openapi.json", (c) => c.json(openApiDocument()));

  api.post("/tasks", async (c) => submitTask(c, deps, authorizer));
  api.get("/tasks/:id", async (c) => readTask(c, deps));
  api.get("/tasks/:id/events", async (c) => streamTask(c, deps));
  api.post("/tasks/:id/messages", async (c) => answerTask(c, deps));
  api.post("/tasks/:id/cancel", async (c) => cancelTask(c, deps));
}

async function submitTask(
  c: TaskContext,
  deps: TaskDeps,
  authorizer: EditAuthorizer,
): Promise<Response> {
  const client = apiClient(c);
  if (client instanceof Response) return client;
  const idempotencyKey = c.req.header("Idempotency-Key")?.trim() ?? "";
  if (!idempotencyKey || idempotencyKey.length > 200) {
    return c.json({ error: "需要 Idempotency-Key" }, 400);
  }
  const body = await readJson(c);
  if (body === undefined) return c.json({ error: "任务参数不合法" }, 400);
  const parsed = CreateTaskBodySchema.safeParse(body);
  if (!parsed.success) return c.json({ error: "任务参数不合法" }, 400);
  const input = normalizeCreateTask(parsed.data);
  if (input.kind === "revise" && !input.documentId) {
    return c.json({ error: "修订任务需要文档" }, 400);
  }
  if (input.kind === "revise" && !taskInstruction(input)) {
    return c.json({ error: "修订任务需要指令" }, 400);
  }
  if (!modelWithinAllowedTiers(input.model, client.key.allowedTiers)) {
    return c.json({ error: "这把密钥不能用这个档位" }, 403);
  }
  const replay = await deps.db.findApiTaskByIdempotency(client.keyId, idempotencyKey);
  if (replay) {
    if (replay.userId !== client.userId) return c.json({ error: "找不到任务" }, 404);
    return c.json(await present(deps.db, replay), 200);
  }
  if (client.key.maxConcurrentTasks != null) {
    const active = await deps.db.countActiveApiTasks(client.keyId);
    if (active >= client.key.maxConcurrentTasks) {
      return c.json({ error: "超过并发上限" }, 429, { "Retry-After": "1" });
    }
  }

  const actor: Actor = { type: "api_client", userId: client.userId, keyId: client.keyId };
  let createdDocumentId: string | undefined;
  try {
    let documentId = input.documentId;
    let sessionId: string;
    if (input.kind === "write") {
      const created = await deps.db.createDocument({
        title: titleOf(input),
        ownerId: client.userId,
        genre: input.brief?.genre ?? "report",
        createdVia: "api",
        actor,
      });
      createdDocumentId = created.id;
      documentId = created.id;
      const tierRef = tierOf(input.model);
      await deps.db.setDocumentMeta(created.id, {
        ...(input.styleRef ? { styleRef: input.styleRef } : {}),
        ...(input.brief ? { brief: { ...input.brief } } : {}),
        ...(tierRef ? { tierRef } : {}),
      });
      const session = await deps.db.openSession({ documentId: created.id, kind: "api", actor });
      sessionId = session.id;
    } else {
      const documentIdForRevise = documentId;
      if (!documentIdForRevise) return c.json({ error: "修订任务需要文档" }, 400);
      const denied = await requireOwnedDocument(
        deps,
        authorizer,
        documentIdForRevise,
        client.userId,
      );
      if (denied) return c.json(denied.body, denied.status);
      sessionId = await reuseApiSession(deps, client.keyId, documentIdForRevise, actor);
    }
    if (!documentId) return c.json({ error: "修订任务需要文档" }, 400);
    const outline = input.review.outlineApproval;
    const claimed = await deps.db.claimApiTask({
      userId: client.userId,
      apiKeyId: client.keyId,
      idempotencyKey,
      kind: input.kind,
      state: outline ? "input_required" : "submitted",
      documentId,
      sessionId,
      input: { ...input, documentId },
      needsInput: outline
        ? {
            kind: "outline_approval",
            prompt: "请确认大纲后再继续",
            ...(input.brief ? { payload: { topic: input.brief.topic } } : {}),
          }
        : null,
      maxConcurrent: client.key.maxConcurrentTasks,
    });
    if (claimed.status === "limited") {
      if (createdDocumentId) await deps.db.discardDocument(createdDocumentId);
      return c.json({ error: "超过并发上限" }, 429, { "Retry-After": "1" });
    }
    if (claimed.status === "replay") {
      if (createdDocumentId && createdDocumentId !== claimed.task.documentId) {
        await deps.db.discardDocument(createdDocumentId);
      }
      return c.json(await present(deps.db, claimed.task), 200);
    }
    if (!outline) void runTask(deps, claimed.task.id);
    return c.json(await present(deps.db, claimed.task), 202);
  } catch (error) {
    if (createdDocumentId) await deps.db.discardDocument(createdDocumentId).catch(() => undefined);
    throw error;
  }
}

async function readTask(c: TaskContext, deps: TaskDeps): Promise<Response> {
  const task = await ownedTask(c, deps);
  if (task instanceof Response) return task;
  return c.json(await present(deps.db, task));
}

async function streamTask(c: TaskContext, deps: TaskDeps): Promise<Response> {
  const task = await ownedTask(c, deps);
  if (task instanceof Response) return task;
  const raw = c.req.header("Last-Event-ID") ?? c.req.query("after") ?? "0";
  const after = Number(raw);
  if (!Number.isInteger(after) || after < 0) return c.json({ error: "事件序号不合法" }, 400);
  return new Response(taskEventStream(deps.db, task.id, after), {
    headers: {
      "content-type": "text/event-stream",
      "cache-control": "no-cache",
    },
  });
}

async function answerTask(c: TaskContext, deps: TaskDeps): Promise<Response> {
  const task = await ownedTask(c, deps);
  if (task instanceof Response) return task;
  const body = await readJson(c);
  if (body === undefined) return c.json({ error: "回复不合法" }, 400);
  const parsed = TaskMessageBodySchema.safeParse(body);
  if (!parsed.success) return c.json({ error: "回复不合法" }, 400);
  if (task.state !== "input_required") return c.json({ error: "任务不在等待输入" }, 409);
  const input = { ...task.input, answer: parsed.data.message };
  await deps.db.patchApiTaskInput(task.id, input);
  await deps.db.appendApiTaskEvent(task.id, "task.message", { message: parsed.data.message });
  void runTask(deps, task.id);
  const updated = await deps.db.getApiTask(task.id);
  return c.json(await present(deps.db, updated ?? { ...task, input }), 202);
}

async function cancelTask(c: TaskContext, deps: TaskDeps): Promise<Response> {
  const task = await ownedTask(c, deps);
  if (task instanceof Response) return task;
  if (task.state === "canceled") return c.json(await present(deps.db, task));
  if (task.state !== "submitted" && task.state !== "working" && task.state !== "input_required") {
    return c.json({ error: "任务已经结束" }, 409);
  }
  const next = await deps.db.transitionApiTask(
    task.id,
    ["submitted", "working", "input_required"],
    { state: "canceled", needsInput: null },
    { type: "task.canceled", payload: { state: "canceled" } },
  );
  if (!next) {
    const again = await deps.db.getApiTask(task.id);
    if (again?.state === "canceled") return c.json(await present(deps.db, again));
    return c.json({ error: "任务已经结束" }, 409);
  }
  if (task.sessionId) {
    try {
      await deps.runtime.abort(task.sessionId);
    } catch {
      // 对话可能还没建起来。取消状态已经落库。
    }
  }
  return c.json(await present(deps.db, next));
}

export async function runTask(deps: TaskDeps, taskId: string): Promise<void> {
  const task = await deps.db.getApiTask(taskId);
  if (!task || (task.state !== "submitted" && task.state !== "input_required")) return;
  const phase: ApiTaskPhase = task.kind === "revise" ? "revising" : "drafting";
  const working = await deps.db.transitionApiTask(
    taskId,
    ["submitted", "input_required"],
    { state: "working", phase, needsInput: null },
    {
      type: "task.working",
      payload: { state: "working", phase, documentId: task.documentId },
    },
  );
  if (!working?.documentId || !working.sessionId) return;
  try {
    const current = await deps.db.getApiTask(taskId);
    if (!current) return;
    if (current.state !== "working" || !current.documentId || !current.sessionId) return;
    const actor: Actor = { type: "api_client", userId: current.userId, keyId: current.apiKeyId };
    const key = await readActiveApiKey(deps.db, current.apiKeyId);
    const document = await deps.db.getDocument(current.documentId);
    const requested = current.input.applyMode;
    const applyMode = resolveApiApplyMode({
      ...(requested ? { requested } : {}),
      humanTouched: document?.humanTouched ?? false,
      policyDefault: key?.defaultApplyMode ?? "auto",
    });
    const instruction = taskInstruction(current.input, current.input.answer);
    const anchors = current.input.anchors;
    const result = await deps.runtime.submitTurn({
      documentId: current.documentId,
      sessionId: current.sessionId,
      kind: current.kind === "revise" ? "revise" : "draft",
      instruction,
      ...(anchors && anchors.length > 0 ? { selection: anchors } : {}),
      scope: anchors && anchors.length > 0 ? "selection" : "document",
      delivery: "followup",
      applyMode,
      idempotencyKey: `task:${taskId}`,
      ...(current.input.model ? { model: current.input.model } : {}),
      actor,
    });
    await deps.db.attachUsageToTask(result.turnId, taskId);
    const after = await deps.db.getApiTask(taskId);
    if (!after) return;
    if (after.state !== "working") return;
    if (result.status === "applied") {
      await deps.db.transitionApiTask(
        taskId,
        ["working"],
        {
          state: "completed",
          turnId: result.turnId,
          ...(result.versionId ? { resultVersionId: result.versionId } : {}),
        },
        {
          type: "task.completed",
          payload: {
            state: "completed",
            documentId: current.documentId,
            resultVersionId: result.versionId ?? null,
          },
        },
      );
      return;
    }
    if (result.status === "proposed") {
      const proposal = await deps.db.proposalForTurn(result.turnId);
      await deps.db.transitionApiTask(
        taskId,
        ["working"],
        {
          state: "completed",
          turnId: result.turnId,
          ...(proposal ? { proposalId: proposal.id } : {}),
        },
        {
          type: "task.completed",
          payload: {
            state: "completed",
            documentId: current.documentId,
            proposalId: proposal?.id ?? null,
          },
        },
      );
      return;
    }
    if (result.status === "cancelled") {
      await deps.db.transitionApiTask(
        taskId,
        ["working"],
        { state: "canceled" },
        { type: "task.canceled", payload: { state: "canceled" } },
      );
      return;
    }
    const turn = await deps.db.getTurn(result.turnId);
    await deps.db.transitionApiTask(
      taskId,
      ["working"],
      {
        state: "failed",
        turnId: result.turnId,
        error: {
          code: result.status === "conflicted" ? "conflict" : "turn_failed",
          message: turn?.error ?? "回合没有完成",
          retryable: false,
        },
      },
      { type: "task.failed", payload: { state: "failed", status: result.status } },
    );
  } catch (error) {
    const message = error instanceof Error ? error.message : "任务失败";
    await deps.db
      .transitionApiTask(
        taskId,
        ["working", "submitted"],
        { state: "failed", error: { code: "internal", message, retryable: true } },
        { type: "task.failed", payload: { state: "failed" } },
      )
      .catch(() => undefined);
  }
}

function taskEventStream(
  db: LwDatabase,
  taskId: string,
  after: number,
): ReadableStream<Uint8Array> {
  const encoder = new TextEncoder();
  let cursor = after;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let closed = false;
  const started = Date.now();
  const finish = (controller: ReadableStreamDefaultController<Uint8Array>) => {
    if (closed) return;
    closed = true;
    if (timer) clearTimeout(timer);
    controller.close();
  };
  return new ReadableStream({
    start(controller) {
      const pump = async () => {
        if (closed) return;
        try {
          const events = await db.taskEventsAfter(taskId, cursor);
          for (const event of events) {
            cursor = event.seq;
            controller.enqueue(
              encoder.encode(`id: ${event.seq}\ndata: ${JSON.stringify(event)}\n\n`),
            );
          }
          const task = await db.getApiTask(taskId);
          const stillRunning = task?.state === "submitted" || task?.state === "working";
          const timedOut = Date.now() - started > STREAM_LIMIT_MS;
          if (!stillRunning && events.length === 0) {
            finish(controller);
            return;
          }
          if (timedOut && events.length === 0) {
            finish(controller);
            return;
          }
          timer = setTimeout(
            () => {
              void pump();
            },
            events.length > 0 ? 0 : 200,
          );
        } catch (error) {
          if (closed) return;
          closed = true;
          if (timer) clearTimeout(timer);
          controller.error(error);
        }
      };
      void pump();
    },
    cancel() {
      closed = true;
      if (timer) clearTimeout(timer);
    },
  });
}

async function present(db: LwDatabase, task: ApiTaskRecord): Promise<ApiTask> {
  const totals = task.turnId
    ? await db.usageTotalsForTurn(task.turnId)
    : { inputTokens: 0, outputTokens: 0, costMicros: 0 };
  return {
    id: task.id,
    state: task.state,
    ...(task.phase ? { phase: task.phase } : {}),
    ...(task.documentId ? { documentId: task.documentId } : {}),
    ...(task.resultVersionId ? { resultVersionId: task.resultVersionId } : {}),
    ...(task.proposalId ? { proposalId: task.proposalId } : {}),
    ...(task.needsInput ? { needsInput: task.needsInput } : {}),
    usage: {
      inputTokens: totals.inputTokens,
      outputTokens: totals.outputTokens,
      costUsd: totals.costMicros / 1_000_000,
    },
    ...(task.error ? { error: task.error } : {}),
    createdAt: task.createdAt,
    updatedAt: task.updatedAt,
  };
}

function apiClient(c: TaskContext): { userId: string; keyId: string; key: ApiKeyView } | Response {
  const actor = c.get("actor");
  if (actor.type !== "api_client") return c.json({ error: "任务接口只接受 API Key" }, 403);
  const key = c.get("apiKey");
  if (!key) return c.json({ error: "密钥已吊销或不存在" }, 401);
  return { userId: actor.userId, keyId: actor.keyId, key };
}

async function ownedTask(c: TaskContext, deps: TaskDeps): Promise<ApiTaskRecord | Response> {
  const client = apiClient(c);
  if (client instanceof Response) return client;
  const id = c.req.param("id");
  if (!id) return c.json({ error: "找不到任务" }, 404);
  const task = await deps.db.getApiTask(id);
  if (!task || task.userId !== client.userId) return c.json({ error: "找不到任务" }, 404);
  return task;
}

async function requireOwnedDocument(
  deps: TaskDeps,
  authorizer: EditAuthorizer,
  documentId: string,
  userId: string,
): Promise<{ status: 403 | 404 | 409; body: { error: string } } | undefined> {
  const document = await deps.db.getDocument(documentId);
  if (!document) return { status: 404, body: { error: "找不到文档" } };
  if (!(await authorizer.canEdit(documentId, userId))) {
    return { status: 403, body: { error: "没有这篇文档的修改权限" } };
  }
  if (document.discardedAt) return { status: 409, body: { error: "文档已归档" } };
  return undefined;
}

async function reuseApiSession(
  deps: TaskDeps,
  apiKeyId: string,
  documentId: string,
  actor: Actor,
): Promise<string> {
  const existingId = await deps.db.latestApiSessionId(apiKeyId, documentId);
  if (existingId) {
    const existing = await deps.db.getSession(existingId);
    if (existing && existing.status === "open" && existing.documentId === documentId) {
      return existing.id;
    }
  }
  const opened = await deps.db.openSession({ documentId, kind: "api", actor });
  return opened.id;
}

function titleOf(input: NormalizedCreateTask): string {
  const raw = input.brief?.topic?.trim() || input.instruction?.trim() || "未命名";
  return raw.slice(0, 120);
}

function tierOf(model: string | undefined): string | undefined {
  if (!model?.startsWith("tier/")) return undefined;
  const tier = model.slice("tier/".length);
  return tier || undefined;
}

async function readJson(c: TaskContext): Promise<unknown> {
  const text = await c.req.text();
  if (!text.trim()) return {};
  try {
    const parsed = JSON.parse(text) as unknown;
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return parsed;
    const record = parsed as Record<string, unknown>;
    const rest: Record<string, unknown> = {};
    for (const [key, value] of Object.entries(record)) {
      if (key === "actor" || key === "ownerId") continue;
      rest[key] = value;
    }
    return rest;
  } catch {
    return undefined;
  }
}

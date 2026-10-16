import {
  type Actor,
  DossierOpSchema,
  type EditOp,
  EditOpSchema,
  TurnRequestSchema,
} from "@luminwrite/contracts";
import {
  ApiKeyInputError,
  type ApiKeyView,
  createApiKey,
  DocumentNotFound,
  type LwDatabase,
  listApiKeys,
  revokeApiKey,
  SessionClosed,
  SessionNotFound,
  SessionScopeError,
  VersionNotFound,
} from "@luminwrite/db";
import type { EditAuthorizer } from "@luminwrite/edition";
import { type AuthPort, ownerAuthorizer } from "@luminwrite/edition";
import type { WritingRuntime } from "@luminwrite/harness";
import type { Context } from "hono";
import { Hono } from "hono";
import { z } from "zod";

import { enforceApiKey, resolveApiApplyMode } from "./api-access.js";
import { defaultRateLimiter, TaskConcurrency, type TokenBucketLimiter } from "./rate-limit.js";

const CreateDocument = z.object({
  title: z.string().min(1),
  genre: z.string().optional(),
});

const OpsBody = z.object({
  ops: z.array(EditOpSchema).min(1),
  summary: z.string().optional(),
  sessionId: z.string().min(1).optional(),
});

const OpenSessionBody = z.object({
  kind: z.enum(["fork", "api", "collab"]),
  baseVersionId: z.string().min(1).optional(),
});

const DossierBody = z.object({
  expectedVersion: z.number().int().positive(),
  ops: z.array(DossierOpSchema).min(1),
});

const DeriveBody = z.object({
  versionId: z.string().min(1),
  inheritDossier: z.boolean(),
  title: z.string().min(1).optional(),
});

const ReadGrantBody = z.object({
  documentId: z.string().min(1),
});

const SessionEditsBody = z.object({
  documentIds: z.array(z.string().min(1)).min(1),
  ops: z.array(EditOpSchema).min(1),
  summary: z.string().optional(),
});

const RollbackBody = z.object({
  versionId: z.string().min(1),
});

const TurnBody = TurnRequestSchema.omit({ documentId: true, actor: true, applyMode: true }).extend({
  applyMode: z.enum(["auto", "proposal"]).optional(),
  model: z.string().min(1).optional(),
  tier: z.string().min(1).optional(),
});

const CreateApiKeyBody = z.object({
  name: z.string().min(1).max(128),
  scopes: z.array(z.string()).optional(),
  maxConcurrentTasks: z.number().int().positive().nullable().optional(),
  dailyCostCapUsd: z.number().nonnegative().nullable().optional(),
  allowedTiers: z.array(z.string()).nullable().optional(),
  defaultApplyMode: z.enum(["auto", "proposal"]).optional(),
  webhookSecret: z.string().min(1).max(256).nullable().optional(),
});

type Env = { Variables: { actor: Actor; apiKey?: ApiKeyView } };
type AppContext = Context<Env>;

export interface AppDeps {
  db: LwDatabase;
  runtime: WritingRuntime;
  auth: AuthPort;
  authorizer?: EditAuthorizer;
  limiter?: TokenBucketLimiter;
}

function httpError(error: unknown): { status: 404 | 409; body: { error: string } } | undefined {
  if (
    error instanceof DocumentNotFound ||
    error instanceof SessionNotFound ||
    error instanceof VersionNotFound
  ) {
    return { status: 404, body: { error: error.message } };
  }
  if (error instanceof SessionScopeError || error instanceof SessionClosed) {
    return { status: 409, body: { error: error.message } };
  }
  return undefined;
}

/** 请求体里的 actor / ownerId 丢掉。身份只来自认证结果。 */
function stripIdentity(value: unknown): unknown {
  if (!value || typeof value !== "object" || Array.isArray(value)) return value;
  const copy = { ...(value as Record<string, unknown>) };
  delete copy.actor;
  delete copy.ownerId;
  return copy;
}

async function readBody(c: AppContext): Promise<unknown> {
  try {
    return stripIdentity(await c.req.json());
  } catch {
    return undefined;
  }
}

function userIdOf(actor: Actor): string | undefined {
  if (actor.type === "system") return undefined;
  return actor.userId;
}

export function createApp(deps: AppDeps): Hono<Env> {
  const app = new Hono<Env>();
  const authorizer =
    deps.authorizer ??
    ownerAuthorizer(async (documentId) => (await deps.db.getDocument(documentId))?.ownerId);
  const limiter = deps.limiter ?? defaultRateLimiter();
  const concurrency = new TaskConcurrency();

  app.get("/health", (c) => c.json({ ok: true, service: "luminwrite-ts" }));

  const api = new Hono<Env>();
  api.use("*", async (c, next) => {
    const actor = await deps.auth.authenticate(c.req.raw);
    if (!actor || actor.type === "system") return c.json({ error: "需要所有者令牌" }, 401);
    c.set("actor", actor);
    if (actor.type === "api_client") {
      const decision = await enforceApiKey({
        db: deps.db,
        limiter,
        keyId: actor.keyId,
        method: c.req.method,
        path: c.req.path,
      });
      if (!decision.ok) {
        const { status, error, retryAfter } = decision.denial;
        if (retryAfter !== undefined) {
          return c.json({ error }, status, { "Retry-After": String(retryAfter) });
        }
        return c.json({ error }, status);
      }
      c.set("apiKey", decision.key);
    }
    await next();
  });

  async function requireDocument(c: AppContext, documentId: string): Promise<Response | undefined> {
    const userId = userIdOf(c.get("actor"));
    if (!userId) return c.json({ error: "需要所有者令牌" }, 401);
    const document = await deps.db.getDocument(documentId);
    if (!document) return c.json({ error: "找不到文档" }, 404);
    if (!(await authorizer.canEdit(documentId, userId))) {
      return c.json({ error: "没有这篇文档的修改权限" }, 403);
    }
    return undefined;
  }

  async function requireSession(c: AppContext, sessionId: string): Promise<Response | undefined> {
    let documentId: string;
    try {
      const session = await deps.db.getSession(sessionId);
      if (!session) return c.json({ error: "找不到会话" }, 404);
      documentId = session.documentId;
    } catch (error) {
      const mapped = httpError(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
    return requireDocument(c, documentId);
  }

  api.post("/documents", async (c) => {
    const parsed = CreateDocument.safeParse(await readBody(c));
    if (!parsed.success) return c.json({ error: "文档参数不合法" }, 400);
    const userId = userIdOf(c.get("actor"));
    if (!userId) return c.json({ error: "需要所有者令牌" }, 401);
    const created = await deps.db.createDocument({
      title: parsed.data.title,
      ownerId: userId,
      ...(parsed.data.genre ? { genre: parsed.data.genre } : {}),
    });
    return c.json(created, 201);
  });

  api.get("/documents/:id", async (c) => {
    const denied = await requireDocument(c, c.req.param("id"));
    if (denied) return denied;
    const document = await deps.db.getDocument(c.req.param("id"));
    if (!document) return c.json({ error: "找不到文档" }, 404);
    const blocks = await deps.db.blocks(document.id);
    return c.json({ ...document, blocks });
  });

  api.get("/documents/:id/versions", async (c) => {
    const denied = await requireDocument(c, c.req.param("id"));
    if (denied) return denied;
    return c.json(await deps.db.listVersions(c.req.param("id")));
  });

  api.post("/documents/:id/ops", async (c) => {
    const denied = await requireDocument(c, c.req.param("id"));
    if (denied) return denied;
    const parsed = OpsBody.safeParse(await readBody(c));
    if (!parsed.success) return c.json({ error: "修订参数不合法" }, 400);
    const actor = c.get("actor");
    try {
      const result = await deps.db.applyManual({
        documentId: c.req.param("id"),
        ops: parsed.data.ops as EditOp[],
        actor,
        ...(parsed.data.summary ? { summary: parsed.data.summary } : {}),
        ...(parsed.data.sessionId ? { sessionId: parsed.data.sessionId } : {}),
      });
      if (!result.ok) return c.json(result, 409);
      return c.json(result);
    } catch (error) {
      const mapped = httpError(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
  });

  api.get("/documents/:id/sessions", async (c) => {
    const denied = await requireDocument(c, c.req.param("id"));
    if (denied) return denied;
    return c.json(await deps.db.listSessions(c.req.param("id")));
  });

  api.post("/documents/:id/sessions", async (c) => {
    const denied = await requireDocument(c, c.req.param("id"));
    if (denied) return denied;
    const parsed = OpenSessionBody.safeParse(await readBody(c));
    if (!parsed.success) return c.json({ error: "会话参数不合法" }, 400);
    try {
      const session = await deps.db.openSession({
        documentId: c.req.param("id"),
        kind: parsed.data.kind,
        actor: c.get("actor"),
        ...(parsed.data.baseVersionId ? { baseVersionId: parsed.data.baseVersionId } : {}),
      });
      return c.json(session, 201);
    } catch (error) {
      const mapped = httpError(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
  });

  api.post("/sessions/:id/close", async (c) => {
    const denied = await requireSession(c, c.req.param("id"));
    if (denied) return denied;
    try {
      await deps.db.closeSession(c.req.param("id"));
      return c.json({ ok: true });
    } catch (error) {
      const mapped = httpError(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
  });

  api.post("/sessions/:id/reads", async (c) => {
    const denied = await requireSession(c, c.req.param("id"));
    if (denied) return denied;
    const parsed = ReadGrantBody.safeParse(await readBody(c));
    if (!parsed.success) return c.json({ error: "阅读授权不合法" }, 400);
    try {
      await deps.db.grantRead(c.req.param("id"), parsed.data.documentId);
      return c.json({ ok: true });
    } catch (error) {
      const mapped = httpError(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
  });

  api.post("/sessions/:id/edits", async (c) => {
    const denied = await requireSession(c, c.req.param("id"));
    if (denied) return denied;
    const parsed = SessionEditsBody.safeParse(await readBody(c));
    if (!parsed.success) return c.json({ error: "修订参数不合法" }, 400);
    try {
      const result = await deps.db.applySessionEdits({
        sessionId: c.req.param("id"),
        documentIds: parsed.data.documentIds,
        ops: parsed.data.ops as EditOp[],
        actor: c.get("actor"),
        ...(parsed.data.summary ? { summary: parsed.data.summary } : {}),
      });
      if (!result.ok) return c.json(result, 409);
      return c.json(result);
    } catch (error) {
      const mapped = httpError(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
  });

  api.get("/documents/:id/dossier", async (c) => {
    const denied = await requireDocument(c, c.req.param("id"));
    if (denied) return denied;
    try {
      return c.json(await deps.db.readDossier(c.req.param("id")));
    } catch (error) {
      const mapped = httpError(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
  });

  api.post("/documents/:id/dossier", async (c) => {
    const denied = await requireDocument(c, c.req.param("id"));
    if (denied) return denied;
    const parsed = DossierBody.safeParse(await readBody(c));
    if (!parsed.success) return c.json({ error: "档案参数不合法" }, 400);
    try {
      const result = await deps.db.applyDossier({
        documentId: c.req.param("id"),
        expectedVersion: parsed.data.expectedVersion,
        ops: parsed.data.ops,
      });
      if (!result.ok) return c.json(result, 409);
      return c.json(result);
    } catch (error) {
      const mapped = httpError(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
  });

  api.post("/documents/:id/derive", async (c) => {
    const denied = await requireDocument(c, c.req.param("id"));
    if (denied) return denied;
    const parsed = DeriveBody.safeParse(await readBody(c));
    if (!parsed.success) return c.json({ error: "派生参数不合法" }, 400);
    const userId = userIdOf(c.get("actor"));
    if (!userId) return c.json({ error: "需要所有者令牌" }, 401);
    try {
      const source = await deps.db.listVersions(c.req.param("id"));
      if (!source.some((version) => version.id === parsed.data.versionId)) {
        return c.json({ error: "版本不属于这篇文档" }, 404);
      }
      const created = await deps.db.deriveDocument({
        versionId: parsed.data.versionId,
        inheritDossier: parsed.data.inheritDossier,
        ownerId: userId,
        actor: c.get("actor"),
        ...(parsed.data.title ? { title: parsed.data.title } : {}),
      });
      return c.json(created, 201);
    } catch (error) {
      const mapped = httpError(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
  });

  api.get("/documents/:id/derived", async (c) => {
    const denied = await requireDocument(c, c.req.param("id"));
    if (denied) return denied;
    return c.json(await deps.db.listDerived(c.req.param("id")));
  });

  api.post("/documents/:id/rollback", async (c) => {
    const denied = await requireDocument(c, c.req.param("id"));
    if (denied) return denied;
    const parsed = RollbackBody.safeParse(await readBody(c));
    if (!parsed.success) return c.json({ error: "回滚参数不合法" }, 400);
    try {
      const result = await deps.db.rollback({
        documentId: c.req.param("id"),
        toVersionId: parsed.data.versionId,
        actor: c.get("actor"),
      });
      if (!result.ok) return c.json(result, 409);
      return c.json(result);
    } catch (error) {
      const mapped = httpError(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
  });

  api.post("/documents/:id/turns", async (c) => {
    const denied = await requireDocument(c, c.req.param("id"));
    if (denied) return denied;
    const parsed = TurnBody.safeParse(await readBody(c));
    if (!parsed.success) return c.json({ error: "回合参数不合法" }, 400);
    const actor = c.get("actor");
    const { model, tier, applyMode: requestedApply, ...rest } = parsed.data;
    let applyMode = requestedApply ?? "auto";
    let release: (() => void) | undefined;
    if (actor.type === "api_client") {
      const key = c.get("apiKey");
      if (!key) return c.json({ error: "密钥已吊销或不存在" }, 401);
      const requestedModel = model ?? tier;
      if (key.allowedTiers && requestedModel && !key.allowedTiers.includes(requestedModel)) {
        return c.json({ error: "这把密钥不能用这个档位" }, 403);
      }
      const document = await deps.db.getDocument(c.req.param("id"));
      applyMode = resolveApiApplyMode({
        ...(requestedApply ? { requested: requestedApply } : {}),
        humanTouched: document?.humanTouched ?? false,
        policyDefault: key.defaultApplyMode,
      });
      if (key.maxConcurrentTasks != null) {
        if (!concurrency.tryAcquire(actor.keyId, key.maxConcurrentTasks)) {
          return c.json({ error: "超过并发上限" }, 429, { "Retry-After": "1" });
        }
        release = () => concurrency.release(actor.keyId);
      }
    }
    try {
      const result = await deps.runtime.submitTurn({
        ...rest,
        applyMode,
        documentId: c.req.param("id"),
        actor,
      });
      return c.json(result);
    } catch (error) {
      const mapped = httpError(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    } finally {
      release?.();
    }
  });

  api.post("/me/api-keys", async (c) => {
    const parsed = CreateApiKeyBody.safeParse(await readBody(c));
    if (!parsed.success) return c.json({ error: "密钥参数不合法" }, 400);
    const userId = userIdOf(c.get("actor"));
    if (!userId) return c.json({ error: "需要所有者令牌" }, 401);
    try {
      const issued = await createApiKey(deps.db, { userId, ...parsed.data });
      return c.json(issued, 201);
    } catch (error) {
      if (error instanceof ApiKeyInputError) return c.json({ error: error.message }, 400);
      throw error;
    }
  });

  api.get("/me/api-keys", async (c) => {
    const userId = userIdOf(c.get("actor"));
    if (!userId) return c.json({ error: "需要所有者令牌" }, 401);
    return c.json(await listApiKeys(deps.db, userId));
  });

  api.delete("/me/api-keys/:id", async (c) => {
    const userId = userIdOf(c.get("actor"));
    if (!userId) return c.json({ error: "需要所有者令牌" }, 401);
    const outcome = await revokeApiKey(deps.db, { id: c.req.param("id"), userId });
    if (outcome === "missing") return c.json({ error: "找不到密钥" }, 404);
    return c.json({ ok: true });
  });

  api.get("/documents/:id/events", async (c) => {
    const denied = await requireDocument(c, c.req.param("id"));
    if (denied) return denied;
    const after = Number(c.req.query("after") ?? "0");
    return c.json(await deps.db.eventsAfter(c.req.param("id"), Number.isFinite(after) ? after : 0));
  });

  api.get("/documents/:id/events/stream", async (c) => {
    const denied = await requireDocument(c, c.req.param("id"));
    if (denied) return denied;
    const after = Number(c.req.query("after") ?? "0");
    const events = await deps.db.eventsAfter(c.req.param("id"), Number.isFinite(after) ? after : 0);
    const body = events
      .map((event) => `id: ${event.seq}\ndata: ${JSON.stringify(event)}\n\n`)
      .join("");
    return new Response(body, { headers: { "content-type": "text/event-stream" } });
  });

  app.route("/api/v1", api);
  return app;
}

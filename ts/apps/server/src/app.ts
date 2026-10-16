import {
  ActorSchema,
  DossierOpSchema,
  type EditOp,
  EditOpSchema,
  TurnRequestSchema,
} from "@luminwrite/contracts";
import {
  DocumentNotFound,
  type LwDatabase,
  SessionClosed,
  SessionNotFound,
  SessionScopeError,
  VersionNotFound,
} from "@luminwrite/db";
import type { WritingRuntime } from "@luminwrite/harness";
import { Hono } from "hono";
import { z } from "zod";

const CreateDocument = z.object({
  title: z.string().min(1),
  ownerId: z.string().min(1),
  genre: z.string().optional(),
});

const OpsBody = z.object({
  ops: z.array(EditOpSchema).min(1),
  actor: ActorSchema,
  summary: z.string().optional(),
  sessionId: z.string().min(1).optional(),
});

const OpenSessionBody = z.object({
  kind: z.enum(["fork", "api", "collab"]),
  baseVersionId: z.string().min(1).optional(),
  actor: ActorSchema,
});

const DossierBody = z.object({
  expectedVersion: z.number().int().positive(),
  ops: z.array(DossierOpSchema).min(1),
});

const DeriveBody = z.object({
  versionId: z.string().min(1),
  inheritDossier: z.boolean(),
  ownerId: z.string().min(1),
  title: z.string().min(1).optional(),
  actor: ActorSchema,
});

const ReadGrantBody = z.object({
  documentId: z.string().min(1),
});

const SessionEditsBody = z.object({
  documentIds: z.array(z.string().min(1)).min(1),
  ops: z.array(EditOpSchema).min(1),
  actor: ActorSchema,
  summary: z.string().optional(),
});

const RollbackBody = z.object({
  versionId: z.string().min(1),
  actor: ActorSchema,
});

export interface AppDeps {
  db: LwDatabase;
  runtime: WritingRuntime;
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

export function createApp(deps: AppDeps): Hono {
  const app = new Hono();

  app.get("/health", (c) => c.json({ ok: true, service: "luminwrite-ts" }));

  app.post("/documents", async (c) => {
    const parsed = CreateDocument.safeParse(await c.req.json());
    if (!parsed.success) return c.json({ error: "文档参数不合法" }, 400);
    const created = await deps.db.createDocument(parsed.data);
    return c.json(created, 201);
  });

  app.get("/documents/:id", async (c) => {
    const document = await deps.db.getDocument(c.req.param("id"));
    if (!document) return c.json({ error: "找不到文档" }, 404);
    const blocks = await deps.db.blocks(document.id);
    return c.json({ ...document, blocks });
  });

  app.get("/documents/:id/versions", async (c) => {
    return c.json(await deps.db.listVersions(c.req.param("id")));
  });

  app.post("/documents/:id/ops", async (c) => {
    const parsed = OpsBody.safeParse(await c.req.json());
    if (!parsed.success) return c.json({ error: "修订参数不合法" }, 400);
    try {
      const result = await deps.db.applyManual({
        documentId: c.req.param("id"),
        ops: parsed.data.ops as EditOp[],
        actor: parsed.data.actor,
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

  app.get("/documents/:id/sessions", async (c) => {
    return c.json(await deps.db.listSessions(c.req.param("id")));
  });

  app.post("/documents/:id/sessions", async (c) => {
    const parsed = OpenSessionBody.safeParse(await c.req.json());
    if (!parsed.success) return c.json({ error: "会话参数不合法" }, 400);
    try {
      const session = await deps.db.openSession({
        documentId: c.req.param("id"),
        kind: parsed.data.kind,
        actor: parsed.data.actor,
        ...(parsed.data.baseVersionId ? { baseVersionId: parsed.data.baseVersionId } : {}),
      });
      return c.json(session, 201);
    } catch (error) {
      const mapped = httpError(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
  });

  app.post("/sessions/:id/close", async (c) => {
    try {
      await deps.db.closeSession(c.req.param("id"));
      return c.json({ ok: true });
    } catch (error) {
      const mapped = httpError(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
  });

  app.post("/sessions/:id/reads", async (c) => {
    const parsed = ReadGrantBody.safeParse(await c.req.json());
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

  app.post("/sessions/:id/edits", async (c) => {
    const parsed = SessionEditsBody.safeParse(await c.req.json());
    if (!parsed.success) return c.json({ error: "修订参数不合法" }, 400);
    try {
      const result = await deps.db.applySessionEdits({
        sessionId: c.req.param("id"),
        documentIds: parsed.data.documentIds,
        ops: parsed.data.ops as EditOp[],
        actor: parsed.data.actor,
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

  app.get("/documents/:id/dossier", async (c) => {
    try {
      return c.json(await deps.db.readDossier(c.req.param("id")));
    } catch (error) {
      const mapped = httpError(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
  });

  app.post("/documents/:id/dossier", async (c) => {
    const parsed = DossierBody.safeParse(await c.req.json());
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

  app.post("/documents/:id/derive", async (c) => {
    const parsed = DeriveBody.safeParse(await c.req.json());
    if (!parsed.success) return c.json({ error: "派生参数不合法" }, 400);
    try {
      const source = await deps.db.listVersions(c.req.param("id"));
      if (!source.some((version) => version.id === parsed.data.versionId)) {
        return c.json({ error: "版本不属于这篇文档" }, 404);
      }
      const created = await deps.db.deriveDocument({
        versionId: parsed.data.versionId,
        inheritDossier: parsed.data.inheritDossier,
        ownerId: parsed.data.ownerId,
        actor: parsed.data.actor,
        ...(parsed.data.title ? { title: parsed.data.title } : {}),
      });
      return c.json(created, 201);
    } catch (error) {
      const mapped = httpError(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
  });

  app.get("/documents/:id/derived", async (c) => {
    return c.json(await deps.db.listDerived(c.req.param("id")));
  });

  app.post("/documents/:id/rollback", async (c) => {
    const parsed = RollbackBody.safeParse(await c.req.json());
    if (!parsed.success) return c.json({ error: "回滚参数不合法" }, 400);
    try {
      const result = await deps.db.rollback({
        documentId: c.req.param("id"),
        toVersionId: parsed.data.versionId,
        actor: parsed.data.actor,
      });
      if (!result.ok) return c.json(result, 409);
      return c.json(result);
    } catch (error) {
      const mapped = httpError(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
  });

  app.post("/documents/:id/turns", async (c) => {
    const body = await c.req.json();
    const parsed = TurnRequestSchema.safeParse({ ...body, documentId: c.req.param("id") });
    if (!parsed.success) return c.json({ error: "回合参数不合法" }, 400);
    try {
      const result = await deps.runtime.submitTurn(parsed.data);
      return c.json(result);
    } catch (error) {
      const mapped = httpError(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
  });

  app.get("/documents/:id/events", async (c) => {
    const after = Number(c.req.query("after") ?? "0");
    return c.json(await deps.db.eventsAfter(c.req.param("id"), Number.isFinite(after) ? after : 0));
  });

  app.get("/documents/:id/events/stream", async (c) => {
    const after = Number(c.req.query("after") ?? "0");
    const events = await deps.db.eventsAfter(c.req.param("id"), Number.isFinite(after) ? after : 0);
    const body = events
      .map((event) => `id: ${event.seq}\ndata: ${JSON.stringify(event)}\n\n`)
      .join("");
    return new Response(body, { headers: { "content-type": "text/event-stream" } });
  });

  return app;
}

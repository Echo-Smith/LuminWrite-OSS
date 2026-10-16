import { ActorSchema, type EditOp, EditOpSchema, TurnRequestSchema } from "@luminwrite/contracts";
import { DocumentNotFound, type LwDatabase } from "@luminwrite/db";
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
});

const RollbackBody = z.object({
  versionId: z.string().min(1),
  actor: ActorSchema,
});

export interface AppDeps {
  db: LwDatabase;
  runtime: WritingRuntime;
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
      });
      if (!result.ok) return c.json(result, 409);
      return c.json(result);
    } catch (error) {
      if (error instanceof DocumentNotFound) return c.json({ error: error.message }, 404);
      throw error;
    }
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
      if (error instanceof DocumentNotFound) return c.json({ error: error.message }, 404);
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
      if (error instanceof DocumentNotFound) return c.json({ error: error.message }, 404);
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

import type { Actor } from "@luminwrite/contracts";
import type { ApiKeyView, LwDatabase } from "@luminwrite/db";
import {
  FactLikeNote,
  factHintText,
  PreferenceConflict,
  PreferenceInputError,
  PreferenceNotFound,
  type PreferenceStatus,
  type ReviewAction,
} from "@luminwrite/db";
import type { EditAuthorizer } from "@luminwrite/edition";
import type { Context, Hono } from "hono";
import { z } from "zod";

type Env = { Variables: { actor: Actor; apiKey?: ApiKeyView } };

const STATUSES = ["candidate", "active", "muted", "rejected", "superseded"] as const;
const KINDS = [
  "habit",
  "sentence_pattern",
  "taboo",
  "term",
  "structure",
  "tone",
  "format",
] as const;

const WriteBody = z.object({
  text: z.string(),
  kind: z.enum(KINDS),
  scopeLevel: z.enum(["global", "pack", "genre", "document"]),
  scopeRef: z.string().optional(),
  strength: z.enum(["must", "prefer"]).optional(),
});

const ReviewBody = z.object({
  action: z.enum(["accept", "reject", "mute", "unmute"]),
});

const CorrectBody = z.object({
  expectedVersion: z.number().int().positive(),
  text: z.string(),
});

const CHAT_NOTICE = "已有的聊天记录里可能还留有原文。可以另外删除这篇文档的会话记录。";

export function registerNoteRoutes(
  api: Hono<Env>,
  db: LwDatabase,
  authorizer: EditAuthorizer,
): void {
  api.get("/me/writing-notes/export", async (c) => {
    const owner = ownerOf(c);
    if (owner instanceof Response) return owner;
    const notes = await db.notes().list(owner.userId, { status: [...STATUSES] });
    return c.json({ notes });
  });

  api.get("/me/writing-notes", async (c) => {
    const owner = ownerOf(c);
    if (owner instanceof Response) return owner;
    const status = c.req.query("status");
    const filter = status ? parseStatuses(status) : undefined;
    if (status && !filter) return c.json({ error: "笔记状态不合法" }, 400);
    const notes = await db.notes().list(owner.userId, filter ? { status: filter } : undefined);
    return c.json({ notes });
  });

  api.post("/me/writing-notes", async (c) => {
    const owner = ownerOf(c);
    if (owner instanceof Response) return owner;
    const parsed = WriteBody.safeParse(await readJson(c));
    if (!parsed.success) return c.json({ error: "笔记参数不合法" }, 400);
    try {
      const written = await db.notes().write(
        owner.userId,
        {
          text: parsed.data.text,
          kind: parsed.data.kind,
          scope: scopeOf(parsed.data.scopeLevel, parsed.data.scopeRef),
          ...(parsed.data.strength ? { strength: parsed.data.strength } : {}),
        },
        { type: "user", id: owner.userId },
      );
      return c.json(
        {
          ...written.preference,
          factHint: written.factHint,
          ...(written.factHint ? { hint: factHintText } : {}),
        },
        201,
      );
    } catch (error) {
      const mapped = noteError(c, error);
      if (mapped) return mapped;
      throw error;
    }
  });

  api.get("/me/writing-notes/:id/audit", async (c) => {
    const owner = ownerOf(c);
    if (owner instanceof Response) return owner;
    try {
      return c.json({ audit: await db.notes().explain(owner.userId, c.req.param("id")) });
    } catch (error) {
      const mapped = noteError(c, error);
      if (mapped) return mapped;
      throw error;
    }
  });

  api.post("/me/writing-notes/:id/review", async (c) => {
    const owner = ownerOf(c);
    if (owner instanceof Response) return owner;
    const parsed = ReviewBody.safeParse(await readJson(c));
    if (!parsed.success) return c.json({ error: "确认参数不合法" }, 400);
    try {
      const note = await db
        .notes()
        .review(owner.userId, c.req.param("id"), parsed.data.action as ReviewAction, {
          type: "user",
          id: owner.userId,
        });
      return c.json(note);
    } catch (error) {
      const mapped = noteError(c, error);
      if (mapped) return mapped;
      throw error;
    }
  });

  api.post("/me/writing-notes/:id/correct", async (c) => {
    const owner = ownerOf(c);
    if (owner instanceof Response) return owner;
    const parsed = CorrectBody.safeParse(await readJson(c));
    if (!parsed.success) return c.json({ error: "改写参数不合法" }, 400);
    try {
      const note = await db
        .notes()
        .correct(owner.userId, c.req.param("id"), parsed.data.expectedVersion, parsed.data.text, {
          type: "user",
          id: owner.userId,
        });
      return c.json(note);
    } catch (error) {
      const mapped = noteError(c, error);
      if (mapped) return mapped;
      throw error;
    }
  });

  api.delete("/me/writing-notes/:id", async (c) => {
    const owner = ownerOf(c);
    if (owner instanceof Response) return owner;
    try {
      const receipt = await db.notes().forget(owner.userId, c.req.param("id"), {
        type: "user",
        id: owner.userId,
      });
      return c.json({ ...receipt, notice: CHAT_NOTICE });
    } catch (error) {
      const mapped = noteError(c, error);
      if (mapped) return mapped;
      throw error;
    }
  });

  api.post("/documents/:id/sessions/:sessionId/discard", async (c) =>
    discard(c, db, authorizer, false),
  );
  api.post("/documents/:id/sessions/:sessionId/undo-discard", async (c) =>
    discard(c, db, authorizer, true),
  );
}

function ownerOf(c: Context<Env>): { userId: string } | Response {
  const actor = c.get("actor");
  if (actor.type !== "user") return c.json({ error: "需要所有者令牌" }, 403);
  return { userId: actor.userId };
}

function scopeOf(
  level: "global" | "pack" | "genre" | "document",
  ref: string | undefined,
): { level: "global" } | { level: "pack" | "genre" | "document"; ref?: string } {
  if (level === "global") return { level: "global" };
  return ref ? { level, ref } : { level };
}

function parseStatuses(raw: string): PreferenceStatus[] | undefined {
  const parts = raw
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
  if (parts.length === 0) return undefined;
  if (!parts.every((item) => (STATUSES as readonly string[]).includes(item))) return undefined;
  return parts as PreferenceStatus[];
}

function noteError(c: Context<Env>, error: unknown): Response | undefined {
  if (error instanceof PreferenceNotFound) return c.json({ error: error.message }, 404);
  if (error instanceof PreferenceConflict) return c.json({ error: error.message }, 409);
  if (error instanceof PreferenceInputError || error instanceof FactLikeNote) {
    return c.json({ error: error.message }, 400);
  }
  return undefined;
}

async function readJson(c: Context<Env>): Promise<unknown> {
  try {
    return await c.req.json();
  } catch {
    return undefined;
  }
}

async function discard(
  c: Context<Env>,
  db: LwDatabase,
  authorizer: EditAuthorizer,
  undo: boolean,
): Promise<Response> {
  const owner = ownerOf(c);
  if (owner instanceof Response) return owner;
  const documentId = c.req.param("id");
  const sessionId = c.req.param("sessionId");
  if (!documentId || !sessionId) return c.json({ error: "找不到会话" }, 404);
  const allowed = await authorizer.canEdit(documentId, owner.userId);
  if (!allowed) return c.json({ error: "找不到文档" }, 404);
  const session = await db.getSession(sessionId);
  if (!session || session.documentId !== documentId) return c.json({ error: "找不到会话" }, 404);
  if (undo) return c.json(await db.undoDiscardSession(sessionId));
  return c.json(await db.discardSession(sessionId));
}

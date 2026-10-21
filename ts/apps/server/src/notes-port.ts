import type { LwDatabase } from "@luminwrite/db";
import { FactLikeNote, PreferenceInputError, type PreferenceKind } from "@luminwrite/db";
import type { WritingNotes } from "@luminwrite/harness";

const LEVELS = new Set(["global", "pack", "genre", "document"]);

export function writingNotesPort(db: LwDatabase): WritingNotes {
  return {
    async proseFor(session) {
      const doc = await documentOf(db, session.documentId);
      if (!doc) return undefined;
      const layer = await db.notes().compileOverlay(doc.ownerId, {
        ...(doc.genre ? { genre: doc.genre } : {}),
        documentId: doc.id,
        ...(doc.styleRef ? { packRef: doc.styleRef } : {}),
      });
      return layer.prose;
    },
    async read(session) {
      const doc = await documentOf(db, session.documentId);
      if (!doc) return [];
      const notes = await db.notes().list(doc.ownerId, { status: ["active"] });
      return notes.map((note) => ({
        id: note.id,
        kind: note.kind,
        statement: note.statement,
        plainConflict: note.plainConflict,
        scope: note.scope,
      }));
    },
    async propose(session, input) {
      if (session.kind === "api") return { ok: false, reason: "这个会话不能提写作笔记" };
      const doc = await documentOf(db, session.documentId);
      if (!doc) return { ok: false, reason: "找不到文档" };
      if (!LEVELS.has(input.scopeLevel)) return { ok: false, reason: "笔记范围不合法" };
      try {
        const note = await db.notes().propose(doc.ownerId, {
          text: input.text,
          kind: input.kind as PreferenceKind,
          scope:
            input.scopeLevel === "global"
              ? { level: "global" }
              : { level: input.scopeLevel as "pack" | "genre" | "document", ref: input.scopeRef },
          sessionId: session.id,
        });
        return { ok: true, id: note.id };
      } catch (error) {
        if (error instanceof FactLikeNote || error instanceof PreferenceInputError) {
          return { ok: false, reason: error.message };
        }
        throw error;
      }
    },
  };
}

async function documentOf(
  db: LwDatabase,
  documentId: string,
): Promise<{ id: string; ownerId: string; genre: string; styleRef: string } | undefined> {
  const rows = await db.query<{ owner_id: string; genre: string; style_ref: string }>(
    `SELECT owner_id, genre, style_ref FROM documents WHERE id = $1`,
    [documentId],
  );
  const row = rows[0];
  if (!row) return undefined;
  return { id: documentId, ownerId: row.owner_id, genre: row.genre, styleRef: row.style_ref };
}

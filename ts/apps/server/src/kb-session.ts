import type { LwDatabase } from "@luminwrite/db";
import type { SessionKnowledge } from "@luminwrite/harness";
import { type KnowledgeBase, retrievalDataNote, type SourceKind } from "@luminwrite/kb";

const KINDS = new Set<string>(["upload", "url", "rss_item", "web", "document"]);

function isKind(value: string): value is SourceKind {
  return KINDS.has(value);
}

async function ownerOf(db: LwDatabase, sessionId: string) {
  const session = await db.getSession(sessionId);
  if (!session) return undefined;
  const document = await db.getDocument(session.documentId);
  if (!document) return undefined;
  return { ownerId: document.ownerId, documentId: document.id };
}

/** 工具不接收 ownerId。当前文档和它的派生血缘默认排除，避免把正在写的稿子当成外部资料。 */
export function sessionKnowledge(db: LwDatabase, kb: KnowledgeBase): SessionKnowledge {
  return {
    async search(input) {
      const owner = await ownerOf(db, input.sessionId);
      if (!owner) throw new Error("找不到会话");
      const kinds = input.kinds?.filter(isKind);
      const days = input.recencyDays;
      const publishedAfter =
        days !== undefined && Number.isFinite(days) && days > 0
          ? new Date(Date.now() - days * 86_400_000).toISOString()
          : undefined;
      const related = await db.relatedDocumentIds(owner.documentId);
      const hits = await kb.search({
        ownerId: owner.ownerId,
        text: input.query,
        limit: input.limit ?? 5,
        ...(kinds && kinds.length > 0 ? { kinds } : {}),
        ...(publishedAfter ? { publishedAfter } : {}),
        ...(input.library ? { library: true } : {}),
        excludeDocumentIds: [owner.documentId, ...related],
      });
      return { note: retrievalDataNote, hits };
    },
    async readSource(input) {
      const owner = await ownerOf(db, input.sessionId);
      if (!owner) throw new Error("找不到会话");
      const range =
        input.charStart !== undefined && input.charEnd !== undefined
          ? { charStart: input.charStart, charEnd: input.charEnd }
          : undefined;
      const source = await kb.readSource(owner.ownerId, input.sourceId, range);
      return { note: retrievalDataNote, ...source };
    },
  };
}

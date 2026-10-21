import type { LwDatabase } from "@luminwrite/db";
import type { JobPort } from "@luminwrite/jobs";
import { KbInputError, type KnowledgeBase } from "@luminwrite/kb";

export const DOC_INDEX_DELAY_SECONDS = 60;

/** 版本变化走 60 秒节流；首次保存、撤销丢弃和移出知识库立刻处理。 */
export function bindDocumentIndex(db: LwDatabase, jobs: JobPort, kb: KnowledgeBase): void {
  db.onDocumentIndex(async (notice) => {
    if (notice.action === "schedule") {
      await jobs.send(
        "doc.index",
        { documentId: notice.documentId },
        { singletonKey: notice.documentId, startAfterSeconds: DOC_INDEX_DELAY_SECONDS },
      );
      return;
    }
    await indexNow(kb, notice.documentId);
  });
}

export async function startDocumentIndex(jobs: JobPort, kb: KnowledgeBase): Promise<void> {
  await jobs.work("doc.index", async (data) => {
    const documentId = documentIdOf(data);
    if (!documentId) return;
    await indexNow(kb, documentId);
  });
}

async function indexNow(kb: KnowledgeBase, documentId: string): Promise<void> {
  try {
    await kb.indexDocument(documentId);
  } catch (error) {
    if (error instanceof KbInputError && error.message.startsWith("找不到")) return;
    throw error;
  }
}

function documentIdOf(data: unknown): string | undefined {
  if (!data || typeof data !== "object") return undefined;
  const value = (data as { documentId?: unknown }).documentId;
  return typeof value === "string" && value.length > 0 ? value : undefined;
}

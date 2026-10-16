import { z } from "zod";

export const SessionKindSchema = z.enum(["main", "fork", "api", "collab"]);
export type SessionKind = z.infer<typeof SessionKindSchema>;

export const OpenSessionKindSchema = z.enum(["fork", "api", "collab"]);
export type OpenSessionKind = z.infer<typeof OpenSessionKindSchema>;

/** 一批 EditOp 只能指向一篇文档。多篇直接拒绝，不落版本。 */
export function editBatchTarget(
  documentIds: readonly string[],
): { ok: true; documentId: string } | { ok: false; message: string } {
  const unique = [...new Set(documentIds.map((id) => id.trim()).filter((id) => id.length > 0))];
  if (unique.length !== 1 || !unique[0]) {
    return { ok: false, message: "一批修订只能针对一篇文档" };
  }
  return { ok: true, documentId: unique[0] };
}

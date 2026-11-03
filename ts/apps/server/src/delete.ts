import {
  type DocumentDeleteClaim,
  DocumentNotFound,
  deletionBusyReason,
  type LwDatabase,
  type Residue,
  type SessionDeleteClaim,
  SessionNotFound,
} from "@luminwrite/db";
import type { WritingRuntime } from "@luminwrite/harness";

export interface DeletedBody {
  deleted: true;
  residue: Residue;
  detail: string;
}

export interface DeleteFailure {
  status: 404 | 409 | 500;
  body: { error: string };
}

type DeleteDeps = { db: LwDatabase; runtime: WritingRuntime };

export function residueDetail(residue: Residue): string {
  if (residue === "cleared") return "已删除";
  return "已删除；数据库日志里的旧片段会在下次检查点后被覆盖";
}

function combine(left: Residue, right: Residue): Residue {
  return left === "wal_pending" || right === "wal_pending" ? "wal_pending" : "cleared";
}

function failure(error: unknown): DeleteFailure {
  if (error instanceof SessionNotFound || error instanceof DocumentNotFound) {
    return { status: 404, body: { error: error.message } };
  }
  const message = error instanceof Error ? error.message : "删除失败";
  return { status: 500, body: { error: message } };
}

/** 先确认没有进行中的工作，再删 pi 和库里的行。任何一步失败都回到已归档，不重试。 */
export async function permanentlyDeleteSession(
  deps: DeleteDeps,
  sessionId: string,
): Promise<{ status: 200; body: DeletedBody } | DeleteFailure> {
  let claim: SessionDeleteClaim;
  try {
    const claimed = await deps.db.claimPermanentSessionDelete(sessionId);
    if (!claimed.ok) return { status: 409, body: { error: claimed.error } };
    claim = claimed.claim;
  } catch (error) {
    return failure(error);
  }
  try {
    const pi = await purgeConversations(
      deps.runtime,
      claim.piConversationId ? [claim.piConversationId] : [],
    );
    if (!pi.ok) {
      await deps.db.releasePermanentSessionDelete(sessionId);
      return { status: pi.status, body: { error: pi.error } };
    }
    await deps.db.eraseSession(sessionId);
    const pg = await deps.db.scrubFreedText();
    const residue = combine(pi.residue, pg);
    return { status: 200, body: { deleted: true, residue, detail: residueDetail(residue) } };
  } catch (error) {
    await deps.db.releasePermanentSessionDelete(sessionId).catch(() => undefined);
    return failure(error);
  }
}

export async function permanentlyDeleteDocument(
  deps: DeleteDeps,
  documentId: string,
): Promise<{ status: 200; body: DeletedBody } | DeleteFailure> {
  let claim: DocumentDeleteClaim;
  try {
    const claimed = await deps.db.claimPermanentDocumentDelete(documentId);
    if (!claimed.ok) return { status: 409, body: { error: claimed.error } };
    claim = claimed.claim;
  } catch (error) {
    return failure(error);
  }
  const release = () => deps.db.releasePermanentDocumentDelete(claim).catch(() => undefined);
  try {
    const piIds = claim.sessions
      .map((session) => session.piConversationId)
      .filter((id): id is string => Boolean(id));
    const pi = await purgeConversations(deps.runtime, piIds);
    if (!pi.ok) {
      await release();
      return { status: pi.status, body: { error: pi.error } };
    }
    await deps.db.eraseDocument(documentId);
    const pg = await deps.db.scrubFreedText();
    const residue = combine(pi.residue, pg);
    return { status: 200, body: { deleted: true, residue, detail: residueDetail(residue) } };
  } catch (error) {
    await release();
    return failure(error);
  }
}

async function purgeConversations(
  runtime: WritingRuntime,
  ids: string[],
): Promise<{ ok: true; residue: Residue } | { ok: false; status: 409; error: string }> {
  if (ids.length === 0) return { ok: true, residue: "cleared" };
  const work = runtime.activePiWork
    ? await runtime.activePiWork(ids)
    : { tasks: 0, submissions: 0, busyConversationIds: [] };
  const reason = deletionBusyReason({
    turns: 0,
    tasks: work.tasks + work.submissions,
    subagents: 0,
  });
  if (reason) return { ok: false, status: 409, error: reason };
  if (!runtime.purgePiConversations) return { ok: true, residue: "cleared" };
  const report = await runtime.purgePiConversations(ids);
  return { ok: true, residue: report.residue };
}

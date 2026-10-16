import { PGlite } from "@electric-sql/pglite";
import type {
  Actor,
  BlockId,
  DossierContent,
  DossierOp,
  EditOp,
  OpError,
  RunEvent,
  SessionKind,
  TurnRequest,
  TurnStatus,
} from "@luminwrite/contracts";
import {
  applyDossierOps,
  DossierContentSchema,
  dossierForDerived,
  editBatchTarget,
  emptyDossier,
} from "@luminwrite/contracts";
import {
  applyOps,
  blockHashSync,
  blockIdOf,
  blockText,
  emptyDoc,
  findBlock,
  listBlocks,
  type PMJSON,
  rebaseOp,
  remapBlockIds,
} from "@luminwrite/doc-model";
import type { UsageEvent } from "@luminwrite/edition";
import { and, desc, eq } from "drizzle-orm";
import { drizzle as drizzlePg } from "drizzle-orm/node-postgres";
import { drizzle, type PgliteDatabase } from "drizzle-orm/pglite";
import pg from "pg";

import { newId } from "./ids.js";
import { migrationSql } from "./migrate.js";
import * as schema from "./schema.js";

const {
  documents,
  docVersions,
  docBlocks,
  turns,
  runEvents,
  proposals,
  proposalHunks,
  usageLedger,
  sessions,
  sessionReads,
  documentDossier,
} = schema;

export class DocumentNotFound extends Error {
  constructor(id: string) {
    super(`找不到文档 ${id}`);
    this.name = "DocumentNotFound";
  }
}

export class SessionNotFound extends Error {
  constructor(id: string) {
    super(`找不到会话 ${id}`);
    this.name = "SessionNotFound";
  }
}

export class SessionClosed extends Error {
  constructor(id: string) {
    super(`会话已关闭 ${id}`);
    this.name = "SessionClosed";
  }
}

export class SessionScopeError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "SessionScopeError";
  }
}

export class VersionNotFound extends Error {
  constructor(id: string) {
    super(`找不到版本 ${id}`);
    this.name = "VersionNotFound";
  }
}

export interface BlockView {
  blockId: BlockId;
  type: string;
  contentHash: string;
  text: string;
  orderKey: string;
}

export interface VersionView {
  id: string;
  seq: number;
  parentId: string | null;
  origin: string;
  summary: string;
  content: PMJSON;
  turnId: string | null;
}

export interface DocumentView {
  id: string;
  ownerId: string;
  title: string;
  humanTouched: boolean;
  headVersionId: string | null;
  head: VersionView | null;
}

export interface TurnView {
  id: string;
  documentId: string;
  sessionId: string;
  status: TurnStatus;
  resultVersionId: string | null;
  error: string | null;
  piSubmissionId: string | null;
  idempotencyKey: string;
}

export interface SessionView {
  id: string;
  documentId: string;
  kind: SessionKind;
  baseVersionId: string;
  piConversationId: string | null;
  createdByType: string;
  createdById: string;
  status: "open" | "closed";
  createdAt: string;
  updatedAt: string;
}

export interface DossierView {
  documentId: string;
  version: number;
  content: DossierContent;
}

export interface DerivedDocumentView {
  id: string;
  title: string;
  derivedFromVersionId: string | null;
  headVersionId: string | null;
}

export type ApplyOutcome =
  | { ok: true; versionId: string; seq: number }
  | { ok: false; errors: OpError[] };

export type ProposeOutcome =
  | {
      ok: true;
      already: boolean;
      status: "applied" | "proposed";
      versionId?: string;
      proposalId?: string;
    }
  | { ok: false; errors: OpError[] };

type Origin = "turn" | "manual" | "restore" | "import" | "api" | "derive";
type Orm = PgliteDatabase<typeof schema>;

interface Closer {
  exec(sqlText: string): Promise<void>;
  close(): Promise<void>;
}

function actorParts(actor: Actor): { actorType: string; actorId: string } {
  if (actor.type === "system") return { actorType: "system", actorId: "system" };
  return { actorType: actor.type, actorId: actor.userId };
}

function asDoc(value: unknown): PMJSON {
  if (typeof value === "string") return JSON.parse(value) as PMJSON;
  return value as PMJSON;
}

export class LwDatabase {
  private readonly orm: Orm;
  private readonly client: Closer;

  constructor(orm: Orm, client: Closer) {
    this.orm = orm;
    this.client = client;
  }

  exec(sqlText: string): Promise<void> {
    return this.client.exec(sqlText);
  }

  close(): Promise<void> {
    return this.client.close();
  }

  async createDocument(input: {
    title: string;
    ownerId: string;
    genre?: string;
    createdVia?: string;
  }): Promise<{ id: string; versionId: string; sessionId: string }> {
    const id = newId("doc");
    const versionId = newId("ver");
    const sessionId = newId("ses");
    const now = new Date();
    await this.orm.transaction(async (tx) => {
      await tx.insert(documents).values({
        id,
        ownerId: input.ownerId,
        title: input.title,
        genre: input.genre ?? "",
        status: "draft",
        headVersionId: versionId,
        finalVersionId: null,
        styleRef: "pkg:luminwrite/plain-professional@1.0.0",
        tierRef: "balanced",
        brief: {},
        authoredByMe: false,
        createdVia: input.createdVia ?? "ui",
        humanTouched: false,
        derivedFromDocumentId: null,
        derivedFromVersionId: null,
        createdAt: now,
      });
      await tx.insert(docVersions).values({
        id: versionId,
        documentId: id,
        seq: 1,
        parentId: null,
        origin: "import",
        actorType: "user",
        actorId: input.ownerId,
        turnId: null,
        piTaskId: null,
        content: emptyDoc(),
        ops: [],
        summary: "空文档",
        createdAt: now,
      });
      await tx.insert(documentDossier).values({
        documentId: id,
        version: 1,
        content: emptyDossier(),
        updatedAt: now,
      });
      await tx.insert(sessions).values({
        id: sessionId,
        documentId: id,
        kind: "main",
        baseVersionId: versionId,
        piConversationId: null,
        createdByType: "user",
        createdById: input.ownerId,
        status: "open",
        createdAt: now,
        updatedAt: now,
      });
    });
    return { id, versionId, sessionId };
  }

  async getDocument(id: string): Promise<DocumentView | undefined> {
    const rows = await this.orm.select().from(documents).where(eq(documents.id, id)).limit(1);
    const row = rows[0];
    if (!row) return undefined;
    const head = row.headVersionId ? await this.versionById(row.headVersionId) : null;
    return {
      id: row.id,
      ownerId: row.ownerId,
      title: row.title,
      humanTouched: row.humanTouched,
      headVersionId: row.headVersionId,
      head,
    };
  }

  async listVersions(documentId: string): Promise<VersionView[]> {
    const rows = await this.orm
      .select()
      .from(docVersions)
      .where(eq(docVersions.documentId, documentId))
      .orderBy(docVersions.seq);
    return rows.map(toVersion);
  }

  async blocks(documentId: string): Promise<BlockView[]> {
    const rows = await this.orm
      .select()
      .from(docBlocks)
      .where(eq(docBlocks.documentId, documentId))
      .orderBy(docBlocks.orderKey);
    return rows.map((row) => ({
      blockId: row.blockId as BlockId,
      type: row.type,
      contentHash: row.contentHash,
      text: row.text,
      orderKey: row.orderKey,
    }));
  }

  async applyManual(input: {
    documentId: string;
    ops: EditOp[];
    actor: Actor;
    summary?: string;
    scope?: BlockId[];
    sessionId?: string;
  }): Promise<ApplyOutcome> {
    return this.orm.transaction(async (tx) => {
      const written = await this.writeOps(tx, {
        documentId: input.documentId,
        ops: input.ops,
        origin: "manual",
        actor: input.actor,
        summary: input.summary ?? "人工修订",
        scope: input.scope,
      });
      if (written.ok && input.sessionId)
        await this.advanceInTx(tx, input.sessionId, written.versionId);
      return written;
    });
  }

  async rollback(input: {
    documentId: string;
    toVersionId: string;
    actor: Actor;
  }): Promise<ApplyOutcome> {
    return this.orm.transaction(async (tx) => {
      await this.lock(tx, input.documentId);
      const target = await this.versionInTx(tx, input.toVersionId);
      if (!target || target.documentId !== input.documentId) {
        return {
          ok: false as const,
          errors: [{ index: 0, code: "schema_invalid" as const, message: "找不到要回滚的版本" }],
        };
      }
      return this.insertSnapshot(tx, {
        documentId: input.documentId,
        content: asDoc(target.content),
        ops: [],
        origin: "restore",
        actor: input.actor,
        summary: `回滚到 ${target.seq}`,
        turnId: null,
        piTaskId: null,
      });
    });
  }

  async beginTurn(
    req: TurnRequest,
  ): Promise<{ turnId: string; created: boolean; status: TurnStatus }> {
    const id = newId("trn");
    const actor = actorParts(req.actor);
    const session = await this.resolveSession(req);
    const inserted = await this.orm
      .insert(turns)
      .values({
        id,
        documentId: req.documentId,
        kind: req.kind,
        delivery: req.delivery,
        status: "queued",
        selection: req.selection ?? null,
        scope: req.scope,
        baseVersionId: session.baseVersionId,
        resultVersionId: null,
        applyMode: req.applyMode,
        actorType: actor.actorType,
        actorId: actor.actorId,
        sessionId: session.id,
        idempotencyKey: req.idempotencyKey,
        piConversationId: null,
        piSubmissionId: null,
        instruction: req.instruction,
        error: null,
        createdAt: new Date(),
      })
      .onConflictDoNothing({ target: [turns.documentId, turns.idempotencyKey] })
      .returning({ id: turns.id });
    if (inserted.length === 0) {
      const existing = await this.turnByKey(req.documentId, req.idempotencyKey);
      if (!existing) throw new Error("幂等键冲突但找不到回合");
      return { turnId: existing.id, created: false, status: existing.status };
    }
    return { turnId: id, created: true, status: "queued" };
  }

  async markTurn(
    turnId: string,
    patch: {
      status?: TurnStatus;
      piConversationId?: string;
      piSubmissionId?: string;
      resultVersionId?: string;
      error?: string;
    },
  ): Promise<void> {
    const update: Partial<typeof turns.$inferInsert> = {};
    if (patch.status) update.status = patch.status;
    if (patch.piConversationId) update.piConversationId = patch.piConversationId;
    if (patch.piSubmissionId) update.piSubmissionId = patch.piSubmissionId;
    if (patch.resultVersionId) update.resultVersionId = patch.resultVersionId;
    if (patch.error) update.error = patch.error;
    if (Object.keys(update).length === 0) return;
    await this.orm.update(turns).set(update).where(eq(turns.id, turnId));
  }

  async getTurn(turnId: string): Promise<TurnView | undefined> {
    const rows = await this.orm.select().from(turns).where(eq(turns.id, turnId)).limit(1);
    const row = rows[0];
    return row ? toTurn(row) : undefined;
  }

  async propose(input: {
    documentId: string;
    turnId: string;
    ops: EditOp[];
    piTaskId: string;
    applyMode: "auto" | "proposal";
    actor: Actor;
    summary: string;
    scope?: BlockId[];
    sessionId?: string;
  }): Promise<ProposeOutcome> {
    return this.orm.transaction(async (tx) => {
      await this.lock(tx, input.documentId);
      if (input.sessionId) {
        const scoped = await this.sessionInTx(tx, input.sessionId);
        if (!scoped || scoped.documentId !== input.documentId || scoped.status !== "open") {
          await tx
            .update(turns)
            .set({ status: "failed", error: "会话只能修订自己所属的文档" })
            .where(eq(turns.id, input.turnId));
          return {
            ok: false as const,
            errors: [
              { index: 0, code: "out_of_scope" as const, message: "会话只能修订自己所属的文档" },
            ],
          };
        }
      }
      const priorVersion = await tx
        .select()
        .from(docVersions)
        .where(eq(docVersions.piTaskId, input.piTaskId))
        .limit(1);
      if (priorVersion[0]) {
        return {
          ok: true as const,
          already: true,
          status: "applied" as const,
          versionId: priorVersion[0].id,
        };
      }
      const priorProposal = await tx
        .select()
        .from(proposals)
        .where(eq(proposals.piTaskId, input.piTaskId))
        .limit(1);
      if (priorProposal[0]) {
        return {
          ok: true as const,
          already: true,
          status: "proposed" as const,
          proposalId: priorProposal[0].id,
        };
      }
      const doc = await this.headInTx(tx, input.documentId);
      const prepared = prepareOps(asDoc(doc.content), input.ops, input.scope);
      if (!prepared.ok) {
        await tx
          .update(turns)
          .set({ status: "conflicted", error: prepared.errors[0]?.message ?? "冲突" })
          .where(eq(turns.id, input.turnId));
        await this.appendEventTx(tx, input.documentId, {
          turnId: input.turnId,
          type: "edits.conflict",
          payload: { errors: prepared.errors },
        });
        return prepared;
      }
      if (input.applyMode === "proposal") {
        const proposalId = newId("prp");
        await tx.insert(proposals).values({
          id: proposalId,
          documentId: input.documentId,
          turnId: input.turnId,
          baseVersionId: doc.id,
          status: "pending",
          piTaskId: input.piTaskId,
          createdAt: new Date(),
        });
        for (const op of prepared.ops) {
          await tx.insert(proposalHunks).values({
            id: newId("hnk"),
            proposalId,
            op,
            status: "pending",
            userEdit: null,
          });
        }
        await tx.update(turns).set({ status: "proposed" }).where(eq(turns.id, input.turnId));
        await this.appendEventTx(tx, input.documentId, {
          turnId: input.turnId,
          type: "edits.proposed",
          payload: { proposalId },
        });
        return { ok: true as const, already: false, status: "proposed" as const, proposalId };
      }
      const written = await this.insertSnapshot(tx, {
        documentId: input.documentId,
        content: prepared.doc,
        ops: prepared.ops,
        origin: "turn",
        actor: input.actor,
        summary: input.summary,
        turnId: input.turnId,
        piTaskId: input.piTaskId,
      });
      if (!written.ok) return written;
      await tx
        .update(turns)
        .set({ status: "applied", resultVersionId: written.versionId })
        .where(eq(turns.id, input.turnId));
      await this.appendEventTx(tx, input.documentId, {
        turnId: input.turnId,
        type: "edits.applied",
        payload: { versionId: written.versionId },
      });
      return {
        ok: true as const,
        already: false,
        status: "applied" as const,
        versionId: written.versionId,
      };
    });
  }

  async acceptProposal(proposalId: string, actor: Actor): Promise<ApplyOutcome> {
    return this.orm.transaction(async (tx) => {
      const found = await tx.select().from(proposals).where(eq(proposals.id, proposalId)).limit(1);
      const proposal = found[0];
      if (!proposal) {
        return {
          ok: false as const,
          errors: [{ index: 0, code: "schema_invalid" as const, message: "找不到提案" }],
        };
      }
      await this.lock(tx, proposal.documentId);
      const hunks = await tx
        .select()
        .from(proposalHunks)
        .where(eq(proposalHunks.proposalId, proposalId));
      const ops = hunks
        .filter((hunk) => hunk.status === "pending" || hunk.status === "accepted")
        .map((hunk) => hunk.op);
      const written = await this.writeOps(tx, {
        documentId: proposal.documentId,
        ops,
        origin: "turn",
        actor,
        summary: "接受提案",
        turnId: proposal.turnId,
      });
      if (!written.ok) {
        await tx
          .update(proposalHunks)
          .set({ status: "stale" })
          .where(eq(proposalHunks.proposalId, proposalId));
        await tx.update(proposals).set({ status: "stale" }).where(eq(proposals.id, proposalId));
        return written;
      }
      await tx
        .update(proposalHunks)
        .set({ status: "accepted" })
        .where(eq(proposalHunks.proposalId, proposalId));
      await tx.update(proposals).set({ status: "accepted" }).where(eq(proposals.id, proposalId));
      if (proposal.turnId) {
        await tx
          .update(turns)
          .set({ status: "applied", resultVersionId: written.versionId })
          .where(eq(turns.id, proposal.turnId));
      }
      return written;
    });
  }

  async rejectProposal(proposalId: string): Promise<void> {
    await this.orm
      .update(proposalHunks)
      .set({ status: "rejected" })
      .where(eq(proposalHunks.proposalId, proposalId));
    await this.orm
      .update(proposals)
      .set({ status: "rejected" })
      .where(eq(proposals.id, proposalId));
  }

  async appendEvent(
    documentId: string,
    event: { turnId?: string; type: string; payload: unknown },
  ): Promise<RunEvent> {
    return this.orm.transaction(async (tx) => {
      await this.lock(tx, documentId);
      return this.appendEventTx(tx, documentId, event);
    });
  }

  async eventsAfter(documentId: string, afterSeq = 0): Promise<RunEvent[]> {
    const rows = await this.orm
      .select()
      .from(runEvents)
      .where(eq(runEvents.documentId, documentId))
      .orderBy(runEvents.seq);
    return rows
      .filter((row) => row.seq > afterSeq)
      .map((row) => ({
        seq: row.seq,
        documentId: row.documentId,
        ...(row.turnId ? { turnId: row.turnId } : {}),
        type: row.type,
        payload: row.payload,
      }));
  }

  async recordUsage(event: UsageEvent & { apiKeyId?: string; apiTaskId?: string }): Promise<void> {
    await this.orm.insert(usageLedger).values({
      id: newId("use"),
      requestId: event.requestId,
      model: event.model,
      inputTokens: event.inputTokens,
      outputTokens: event.outputTokens,
      costMicros: event.costMicros,
      apiKeyId: event.apiKeyId ?? null,
      apiTaskId: event.apiTaskId ?? null,
      createdAt: new Date(),
    });
  }

  async resolveSession(req: TurnRequest): Promise<SessionView> {
    if (req.sessionId) {
      const session = await this.getSession(req.sessionId);
      if (!session) throw new SessionNotFound(req.sessionId);
      if (session.documentId !== req.documentId) {
        throw new SessionScopeError("会话只能修订自己所属的文档");
      }
      if (session.status !== "open") throw new SessionClosed(session.id);
      return session;
    }
    const main = await this.mainSession(req.documentId);
    if (!main) throw new SessionNotFound(req.documentId);
    return main;
  }

  async getSession(id: string): Promise<SessionView | undefined> {
    const rows = await this.orm.select().from(sessions).where(eq(sessions.id, id)).limit(1);
    const row = rows[0];
    return row ? toSession(row) : undefined;
  }

  async mainSession(documentId: string): Promise<SessionView | undefined> {
    const rows = await this.orm
      .select()
      .from(sessions)
      .where(and(eq(sessions.documentId, documentId), eq(sessions.kind, "main")))
      .limit(1);
    const row = rows[0];
    return row ? toSession(row) : undefined;
  }

  async listSessions(documentId: string): Promise<SessionView[]> {
    const rows = await this.orm
      .select()
      .from(sessions)
      .where(eq(sessions.documentId, documentId))
      .orderBy(sessions.createdAt);
    return rows.map(toSession);
  }

  async openSession(input: {
    documentId: string;
    kind: "fork" | "api" | "collab";
    baseVersionId?: string;
    actor: Actor;
  }): Promise<SessionView> {
    return this.orm.transaction(async (tx) => {
      const doc = await this.lock(tx, input.documentId);
      let baseVersionId = input.baseVersionId;
      if (input.kind === "fork") {
        if (!baseVersionId) throw new SessionScopeError("分叉会话必须指定版本");
        const version = await this.versionInTx(tx, baseVersionId);
        if (!version || version.documentId !== input.documentId) {
          throw new VersionNotFound(baseVersionId);
        }
      } else {
        if (!doc.headVersionId) throw new DocumentNotFound(input.documentId);
        baseVersionId = doc.headVersionId;
      }
      if (!baseVersionId) throw new SessionScopeError("分叉会话必须指定版本");
      const actor = actorParts(input.actor);
      const now = new Date();
      const id = newId("ses");
      await tx.insert(sessions).values({
        id,
        documentId: input.documentId,
        kind: input.kind,
        baseVersionId,
        piConversationId: null,
        createdByType: actor.actorType,
        createdById: actor.actorId,
        status: "open",
        createdAt: now,
        updatedAt: now,
      });
      const created = await this.sessionInTx(tx, id);
      if (!created) throw new SessionNotFound(id);
      return toSession(created);
    });
  }

  async closeSession(sessionId: string): Promise<void> {
    const session = await this.getSession(sessionId);
    if (!session) throw new SessionNotFound(sessionId);
    await this.orm
      .update(sessions)
      .set({ status: "closed", updatedAt: new Date() })
      .where(eq(sessions.id, sessionId));
  }

  async setSessionConversation(sessionId: string, piConversationId: string): Promise<void> {
    const session = await this.getSession(sessionId);
    if (!session) throw new SessionNotFound(sessionId);
    await this.orm
      .update(sessions)
      .set({ piConversationId, updatedAt: new Date() })
      .where(eq(sessions.id, sessionId));
  }

  async advanceSessionBase(sessionId: string, versionId: string): Promise<void> {
    await this.orm.transaction(async (tx) => {
      await this.advanceInTx(tx, sessionId, versionId);
    });
  }

  async grantRead(sessionId: string, documentId: string): Promise<void> {
    const session = await this.getSession(sessionId);
    if (!session) throw new SessionNotFound(sessionId);
    const doc = await this.getDocument(documentId);
    if (!doc) throw new DocumentNotFound(documentId);
    await this.orm.insert(sessionReads).values({ sessionId, documentId }).onConflictDoNothing();
  }

  async canRead(sessionId: string, documentId: string): Promise<boolean> {
    const session = await this.getSession(sessionId);
    if (!session) return false;
    if (session.documentId === documentId) return true;
    const rows = await this.orm
      .select()
      .from(sessionReads)
      .where(and(eq(sessionReads.sessionId, sessionId), eq(sessionReads.documentId, documentId)))
      .limit(1);
    return Boolean(rows[0]);
  }

  async readDossier(documentId: string): Promise<DossierView> {
    const rows = await this.orm
      .select()
      .from(documentDossier)
      .where(eq(documentDossier.documentId, documentId))
      .limit(1);
    const row = rows[0];
    if (!row) throw new DocumentNotFound(documentId);
    return {
      documentId,
      version: row.version,
      content: parseDossier(row.content),
    };
  }

  async applyDossier(input: {
    documentId: string;
    expectedVersion: number;
    ops: DossierOp[];
  }): Promise<
    { ok: true; version: number; content: DossierContent } | { ok: false; message: string }
  > {
    return this.orm.transaction(async (tx) => {
      await this.lock(tx, input.documentId);
      const rows = await tx
        .select()
        .from(documentDossier)
        .where(eq(documentDossier.documentId, input.documentId))
        .limit(1);
      const row = rows[0];
      if (!row) throw new DocumentNotFound(input.documentId);
      if (row.version !== input.expectedVersion) return { ok: false, message: "档案版本已变化" };
      if (input.ops.length === 0) return { ok: false, message: "没有档案操作" };
      const content = applyDossierOps(parseDossier(row.content), input.ops);
      const version = row.version + 1;
      await tx
        .update(documentDossier)
        .set({ version, content, updatedAt: new Date() })
        .where(eq(documentDossier.documentId, input.documentId));
      return { ok: true, version, content };
    });
  }

  async deriveDocument(input: {
    versionId: string;
    inheritDossier: boolean;
    ownerId: string;
    title?: string;
    actor: Actor;
  }): Promise<{ id: string; versionId: string; sessionId: string }> {
    return this.orm.transaction(async (tx) => {
      const source = await this.versionInTx(tx, input.versionId);
      if (!source) throw new VersionNotFound(input.versionId);
      const parents = await tx
        .select()
        .from(documents)
        .where(eq(documents.id, source.documentId))
        .limit(1);
      const parent = parents[0];
      if (!parent) throw new DocumentNotFound(source.documentId);
      const dossierRows = await tx
        .select()
        .from(documentDossier)
        .where(eq(documentDossier.documentId, source.documentId))
        .limit(1);
      const sourceDossier = parseDossier(dossierRows[0]?.content);
      const id = newId("doc");
      const sessionId = newId("ses");
      const now = new Date();
      const actor = actorParts(input.actor);
      await tx.insert(documents).values({
        id,
        ownerId: input.ownerId,
        title: input.title ?? `${parent.title}的派生`,
        genre: parent.genre,
        status: "draft",
        headVersionId: null,
        finalVersionId: null,
        styleRef: parent.styleRef,
        tierRef: parent.tierRef,
        brief: {},
        authoredByMe: false,
        createdVia: "derive",
        humanTouched: false,
        derivedFromDocumentId: source.documentId,
        derivedFromVersionId: source.id,
        createdAt: now,
      });
      const written = await this.insertSnapshot(tx, {
        documentId: id,
        content: remapBlockIds(asDoc(source.content)),
        ops: [],
        origin: "derive",
        actor: input.actor,
        summary: `派生自 ${source.id}`,
        turnId: null,
        piTaskId: null,
      });
      if (!written.ok) throw new SessionScopeError(written.errors[0]?.message ?? "派生失败");
      await tx.insert(documentDossier).values({
        documentId: id,
        version: 1,
        content: dossierForDerived(sourceDossier, input.inheritDossier),
        updatedAt: now,
      });
      await tx.insert(sessions).values({
        id: sessionId,
        documentId: id,
        kind: "main",
        baseVersionId: written.versionId,
        piConversationId: null,
        createdByType: actor.actorType,
        createdById: actor.actorId,
        status: "open",
        createdAt: now,
        updatedAt: now,
      });
      return { id, versionId: written.versionId, sessionId };
    });
  }

  async listDerived(documentId: string): Promise<DerivedDocumentView[]> {
    const rows = await this.orm
      .select()
      .from(documents)
      .where(eq(documents.derivedFromDocumentId, documentId))
      .orderBy(documents.createdAt);
    return rows.map((row) => ({
      id: row.id,
      title: row.title,
      derivedFromVersionId: row.derivedFromVersionId,
      headVersionId: row.headVersionId,
    }));
  }

  async readBlocksAt(documentId: string, versionId: string): Promise<BlockView[]> {
    const version = await this.versionById(versionId);
    if (!version) throw new VersionNotFound(versionId);
    const row = await this.orm
      .select()
      .from(docVersions)
      .where(eq(docVersions.id, versionId))
      .limit(1);
    if (row[0]?.documentId !== documentId) throw new VersionNotFound(versionId);
    return blocksOf(documentId, version.content);
  }

  async applySessionEdits(input: {
    sessionId: string;
    documentIds: string[];
    ops: EditOp[];
    actor: Actor;
    summary?: string;
  }): Promise<ApplyOutcome> {
    const target = editBatchTarget(input.documentIds);
    if (!target.ok) {
      return {
        ok: false,
        errors: [{ index: 0, code: "out_of_scope", message: target.message }],
      };
    }
    const session = await this.getSession(input.sessionId);
    if (!session) throw new SessionNotFound(input.sessionId);
    if (session.status !== "open") throw new SessionClosed(input.sessionId);
    if (session.documentId !== target.documentId) {
      return {
        ok: false,
        errors: [{ index: 0, code: "out_of_scope", message: "会话只能修订自己所属的文档" }],
      };
    }
    return this.applyManual({
      documentId: target.documentId,
      ops: input.ops,
      actor: input.actor,
      summary: input.summary ?? "会话修订",
      sessionId: input.sessionId,
    });
  }

  private async versionById(id: string): Promise<VersionView | null> {
    const rows = await this.orm.select().from(docVersions).where(eq(docVersions.id, id)).limit(1);
    const row = rows[0];
    return row ? toVersion(row) : null;
  }

  private async sessionInTx(tx: Tx, id: string) {
    const rows = await tx.select().from(sessions).where(eq(sessions.id, id)).limit(1);
    return rows[0];
  }

  private async advanceInTx(tx: Tx, sessionId: string, versionId: string) {
    const session = await this.sessionInTx(tx, sessionId);
    if (!session) throw new SessionNotFound(sessionId);
    if (session.status !== "open") throw new SessionClosed(sessionId);
    const version = await this.versionInTx(tx, versionId);
    if (!version || version.documentId !== session.documentId) throw new VersionNotFound(versionId);
    await tx
      .update(sessions)
      .set({ baseVersionId: versionId, updatedAt: new Date() })
      .where(eq(sessions.id, sessionId));
  }

  private async versionInTx(tx: Tx, id: string) {
    const rows = await tx.select().from(docVersions).where(eq(docVersions.id, id)).limit(1);
    return rows[0];
  }

  private async turnByKey(documentId: string, key: string): Promise<TurnView | undefined> {
    const rows = await this.orm
      .select()
      .from(turns)
      .where(and(eq(turns.documentId, documentId), eq(turns.idempotencyKey, key)))
      .limit(1);
    const row = rows[0];
    return row ? toTurn(row) : undefined;
  }

  private async lock(tx: Tx, documentId: string) {
    const rows = await tx
      .select()
      .from(documents)
      .where(eq(documents.id, documentId))
      .for("update")
      .limit(1);
    if (!rows[0]) throw new DocumentNotFound(documentId);
    return rows[0];
  }

  private async headInTx(tx: Tx, documentId: string) {
    const doc = await this.lock(tx, documentId);
    if (!doc.headVersionId) throw new DocumentNotFound(documentId);
    const version = await this.versionInTx(tx, doc.headVersionId);
    if (!version) throw new DocumentNotFound(documentId);
    return version;
  }

  private async writeOps(
    tx: Tx,
    input: {
      documentId: string;
      ops: EditOp[];
      origin: Origin;
      actor: Actor;
      summary: string;
      scope?: BlockId[];
      turnId?: string | null;
      piTaskId?: string | null;
    },
  ): Promise<ApplyOutcome> {
    const head = await this.headInTx(tx, input.documentId);
    const prepared = prepareOps(asDoc(head.content), input.ops, input.scope);
    if (!prepared.ok) return prepared;
    return this.insertSnapshot(tx, {
      documentId: input.documentId,
      content: prepared.doc,
      ops: prepared.ops,
      origin: input.origin,
      actor: input.actor,
      summary: input.summary,
      turnId: input.turnId ?? null,
      piTaskId: input.piTaskId ?? null,
    });
  }

  private async insertSnapshot(
    tx: Tx,
    input: {
      documentId: string;
      content: PMJSON;
      ops: EditOp[];
      origin: Origin;
      actor: Actor;
      summary: string;
      turnId: string | null;
      piTaskId: string | null;
    },
  ): Promise<ApplyOutcome> {
    const doc = await this.lock(tx, input.documentId);
    const latest = await tx
      .select()
      .from(docVersions)
      .where(eq(docVersions.documentId, input.documentId))
      .orderBy(desc(docVersions.seq))
      .limit(1);
    const seq = (latest[0]?.seq ?? 0) + 1;
    const versionId = newId("ver");
    const actor = actorParts(input.actor);
    await tx.insert(docVersions).values({
      id: versionId,
      documentId: input.documentId,
      seq,
      parentId: doc.headVersionId,
      origin: input.origin,
      actorType: actor.actorType,
      actorId: actor.actorId,
      turnId: input.turnId,
      piTaskId: input.piTaskId,
      content: input.content,
      ops: input.ops,
      summary: input.summary,
      createdAt: new Date(),
    });
    await tx
      .update(documents)
      .set({ headVersionId: versionId })
      .where(eq(documents.id, input.documentId));
    if (input.actor.type === "user" && input.origin === "manual") {
      await tx
        .update(documents)
        .set({ humanTouched: true })
        .where(eq(documents.id, input.documentId));
    }
    await tx.delete(docBlocks).where(eq(docBlocks.documentId, input.documentId));
    const projected = listBlocks(input.content).map((node, index) => {
      const blockId = blockIdOf(node);
      if (!blockId) throw new Error("块缺少 id");
      return {
        documentId: input.documentId,
        blockId,
        type: node.type,
        contentHash: blockHashSync(node),
        text: blockText(node),
        orderKey: String(index).padStart(6, "0"),
        searchText: blockText(node),
      };
    });
    if (projected.length > 0) await tx.insert(docBlocks).values(projected);
    return { ok: true, versionId, seq };
  }

  private async appendEventTx(
    tx: Tx,
    documentId: string,
    event: { turnId?: string | null; type: string; payload: unknown },
  ): Promise<RunEvent> {
    const latest = await tx
      .select()
      .from(runEvents)
      .where(eq(runEvents.documentId, documentId))
      .orderBy(desc(runEvents.seq))
      .limit(1);
    const seq = (latest[0]?.seq ?? 0) + 1;
    await tx.insert(runEvents).values({
      id: newId("evt"),
      documentId,
      seq,
      turnId: event.turnId ?? null,
      type: event.type,
      payload: event.payload ?? {},
      createdAt: new Date(),
    });
    return {
      seq,
      documentId,
      ...(event.turnId ? { turnId: event.turnId } : {}),
      type: event.type,
      payload: event.payload,
    };
  }
}

type Tx = Parameters<Parameters<Orm["transaction"]>[0]>[0];

function toVersion(row: typeof docVersions.$inferSelect): VersionView {
  return {
    id: row.id,
    seq: row.seq,
    parentId: row.parentId,
    origin: row.origin,
    summary: row.summary,
    content: asDoc(row.content),
    turnId: row.turnId,
  };
}

function toSession(row: typeof sessions.$inferSelect): SessionView {
  return {
    id: row.id,
    documentId: row.documentId,
    kind: row.kind as SessionKind,
    baseVersionId: row.baseVersionId,
    piConversationId: row.piConversationId,
    createdByType: row.createdByType,
    createdById: row.createdById,
    status: row.status === "closed" ? "closed" : "open",
    createdAt: stamp(row.createdAt),
    updatedAt: stamp(row.updatedAt),
  };
}

function stamp(value: Date | string): string {
  return value instanceof Date ? value.toISOString() : new Date(value).toISOString();
}

function parseDossier(value: unknown): DossierContent {
  const raw = typeof value === "string" ? (JSON.parse(value) as unknown) : value;
  const parsed = DossierContentSchema.safeParse(raw);
  return parsed.success ? parsed.data : emptyDossier();
}

function blocksOf(_documentId: string, content: PMJSON): BlockView[] {
  return listBlocks(content).map((node, index) => {
    const blockId = blockIdOf(node);
    if (!blockId) throw new Error("块缺少 id");
    return {
      blockId,
      type: node.type,
      contentHash: blockHashSync(node),
      text: blockText(node),
      orderKey: String(index).padStart(6, "0"),
    };
  });
}

function toTurn(row: typeof turns.$inferSelect): TurnView {
  return {
    id: row.id,
    documentId: row.documentId,
    sessionId: row.sessionId,
    status: row.status as TurnStatus,
    resultVersionId: row.resultVersionId,
    error: row.error,
    piSubmissionId: row.piSubmissionId,
    idempotencyKey: row.idempotencyKey,
  };
}

function prepareOps(
  doc: PMJSON,
  ops: EditOp[],
  scope: BlockId[] | undefined,
): { ok: true; doc: PMJSON; ops: EditOp[] } | { ok: false; errors: OpError[] } {
  const rebased: EditOp[] = [];
  for (let index = 0; index < ops.length; index += 1) {
    const op = ops[index];
    if (!op) continue;
    if (op.op === "insert_block") {
      rebased.push(op);
      continue;
    }
    const found = findBlock(doc, op.blockId);
    if (!found) {
      return {
        ok: false,
        errors: [{ index, code: "block_not_found", message: `找不到块 ${op.blockId}` }],
      };
    }
    if (blockHashSync(found.node) === op.contentHash) {
      rebased.push(op);
      continue;
    }
    const next = rebaseOp(op, found.node);
    if (!next) {
      return {
        ok: false,
        errors: [
          {
            index,
            code: "hash_mismatch",
            message: `块 ${op.blockId} 已变化，无法把旧锚点接到当前文本`,
          },
        ],
      };
    }
    rebased.push(next);
  }
  const applied = applyOps(doc, rebased, scope ? { scope } : undefined);
  if (!applied.ok) return applied;
  return { ok: true, doc: applied.doc, ops: rebased };
}

export async function openMemoryDatabase(): Promise<LwDatabase> {
  const client = new PGlite();
  await client.exec(migrationSql);
  const orm = drizzle({ client, schema });
  return new LwDatabase(orm, {
    exec: (sqlText) => client.exec(sqlText).then(() => undefined),
    close: () => client.close(),
  });
}

export async function openPostgres(connectionString: string): Promise<LwDatabase> {
  const pool = new pg.Pool({ connectionString });
  await pool.query(migrationSql);
  const orm = drizzlePg({ client: pool, schema });
  return new LwDatabase(orm as unknown as Orm, {
    exec: async (sqlText) => {
      await pool.query(sqlText);
    },
    close: () => pool.end().then(() => undefined),
  });
}

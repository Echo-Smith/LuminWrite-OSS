import { mkdir } from "node:fs/promises";
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
  type BlockDiff,
  blockHashSync,
  blockIdOf,
  blockText,
  diffBlocks,
  emptyDoc,
  findBlock,
  listBlocks,
  type PMJSON,
  rebaseOp,
  remapBlockIds,
} from "@luminwrite/doc-model";
import type { UsageEvent } from "@luminwrite/edition";
import { and, desc, eq, gte, isNotNull, isNull, lte, type SQL, sql } from "drizzle-orm";
import { drizzle as drizzlePg } from "drizzle-orm/node-postgres";
import { drizzle, type PgliteDatabase } from "drizzle-orm/pglite";
import pg from "pg";

import { type DatabaseLocation, pgliteDataDir } from "./config.js";
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
  apiKeys,
  apiKeyPolicies,
  providerCredentials,
  modelRegistry,
  modelSlotBindings,
  sessions,
  sessionReads,
  documentDossier,
  users,
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
  ops: EditOp[];
  turnId: string | null;
  actorType: string;
  actorId: string;
  apiKeyId: string | null;
  createdAt: string;
}

export interface VersionDetail extends VersionView {
  documentId: string;
}

export interface OwnerRow {
  id: string;
  displayName: string;
  tokenHash: string;
}

export interface ApiKeyRecord {
  id: string;
  userId: string;
  name: string;
  prefix: string;
  hash: string;
  scopes: string[];
  createdAt: Date;
  lastUsedAt: Date | null;
  revokedAt: Date | null;
  maxConcurrentTasks: number | null;
  dailyCostCapUsd: number | null;
  allowedTiers: string[] | null;
  defaultApplyMode: "auto" | "proposal";
  webhookSecret: string | null;
}

export interface CredentialRow {
  id: string;
  userId: string;
  provider: string;
  label: string;
  baseUrl: string | null;
  ciphertext: Buffer;
  nonce: Buffer;
  keyVersion: number;
  fingerprint: string;
  status: "active" | "disabled" | "invalid";
  lastVerifiedAt: Date | null;
  lastErrorCode: string | null;
  createdAt: Date;
  updatedAt: Date;
}

export interface RegistryEntryRow {
  id: string;
  ownerId: string;
  ref: string;
  kind: "model" | "tier";
  spec: Record<string, unknown>;
}

export interface NewCredentialRow {
  id: string;
  userId: string;
  provider: string;
  label: string;
  baseUrl: string | null;
  ciphertext: Buffer;
  nonce: Buffer;
  keyVersion: number;
  fingerprint: string;
  status: "active" | "disabled" | "invalid";
}

export interface NewApiKeyRow {
  id: string;
  userId: string;
  name: string;
  prefix: string;
  hash: string;
  scopes: string[];
  maxConcurrentTasks: number | null;
  dailyCostCapUsd: number | null;
  allowedTiers: string[] | null;
  defaultApplyMode: "auto" | "proposal";
  webhookSecret: string | null;
}

export interface DocumentView {
  id: string;
  ownerId: string;
  title: string;
  status: string;
  humanTouched: boolean;
  headVersionId: string | null;
  head: VersionView | null;
  savedAt: string | null;
  discardedAt: string | null;
  createdVia: string;
  createdAt: string;
}

export interface DocumentListItem {
  id: string;
  title: string;
  status: string;
  savedAt: string;
  createdVia: string;
  humanTouched: boolean;
  headVersionId: string | null;
  createdAt: string;
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
  modelRef: string | null;
  tierRef: string | null;
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
  modelPin: string | null;
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

/** 同一 actor 的连续人工修订合并成一个版本的窗口。 */
export const AUTOSAVE_WINDOW_MS = 30_000;
export const DEFAULT_DISCARD_RETENTION_DAYS = 30;
export const DEFAULT_DISCARD_PURGE_INTERVAL_MS = 60 * 60 * 1000;

const SAVED_ON_CREATE = new Set(["api", "import", "derive"]);

/**
 * 知识库和检索以后只用这个判断。未保存、已丢弃的文档都不进。
 * 列表用同样的条件（saved_at 非空且 discarded_at 为空）。
 */
export function documentVisibleToKb(doc: {
  savedAt: Date | string | null;
  discardedAt: Date | string | null;
}): boolean {
  return doc.savedAt != null && doc.discardedAt == null;
}

export function discardRetentionDays(env: NodeJS.ProcessEnv = {}): number {
  return readNonNegative(env.LW_DISCARD_RETENTION_DAYS, DEFAULT_DISCARD_RETENTION_DAYS);
}

export function discardPurgeIntervalMs(env: NodeJS.ProcessEnv = {}): number {
  return readNonNegative(env.LW_DISCARD_PURGE_INTERVAL_MS, DEFAULT_DISCARD_PURGE_INTERVAL_MS);
}

function readNonNegative(raw: string | undefined, fallback: number): number {
  if (raw == null || raw.trim() === "") return fallback;
  const parsed = Number(raw);
  if (!Number.isFinite(parsed) || parsed < 0) return fallback;
  return parsed;
}

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

function apiKeyIdOf(actor: Actor): string | null {
  return actor.type === "api_client" ? actor.keyId : null;
}

function asDoc(value: unknown): PMJSON {
  if (typeof value === "string") return JSON.parse(value) as PMJSON;
  return value as PMJSON;
}

export class LwDatabase {
  private readonly orm: Orm;
  private readonly client: Closer;
  private readonly clock: () => Date;

  constructor(orm: Orm, client: Closer, clock: () => Date = () => new Date()) {
    this.orm = orm;
    this.client = client;
    this.clock = clock;
  }

  exec(sqlText: string): Promise<void> {
    return this.client.exec(sqlText);
  }

  close(): Promise<void> {
    return this.client.close();
  }

  async getOwner(): Promise<OwnerRow | undefined> {
    const rows = await this.orm.select().from(users).limit(1);
    const row = rows[0];
    if (!row) return undefined;
    return { id: row.id, displayName: row.displayName, tokenHash: row.tokenHash };
  }

  async insertOwner(input: {
    id: string;
    displayName: string;
    tokenHash: string;
  }): Promise<"inserted" | "exists"> {
    try {
      await this.orm.insert(users).values({
        id: input.id,
        displayName: input.displayName,
        tokenHash: input.tokenHash,
        singleton: true,
        createdAt: new Date(),
      });
      return "inserted";
    } catch (error) {
      if (!isUniqueViolation(error)) throw error;
      const existing = await this.getOwner();
      if (!existing) throw error;
      return "exists";
    }
  }

  async updateOwnerTokenHash(id: string, tokenHash: string): Promise<void> {
    await this.orm.update(users).set({ tokenHash }).where(eq(users.id, id));
  }

  async deleteOwner(id: string): Promise<void> {
    await this.orm.delete(users).where(eq(users.id, id));
  }

  async createDocument(input: {
    title: string;
    ownerId: string;
    genre?: string;
    createdVia?: string;
    actor?: Actor;
  }): Promise<{ id: string; versionId: string; sessionId: string }> {
    const id = newId("doc");
    const versionId = newId("ver");
    const sessionId = newId("ses");
    const now = this.clock();
    const via = input.createdVia ?? "ui";
    const actor = input.actor ?? { type: "user" as const, userId: input.ownerId };
    const parts = actorParts(actor);
    const savedAt = SAVED_ON_CREATE.has(via) || actor.type === "api_client" ? now : null;
    await this.orm.transaction(async (tx) => {
      await tx.insert(documents).values({
        id,
        ownerId: input.ownerId,
        title: input.title,
        genre: input.genre ?? "",
        status: "active",
        headVersionId: versionId,
        styleRef: "pkg:luminwrite/plain-professional@1.0.0",
        tierRef: "balanced",
        brief: {},
        authoredByMe: false,
        createdVia: via,
        humanTouched: false,
        derivedFromDocumentId: null,
        derivedFromVersionId: null,
        savedAt,
        savedVersionId: savedAt ? versionId : null,
        discardedAt: null,
        createdAt: now,
      });
      await tx.insert(docVersions).values({
        id: versionId,
        documentId: id,
        seq: 1,
        parentId: null,
        origin: "import",
        actorType: parts.actorType,
        actorId: parts.actorId,
        apiKeyId: apiKeyIdOf(actor),
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
        createdByType: parts.actorType,
        createdById: parts.actorId,
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
      status: row.status,
      humanTouched: row.humanTouched,
      headVersionId: row.headVersionId,
      head,
      savedAt: stampOrNull(row.savedAt),
      discardedAt: stampOrNull(row.discardedAt),
      createdVia: row.createdVia,
      createdAt: stamp(row.createdAt),
    };
  }

  async listDocuments(ownerId: string): Promise<DocumentListItem[]> {
    const rows = await this.orm
      .select()
      .from(documents)
      .where(
        and(
          eq(documents.ownerId, ownerId),
          isNotNull(documents.savedAt),
          isNull(documents.discardedAt),
        ),
      )
      .orderBy(desc(documents.createdAt), desc(documents.id));
    return rows.map((row) => ({
      id: row.id,
      title: row.title,
      status: row.status,
      savedAt: stamp(row.savedAt ?? row.createdAt),
      createdVia: row.createdVia,
      humanTouched: row.humanTouched,
      headVersionId: row.headVersionId,
      createdAt: stamp(row.createdAt),
    }));
  }

  async saveDocument(id: string): Promise<{ savedAt: string; already: boolean }> {
    return this.orm.transaction(async (tx) => {
      const doc = await this.lock(tx, id);
      if (doc.savedAt) return { savedAt: stamp(doc.savedAt), already: true };
      const now = this.clock();
      await tx
        .update(documents)
        .set({ savedAt: now, savedVersionId: doc.headVersionId, status: "active" })
        .where(eq(documents.id, id));
      return { savedAt: now.toISOString(), already: false };
    });
  }

  async discardDocument(id: string): Promise<{ discardedAt: string }> {
    return this.orm.transaction(async (tx) => {
      const doc = await this.lock(tx, id);
      if (doc.discardedAt) return { discardedAt: stamp(doc.discardedAt) };
      const now = this.clock();
      await tx.update(documents).set({ discardedAt: now }).where(eq(documents.id, id));
      return { discardedAt: now.toISOString() };
    });
  }

  async undoDiscard(id: string): Promise<{ discardedAt: null }> {
    return this.orm.transaction(async (tx) => {
      await this.lock(tx, id);
      await tx.update(documents).set({ discardedAt: null }).where(eq(documents.id, id));
      return { discardedAt: null };
    });
  }

  async getVersion(id: string): Promise<VersionDetail | undefined> {
    const rows = await this.orm.select().from(docVersions).where(eq(docVersions.id, id)).limit(1);
    const row = rows[0];
    if (!row) return undefined;
    return { ...toVersion(row), documentId: row.documentId };
  }

  async diffVersions(documentId: string, fromId: string, toId: string): Promise<BlockDiff[]> {
    const from = await this.versionRow(fromId);
    const to = await this.versionRow(toId);
    if (!from || from.documentId !== documentId) throw new VersionNotFound(fromId);
    if (!to || to.documentId !== documentId) throw new VersionNotFound(toId);
    return diffBlocks(asDoc(from.content), asDoc(to.content));
  }

  /**
   * 硬删已过保留期的丢弃文档。只在这个事务里把 `lw.version_write` 设成 `purge`，
   * 触发器才放行版本删除。普通 DELETE / UPDATE 仍然失败。
   */
  async purgeDiscarded(input?: {
    retentionDays?: number;
    now?: Date;
  }): Promise<{ purged: string[] }> {
    const retentionDays = input?.retentionDays ?? DEFAULT_DISCARD_RETENTION_DAYS;
    if (!Number.isFinite(retentionDays) || retentionDays < 0) {
      throw new Error("保留天数不合法");
    }
    const now = input?.now ?? this.clock();
    const cutoff = new Date(now.getTime() - retentionDays * 24 * 60 * 60 * 1000);
    return this.orm.transaction(async (tx) => {
      const rows = await tx
        .select({ id: documents.id })
        .from(documents)
        .where(and(isNotNull(documents.discardedAt), lte(documents.discardedAt, cutoff)));
      const ids = rows.map((row) => row.id);
      if (ids.length === 0) return { purged: [] };
      await tx.execute(sql`select set_config('lw.version_write', 'purge', true)`);
      for (const id of ids) await this.purgeOne(tx, id);
      return { purged: ids };
    });
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
      modelRef?: string;
      tierRef?: string | null;
    },
  ): Promise<void> {
    const update: Partial<typeof turns.$inferInsert> = {};
    if (patch.status) update.status = patch.status;
    if (patch.piConversationId) update.piConversationId = patch.piConversationId;
    if (patch.piSubmissionId) update.piSubmissionId = patch.piSubmissionId;
    if (patch.resultVersionId) update.resultVersionId = patch.resultVersionId;
    if (patch.error) update.error = patch.error;
    if (patch.modelRef) update.modelRef = patch.modelRef;
    if (patch.tierRef !== undefined) update.tierRef = patch.tierRef;
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

  async recordUsage(
    event: UsageEvent & {
      apiKeyId?: string;
      apiTaskId?: string;
      userId?: string;
      credentialId?: string;
      modelRef?: string;
      tierRef?: string;
      sessionId?: string;
      turnId?: string;
      cacheReadTokens?: number;
      cacheWriteTokens?: number;
      priceKnown?: boolean;
    },
  ): Promise<void> {
    await this.orm.insert(usageLedger).values({
      id: newId("use"),
      requestId: event.requestId,
      model: event.model,
      inputTokens: event.inputTokens,
      outputTokens: event.outputTokens,
      costMicros: event.costMicros,
      apiKeyId: event.apiKeyId ?? null,
      apiTaskId: event.apiTaskId ?? null,
      userId: event.userId ?? null,
      credentialId: event.credentialId ?? null,
      modelRef: event.modelRef ?? null,
      tierRef: event.tierRef ?? null,
      sessionId: event.sessionId ?? null,
      turnId: event.turnId ?? null,
      cacheReadTokens: event.cacheReadTokens ?? null,
      cacheWriteTokens: event.cacheWriteTokens ?? null,
      priceKnown: event.priceKnown ?? true,
      createdAt: new Date(),
    });
  }

  async usageCostMicrosForUser(userId: string, since: Date): Promise<number> {
    const rows = await this.orm
      .select({
        total: sql<string | number>`coalesce(sum(${usageLedger.costMicros}), 0)`,
      })
      .from(usageLedger)
      .where(and(eq(usageLedger.userId, userId), gte(usageLedger.createdAt, since)));
    return Number(rows[0]?.total ?? 0);
  }

  async usageForTurn(turnId: string): Promise<
    Array<{
      userId: string | null;
      apiKeyId: string | null;
      credentialId: string | null;
      modelRef: string | null;
      tierRef: string | null;
      sessionId: string | null;
      costMicros: number;
      priceKnown: boolean;
      model: string;
    }>
  > {
    const rows = await this.orm.select().from(usageLedger).where(eq(usageLedger.turnId, turnId));
    return rows.map((row) => ({
      userId: row.userId,
      apiKeyId: row.apiKeyId,
      credentialId: row.credentialId,
      modelRef: row.modelRef,
      tierRef: row.tierRef,
      sessionId: row.sessionId,
      costMicros: row.costMicros,
      priceKnown: row.priceKnown,
      model: row.model,
    }));
  }

  async usageCostMicrosSince(apiKeyId: string, since: Date): Promise<number> {
    const rows = await this.orm
      .select({
        total: sql<string | number>`coalesce(sum(${usageLedger.costMicros}), 0)`,
      })
      .from(usageLedger)
      .where(and(eq(usageLedger.apiKeyId, apiKeyId), gte(usageLedger.createdAt, since)));
    return Number(rows[0]?.total ?? 0);
  }

  async insertApiKey(input: NewApiKeyRow): Promise<void> {
    const now = new Date();
    await this.orm.transaction(async (tx) => {
      await tx.insert(apiKeys).values({
        id: input.id,
        userId: input.userId,
        name: input.name,
        prefix: input.prefix,
        hash: input.hash,
        scopes: input.scopes,
        createdAt: now,
        lastUsedAt: null,
        revokedAt: null,
      });
      await tx.insert(apiKeyPolicies).values({
        apiKeyId: input.id,
        maxConcurrentTasks: input.maxConcurrentTasks,
        dailyCostCapUsd: input.dailyCostCapUsd,
        allowedTiers: input.allowedTiers,
        defaultApplyMode: input.defaultApplyMode,
        webhookSecret: input.webhookSecret,
      });
    });
  }

  async findApiKeyByHash(hash: string): Promise<ApiKeyRecord | undefined> {
    const rows = await this.selectApiKeys(eq(apiKeys.hash, hash));
    return rows[0];
  }

  async getApiKey(id: string): Promise<ApiKeyRecord | undefined> {
    const rows = await this.selectApiKeys(eq(apiKeys.id, id));
    return rows[0];
  }

  async listApiKeys(userId: string): Promise<ApiKeyRecord[]> {
    return this.selectApiKeys(eq(apiKeys.userId, userId));
  }

  async touchApiKey(id: string, at = new Date()): Promise<void> {
    await this.orm.update(apiKeys).set({ lastUsedAt: at }).where(eq(apiKeys.id, id));
  }

  async revokeApiKey(id: string, userId: string): Promise<"revoked" | "missing"> {
    const rows = await this.orm
      .select({ id: apiKeys.id, revokedAt: apiKeys.revokedAt })
      .from(apiKeys)
      .where(and(eq(apiKeys.id, id), eq(apiKeys.userId, userId)))
      .limit(1);
    const row = rows[0];
    if (!row) return "missing";
    if (!row.revokedAt) {
      await this.orm.update(apiKeys).set({ revokedAt: new Date() }).where(eq(apiKeys.id, id));
    }
    return "revoked";
  }

  async insertCredential(input: NewCredentialRow): Promise<void> {
    const now = new Date();
    await this.orm.insert(providerCredentials).values({
      id: input.id,
      userId: input.userId,
      provider: input.provider,
      label: input.label,
      baseUrl: input.baseUrl,
      ciphertext: input.ciphertext,
      nonce: input.nonce,
      keyVersion: input.keyVersion,
      fingerprint: input.fingerprint,
      status: input.status,
      lastVerifiedAt: null,
      lastErrorCode: null,
      createdAt: now,
      updatedAt: now,
    });
  }

  async getCredential(id: string): Promise<CredentialRow | undefined> {
    const rows = await this.orm
      .select()
      .from(providerCredentials)
      .where(eq(providerCredentials.id, id))
      .limit(1);
    const row = rows[0];
    return row ? toCredential(row) : undefined;
  }

  async listCredentials(userId: string): Promise<CredentialRow[]> {
    const rows = await this.orm
      .select()
      .from(providerCredentials)
      .where(eq(providerCredentials.userId, userId))
      .orderBy(desc(providerCredentials.createdAt));
    return rows.map(toCredential);
  }

  async listCredentialRows(): Promise<CredentialRow[]> {
    const rows = await this.orm.select().from(providerCredentials);
    return rows.map(toCredential);
  }

  async updateCredentialSeal(
    id: string,
    seal: { ciphertext: Buffer; nonce: Buffer; keyVersion: number; fingerprint: string },
  ): Promise<void> {
    await this.orm
      .update(providerCredentials)
      .set({
        ciphertext: seal.ciphertext,
        nonce: seal.nonce,
        keyVersion: seal.keyVersion,
        fingerprint: seal.fingerprint,
        updatedAt: new Date(),
      })
      .where(eq(providerCredentials.id, id));
  }

  async updateCredentialMeta(
    id: string,
    patch: {
      label?: string;
      baseUrl?: string | null;
      status?: "active" | "disabled" | "invalid";
      lastVerifiedAt?: Date | null;
      lastErrorCode?: string | null;
    },
  ): Promise<void> {
    await this.orm
      .update(providerCredentials)
      .set({ ...patch, updatedAt: new Date() })
      .where(eq(providerCredentials.id, id));
  }

  async deleteCredential(id: string, userId: string): Promise<"deleted" | "missing"> {
    const rows = await this.orm
      .delete(providerCredentials)
      .where(and(eq(providerCredentials.id, id), eq(providerCredentials.userId, userId)))
      .returning({ id: providerCredentials.id });
    return rows.length > 0 ? "deleted" : "missing";
  }

  async setSessionModelPin(sessionId: string, modelPin: string | null): Promise<void> {
    await this.orm
      .update(sessions)
      .set({ modelPin, updatedAt: new Date() })
      .where(eq(sessions.id, sessionId));
  }

  async listRegistryEntries(ownerId: string): Promise<RegistryEntryRow[]> {
    const rows = await this.orm
      .select()
      .from(modelRegistry)
      .where(eq(modelRegistry.ownerId, ownerId));
    return rows.map((row) => ({
      id: row.id,
      ownerId: row.ownerId,
      ref: row.ref,
      kind: row.kind === "tier" ? "tier" : "model",
      spec: row.spec,
    }));
  }

  async upsertRegistryEntry(input: {
    ownerId: string;
    ref: string;
    kind: "model" | "tier";
    spec: Record<string, unknown>;
  }): Promise<void> {
    const existing = await this.orm
      .select({ id: modelRegistry.id })
      .from(modelRegistry)
      .where(and(eq(modelRegistry.ownerId, input.ownerId), eq(modelRegistry.ref, input.ref)))
      .limit(1);
    const now = new Date();
    const current = existing[0];
    if (current) {
      await this.orm
        .update(modelRegistry)
        .set({ kind: input.kind, spec: input.spec, updatedAt: now })
        .where(eq(modelRegistry.id, current.id));
      return;
    }
    await this.orm.insert(modelRegistry).values({
      id: newId("reg"),
      ownerId: input.ownerId,
      ref: input.ref,
      kind: input.kind,
      spec: input.spec,
      createdAt: now,
      updatedAt: now,
    });
  }

  async listSlotBindings(ownerId: string): Promise<Array<{ slot: string; targetRef: string }>> {
    const rows = await this.orm
      .select()
      .from(modelSlotBindings)
      .where(eq(modelSlotBindings.ownerId, ownerId));
    return rows.map((row) => ({ slot: row.slot, targetRef: row.targetRef }));
  }

  async upsertSlotBinding(ownerId: string, slot: string, targetRef: string): Promise<void> {
    const existing = await this.orm
      .select({ slot: modelSlotBindings.slot })
      .from(modelSlotBindings)
      .where(and(eq(modelSlotBindings.ownerId, ownerId), eq(modelSlotBindings.slot, slot)))
      .limit(1);
    if (existing[0]) {
      await this.orm
        .update(modelSlotBindings)
        .set({ targetRef, updatedAt: new Date() })
        .where(and(eq(modelSlotBindings.ownerId, ownerId), eq(modelSlotBindings.slot, slot)));
      return;
    }
    await this.orm.insert(modelSlotBindings).values({
      ownerId,
      slot,
      targetRef,
      updatedAt: new Date(),
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

  /** 祖先和全部派生后代。不含自己。 */
  async relatedDocumentIds(documentId: string): Promise<string[]> {
    const related: string[] = [];
    const seen = new Set<string>([documentId]);
    let cursor: string | null = documentId;
    while (cursor) {
      const rows = await this.orm
        .select({ parentId: documents.derivedFromDocumentId })
        .from(documents)
        .where(eq(documents.id, cursor))
        .limit(1);
      const parentId: string | null = rows[0]?.parentId ?? null;
      if (!parentId || seen.has(parentId)) break;
      seen.add(parentId);
      related.push(parentId);
      cursor = parentId;
    }
    const queue = [documentId];
    while (queue.length > 0) {
      const current = queue.shift();
      if (!current) break;
      const children = await this.orm
        .select({ id: documents.id })
        .from(documents)
        .where(eq(documents.derivedFromDocumentId, current));
      for (const child of children) {
        if (seen.has(child.id)) continue;
        seen.add(child.id);
        related.push(child.id);
        queue.push(child.id);
      }
    }
    return related;
  }

  async readRelatedDossier(sessionId: string, documentId: string): Promise<DossierView> {
    const session = await this.getSession(sessionId);
    if (!session) throw new SessionNotFound(sessionId);
    if (session.status !== "open") throw new SessionClosed(sessionId);
    const granted = await this.orm
      .select({ documentId: sessionReads.documentId })
      .from(sessionReads)
      .where(and(eq(sessionReads.sessionId, sessionId), eq(sessionReads.documentId, documentId)))
      .limit(1);
    const allowed =
      Boolean(granted[0]) ||
      (await this.relatedDocumentIds(session.documentId)).includes(documentId);
    if (!allowed) throw new SessionScopeError("这个会话不能读这篇文档的档案");
    return this.readDossier(documentId);
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
      const now = this.clock();
      const actor = actorParts(input.actor);
      await tx.insert(documents).values({
        id,
        ownerId: input.ownerId,
        title: input.title ?? `${parent.title}的派生`,
        genre: parent.genre,
        status: "active",
        headVersionId: null,
        styleRef: parent.styleRef,
        tierRef: parent.tierRef,
        brief: {},
        authoredByMe: false,
        createdVia: "derive",
        humanTouched: false,
        derivedFromDocumentId: source.documentId,
        derivedFromVersionId: source.id,
        savedAt: now,
        savedVersionId: null,
        discardedAt: null,
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
      await tx
        .update(documents)
        .set({ savedVersionId: written.versionId })
        .where(eq(documents.id, id));
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
      .where(
        and(
          eq(documents.derivedFromDocumentId, documentId),
          isNotNull(documents.savedAt),
          isNull(documents.discardedAt),
        ),
      )
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
    const row = await this.versionRow(id);
    return row ? toVersion(row) : null;
  }

  private async versionRow(id: string) {
    const rows = await this.orm.select().from(docVersions).where(eq(docVersions.id, id)).limit(1);
    return rows[0];
  }

  private async purgeOne(tx: Tx, id: string) {
    const ses = await tx
      .select({ id: sessions.id })
      .from(sessions)
      .where(eq(sessions.documentId, id));
    for (const session of ses) {
      await tx.delete(sessionReads).where(eq(sessionReads.sessionId, session.id));
    }
    await tx.delete(sessionReads).where(eq(sessionReads.documentId, id));
    await tx.delete(sessions).where(eq(sessions.documentId, id));
    await tx.delete(docVersions).where(eq(docVersions.documentId, id));
    const props = await tx
      .select({ id: proposals.id })
      .from(proposals)
      .where(eq(proposals.documentId, id));
    for (const proposal of props) {
      await tx.delete(proposalHunks).where(eq(proposalHunks.proposalId, proposal.id));
    }
    await tx.delete(proposals).where(eq(proposals.documentId, id));
    await tx.delete(docBlocks).where(eq(docBlocks.documentId, id));
    await tx.delete(runEvents).where(eq(runEvents.documentId, id));
    await tx.delete(documents).where(eq(documents.id, id));
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
    const coalesced = await this.coalesceManual(tx, doc, input);
    if (coalesced) return coalesced;
    const latest = await tx
      .select()
      .from(docVersions)
      .where(eq(docVersions.documentId, input.documentId))
      .orderBy(desc(docVersions.seq))
      .limit(1);
    const seq = (latest[0]?.seq ?? 0) + 1;
    const versionId = newId("ver");
    const actor = actorParts(input.actor);
    const now = this.clock();
    await tx.insert(docVersions).values({
      id: versionId,
      documentId: input.documentId,
      seq,
      parentId: doc.headVersionId,
      origin: input.origin,
      actorType: actor.actorType,
      actorId: actor.actorId,
      apiKeyId: apiKeyIdOf(input.actor),
      turnId: input.turnId,
      piTaskId: input.piTaskId,
      content: input.content,
      ops: input.ops,
      summary: input.summary,
      createdAt: now,
    });
    await tx
      .update(documents)
      .set({ headVersionId: versionId })
      .where(eq(documents.id, input.documentId));
    await this.touchHuman(tx, input.documentId, input.actor, input.origin);
    await this.replaceBlocks(tx, input.documentId, input.content);
    return { ok: true, versionId, seq };
  }

  private async coalesceManual(
    tx: Tx,
    doc: typeof documents.$inferSelect,
    input: {
      content: PMJSON;
      ops: EditOp[];
      origin: Origin;
      actor: Actor;
      summary: string;
    },
  ): Promise<ApplyOutcome | undefined> {
    if (input.origin !== "manual" || !doc.savedAt || !doc.headVersionId) return undefined;
    if (doc.headVersionId === doc.savedVersionId) return undefined;
    const head = await this.versionInTx(tx, doc.headVersionId);
    if (!head || head.origin !== "manual") return undefined;
    const actor = actorParts(input.actor);
    if (head.actorType !== actor.actorType || head.actorId !== actor.actorId) return undefined;
    if ((head.apiKeyId ?? null) !== apiKeyIdOf(input.actor)) return undefined;
    const created = asDate(head.createdAt);
    if (!created) return undefined;
    const elapsed = this.clock().getTime() - created.getTime();
    if (elapsed < 0 || elapsed >= AUTOSAVE_WINDOW_MS) return undefined;
    await tx.execute(sql`select set_config('lw.version_write', 'coalesce', true)`);
    await tx
      .update(docVersions)
      .set({
        content: input.content,
        ops: [...asOps(head.ops), ...input.ops],
        summary: input.summary,
      })
      .where(eq(docVersions.id, head.id));
    await this.touchHuman(tx, doc.id, input.actor, input.origin);
    await this.replaceBlocks(tx, doc.id, input.content);
    return { ok: true, versionId: head.id, seq: head.seq };
  }

  private async touchHuman(tx: Tx, documentId: string, actor: Actor, origin: Origin) {
    if (actor.type !== "user" || origin !== "manual") return;
    await tx.update(documents).set({ humanTouched: true }).where(eq(documents.id, documentId));
  }

  private async replaceBlocks(tx: Tx, documentId: string, content: PMJSON) {
    await tx.delete(docBlocks).where(eq(docBlocks.documentId, documentId));
    const projected = listBlocks(content).map((node, index) => {
      const blockId = blockIdOf(node);
      if (!blockId) throw new Error("块缺少 id");
      return {
        documentId,
        blockId,
        type: node.type,
        contentHash: blockHashSync(node),
        text: blockText(node),
        orderKey: String(index).padStart(6, "0"),
        searchText: blockText(node),
      };
    });
    if (projected.length > 0) await tx.insert(docBlocks).values(projected);
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

  private async selectApiKeys(where: SQL): Promise<ApiKeyRecord[]> {
    const rows = await this.orm
      .select({
        id: apiKeys.id,
        userId: apiKeys.userId,
        name: apiKeys.name,
        prefix: apiKeys.prefix,
        hash: apiKeys.hash,
        scopes: apiKeys.scopes,
        createdAt: apiKeys.createdAt,
        lastUsedAt: apiKeys.lastUsedAt,
        revokedAt: apiKeys.revokedAt,
        maxConcurrentTasks: apiKeyPolicies.maxConcurrentTasks,
        dailyCostCapUsd: apiKeyPolicies.dailyCostCapUsd,
        allowedTiers: apiKeyPolicies.allowedTiers,
        defaultApplyMode: apiKeyPolicies.defaultApplyMode,
        webhookSecret: apiKeyPolicies.webhookSecret,
      })
      .from(apiKeys)
      .innerJoin(apiKeyPolicies, eq(apiKeyPolicies.apiKeyId, apiKeys.id))
      .where(where)
      .orderBy(desc(apiKeys.createdAt));
    return rows.map((row) => ({
      id: row.id,
      userId: row.userId,
      name: row.name,
      prefix: row.prefix,
      hash: row.hash,
      scopes: asStringArray(row.scopes),
      createdAt: asDate(row.createdAt) ?? new Date(0),
      lastUsedAt: asDate(row.lastUsedAt),
      revokedAt: asDate(row.revokedAt),
      maxConcurrentTasks: asNumber(row.maxConcurrentTasks),
      dailyCostCapUsd: asNumber(row.dailyCostCapUsd),
      allowedTiers: row.allowedTiers == null ? null : asStringArray(row.allowedTiers),
      defaultApplyMode: row.defaultApplyMode === "proposal" ? "proposal" : "auto",
      webhookSecret: row.webhookSecret,
    }));
  }
}

function asDate(value: Date | string | null): Date | null {
  if (value == null) return null;
  return value instanceof Date ? value : new Date(value);
}

function asNumber(value: number | string | null): number | null {
  if (value == null) return null;
  const parsed = typeof value === "number" ? value : Number(value);
  return Number.isFinite(parsed) ? parsed : null;
}

function asStringArray(value: unknown): string[] {
  if (Array.isArray(value)) return value.map(String);
  if (typeof value === "string" && value.startsWith("{") && value.endsWith("}")) {
    const inner = value.slice(1, -1);
    return inner ? inner.split(",") : [];
  }
  return [];
}

function asBuffer(value: Buffer | Uint8Array | string): Buffer {
  if (Buffer.isBuffer(value)) return value;
  if (value instanceof Uint8Array) return Buffer.from(value);
  const hex = value.startsWith("\\x") ? value.slice(2) : value;
  return Buffer.from(hex, "hex");
}

function asCredentialStatus(value: string): CredentialRow["status"] {
  if (value === "disabled" || value === "invalid") return value;
  return "active";
}

function toCredential(row: typeof providerCredentials.$inferSelect): CredentialRow {
  return {
    id: row.id,
    userId: row.userId,
    provider: row.provider,
    label: row.label,
    baseUrl: row.baseUrl,
    ciphertext: asBuffer(row.ciphertext),
    nonce: asBuffer(row.nonce),
    keyVersion: row.keyVersion,
    fingerprint: row.fingerprint,
    status: asCredentialStatus(row.status),
    lastVerifiedAt: asDate(row.lastVerifiedAt),
    lastErrorCode: row.lastErrorCode,
    createdAt: asDate(row.createdAt) ?? new Date(0),
    updatedAt: asDate(row.updatedAt) ?? new Date(0),
  };
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
    ops: asOps(row.ops),
    turnId: row.turnId,
    actorType: row.actorType,
    actorId: row.actorId,
    apiKeyId: row.apiKeyId,
    createdAt: stamp(row.createdAt),
  };
}

function isUniqueViolation(error: unknown): boolean {
  const seen = new Set<unknown>();
  let current: unknown = error;
  while (current && typeof current === "object" && !seen.has(current)) {
    seen.add(current);
    const record = current as { code?: unknown; message?: unknown; cause?: unknown };
    if (record.code === "23505") return true;
    if (
      typeof record.message === "string" &&
      /duplicate key|unique constraint/i.test(record.message)
    ) {
      return true;
    }
    current = record.cause;
  }
  return false;
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
    modelPin: row.modelPin,
    createdAt: stamp(row.createdAt),
    updatedAt: stamp(row.updatedAt),
  };
}

function stamp(value: Date | string): string {
  return value instanceof Date ? value.toISOString() : new Date(value).toISOString();
}

function stampOrNull(value: Date | string | null): string | null {
  if (value == null) return null;
  return stamp(value);
}

function asOps(value: unknown): EditOp[] {
  const raw = typeof value === "string" ? (JSON.parse(value) as unknown) : value;
  return Array.isArray(raw) ? (raw as EditOp[]) : [];
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
    modelRef: row.modelRef,
    tierRef: row.tierRef,
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

export async function openMemoryDatabase(options?: { now?: () => Date }): Promise<LwDatabase> {
  const client = new PGlite();
  await client.exec(migrationSql);
  const orm = drizzle({ client, schema });
  return new LwDatabase(
    orm,
    {
      exec: (sqlText) => client.exec(sqlText).then(() => undefined),
      close: () => client.close(),
    },
    options?.now,
  );
}

/** 本机默认：数据目录里的 PGlite。`location.kind === "postgres"` 时走正式 Postgres。 */
export async function openDatabase(location: DatabaseLocation): Promise<LwDatabase> {
  if (location.kind === "postgres") {
    if (!location.databaseUrl) throw new Error("Postgres 缺少连接串");
    return openPostgres(location.databaseUrl);
  }
  const dir = pgliteDataDir(location.dataDir);
  await mkdir(dir, { recursive: true });
  const client = new PGlite(dir);
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

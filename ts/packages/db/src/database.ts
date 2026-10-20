import { mkdir } from "node:fs/promises";
import { PGlite } from "@electric-sql/pglite";
import { vector } from "@electric-sql/pglite-pgvector";
import type {
  Actor,
  ApiTaskErrorBody,
  ApiTaskNeedsInput,
  ApiTaskPhase,
  ApiTaskState,
  BlockId,
  DossierContent,
  DossierOp,
  EditOp,
  NormalizedCreateTask,
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
import type { ArticleListQuery } from "@luminwrite/library";
import { normalizeTagName } from "@luminwrite/library";
import { and, desc, eq, gte, inArray, isNotNull, isNull, lte, type SQL, sql } from "drizzle-orm";
import { drizzle as drizzlePg } from "drizzle-orm/node-postgres";
import { drizzle, type PgliteDatabase } from "drizzle-orm/pglite";
import pg from "pg";

import { type DatabaseLocation, pgliteDataDir } from "./config.js";
import { newId } from "./ids.js";
import { migrationSql } from "./migrate.js";
import * as schema from "./schema.js";

const {
  documents,
  tags,
  documentTags,
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
  sources,
  evidence,
  apiTasks,
  apiTaskEvents,
} = schema;

export interface ResearchPlanRow {
  id: string;
  turnId: string;
  sessionId: string;
  plan: unknown;
  gaps: unknown;
  createdAt: string;
}

export interface SubagentRunRow {
  id: string;
  turnId: string;
  parentId: string | null;
  sessionId: string;
  piConversationId: string | null;
  piTaskId: string;
  agentRef: string;
  slot: string;
  modelRef: string | null;
  input: unknown;
  output: unknown;
  status: string;
  createdAt: string;
}

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
  kbExcluded: boolean;
  createdVia: string;
  createdAt: string;
}

/** 版本写入之后通知知识库。schedule 是 60 秒节流；index / remove 立刻执行。 */
export type DocumentIndexNotice = {
  documentId: string;
  action: "schedule" | "index" | "remove";
};

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

/** 文章库里的一篇。status 由 discarded_at 推出，不写 documents.status。 */
export interface ArticleView {
  id: string;
  title: string;
  genre: string;
  status: "active" | "archived";
  tags: string[];
  savedAt: string;
  discardedAt: string | null;
  kbExcluded: boolean;
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

export interface HunkView {
  id: string;
  status: string;
  op: EditOp;
}

export interface ApiTaskRecord {
  id: string;
  userId: string;
  apiKeyId: string;
  idempotencyKey: string;
  kind: "write" | "revise";
  state: ApiTaskState;
  phase: ApiTaskPhase | null;
  documentId: string | null;
  sessionId: string | null;
  turnId: string | null;
  resultVersionId: string | null;
  proposalId: string | null;
  needsInput: ApiTaskNeedsInput | null;
  input: NormalizedCreateTask;
  error: ApiTaskErrorBody | null;
  createdAt: string;
  updatedAt: string;
}

export interface ApiTaskEventRecord {
  seq: number;
  taskId: string;
  type: string;
  payload: unknown;
  createdAt: string;
}

export interface ProposalView {
  id: string;
  documentId: string;
  turnId: string | null;
  baseVersionId: string;
  status: string;
  createdAt: string;
  hunks: HunkView[];
}

export type HunkOutcome =
  | { ok: true; already: boolean; status: "accepted"; versionId?: string; seq?: number }
  | { ok: true; already: boolean; status: "rejected" }
  | { ok: false; errors: OpError[] };

/** 同一 actor 的连续人工修订合并成一个版本的窗口。 */
export const AUTOSAVE_WINDOW_MS = 30_000;
export const DEFAULT_DISCARD_RETENTION_DAYS = 30;
export const DEFAULT_DISCARD_PURGE_INTERVAL_MS = 60 * 60 * 1000;

const SAVED_ON_CREATE = new Set(["api", "import", "derive"]);

/**
 * 知识库和检索以后只用这个判断。未保存、已丢弃、移出知识库的文档都不进。
 * 列表仍只看 saved_at 和 discarded_at。
 */
export function documentVisibleToKb(doc: {
  savedAt: Date | string | null;
  discardedAt: Date | string | null;
  kbExcluded?: boolean;
}): boolean {
  return doc.savedAt != null && doc.discardedAt == null && doc.kbExcluded !== true;
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

export type SqlQuery = <T = Record<string, unknown>>(
  sqlText: string,
  params?: unknown[],
) => Promise<T[]>;

interface Closer {
  exec(sqlText: string): Promise<void>;
  query<T>(sqlText: string, params?: unknown[]): Promise<T[]>;
  transaction<T>(fn: (query: SqlQuery) => Promise<T>): Promise<T>;
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

export type DatabaseDriver =
  | { kind: "pglite"; client: PGlite }
  | { kind: "postgres"; connectionString: string };

export class LwDatabase {
  readonly driver: DatabaseDriver;
  private readonly orm: Orm;
  private readonly client: Closer;
  private readonly clock: () => Date;
  private indexListener: ((notice: DocumentIndexNotice) => void | Promise<void>) | null = null;

  constructor(
    orm: Orm,
    client: Closer,
    clock: () => Date = () => new Date(),
    driver?: DatabaseDriver,
  ) {
    this.orm = orm;
    this.client = client;
    this.clock = clock;
    this.driver = driver ?? { kind: "postgres", connectionString: "" };
  }

  /** 服务器接上队列后登记。没登记时版本照常落库，只是不进知识库。 */
  onDocumentIndex(listener: (notice: DocumentIndexNotice) => void | Promise<void>): void {
    this.indexListener = listener;
  }

  private async notifyIndex(notice: DocumentIndexNotice): Promise<void> {
    await this.indexListener?.(notice);
  }

  private async afterVersion(documentId: string): Promise<void> {
    const doc = await this.getDocument(documentId);
    if (!doc || !documentVisibleToKb(doc)) return;
    await this.notifyIndex({ documentId, action: "schedule" });
  }

  private async deleteDocumentChunks(tx: Tx, id: string): Promise<void> {
    await tx.execute(
      sql`DELETE FROM chunks WHERE source_id IN (
        SELECT id FROM sources WHERE document_id = ${id} AND kind = 'document'
      )`,
    );
  }

  exec(sqlText: string): Promise<void> {
    return this.client.exec(sqlText);
  }

  query<T>(sqlText: string, params?: unknown[]): Promise<T[]> {
    return this.client.query<T>(sqlText, params);
  }

  transaction<T>(fn: (query: SqlQuery) => Promise<T>): Promise<T> {
    return this.client.transaction(fn);
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
    if (savedAt) await this.afterVersion(id);
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
      kbExcluded: row.kbExcluded,
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

  async listArticles(ownerId: string, query: ArticleListQuery): Promise<ArticleView[]> {
    const filters: SQL[] = [eq(documents.ownerId, ownerId), isNotNull(documents.savedAt)];
    if (query.archived === "archived") filters.push(isNotNull(documents.discardedAt));
    else if (query.archived === "active") filters.push(isNull(documents.discardedAt));
    if (query.genre) filters.push(eq(documents.genre, query.genre));
    if (query.createdVia) filters.push(eq(documents.createdVia, query.createdVia));
    if (query.kbExcluded === true) filters.push(eq(documents.kbExcluded, true));
    if (query.kbExcluded === false) filters.push(eq(documents.kbExcluded, false));
    if (query.text) {
      const pattern = likeContains(query.text);
      filters.push(sql`(
        ${documents.title} ilike ${pattern}
        or exists (
          select 1 from doc_blocks
          where doc_blocks.document_id = ${documents.id}
            and doc_blocks.text ilike ${pattern}
        )
      )`);
    }
    for (const tag of query.tags) {
      filters.push(sql`exists (
        select 1 from document_tags
        join tags on tags.id = document_tags.tag_id
        where document_tags.document_id = ${documents.id}
          and tags.owner_id = ${ownerId}
          and tags.name = ${tag}
      )`);
    }
    const rows = await this.orm
      .select()
      .from(documents)
      .where(and(...filters))
      .orderBy(desc(documents.createdAt), desc(documents.id))
      .limit(query.limit);
    const names = await this.tagsByDocument(rows.map((row) => row.id));
    return rows.map((row) => toArticle(row, names.get(row.id) ?? []));
  }

  async getArticle(ownerId: string, documentId: string): Promise<ArticleView | undefined> {
    const rows = await this.orm
      .select()
      .from(documents)
      .where(
        and(
          eq(documents.id, documentId),
          eq(documents.ownerId, ownerId),
          isNotNull(documents.savedAt),
        ),
      )
      .limit(1);
    const row = rows[0];
    if (!row) return undefined;
    return toArticle(row, await this.tagNames(row.id));
  }

  async listTags(ownerId: string): Promise<string[]> {
    const rows = await this.orm
      .select({ name: tags.name })
      .from(tags)
      .where(eq(tags.ownerId, ownerId))
      .orderBy(tags.name);
    return rows.map((row) => row.name);
  }

  async addDocumentTag(documentId: string, ownerId: string, rawName: string): Promise<string[]> {
    const name = normalizeTagName(rawName);
    return this.orm.transaction(async (tx) => {
      const doc = await this.lock(tx, documentId);
      if (doc.ownerId !== ownerId) throw new DocumentNotFound(documentId);
      const existing = await tx
        .select({ id: tags.id })
        .from(tags)
        .where(and(eq(tags.ownerId, ownerId), eq(tags.name, name)))
        .limit(1);
      let tagId = existing[0]?.id;
      if (!tagId) {
        tagId = newId("tag");
        await tx.insert(tags).values({ id: tagId, ownerId, name });
      }
      await tx.insert(documentTags).values({ documentId, tagId }).onConflictDoNothing();
      return this.tagNames(documentId, tx);
    });
  }

  async removeDocumentTag(documentId: string, ownerId: string, rawName: string): Promise<string[]> {
    const name = normalizeTagName(rawName);
    return this.orm.transaction(async (tx) => {
      const doc = await this.lock(tx, documentId);
      if (doc.ownerId !== ownerId) throw new DocumentNotFound(documentId);
      const existing = await tx
        .select({ id: tags.id })
        .from(tags)
        .where(and(eq(tags.ownerId, ownerId), eq(tags.name, name)))
        .limit(1);
      const tagId = existing[0]?.id;
      if (!tagId) throw new Error("找不到标签");
      const removed = await tx
        .delete(documentTags)
        .where(and(eq(documentTags.documentId, documentId), eq(documentTags.tagId, tagId)))
        .returning({ tagId: documentTags.tagId });
      if (removed.length === 0) throw new Error("找不到标签");
      await this.deleteUnusedTags(tx, ownerId);
      return this.tagNames(documentId, tx);
    });
  }

  async saveDocument(id: string): Promise<{ savedAt: string; already: boolean }> {
    const result = await this.orm.transaction(async (tx) => {
      const doc = await this.lock(tx, id);
      if (doc.savedAt) return { savedAt: stamp(doc.savedAt), already: true };
      const now = this.clock();
      await tx
        .update(documents)
        .set({ savedAt: now, savedVersionId: doc.headVersionId, status: "active" })
        .where(eq(documents.id, id));
      return { savedAt: now.toISOString(), already: false };
    });
    if (!result.already) await this.notifyIndex({ documentId: id, action: "index" });
    return result;
  }

  async discardDocument(id: string): Promise<{ discardedAt: string }> {
    const result = await this.orm.transaction(async (tx) => {
      const doc = await this.lock(tx, id);
      if (doc.discardedAt) return { discardedAt: stamp(doc.discardedAt) };
      const now = this.clock();
      await tx.update(documents).set({ discardedAt: now }).where(eq(documents.id, id));
      await this.deleteDocumentChunks(tx, id);
      return { discardedAt: now.toISOString() };
    });
    await this.notifyIndex({ documentId: id, action: "remove" });
    return result;
  }

  async undoDiscard(id: string): Promise<{ discardedAt: null }> {
    const result = await this.orm.transaction(async (tx) => {
      await this.lock(tx, id);
      await tx.update(documents).set({ discardedAt: null }).where(eq(documents.id, id));
      return { discardedAt: null };
    });
    await this.notifyIndex({ documentId: id, action: "index" });
    return result;
  }

  async setKbExcluded(id: string, excluded: boolean): Promise<{ kbExcluded: boolean }> {
    await this.orm.transaction(async (tx) => {
      await this.lock(tx, id);
      await tx.update(documents).set({ kbExcluded: excluded }).where(eq(documents.id, id));
      if (excluded) await this.deleteDocumentChunks(tx, id);
    });
    await this.notifyIndex({ documentId: id, action: excluded ? "remove" : "index" });
    return { kbExcluded: excluded };
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
    const written = await this.orm.transaction(async (tx) => {
      const outcome = await this.writeOps(tx, {
        documentId: input.documentId,
        ops: input.ops,
        origin: "manual",
        actor: input.actor,
        summary: input.summary ?? "人工修订",
        scope: input.scope,
      });
      if (outcome.ok && input.sessionId)
        await this.advanceInTx(tx, input.sessionId, outcome.versionId);
      return outcome;
    });
    if (written.ok) await this.afterVersion(input.documentId);
    return written;
  }

  async rollback(input: {
    documentId: string;
    toVersionId: string;
    actor: Actor;
  }): Promise<ApplyOutcome> {
    const written = await this.orm.transaction(async (tx) => {
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
    if (written.ok) await this.afterVersion(input.documentId);
    return written;
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
    const outcome = await this.orm.transaction(async (tx) => {
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
    if (outcome.ok && outcome.status === "applied" && !outcome.already) {
      await this.afterVersion(input.documentId);
    }
    return outcome;
  }

  async acceptProposal(proposalId: string, actor: Actor): Promise<ApplyOutcome> {
    const proposal = await this.proposalRow(proposalId);
    if (!proposal) {
      return { ok: false, errors: [{ index: 0, code: "schema_invalid", message: "找不到提案" }] };
    }
    const pending = (await this.hunkRows(proposalId)).filter((hunk) => hunk.status === "pending");
    let last: ApplyOutcome | undefined;
    for (const hunk of pending) {
      const decided = await this.acceptHunk({
        documentId: proposal.documentId,
        proposalId,
        hunkId: hunk.id,
        actor,
      });
      if (decided.ok && decided.status === "accepted" && decided.versionId && decided.seq) {
        last = { ok: true, versionId: decided.versionId, seq: decided.seq };
      }
    }
    return (
      last ?? {
        ok: false,
        errors: [{ index: 0, code: "schema_invalid", message: "没有可接受的块" }],
      }
    );
  }

  async rejectProposal(proposalId: string): Promise<void> {
    const proposal = await this.proposalRow(proposalId);
    if (!proposal) return;
    await this.orm.transaction(async (tx) => {
      await this.lock(tx, proposal.documentId);
      const hunks = await this.hunkRows(proposalId, tx);
      for (const hunk of hunks) {
        if (hunk.status !== "pending") continue;
        await tx
          .update(proposalHunks)
          .set({ status: "rejected" })
          .where(eq(proposalHunks.id, hunk.id));
      }
      await this.refreshProposalStatus(tx, proposalId);
    });
  }

  async listProposals(documentId: string): Promise<ProposalView[]> {
    const rows = await this.orm
      .select()
      .from(proposals)
      .where(eq(proposals.documentId, documentId))
      .orderBy(proposals.createdAt, proposals.id);
    const views: ProposalView[] = [];
    for (const row of rows) views.push(await this.proposalView(row));
    return views;
  }

  async getProposal(proposalId: string): Promise<ProposalView | undefined> {
    const row = await this.proposalRow(proposalId);
    return row ? this.proposalView(row) : undefined;
  }

  async stageProposal(input: {
    documentId: string;
    ops: EditOp[];
    sessionId?: string;
  }): Promise<{ ok: true; proposal: ProposalView } | { ok: false; errors: OpError[] }> {
    return this.orm.transaction(async (tx) => {
      const head = await this.headInTx(tx, input.documentId);
      if (input.sessionId) {
        const scoped = await this.sessionInTx(tx, input.sessionId);
        if (!scoped || scoped.documentId !== input.documentId || scoped.status !== "open") {
          return {
            ok: false as const,
            errors: [
              { index: 0, code: "out_of_scope" as const, message: "会话只能修订自己所属的文档" },
            ],
          };
        }
      }
      const proposalId = newId("prp");
      await tx.insert(proposals).values({
        id: proposalId,
        documentId: input.documentId,
        turnId: null,
        baseVersionId: head.id,
        status: "pending",
        piTaskId: null,
        createdAt: new Date(),
      });
      for (const op of input.ops) {
        await tx.insert(proposalHunks).values({
          id: newId("hnk"),
          proposalId,
          op,
          status: "pending",
          userEdit: null,
        });
      }
      await this.retargetHunks(tx, input.documentId, asDoc(head.content), "rebase");
      const row = await this.proposalRow(proposalId, tx);
      if (!row) {
        return {
          ok: false as const,
          errors: [{ index: 0, code: "schema_invalid" as const, message: "提案没有写入" }],
        };
      }
      await this.appendEventTx(tx, input.documentId, {
        type: "edits.proposed",
        payload: { proposalId },
      });
      return { ok: true as const, proposal: await this.proposalView(row, tx) };
    });
  }

  async acceptHunk(input: {
    documentId: string;
    proposalId: string;
    hunkId: string;
    actor: Actor;
  }): Promise<HunkOutcome> {
    let documentId: string | undefined;
    const written = await this.orm.transaction(async (tx) => {
      const proposal = await this.proposalRow(input.proposalId, tx);
      if (!proposal || proposal.documentId !== input.documentId) {
        return {
          ok: false as const,
          errors: [{ index: 0, code: "schema_invalid" as const, message: "找不到提案" }],
        };
      }
      await this.lock(tx, proposal.documentId);
      const hunk = await this.hunkRow(input.hunkId, tx);
      if (!hunk || hunk.proposalId !== proposal.id) {
        return {
          ok: false as const,
          errors: [{ index: 0, code: "schema_invalid" as const, message: "找不到这块修订" }],
        };
      }
      if (hunk.status === "accepted") {
        return { ok: true as const, already: true, status: "accepted" as const };
      }
      if (hunk.status !== "pending") {
        return {
          ok: false as const,
          errors: [{ index: 0, code: "schema_invalid" as const, message: "这块修订已经结束" }],
        };
      }
      await tx
        .update(proposalHunks)
        .set({ status: "accepted" })
        .where(eq(proposalHunks.id, hunk.id));
      const applied = await this.writeOps(tx, {
        documentId: proposal.documentId,
        ops: [asOp(hunk.op)],
        origin: "turn",
        actor: input.actor,
        summary: "接受一块修订",
        turnId: proposal.turnId,
        piTaskId: null,
      });
      if (!applied.ok) {
        await tx
          .update(proposalHunks)
          .set({ status: "stale" })
          .where(eq(proposalHunks.id, hunk.id));
        await this.refreshProposalStatus(tx, proposal.id);
        await this.appendEventTx(tx, proposal.documentId, {
          turnId: proposal.turnId,
          type: "hunk.stale",
          payload: { proposalId: proposal.id, hunkId: hunk.id },
        });
        return applied;
      }
      await this.refreshProposalStatus(tx, proposal.id);
      const refreshed = await this.proposalRow(proposal.id, tx);
      if (refreshed?.status === "accepted" && proposal.turnId) {
        await tx
          .update(turns)
          .set({ status: "applied", resultVersionId: applied.versionId })
          .where(eq(turns.id, proposal.turnId));
      }
      const main = await tx
        .select()
        .from(sessions)
        .where(
          and(
            eq(sessions.documentId, proposal.documentId),
            eq(sessions.kind, "main"),
            eq(sessions.status, "open"),
          ),
        )
        .limit(1);
      if (main[0]) await this.advanceInTx(tx, main[0].id, applied.versionId);
      await this.appendEventTx(tx, proposal.documentId, {
        turnId: proposal.turnId,
        type: "hunk.accepted",
        payload: { proposalId: proposal.id, hunkId: hunk.id, versionId: applied.versionId },
      });
      documentId = proposal.documentId;
      return {
        ok: true as const,
        already: false,
        status: "accepted" as const,
        versionId: applied.versionId,
        seq: applied.seq,
      };
    });
    if (written.ok && written.status === "accepted" && written.versionId && documentId) {
      await this.afterVersion(documentId);
    }
    return written;
  }

  async rejectHunk(input: {
    documentId: string;
    proposalId: string;
    hunkId: string;
  }): Promise<HunkOutcome> {
    return this.orm.transaction(async (tx) => {
      const proposal = await this.proposalRow(input.proposalId, tx);
      if (!proposal || proposal.documentId !== input.documentId) {
        return {
          ok: false as const,
          errors: [{ index: 0, code: "schema_invalid" as const, message: "找不到提案" }],
        };
      }
      await this.lock(tx, proposal.documentId);
      const hunk = await this.hunkRow(input.hunkId, tx);
      if (!hunk || hunk.proposalId !== proposal.id) {
        return {
          ok: false as const,
          errors: [{ index: 0, code: "schema_invalid" as const, message: "找不到这块修订" }],
        };
      }
      if (hunk.status === "rejected") {
        return { ok: true as const, already: true, status: "rejected" as const };
      }
      if (hunk.status !== "pending") {
        return {
          ok: false as const,
          errors: [{ index: 0, code: "schema_invalid" as const, message: "这块修订已经结束" }],
        };
      }
      await tx
        .update(proposalHunks)
        .set({ status: "rejected" })
        .where(eq(proposalHunks.id, hunk.id));
      await this.refreshProposalStatus(tx, proposal.id);
      await this.appendEventTx(tx, proposal.documentId, {
        turnId: proposal.turnId,
        type: "hunk.rejected",
        payload: { proposalId: proposal.id, hunkId: hunk.id },
      });
      return { ok: true as const, already: false, status: "rejected" as const };
    });
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

  async saveResearchPlan(input: {
    turnId: string;
    sessionId: string;
    plan: unknown;
    gaps: string[];
  }): Promise<{ id: string }> {
    const id = newId("rpl");
    await this.query(
      `INSERT INTO research_plans (id, turn_id, session_id, plan, gaps)
       VALUES ($1, $2, $3, $4::jsonb, $5::jsonb)
       ON CONFLICT (turn_id) DO UPDATE
         SET session_id = EXCLUDED.session_id,
             plan = EXCLUDED.plan,
             gaps = EXCLUDED.gaps`,
      [id, input.turnId, input.sessionId, JSON.stringify(input.plan), JSON.stringify(input.gaps)],
    );
    const rows = await this.query<{ id: string }>(
      `SELECT id FROM research_plans WHERE turn_id = $1`,
      [input.turnId],
    );
    const saved = rows[0];
    if (!saved) throw new Error("研究计划没有写入");
    return { id: saved.id };
  }

  async listResearchPlans(documentId: string): Promise<ResearchPlanRow[]> {
    const rows = await this.query<{
      id: string;
      turn_id: string;
      session_id: string;
      plan: unknown;
      gaps: unknown;
      created_at: Date | string;
    }>(
      `SELECT p.id, p.turn_id, p.session_id, p.plan, p.gaps, p.created_at
       FROM research_plans p
       JOIN sessions s ON s.id = p.session_id
       WHERE s.document_id = $1
       ORDER BY p.created_at, p.id`,
      [documentId],
    );
    return rows.map((row) => ({
      id: row.id,
      turnId: row.turn_id,
      sessionId: row.session_id,
      plan: row.plan,
      gaps: row.gaps,
      createdAt:
        row.created_at instanceof Date ? row.created_at.toISOString() : String(row.created_at),
    }));
  }

  async saveSubagentRun(input: {
    turnId: string;
    parentId?: string | null;
    sessionId: string;
    piConversationId?: string | null;
    piTaskId: string;
    agentRef: string;
    slot: string;
    modelRef?: string | null;
    input: unknown;
    output: unknown;
    status: string;
  }): Promise<{ id: string }> {
    const id = newId("sar");
    await this.query(
      `INSERT INTO subagent_runs (
         id, turn_id, parent_id, session_id, pi_conversation_id, pi_task_id,
         agent_ref, slot, model_ref, input, output, status
       )
       VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb, $11::jsonb, $12)
       ON CONFLICT (pi_task_id) DO UPDATE
         SET status = EXCLUDED.status,
             output = EXCLUDED.output,
             model_ref = EXCLUDED.model_ref,
             pi_conversation_id = EXCLUDED.pi_conversation_id,
             slot = EXCLUDED.slot,
             agent_ref = EXCLUDED.agent_ref,
             input = EXCLUDED.input`,
      [
        id,
        input.turnId,
        input.parentId ?? null,
        input.sessionId,
        input.piConversationId ?? null,
        input.piTaskId,
        input.agentRef,
        input.slot,
        input.modelRef ?? null,
        JSON.stringify(input.input),
        JSON.stringify(input.output),
        input.status,
      ],
    );
    const rows = await this.query<{ id: string }>(
      `SELECT id FROM subagent_runs WHERE pi_task_id = $1`,
      [input.piTaskId],
    );
    const saved = rows[0];
    if (!saved) throw new Error("子代理运行没有写入");
    return { id: saved.id };
  }

  async listSubagentRuns(documentId: string): Promise<SubagentRunRow[]> {
    const rows = await this.query<{
      id: string;
      turn_id: string;
      parent_id: string | null;
      session_id: string;
      pi_conversation_id: string | null;
      pi_task_id: string;
      agent_ref: string;
      slot: string;
      model_ref: string | null;
      input: unknown;
      output: unknown;
      status: string;
      created_at: Date | string;
    }>(
      `SELECT r.id, r.turn_id, r.parent_id, r.session_id, r.pi_conversation_id, r.pi_task_id,
              r.agent_ref, r.slot, r.model_ref, r.input, r.output, r.status, r.created_at
       FROM subagent_runs r
       JOIN turns t ON t.id = r.turn_id
       WHERE t.document_id = $1
       ORDER BY r.created_at, r.id`,
      [documentId],
    );
    return rows.map((row) => ({
      id: row.id,
      turnId: row.turn_id,
      parentId: row.parent_id,
      sessionId: row.session_id,
      piConversationId: row.pi_conversation_id,
      piTaskId: row.pi_task_id,
      agentRef: row.agent_ref,
      slot: row.slot,
      modelRef: row.model_ref,
      input: row.input,
      output: row.output,
      status: row.status,
      createdAt:
        row.created_at instanceof Date ? row.created_at.toISOString() : String(row.created_at),
    }));
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
    const created = await this.orm.transaction(async (tx) => {
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
    await this.afterVersion(created.id);
    return created;
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

  private async proposalRow(id: string, tx?: Tx) {
    const query = tx ?? this.orm;
    const rows = await query.select().from(proposals).where(eq(proposals.id, id)).limit(1);
    return rows[0];
  }

  private async hunkRow(id: string, tx?: Tx) {
    const query = tx ?? this.orm;
    const rows = await query.select().from(proposalHunks).where(eq(proposalHunks.id, id)).limit(1);
    return rows[0];
  }

  private async hunkRows(proposalId: string, tx?: Tx) {
    const query = tx ?? this.orm;
    return query
      .select()
      .from(proposalHunks)
      .where(eq(proposalHunks.proposalId, proposalId))
      .orderBy(proposalHunks.id);
  }

  private async proposalView(row: typeof proposals.$inferSelect, tx?: Tx): Promise<ProposalView> {
    const hunks = await this.hunkRows(row.id, tx);
    return {
      id: row.id,
      documentId: row.documentId,
      turnId: row.turnId,
      baseVersionId: row.baseVersionId,
      status: row.status,
      createdAt: stamp(row.createdAt),
      hunks: hunks.map((hunk) => ({ id: hunk.id, status: hunk.status, op: asOp(hunk.op) })),
    };
  }

  private async refreshProposalStatus(tx: Tx, proposalId: string) {
    const hunks = await this.hunkRows(proposalId, tx);
    const pending = hunks.some((hunk) => hunk.status === "pending");
    const accepted = hunks.some((hunk) => hunk.status === "accepted");
    const stale = hunks.some((hunk) => hunk.status === "stale");
    const status = pending ? "pending" : accepted ? "accepted" : stale ? "stale" : "rejected";
    await tx.update(proposals).set({ status }).where(eq(proposals.id, proposalId));
  }

  private async retargetHunks(tx: Tx, documentId: string, doc: PMJSON, mode: "rebase" | "all") {
    const open = await tx.select().from(proposals).where(eq(proposals.documentId, documentId));
    for (const proposal of open) {
      const hunks = await this.hunkRows(proposal.id, tx);
      let changed = false;
      const newlyStale: string[] = [];
      for (const hunk of hunks) {
        if (hunk.status !== "pending") continue;
        if (mode === "rebase" && canRebase(doc, asOp(hunk.op))) continue;
        await tx
          .update(proposalHunks)
          .set({ status: "stale" })
          .where(eq(proposalHunks.id, hunk.id));
        newlyStale.push(hunk.id);
        changed = true;
      }
      if (!changed) continue;
      await this.refreshProposalStatus(tx, proposal.id);
      await this.appendEventTx(tx, documentId, {
        turnId: proposal.turnId,
        type: "hunk.stale",
        payload: { proposalId: proposal.id, hunkIds: newlyStale },
      });
    }
  }

  private async tagNames(documentId: string, tx?: Tx): Promise<string[]> {
    const query = tx ?? this.orm;
    const rows = await query
      .select({ name: tags.name })
      .from(documentTags)
      .innerJoin(tags, eq(documentTags.tagId, tags.id))
      .where(eq(documentTags.documentId, documentId))
      .orderBy(tags.name);
    return rows.map((row) => row.name);
  }

  private async tagsByDocument(ids: string[]): Promise<Map<string, string[]>> {
    const grouped = new Map<string, string[]>();
    if (ids.length === 0) return grouped;
    const rows = await this.orm
      .select({ documentId: documentTags.documentId, name: tags.name })
      .from(documentTags)
      .innerJoin(tags, eq(documentTags.tagId, tags.id))
      .where(inArray(documentTags.documentId, ids))
      .orderBy(tags.name);
    for (const row of rows) {
      const list = grouped.get(row.documentId) ?? [];
      list.push(row.name);
      grouped.set(row.documentId, list);
    }
    return grouped;
  }

  private async deleteUnusedTags(tx: Tx, ownerId: string) {
    await tx.execute(sql`
      delete from tags
      where owner_id = ${ownerId}
        and not exists (
          select 1 from document_tags where document_tags.tag_id = tags.id
        )
    `);
  }

  private async purgeOne(tx: Tx, id: string) {
    await tx.execute(
      sql`DELETE FROM subagent_runs WHERE session_id IN (
        SELECT id FROM sessions WHERE document_id = ${id}
      )`,
    );
    await tx.execute(
      sql`DELETE FROM research_plans WHERE session_id IN (
        SELECT id FROM sessions WHERE document_id = ${id}
      )`,
    );
    await tx.delete(evidence).where(eq(evidence.citingDocumentId, id));
    await tx.delete(sources).where(eq(sources.documentId, id));
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
    const owner = await tx
      .select({ ownerId: documents.ownerId })
      .from(documents)
      .where(eq(documents.id, id))
      .limit(1);
    await tx.delete(documents).where(eq(documents.id, id));
    const ownerId = owner[0]?.ownerId;
    if (ownerId) await this.deleteUnusedTags(tx, ownerId);
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
    await this.retargetHunks(
      tx,
      input.documentId,
      input.content,
      input.origin === "restore" ? "all" : "rebase",
    );
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
    await this.retargetHunks(tx, doc.id, input.content, "rebase");
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

  async findApiTaskByIdempotency(
    apiKeyId: string,
    idempotencyKey: string,
  ): Promise<ApiTaskRecord | undefined> {
    const rows = await this.orm
      .select()
      .from(apiTasks)
      .where(and(eq(apiTasks.apiKeyId, apiKeyId), eq(apiTasks.idempotencyKey, idempotencyKey)))
      .limit(1);
    const row = rows[0];
    return row ? toApiTask(row) : undefined;
  }

  async getApiTask(id: string): Promise<ApiTaskRecord | undefined> {
    const rows = await this.orm.select().from(apiTasks).where(eq(apiTasks.id, id)).limit(1);
    const row = rows[0];
    return row ? toApiTask(row) : undefined;
  }

  async countActiveApiTasks(apiKeyId: string): Promise<number> {
    const rows = await this.orm
      .select({ n: sql<number>`count(*)::int` })
      .from(apiTasks)
      .where(
        and(
          eq(apiTasks.apiKeyId, apiKeyId),
          inArray(apiTasks.state, ["submitted", "working", "input_required"]),
        ),
      );
    return Number(rows[0]?.n ?? 0);
  }

  async latestApiSessionId(apiKeyId: string, documentId: string): Promise<string | undefined> {
    const rows = await this.orm
      .select({ sessionId: apiTasks.sessionId })
      .from(apiTasks)
      .where(
        and(
          eq(apiTasks.apiKeyId, apiKeyId),
          eq(apiTasks.documentId, documentId),
          isNotNull(apiTasks.sessionId),
        ),
      )
      .orderBy(desc(apiTasks.createdAt), desc(apiTasks.id))
      .limit(1);
    return rows[0]?.sessionId ?? undefined;
  }

  async claimApiTask(input: {
    userId: string;
    apiKeyId: string;
    idempotencyKey: string;
    kind: "write" | "revise";
    state: "submitted" | "input_required";
    phase?: ApiTaskPhase | null;
    documentId: string;
    sessionId: string;
    input: NormalizedCreateTask;
    needsInput: ApiTaskNeedsInput | null;
    maxConcurrent: number | null;
  }): Promise<{ status: "created" | "replay"; task: ApiTaskRecord } | { status: "limited" }> {
    try {
      return await this.orm.transaction(async (tx) => {
        const existing = await tx
          .select()
          .from(apiTasks)
          .where(
            and(
              eq(apiTasks.apiKeyId, input.apiKeyId),
              eq(apiTasks.idempotencyKey, input.idempotencyKey),
            ),
          )
          .limit(1);
        const prior = existing[0];
        if (prior) return { status: "replay" as const, task: toApiTask(prior) };
        if (input.maxConcurrent != null) {
          const active = await tx
            .select({ n: sql<number>`count(*)::int` })
            .from(apiTasks)
            .where(
              and(
                eq(apiTasks.apiKeyId, input.apiKeyId),
                inArray(apiTasks.state, ["submitted", "working", "input_required"]),
              ),
            );
          if (Number(active[0]?.n ?? 0) >= input.maxConcurrent)
            return { status: "limited" as const };
        }
        const now = new Date();
        const id = newId("tsk");
        await tx.insert(apiTasks).values({
          id,
          userId: input.userId,
          apiKeyId: input.apiKeyId,
          idempotencyKey: input.idempotencyKey,
          kind: input.kind,
          state: input.state,
          phase: input.phase ?? null,
          documentId: input.documentId,
          sessionId: input.sessionId,
          turnId: null,
          resultVersionId: null,
          proposalId: null,
          needsInput: input.needsInput,
          input: taskInputRecord(input.input),
          error: null,
          createdAt: now,
          updatedAt: now,
        });
        await this.appendApiTaskEventTx(tx, id, "task.submitted", {
          state: "submitted",
          documentId: input.documentId,
        });
        if (input.state === "input_required" && input.needsInput) {
          await this.appendApiTaskEventTx(tx, id, "task.input_required", {
            state: "input_required",
            needsInput: input.needsInput,
          });
        }
        const created = await tx.select().from(apiTasks).where(eq(apiTasks.id, id)).limit(1);
        const row = created[0];
        if (!row) throw new Error("任务没有写入");
        return { status: "created" as const, task: toApiTask(row) };
      });
    } catch (error) {
      if (!isUniqueViolation(error)) throw error;
      const found = await this.findApiTaskByIdempotency(input.apiKeyId, input.idempotencyKey);
      if (!found) throw error;
      return { status: "replay", task: found };
    }
  }

  async transitionApiTask(
    id: string,
    from: readonly ApiTaskState[],
    patch: {
      state: ApiTaskState;
      phase?: ApiTaskPhase | null;
      turnId?: string | null;
      resultVersionId?: string | null;
      proposalId?: string | null;
      needsInput?: ApiTaskNeedsInput | null;
      error?: ApiTaskErrorBody | null;
    },
    event: { type: string; payload: unknown },
  ): Promise<ApiTaskRecord | undefined> {
    return this.orm.transaction(async (tx) => {
      const updated = await tx
        .update(apiTasks)
        .set({
          state: patch.state,
          updatedAt: new Date(),
          ...("phase" in patch ? { phase: patch.phase ?? null } : {}),
          ...("turnId" in patch ? { turnId: patch.turnId ?? null } : {}),
          ...("resultVersionId" in patch ? { resultVersionId: patch.resultVersionId ?? null } : {}),
          ...("proposalId" in patch ? { proposalId: patch.proposalId ?? null } : {}),
          ...("needsInput" in patch ? { needsInput: patch.needsInput ?? null } : {}),
          ...("error" in patch ? { error: patch.error ?? null } : {}),
        })
        .where(and(eq(apiTasks.id, id), inArray(apiTasks.state, [...from])))
        .returning();
      const row = updated[0];
      if (!row) return undefined;
      await this.appendApiTaskEventTx(tx, id, event.type, event.payload);
      return toApiTask(row);
    });
  }

  async patchApiTaskInput(id: string, input: NormalizedCreateTask): Promise<void> {
    await this.orm
      .update(apiTasks)
      .set({ input: taskInputRecord(input), updatedAt: new Date() })
      .where(eq(apiTasks.id, id));
  }

  async appendApiTaskEvent(taskId: string, type: string, payload: unknown): Promise<void> {
    await this.orm.transaction(async (tx) => {
      await this.appendApiTaskEventTx(tx, taskId, type, payload);
    });
  }

  async taskEventsAfter(taskId: string, afterSeq: number): Promise<ApiTaskEventRecord[]> {
    const rows = await this.orm
      .select()
      .from(apiTaskEvents)
      .where(eq(apiTaskEvents.taskId, taskId))
      .orderBy(apiTaskEvents.seq);
    return rows
      .filter((row) => row.seq > afterSeq)
      .map((row) => ({
        seq: row.seq,
        taskId: row.taskId,
        type: row.type,
        payload: row.payload,
        createdAt: stamp(row.createdAt),
      }));
  }

  async proposalForTurn(turnId: string): Promise<ProposalView | undefined> {
    const rows = await this.orm
      .select()
      .from(proposals)
      .where(eq(proposals.turnId, turnId))
      .limit(1);
    const row = rows[0];
    return row ? this.proposalView(row) : undefined;
  }

  async usageTotalsForTurn(turnId: string): Promise<{
    inputTokens: number;
    outputTokens: number;
    costMicros: number;
  }> {
    const rows = await this.orm
      .select({
        inputTokens: sql<number>`coalesce(sum(${usageLedger.inputTokens}), 0)`,
        outputTokens: sql<number>`coalesce(sum(${usageLedger.outputTokens}), 0)`,
        costMicros: sql<number>`coalesce(sum(${usageLedger.costMicros}), 0)`,
      })
      .from(usageLedger)
      .where(eq(usageLedger.turnId, turnId));
    const row = rows[0];
    return {
      inputTokens: Number(row?.inputTokens ?? 0),
      outputTokens: Number(row?.outputTokens ?? 0),
      costMicros: Number(row?.costMicros ?? 0),
    };
  }

  async attachUsageToTask(turnId: string, apiTaskId: string): Promise<void> {
    await this.orm.update(usageLedger).set({ apiTaskId }).where(eq(usageLedger.turnId, turnId));
  }

  async setDocumentMeta(
    id: string,
    meta: { styleRef?: string; brief?: Record<string, unknown>; tierRef?: string },
  ): Promise<void> {
    const patch: { styleRef?: string; brief?: Record<string, unknown>; tierRef?: string } = {};
    if (meta.styleRef) patch.styleRef = meta.styleRef;
    if (meta.brief) patch.brief = meta.brief;
    if (meta.tierRef) patch.tierRef = meta.tierRef;
    if (!patch.styleRef && !patch.brief && !patch.tierRef) return;
    await this.orm.update(documents).set(patch).where(eq(documents.id, id));
  }

  private async appendApiTaskEventTx(
    tx: Tx,
    taskId: string,
    type: string,
    payload: unknown,
  ): Promise<void> {
    const latest = await tx
      .select({ seq: apiTaskEvents.seq })
      .from(apiTaskEvents)
      .where(eq(apiTaskEvents.taskId, taskId))
      .orderBy(desc(apiTaskEvents.seq))
      .limit(1);
    const seq = (latest[0]?.seq ?? 0) + 1;
    await tx.insert(apiTaskEvents).values({
      id: newId("evt"),
      taskId,
      seq,
      type,
      payload: payload ?? {},
      createdAt: new Date(),
    });
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

const TASK_STATES: readonly ApiTaskState[] = [
  "submitted",
  "working",
  "input_required",
  "completed",
  "failed",
  "canceled",
  "rejected",
];

const TASK_PHASES: readonly ApiTaskPhase[] = [
  "planning",
  "researching",
  "outlining",
  "drafting",
  "reviewing",
  "revising",
];

function taskInputRecord(input: NormalizedCreateTask): Record<string, unknown> {
  return { ...input };
}

function asTaskState(value: string): ApiTaskState {
  for (const state of TASK_STATES) {
    if (state === value) return state;
  }
  return "failed";
}

function asTaskPhase(value: string | null): ApiTaskPhase | null {
  if (!value) return null;
  for (const phase of TASK_PHASES) {
    if (phase === value) return phase;
  }
  return null;
}

function asTaskKind(value: string): "write" | "revise" {
  return value === "revise" ? "revise" : "write";
}

function asNeedsInput(value: unknown): ApiTaskNeedsInput | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null;
  const record = value as Record<string, unknown>;
  if (record.kind !== "outline_approval" && record.kind !== "clarification") return null;
  if (typeof record.prompt !== "string") return null;
  return {
    kind: record.kind,
    prompt: record.prompt,
    ...(record.payload && typeof record.payload === "object" && !Array.isArray(record.payload)
      ? { payload: record.payload as Record<string, unknown> }
      : {}),
  };
}

function asTaskError(value: unknown): ApiTaskErrorBody | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null;
  const record = value as Record<string, unknown>;
  if (typeof record.code !== "string" || typeof record.message !== "string") return null;
  if (typeof record.retryable !== "boolean") return null;
  return { code: record.code, message: record.message, retryable: record.retryable };
}

function asTaskInput(value: unknown, kind: "write" | "revise"): NormalizedCreateTask {
  const record = value && typeof value === "object" && !Array.isArray(value) ? value : {};
  const input = record as NormalizedCreateTask;
  const sources = input.sources;
  return {
    kind,
    ...(input.documentId ? { documentId: input.documentId } : {}),
    ...(input.brief ? { brief: input.brief } : {}),
    ...(input.instruction ? { instruction: input.instruction } : {}),
    ...(input.anchors ? { anchors: input.anchors } : {}),
    ...(input.styleRef ? { styleRef: input.styleRef } : {}),
    ...(input.model ? { model: input.model } : {}),
    sources: {
      kb: sources?.kb ?? true,
      rss: sources?.rss ?? true,
      library: sources?.library ?? false,
      web: sources?.web ?? false,
      urls: sources?.urls ?? [],
    },
    review: { outlineApproval: input.review?.outlineApproval ?? false },
    ...(input.applyMode ? { applyMode: input.applyMode } : {}),
    ...(input.callbackUrl ? { callbackUrl: input.callbackUrl } : {}),
    ...(input.answer ? { answer: input.answer } : {}),
  };
}

function toApiTask(row: typeof apiTasks.$inferSelect): ApiTaskRecord {
  const kind = asTaskKind(row.kind);
  return {
    id: row.id,
    userId: row.userId,
    apiKeyId: row.apiKeyId,
    idempotencyKey: row.idempotencyKey,
    kind,
    state: asTaskState(row.state),
    phase: asTaskPhase(row.phase),
    documentId: row.documentId,
    sessionId: row.sessionId,
    turnId: row.turnId,
    resultVersionId: row.resultVersionId,
    proposalId: row.proposalId,
    needsInput: asNeedsInput(row.needsInput),
    input: asTaskInput(row.input, kind),
    error: asTaskError(row.error),
    createdAt: stamp(row.createdAt),
    updatedAt: stamp(row.updatedAt),
  };
}

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

function toArticle(row: typeof documents.$inferSelect, tagList: string[]): ArticleView {
  return {
    id: row.id,
    title: row.title,
    genre: row.genre,
    status: row.discardedAt ? "archived" : "active",
    tags: tagList,
    savedAt: stamp(row.savedAt ?? row.createdAt),
    discardedAt: stampOrNull(row.discardedAt),
    kbExcluded: row.kbExcluded,
    createdVia: row.createdVia,
    humanTouched: row.humanTouched,
    headVersionId: row.headVersionId,
    createdAt: stamp(row.createdAt),
  };
}

function likeContains(text: string): string {
  return `%${text.replace(/[\\%_]/g, (char) => `\\${char}`)}%`;
}

function asOps(value: unknown): EditOp[] {
  const raw = typeof value === "string" ? (JSON.parse(value) as unknown) : value;
  return Array.isArray(raw) ? (raw as EditOp[]) : [];
}

function asOp(value: unknown): EditOp {
  const raw = typeof value === "string" ? (JSON.parse(value) as unknown) : value;
  return raw as EditOp;
}

function canRebase(doc: PMJSON, op: EditOp): boolean {
  if (op.op === "insert_block") {
    if (op.afterBlockId && !findBlock(doc, op.afterBlockId)) return false;
    if (op.parentBlockId && !findBlock(doc, op.parentBlockId)) return false;
    return true;
  }
  const found = findBlock(doc, op.blockId);
  if (!found) return false;
  if (blockHashSync(found.node) === op.contentHash) return true;
  return rebaseOp(op, found.node) !== null;
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

function pgliteCloser(client: PGlite): Closer {
  return {
    exec: (sqlText) => client.exec(sqlText).then(() => undefined),
    query: async <T>(sqlText: string, params?: unknown[]) =>
      (await client.query(sqlText, params)).rows as T[],
    transaction: (fn) =>
      client.transaction(async (tx) =>
        fn(
          async <T>(sqlText: string, params?: unknown[]) =>
            (await tx.query(sqlText, params)).rows as T[],
        ),
      ),
    close: () => client.close(),
  };
}

export async function openMemoryDatabase(options?: { now?: () => Date }): Promise<LwDatabase> {
  const client = new PGlite({ extensions: { vector } });
  await client.exec(migrationSql);
  const orm = drizzle({ client, schema });
  return new LwDatabase(orm, pgliteCloser(client), options?.now ?? (() => new Date()), {
    kind: "pglite",
    client,
  });
}

/** 本机默认：数据目录里的 PGlite。`location.kind === "postgres"` 时走正式 Postgres。 */
export async function openDatabase(location: DatabaseLocation): Promise<LwDatabase> {
  if (location.kind === "postgres") {
    if (!location.databaseUrl) throw new Error("Postgres 缺少连接串");
    return openPostgres(location.databaseUrl);
  }
  const dir = pgliteDataDir(location.dataDir);
  await mkdir(dir, { recursive: true });
  const client = new PGlite(dir, { extensions: { vector } });
  await client.exec(migrationSql);
  const orm = drizzle({ client, schema });
  return new LwDatabase(orm, pgliteCloser(client), () => new Date(), {
    kind: "pglite",
    client,
  });
}

export async function openPostgres(connectionString: string): Promise<LwDatabase> {
  const pool = new pg.Pool({ connectionString });
  await pool.query(migrationSql);
  const orm = drizzlePg({ client: pool, schema });
  return new LwDatabase(
    orm as unknown as Orm,
    {
      exec: async (sqlText) => {
        await pool.query(sqlText);
      },
      query: async (sqlText, params) => (await pool.query(sqlText, params)).rows,
      transaction: async (fn) => {
        const client = await pool.connect();
        try {
          await client.query("begin");
          const result = await fn(
            async (sqlText, params) => (await client.query(sqlText, params)).rows,
          );
          await client.query("commit");
          return result;
        } catch (error) {
          await client.query("rollback");
          throw error;
        } finally {
          client.release();
        }
      },
      close: () => pool.end().then(() => undefined),
    },
    () => new Date(),
    { kind: "postgres", connectionString },
  );
}

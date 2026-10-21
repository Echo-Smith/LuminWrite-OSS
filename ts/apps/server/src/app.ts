import type { MutableModels } from "@earendil-works/pi-ai";
import {
  type Actor,
  DossierOpSchema,
  type EditOp,
  EditOpSchema,
  type RegistryEntrySpec,
  TurnRequestSchema,
} from "@luminwrite/contracts";
import {
  ApiKeyInputError,
  type ApiKeyView,
  CredentialInputError,
  createApiKey,
  createCredential,
  DocumentNotFound,
  deleteCredential,
  discardRetentionDays,
  type LwDatabase,
  listApiKeys,
  listCredentialViews,
  type MasterKeyring,
  revokeApiKey,
  SessionClosed,
  SessionNotFound,
  SessionScopeError,
  updateCredential,
  VersionNotFound,
} from "@luminwrite/db";
import type { EditAuthorizer } from "@luminwrite/edition";
import { type AuthPort, ownerAuthorizer } from "@luminwrite/edition";
import type { WritingRuntime } from "@luminwrite/harness";
import {
  createKnowledgeBase,
  KbInputError,
  type KnowledgeBase,
  retrievalDataNote,
} from "@luminwrite/kb";
import { TurnSpendError } from "@luminwrite/models";
import { readStoredPlan } from "@luminwrite/research";
import {
  createFeedService,
  FeedError,
  type FeedService,
  importUrl,
  type PageFetch,
  SafeFetchError,
} from "@luminwrite/sources";
import type { Context } from "hono";
import { Hono } from "hono";
import { z } from "zod";

import { enforceApiKey, resolveApiApplyMode } from "./api-access.js";
import { bindSlot, listForUser, probeCredential, saveRegistryEntry } from "./model-registry.js";
import { defaultRateLimiter, TaskConcurrency, type TokenBucketLimiter } from "./rate-limit.js";

const CreateDocument = z.object({
  title: z.string().min(1),
  genre: z.string().optional(),
});

const OpsBody = z.object({
  ops: z.array(EditOpSchema).min(1),
  summary: z.string().optional(),
  sessionId: z.string().min(1).optional(),
  applyMode: z.enum(["auto", "proposal"]).optional(),
});

const OpenSessionBody = z.object({
  kind: z.enum(["fork", "api", "collab"]),
  baseVersionId: z.string().min(1).optional(),
});

const DossierBody = z.object({
  expectedVersion: z.number().int().positive(),
  ops: z.array(DossierOpSchema).min(1),
});

const DeriveBody = z.object({
  versionId: z.string().min(1),
  inheritDossier: z.boolean(),
  title: z.string().min(1).optional(),
});

const ReadGrantBody = z.object({
  documentId: z.string().min(1),
});

const SessionEditsBody = z.object({
  documentIds: z.array(z.string().min(1)).min(1),
  ops: z.array(EditOpSchema).min(1),
  summary: z.string().optional(),
});

const RollbackBody = z.object({
  versionId: z.string().min(1),
});

const KbExcludedBody = z.object({
  excluded: z.boolean(),
});

const CiteBody = z.object({
  sourceId: z.string().min(1),
  quote: z.string().min(1).max(4_000),
  blockId: z.string().min(1).optional(),
  planId: z.string().min(1).max(80).optional(),
  questionId: z.string().min(1).max(64).optional(),
});

const TurnBody = TurnRequestSchema.omit({ documentId: true, actor: true, applyMode: true }).extend({
  applyMode: z.enum(["auto", "proposal"]).optional(),
  model: z.string().min(1).optional(),
  tier: z.string().min(1).optional(),
});

const CreateCredentialBody = z.object({
  provider: z.string().min(1).max(64),
  label: z.string().min(1).max(128),
  key: z.string().min(1).max(4096),
  baseUrl: z.string().max(2048).nullable().optional(),
});

const SlotBody = z.object({
  targetRef: z.string().min(1).max(200),
});

const RegistryBody = z.object({
  ref: z.string().min(1).max(200),
  spec: z.record(z.string(), z.unknown()),
});

const PatchCredentialBody = z.object({
  label: z.string().max(128).optional(),
  key: z.string().max(4096).optional(),
  baseUrl: z.string().max(2048).nullable().optional(),
  status: z.enum(["active", "disabled"]).optional(),
});

const UploadBody = z.object({
  filename: z.string().min(1).max(200),
  mime: z.string().max(200).optional(),
  dataBase64: z
    .string()
    .min(1)
    .max(Math.ceil((20 * 1024 * 1024 * 4) / 3) + 16),
  folderId: z.string().min(1).optional(),
  title: z.string().max(200).optional(),
});

const FolderBody = z.object({
  name: z.string().min(1).max(80),
  parentId: z.string().min(1).optional(),
});

const SOURCE_KINDS = ["upload", "url", "rss_item", "web", "document"] as const;

const SubscribeBody = z.object({
  url: z.string().min(1).max(2048),
  fullText: z.boolean().optional(),
  isHotspot: z.boolean().optional(),
  folderId: z.string().min(1).optional(),
});

const OpmlBody = z.object({
  xml: z.string().min(1).max(1_000_000),
});

const UrlImportBody = z.object({
  url: z.string().min(1).max(2048),
  folderId: z.string().min(1).optional(),
  title: z.string().max(200).optional(),
});

const CreateApiKeyBody = z.object({
  name: z.string().min(1).max(128),
  scopes: z.array(z.string()).optional(),
  maxConcurrentTasks: z.number().int().positive().nullable().optional(),
  dailyCostCapUsd: z.number().nonnegative().nullable().optional(),
  allowedTiers: z.array(z.string()).nullable().optional(),
  defaultApplyMode: z.enum(["auto", "proposal"]).optional(),
  webhookSecret: z.string().min(1).max(256).nullable().optional(),
});

type Env = { Variables: { actor: Actor; apiKey?: ApiKeyView } };
type AppContext = Context<Env>;

export interface AppDeps {
  db: LwDatabase;
  runtime: WritingRuntime;
  auth: AuthPort;
  authorizer?: EditAuthorizer;
  limiter?: TokenBucketLimiter;
  keyring?: MasterKeyring;
  env?: NodeJS.ProcessEnv;
  models?: MutableModels;
  kb?: KnowledgeBase;
  feeds?: FeedService;
  pageFetch?: PageFetch;
}

function httpError(error: unknown): { status: 404 | 409; body: { error: string } } | undefined {
  if (
    error instanceof DocumentNotFound ||
    error instanceof SessionNotFound ||
    error instanceof VersionNotFound
  ) {
    return { status: 404, body: { error: error.message } };
  }
  if (error instanceof SessionScopeError || error instanceof SessionClosed) {
    return { status: 409, body: { error: error.message } };
  }
  return undefined;
}

/** 请求体里的 actor / ownerId 丢掉。身份只来自认证结果。 */
function stripIdentity(value: unknown): unknown {
  if (!value || typeof value !== "object" || Array.isArray(value)) return value;
  const copy = { ...(value as Record<string, unknown>) };
  delete copy.actor;
  delete copy.ownerId;
  return copy;
}

async function readBody(c: AppContext): Promise<unknown> {
  try {
    return stripIdentity(await c.req.json());
  } catch {
    return undefined;
  }
}

function kbFailure(error: unknown): { status: 400 | 404; body: { error: string } } | undefined {
  if (error instanceof SafeFetchError) return { status: 400, body: { error: error.message } };
  if (!(error instanceof KbInputError || error instanceof FeedError)) return undefined;
  const status = error.message.startsWith("找不到") ? 404 : 400;
  return { status, body: { error: error.message } };
}

function userIdOf(actor: Actor): string | undefined {
  if (actor.type === "system") return undefined;
  return actor.userId;
}

export function createApp(deps: AppDeps): Hono<Env> {
  const app = new Hono<Env>();
  const authorizer =
    deps.authorizer ??
    ownerAuthorizer(async (documentId) => (await deps.db.getDocument(documentId))?.ownerId);
  const limiter = deps.limiter ?? defaultRateLimiter();
  const concurrency = new TaskConcurrency();
  const kb = deps.kb ?? createKnowledgeBase({ db: deps.db });
  const pageFetch: PageFetch =
    deps.pageFetch ??
    (async () => {
      throw new SafeFetchError("测试不访问网络");
    });
  const feeds = deps.feeds ?? createFeedService({ db: deps.db, kb, fetch: pageFetch });

  app.get("/health", (c) => c.json({ ok: true, service: "luminwrite-ts" }));

  const api = new Hono<Env>();
  api.use("*", async (c, next) => {
    const actor = await deps.auth.authenticate(c.req.raw);
    if (!actor || actor.type === "system") return c.json({ error: "需要所有者令牌" }, 401);
    c.set("actor", actor);
    if (actor.type === "api_client") {
      const decision = await enforceApiKey({
        db: deps.db,
        limiter,
        keyId: actor.keyId,
        method: c.req.method,
        path: c.req.path,
      });
      if (!decision.ok) {
        const { status, error, retryAfter } = decision.denial;
        if (retryAfter !== undefined) {
          return c.json({ error }, status, { "Retry-After": String(retryAfter) });
        }
        return c.json({ error }, status);
      }
      c.set("apiKey", decision.key);
    }
    await next();
  });

  async function requireDocument(c: AppContext, documentId: string): Promise<Response | undefined> {
    const userId = userIdOf(c.get("actor"));
    if (!userId) return c.json({ error: "需要所有者令牌" }, 401);
    const document = await deps.db.getDocument(documentId);
    if (!document) return c.json({ error: "找不到文档" }, 404);
    if (!(await authorizer.canEdit(documentId, userId))) {
      return c.json({ error: "没有这篇文档的修改权限" }, 403);
    }
    return undefined;
  }

  async function requireSession(c: AppContext, sessionId: string): Promise<Response | undefined> {
    let documentId: string;
    try {
      const session = await deps.db.getSession(sessionId);
      if (!session) return c.json({ error: "找不到会话" }, 404);
      documentId = session.documentId;
    } catch (error) {
      const mapped = httpError(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
    return requireDocument(c, documentId);
  }

  api.get("/documents", async (c) => {
    const userId = userIdOf(c.get("actor"));
    if (!userId) return c.json({ error: "需要所有者令牌" }, 401);
    return c.json(await deps.db.listDocuments(userId));
  });

  api.post("/documents", async (c) => {
    const parsed = CreateDocument.safeParse(await readBody(c));
    if (!parsed.success) return c.json({ error: "文档参数不合法" }, 400);
    const actor = c.get("actor");
    const userId = userIdOf(actor);
    if (!userId) return c.json({ error: "需要所有者令牌" }, 401);
    const created = await deps.db.createDocument({
      title: parsed.data.title,
      ownerId: userId,
      actor,
      createdVia: actor.type === "api_client" ? "api" : "ui",
      ...(parsed.data.genre ? { genre: parsed.data.genre } : {}),
    });
    return c.json(created, 201);
  });

  api.get("/documents/:id", async (c) => {
    const denied = await requireDocument(c, c.req.param("id"));
    if (denied) return denied;
    const document = await deps.db.getDocument(c.req.param("id"));
    if (!document) return c.json({ error: "找不到文档" }, 404);
    const blocks = await deps.db.blocks(document.id);
    return c.json({ ...document, blocks });
  });

  api.get("/documents/:id/versions", async (c) => {
    const denied = await requireDocument(c, c.req.param("id"));
    if (denied) return denied;
    return c.json(await deps.db.listVersions(c.req.param("id")));
  });

  api.get("/versions/:id", async (c) => {
    const version = await deps.db.getVersion(c.req.param("id"));
    if (!version) return c.json({ error: "找不到版本" }, 404);
    const denied = await requireDocument(c, version.documentId);
    if (denied) return denied;
    return c.json(version);
  });

  api.get("/documents/:id/diff", async (c) => {
    const denied = await requireDocument(c, c.req.param("id"));
    if (denied) return denied;
    const from = c.req.query("from");
    const to = c.req.query("to");
    if (!from || !to) return c.json({ error: "需要 from 和 to" }, 400);
    try {
      return c.json(await deps.db.diffVersions(c.req.param("id"), from, to));
    } catch (error) {
      const mapped = httpError(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
  });

  api.post("/documents/:id/save", async (c) => {
    const denied = await requireDocument(c, c.req.param("id"));
    if (denied) return denied;
    try {
      return c.json(await deps.db.saveDocument(c.req.param("id")));
    } catch (error) {
      const mapped = httpError(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
  });

  api.post("/documents/:id/discard/undo", async (c) => {
    const denied = await requireDocument(c, c.req.param("id"));
    if (denied) return denied;
    try {
      return c.json(await deps.db.undoDiscard(c.req.param("id")));
    } catch (error) {
      const mapped = httpError(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
  });

  api.post("/documents/:id/kb", async (c) => {
    const denied = await requireDocument(c, c.req.param("id"));
    if (denied) return denied;
    const parsed = KbExcludedBody.safeParse(await readBody(c));
    if (!parsed.success) return c.json({ error: "知识库开关参数不合法" }, 400);
    try {
      return c.json(await deps.db.setKbExcluded(c.req.param("id"), parsed.data.excluded));
    } catch (error) {
      const mapped = httpError(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
  });

  api.post("/documents/:id/citations", async (c) => {
    const denied = await requireDocument(c, c.req.param("id"));
    if (denied) return denied;
    const userId = userIdOf(c.get("actor"));
    if (!userId) return c.json({ error: "需要所有者令牌" }, 401);
    const parsed = CiteBody.safeParse(await readBody(c));
    if (!parsed.success) return c.json({ error: "引用参数不合法" }, 400);
    try {
      const evidence = await kb.recordEvidence({
        ownerId: userId,
        citingDocumentId: c.req.param("id"),
        sourceId: parsed.data.sourceId,
        quote: parsed.data.quote,
        ...(parsed.data.blockId ? { blockId: parsed.data.blockId } : {}),
        ...(parsed.data.planId ? { planId: parsed.data.planId } : {}),
        ...(parsed.data.questionId ? { questionId: parsed.data.questionId } : {}),
      });
      return c.json(evidence, 201);
    } catch (error) {
      const mapped = kbFailure(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
  });

  api.get("/documents/:id/plans", async (c) => {
    const denied = await requireDocument(c, c.req.param("id"));
    if (denied) return denied;
    const rows = await deps.db.listResearchPlans(c.req.param("id"));
    return c.json(rows.map((row) => readStoredPlan(row)));
  });

  api.get("/documents/:id/subagent-runs", async (c) => {
    const denied = await requireDocument(c, c.req.param("id"));
    if (denied) return denied;
    return c.json(await deps.db.listSubagentRuns(c.req.param("id")));
  });

  api.get("/documents/:id/citations/:evidenceId", async (c) => {
    const denied = await requireDocument(c, c.req.param("id"));
    if (denied) return denied;
    const userId = userIdOf(c.get("actor"));
    if (!userId) return c.json({ error: "需要所有者令牌" }, 401);
    try {
      return c.json(await kb.resolveEvidence(userId, c.req.param("evidenceId")));
    } catch (error) {
      const mapped = kbFailure(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
  });

  api.post("/documents/:id/discard", async (c) => {
    const denied = await requireDocument(c, c.req.param("id"));
    if (denied) return denied;
    try {
      return c.json(await deps.db.discardDocument(c.req.param("id")));
    } catch (error) {
      const mapped = httpError(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
  });

  api.post("/documents/:id/ops", async (c) => {
    const denied = await requireDocument(c, c.req.param("id"));
    if (denied) return denied;
    const parsed = OpsBody.safeParse(await readBody(c));
    if (!parsed.success) return c.json({ error: "修订参数不合法" }, 400);
    const actor = c.get("actor");
    try {
      if (actor.type === "api_client") {
        const key = c.get("apiKey");
        if (!key) return c.json({ error: "密钥已吊销或不存在" }, 401);
        const document = await deps.db.getDocument(c.req.param("id"));
        const applyMode = resolveApiApplyMode({
          ...(parsed.data.applyMode ? { requested: parsed.data.applyMode } : {}),
          humanTouched: document?.humanTouched ?? false,
          policyDefault: key.defaultApplyMode,
        });
        if (applyMode === "proposal") {
          const staged = await deps.db.stageProposal({
            documentId: c.req.param("id"),
            ops: parsed.data.ops as EditOp[],
            ...(parsed.data.sessionId ? { sessionId: parsed.data.sessionId } : {}),
          });
          if (!staged.ok) return c.json(staged, 409);
          return c.json({ ok: true, status: "proposed", proposal: staged.proposal });
        }
      }
      const result = await deps.db.applyManual({
        documentId: c.req.param("id"),
        ops: parsed.data.ops as EditOp[],
        actor,
        ...(parsed.data.summary ? { summary: parsed.data.summary } : {}),
        ...(parsed.data.sessionId ? { sessionId: parsed.data.sessionId } : {}),
      });
      if (!result.ok) return c.json(result, 409);
      return c.json(result);
    } catch (error) {
      const mapped = httpError(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
  });

  api.get("/documents/:id/proposals", async (c) => {
    const denied = await requireDocument(c, c.req.param("id"));
    if (denied) return denied;
    return c.json(await deps.db.listProposals(c.req.param("id")));
  });

  api.get("/documents/:id/proposals/:proposalId", async (c) => {
    const denied = await requireDocument(c, c.req.param("id"));
    if (denied) return denied;
    const proposal = await deps.db.getProposal(c.req.param("proposalId"));
    if (!proposal || proposal.documentId !== c.req.param("id")) {
      return c.json({ error: "找不到提案" }, 404);
    }
    return c.json(proposal);
  });

  api.post("/documents/:id/proposals/:proposalId/hunks/:hunkId/accept", async (c) => {
    const denied = await requireDocument(c, c.req.param("id"));
    if (denied) return denied;
    const result = await deps.db.acceptHunk({
      documentId: c.req.param("id"),
      proposalId: c.req.param("proposalId"),
      hunkId: c.req.param("hunkId"),
      actor: c.get("actor"),
    });
    if (!result.ok) {
      const missing = result.errors.some((error) => error.message.startsWith("找不到"));
      return c.json(result, missing ? 404 : 409);
    }
    return c.json(result);
  });

  api.post("/documents/:id/proposals/:proposalId/hunks/:hunkId/reject", async (c) => {
    const denied = await requireDocument(c, c.req.param("id"));
    if (denied) return denied;
    const result = await deps.db.rejectHunk({
      documentId: c.req.param("id"),
      proposalId: c.req.param("proposalId"),
      hunkId: c.req.param("hunkId"),
    });
    if (!result.ok) {
      const missing = result.errors.some((error) => error.message.startsWith("找不到"));
      return c.json(result, missing ? 404 : 409);
    }
    return c.json(result);
  });

  api.get("/documents/:id/sessions", async (c) => {
    const denied = await requireDocument(c, c.req.param("id"));
    if (denied) return denied;
    return c.json(await deps.db.listSessions(c.req.param("id")));
  });

  api.post("/documents/:id/sessions", async (c) => {
    const denied = await requireDocument(c, c.req.param("id"));
    if (denied) return denied;
    const parsed = OpenSessionBody.safeParse(await readBody(c));
    if (!parsed.success) return c.json({ error: "会话参数不合法" }, 400);
    try {
      const session = await deps.db.openSession({
        documentId: c.req.param("id"),
        kind: parsed.data.kind,
        actor: c.get("actor"),
        ...(parsed.data.baseVersionId ? { baseVersionId: parsed.data.baseVersionId } : {}),
      });
      return c.json(session, 201);
    } catch (error) {
      const mapped = httpError(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
  });

  api.post("/sessions/:id/close", async (c) => {
    const denied = await requireSession(c, c.req.param("id"));
    if (denied) return denied;
    try {
      await deps.db.closeSession(c.req.param("id"));
      return c.json({ ok: true });
    } catch (error) {
      const mapped = httpError(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
  });

  api.post("/sessions/:id/reads", async (c) => {
    const denied = await requireSession(c, c.req.param("id"));
    if (denied) return denied;
    const parsed = ReadGrantBody.safeParse(await readBody(c));
    if (!parsed.success) return c.json({ error: "阅读授权不合法" }, 400);
    try {
      await deps.db.grantRead(c.req.param("id"), parsed.data.documentId);
      return c.json({ ok: true });
    } catch (error) {
      const mapped = httpError(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
  });

  api.post("/sessions/:id/edits", async (c) => {
    const denied = await requireSession(c, c.req.param("id"));
    if (denied) return denied;
    const parsed = SessionEditsBody.safeParse(await readBody(c));
    if (!parsed.success) return c.json({ error: "修订参数不合法" }, 400);
    try {
      const result = await deps.db.applySessionEdits({
        sessionId: c.req.param("id"),
        documentIds: parsed.data.documentIds,
        ops: parsed.data.ops as EditOp[],
        actor: c.get("actor"),
        ...(parsed.data.summary ? { summary: parsed.data.summary } : {}),
      });
      if (!result.ok) return c.json(result, 409);
      return c.json(result);
    } catch (error) {
      const mapped = httpError(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
  });

  api.get("/documents/:id/dossier", async (c) => {
    const denied = await requireDocument(c, c.req.param("id"));
    if (denied) return denied;
    try {
      return c.json(await deps.db.readDossier(c.req.param("id")));
    } catch (error) {
      const mapped = httpError(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
  });

  api.post("/documents/:id/dossier", async (c) => {
    const denied = await requireDocument(c, c.req.param("id"));
    if (denied) return denied;
    const parsed = DossierBody.safeParse(await readBody(c));
    if (!parsed.success) return c.json({ error: "档案参数不合法" }, 400);
    try {
      const result = await deps.db.applyDossier({
        documentId: c.req.param("id"),
        expectedVersion: parsed.data.expectedVersion,
        ops: parsed.data.ops,
      });
      if (!result.ok) return c.json(result, 409);
      return c.json(result);
    } catch (error) {
      const mapped = httpError(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
  });

  api.post("/documents/:id/derive", async (c) => {
    const denied = await requireDocument(c, c.req.param("id"));
    if (denied) return denied;
    const parsed = DeriveBody.safeParse(await readBody(c));
    if (!parsed.success) return c.json({ error: "派生参数不合法" }, 400);
    const userId = userIdOf(c.get("actor"));
    if (!userId) return c.json({ error: "需要所有者令牌" }, 401);
    try {
      const source = await deps.db.listVersions(c.req.param("id"));
      if (!source.some((version) => version.id === parsed.data.versionId)) {
        return c.json({ error: "版本不属于这篇文档" }, 404);
      }
      const created = await deps.db.deriveDocument({
        versionId: parsed.data.versionId,
        inheritDossier: parsed.data.inheritDossier,
        ownerId: userId,
        actor: c.get("actor"),
        ...(parsed.data.title ? { title: parsed.data.title } : {}),
      });
      return c.json(created, 201);
    } catch (error) {
      const mapped = httpError(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
  });

  api.get("/documents/:id/derived", async (c) => {
    const denied = await requireDocument(c, c.req.param("id"));
    if (denied) return denied;
    return c.json(await deps.db.listDerived(c.req.param("id")));
  });

  const restore = async (c: AppContext) => {
    const documentId = c.req.param("id");
    if (!documentId) return c.json({ error: "找不到文档" }, 404);
    const denied = await requireDocument(c, documentId);
    if (denied) return denied;
    const parsed = RollbackBody.safeParse(await readBody(c));
    if (!parsed.success) return c.json({ error: "回滚参数不合法" }, 400);
    try {
      const result = await deps.db.rollback({
        documentId,
        toVersionId: parsed.data.versionId,
        actor: c.get("actor"),
      });
      if (!result.ok) return c.json(result, 409);
      return c.json(result);
    } catch (error) {
      const mapped = httpError(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
  };
  api.post("/documents/:id/restore", restore);
  api.post("/documents/:id/rollback", restore);

  api.post("/admin/purge-discarded", async (c) => {
    if (c.get("actor").type !== "user") return c.json({ error: "需要所有者令牌" }, 403);
    const parsed = z
      .object({ retentionDays: z.number().nonnegative().optional() })
      .safeParse((await readBody(c)) ?? {});
    if (!parsed.success) return c.json({ error: "清退参数不合法" }, 400);
    const retentionDays = parsed.data.retentionDays ?? discardRetentionDays(deps.env ?? {});
    return c.json(await deps.db.purgeDiscarded({ retentionDays }));
  });

  api.post("/documents/:id/turns", async (c) => {
    const denied = await requireDocument(c, c.req.param("id"));
    if (denied) return denied;
    const parsed = TurnBody.safeParse(await readBody(c));
    if (!parsed.success) return c.json({ error: "回合参数不合法" }, 400);
    const actor = c.get("actor");
    const { model, tier, applyMode: requestedApply, ...rest } = parsed.data;
    let applyMode = requestedApply ?? "auto";
    let release: (() => void) | undefined;
    if (actor.type === "api_client") {
      const key = c.get("apiKey");
      if (!key) return c.json({ error: "密钥已吊销或不存在" }, 401);
      const requestedModel = model ?? tier;
      if (key.allowedTiers && requestedModel && !key.allowedTiers.includes(requestedModel)) {
        return c.json({ error: "这把密钥不能用这个档位" }, 403);
      }
      const document = await deps.db.getDocument(c.req.param("id"));
      applyMode = resolveApiApplyMode({
        ...(requestedApply ? { requested: requestedApply } : {}),
        humanTouched: document?.humanTouched ?? false,
        policyDefault: key.defaultApplyMode,
      });
      if (key.maxConcurrentTasks != null) {
        if (!concurrency.tryAcquire(actor.keyId, key.maxConcurrentTasks)) {
          return c.json({ error: "超过并发上限" }, 429, { "Retry-After": "1" });
        }
        release = () => concurrency.release(actor.keyId);
      }
    }
    try {
      const requestedModel = model ?? tier;
      const result = await deps.runtime.submitTurn({
        ...rest,
        ...(requestedModel ? { model: requestedModel } : {}),
        applyMode,
        documentId: c.req.param("id"),
        actor,
      });
      return c.json(result);
    } catch (error) {
      if (error instanceof TurnSpendError) {
        return c.json({ error: error.message }, 429, {
          "Retry-After": String(error.retryAfterSeconds),
        });
      }
      const mapped = httpError(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    } finally {
      release?.();
    }
  });

  api.post("/me/api-keys", async (c) => {
    const parsed = CreateApiKeyBody.safeParse(await readBody(c));
    if (!parsed.success) return c.json({ error: "密钥参数不合法" }, 400);
    const userId = userIdOf(c.get("actor"));
    if (!userId) return c.json({ error: "需要所有者令牌" }, 401);
    try {
      const issued = await createApiKey(deps.db, { userId, ...parsed.data });
      return c.json(issued, 201);
    } catch (error) {
      if (error instanceof ApiKeyInputError) return c.json({ error: error.message }, 400);
      throw error;
    }
  });

  api.get("/me/api-keys", async (c) => {
    const userId = userIdOf(c.get("actor"));
    if (!userId) return c.json({ error: "需要所有者令牌" }, 401);
    return c.json(await listApiKeys(deps.db, userId));
  });

  api.delete("/me/api-keys/:id", async (c) => {
    const userId = userIdOf(c.get("actor"));
    if (!userId) return c.json({ error: "需要所有者令牌" }, 401);
    const outcome = await revokeApiKey(deps.db, { id: c.req.param("id"), userId });
    if (outcome === "missing") return c.json({ error: "找不到密钥" }, 404);
    return c.json({ ok: true });
  });

  function requireKeyring(c: AppContext): MasterKeyring | Response {
    if (!deps.keyring) return c.json({ error: "还没有主密钥" }, 500);
    return deps.keyring;
  }

  api.get("/me/credentials", async (c) => {
    const userId = userIdOf(c.get("actor"));
    if (!userId) return c.json({ error: "需要所有者令牌" }, 401);
    return c.json(await listCredentialViews(deps.db, userId));
  });

  api.post("/me/credentials", async (c) => {
    const ring = requireKeyring(c);
    if (ring instanceof Response) return ring;
    const parsed = CreateCredentialBody.safeParse(await readBody(c));
    if (!parsed.success) return c.json({ error: "凭据参数不合法" }, 400);
    const userId = userIdOf(c.get("actor"));
    if (!userId) return c.json({ error: "需要所有者令牌" }, 401);
    try {
      const created = await createCredential(deps.db, ring, {
        userId,
        provider: parsed.data.provider,
        label: parsed.data.label,
        key: parsed.data.key,
        ...(parsed.data.baseUrl !== undefined ? { baseUrl: parsed.data.baseUrl } : {}),
        ...(deps.env ? { env: deps.env } : {}),
      });
      return c.json(created, 201);
    } catch (error) {
      if (error instanceof CredentialInputError) return c.json({ error: error.message }, 400);
      throw error;
    }
  });

  api.patch("/me/credentials/:id", async (c) => {
    const ring = requireKeyring(c);
    if (ring instanceof Response) return ring;
    const parsed = PatchCredentialBody.safeParse(await readBody(c));
    if (!parsed.success) return c.json({ error: "凭据参数不合法" }, 400);
    const userId = userIdOf(c.get("actor"));
    if (!userId) return c.json({ error: "需要所有者令牌" }, 401);
    try {
      const updated = await updateCredential(deps.db, ring, {
        id: c.req.param("id"),
        userId,
        ...(parsed.data.label !== undefined ? { label: parsed.data.label } : {}),
        ...(parsed.data.key ? { key: parsed.data.key } : {}),
        ...(parsed.data.baseUrl !== undefined ? { baseUrl: parsed.data.baseUrl } : {}),
        ...(parsed.data.status ? { status: parsed.data.status } : {}),
        ...(deps.env ? { env: deps.env } : {}),
      });
      if (!updated) return c.json({ error: "找不到凭据" }, 404);
      return c.json(updated);
    } catch (error) {
      if (error instanceof CredentialInputError) return c.json({ error: error.message }, 400);
      throw error;
    }
  });

  api.post("/me/credentials/:id/verify", async (c) => {
    const ring = requireKeyring(c);
    if (ring instanceof Response) return ring;
    const userId = userIdOf(c.get("actor"));
    if (!userId) return c.json({ error: "需要所有者令牌" }, 401);
    const outcome = await probeCredential(deps, { id: c.req.param("id"), userId });
    if (!outcome.ok) return c.json({ error: outcome.error }, outcome.status);
    const views = await listCredentialViews(deps.db, userId);
    const verified = views.find((view) => view.id === c.req.param("id"));
    if (!verified) return c.json({ error: "找不到凭据" }, 404);
    return c.json(verified);
  });

  api.get("/models", async (c) => {
    const userId = userIdOf(c.get("actor"));
    if (!userId) return c.json({ error: "需要所有者令牌" }, 401);
    return c.json(await listForUser(deps, userId));
  });

  api.put("/me/model-slots/:slot", async (c) => {
    const userId = userIdOf(c.get("actor"));
    if (!userId) return c.json({ error: "需要所有者令牌" }, 401);
    const parsed = SlotBody.safeParse(await readBody(c));
    if (!parsed.success) return c.json({ error: "槽位参数不合法" }, 400);
    const bound = await bindSlot(deps, userId, c.req.param("slot"), parsed.data.targetRef);
    if (!bound.ok) return c.json({ error: bound.error }, 400);
    return c.json({ slot: bound.slot, targetRef: parsed.data.targetRef });
  });

  api.put("/me/registry", async (c) => {
    const userId = userIdOf(c.get("actor"));
    if (!userId) return c.json({ error: "需要所有者令牌" }, 401);
    const parsed = RegistryBody.safeParse(await readBody(c));
    if (!parsed.success) return c.json({ error: "注册表参数不合法" }, 400);
    const spec = parsed.data.spec;
    if (spec.kind !== "model" && spec.kind !== "tier") {
      return c.json({ error: "注册表参数不合法" }, 400);
    }
    try {
      await saveRegistryEntry(deps, userId, parsed.data.ref, spec as RegistryEntrySpec);
    } catch (error) {
      const message = error instanceof Error ? error.message : "注册表参数不合法";
      return c.json({ error: message }, 400);
    }
    return c.json({ ref: parsed.data.ref });
  });

  api.delete("/me/credentials/:id", async (c) => {
    const userId = userIdOf(c.get("actor"));
    if (!userId) return c.json({ error: "需要所有者令牌" }, 401);
    const outcome = await deleteCredential(deps.db, { id: c.req.param("id"), userId });
    if (outcome === "missing") return c.json({ error: "找不到凭据" }, 404);
    return c.json({ ok: true });
  });

  api.get("/documents/:id/events", async (c) => {
    const denied = await requireDocument(c, c.req.param("id"));
    if (denied) return denied;
    const after = Number(c.req.query("after") ?? "0");
    return c.json(await deps.db.eventsAfter(c.req.param("id"), Number.isFinite(after) ? after : 0));
  });

  api.get("/documents/:id/events/stream", async (c) => {
    const denied = await requireDocument(c, c.req.param("id"));
    if (denied) return denied;
    const after = Number(c.req.query("after") ?? "0");
    const events = await deps.db.eventsAfter(c.req.param("id"), Number.isFinite(after) ? after : 0);
    const body = events
      .map((event) => `id: ${event.seq}\ndata: ${JSON.stringify(event)}\n\n`)
      .join("");
    return new Response(body, { headers: { "content-type": "text/event-stream" } });
  });

  api.post("/feeds/opml", async (c) => {
    const userId = userIdOf(c.get("actor"));
    if (!userId) return c.json({ error: "需要所有者令牌" }, 401);
    const parsed = OpmlBody.safeParse(await readBody(c));
    if (!parsed.success) return c.json({ error: "OPML 参数不合法" }, 400);
    try {
      return c.json(await feeds.importOpml(userId, parsed.data.xml));
    } catch (error) {
      const mapped = kbFailure(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
  });

  api.get("/feeds/opml", async (c) => {
    const userId = userIdOf(c.get("actor"));
    if (!userId) return c.json({ error: "需要所有者令牌" }, 401);
    return c.json({ xml: await feeds.exportOpml(userId) });
  });

  api.get("/feeds", async (c) => {
    const userId = userIdOf(c.get("actor"));
    if (!userId) return c.json({ error: "需要所有者令牌" }, 401);
    return c.json({ feeds: await feeds.list(userId) });
  });

  api.post("/feeds", async (c) => {
    const userId = userIdOf(c.get("actor"));
    if (!userId) return c.json({ error: "需要所有者令牌" }, 401);
    const parsed = SubscribeBody.safeParse(await readBody(c));
    if (!parsed.success) return c.json({ error: "订阅参数不合法" }, 400);
    try {
      const feed = await feeds.subscribe(userId, {
        url: parsed.data.url,
        ...(parsed.data.fullText !== undefined ? { fullText: parsed.data.fullText } : {}),
        ...(parsed.data.isHotspot !== undefined ? { isHotspot: parsed.data.isHotspot } : {}),
        ...(parsed.data.folderId ? { folderId: parsed.data.folderId } : {}),
      });
      const outcome = await feeds.refresh(feed.id);
      return c.json({ feed, ...outcome }, 201);
    } catch (error) {
      const mapped = kbFailure(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
  });

  api.post("/feeds/:id/refresh", async (c) => {
    const userId = userIdOf(c.get("actor"));
    if (!userId) return c.json({ error: "需要所有者令牌" }, 401);
    const feed = await feeds.get(c.req.param("id"));
    if (!feed || feed.ownerId !== userId) return c.json({ error: "找不到订阅" }, 404);
    try {
      return c.json(await feeds.refresh(feed.id));
    } catch (error) {
      const mapped = kbFailure(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
  });

  api.delete("/feeds/:id", async (c) => {
    const userId = userIdOf(c.get("actor"));
    if (!userId) return c.json({ error: "需要所有者令牌" }, 401);
    const keep = c.req.query("keepItems");
    try {
      await feeds.unsubscribe(c.req.param("id"), {
        keepItems: keep === "1" || keep === "true",
        ownerId: userId,
      });
      return c.json({ ok: true });
    } catch (error) {
      const mapped = kbFailure(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
  });

  api.post("/sources/folders", async (c) => {
    const userId = userIdOf(c.get("actor"));
    if (!userId) return c.json({ error: "需要所有者令牌" }, 401);
    const parsed = FolderBody.safeParse(await readBody(c));
    if (!parsed.success) return c.json({ error: "文件夹参数不合法" }, 400);
    try {
      return c.json(
        await kb.createFolder({
          ownerId: userId,
          name: parsed.data.name,
          ...(parsed.data.parentId ? { parentId: parsed.data.parentId } : {}),
        }),
        201,
      );
    } catch (error) {
      const mapped = kbFailure(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
  });

  api.post("/sources", async (c) => {
    const userId = userIdOf(c.get("actor"));
    if (!userId) return c.json({ error: "需要所有者令牌" }, 401);
    const parsed = UploadBody.safeParse(await readBody(c));
    if (!parsed.success) return c.json({ error: "上传参数不合法" }, 400);
    let bytes: Buffer;
    try {
      bytes = Buffer.from(parsed.data.dataBase64, "base64");
    } catch {
      return c.json({ error: "文件内容不是 base64" }, 400);
    }
    if (bytes.length === 0) return c.json({ error: "文件是空的" }, 400);
    if (bytes.length > 20 * 1024 * 1024) return c.json({ error: "文件超过 20MB" }, 400);
    try {
      const outcome = await kb.ingest({
        ownerId: userId,
        filename: parsed.data.filename,
        bytes,
        ...(parsed.data.mime ? { mime: parsed.data.mime } : {}),
        ...(parsed.data.folderId ? { folderId: parsed.data.folderId } : {}),
        ...(parsed.data.title ? { title: parsed.data.title } : {}),
      });
      return c.json(outcome, outcome.status === "indexed" ? 201 : 200);
    } catch (error) {
      const mapped = kbFailure(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
  });

  api.post("/sources/url", async (c) => {
    const userId = userIdOf(c.get("actor"));
    if (!userId) return c.json({ error: "需要所有者令牌" }, 401);
    const parsed = UrlImportBody.safeParse(await readBody(c));
    if (!parsed.success) return c.json({ error: "网址参数不合法" }, 400);
    try {
      const outcome = await importUrl({
        kb,
        ownerId: userId,
        url: parsed.data.url,
        fetch: pageFetch,
        ...(parsed.data.folderId ? { folderId: parsed.data.folderId } : {}),
        ...(parsed.data.title ? { title: parsed.data.title } : {}),
      });
      return c.json(outcome, outcome.status === "indexed" ? 201 : 200);
    } catch (error) {
      const mapped = kbFailure(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
  });

  api.get("/sources/search", async (c) => {
    const userId = userIdOf(c.get("actor"));
    if (!userId) return c.json({ error: "需要所有者令牌" }, 401);
    const text = c.req.query("q")?.trim() ?? "";
    if (!text) return c.json({ error: "缺少查询" }, 400);
    const limit = Number(c.req.query("limit") ?? "5");
    const kinds = c.req
      .query("kinds")
      ?.split(",")
      .map((kind) => kind.trim())
      .filter((kind): kind is (typeof SOURCE_KINDS)[number] =>
        (SOURCE_KINDS as readonly string[]).includes(kind),
      );
    const folderIds = c.req
      .query("folderIds")
      ?.split(",")
      .map((id) => id.trim())
      .filter((id) => id.length > 0);
    const library = c.req.query("library") === "1" || c.req.query("library") === "true";
    const hits = await kb.search({
      ownerId: userId,
      text,
      limit: Number.isFinite(limit) ? limit : 5,
      ...(kinds && kinds.length > 0 ? { kinds } : {}),
      ...(folderIds && folderIds.length > 0 ? { folderIds } : {}),
      ...(library ? { library: true } : {}),
    });
    return c.json({ note: retrievalDataNote, hits });
  });

  api.get("/sources/:id", async (c) => {
    const userId = userIdOf(c.get("actor"));
    if (!userId) return c.json({ error: "需要所有者令牌" }, 401);
    const start = c.req.query("start");
    const end = c.req.query("end");
    const range =
      start !== undefined && end !== undefined
        ? { charStart: Number(start), charEnd: Number(end) }
        : undefined;
    try {
      const source = await kb.readSource(userId, c.req.param("id"), range);
      return c.json({ note: retrievalDataNote, ...source });
    } catch (error) {
      const mapped = kbFailure(error);
      if (mapped) return c.json(mapped.body, mapped.status);
      throw error;
    }
  });

  app.route("/api/v1", api);
  app.onError((error, c) => {
    if (error instanceof CredentialInputError || error instanceof ApiKeyInputError) {
      return c.json({ error: error.message }, 400);
    }
    const mapped = kbFailure(error);
    if (mapped) return c.json(mapped.body, mapped.status);
    return c.json({ error: "服务器出错" }, 500);
  });
  return app;
}

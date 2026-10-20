import type { DossierContent, EditOp } from "@luminwrite/contracts";
import type { PMJSON } from "@luminwrite/doc-model";
import { sql } from "drizzle-orm";
import {
  boolean,
  customType,
  doublePrecision,
  index,
  integer,
  jsonb,
  pgTable,
  primaryKey,
  text,
  timestamp,
  uniqueIndex,
} from "drizzle-orm/pg-core";

const bytea = customType<{ data: Buffer; driverData: Buffer | Uint8Array | string }>({
  dataType() {
    return "bytea";
  },
  toDriver(value) {
    return value;
  },
  fromDriver(value) {
    if (Buffer.isBuffer(value)) return value;
    if (value instanceof Uint8Array) return Buffer.from(value);
    if (typeof value === "string") {
      const hex = value.startsWith("\\x") ? value.slice(2) : value;
      return Buffer.from(hex, "hex");
    }
    return Buffer.from(value);
  },
});

/**
 * OSS 只有一行。`singleton` 上的唯一约束把“一行”落在库里，商业版多用户时去掉这列即可。
 * 令牌只存 SHA-256，明文在 `${LW_DATA_DIR}/owner.token`。
 */
export const users = pgTable(
  "users",
  {
    id: text("id").primaryKey(),
    displayName: text("display_name").notNull(),
    tokenHash: text("token_hash").notNull(),
    createdAt: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),
    singleton: boolean("singleton").notNull().default(true),
  },
  (table) => [uniqueIndex("uk_users_singleton").on(table.singleton)],
);

export const tags = pgTable(
  "tags",
  {
    id: text("id").primaryKey(),
    ownerId: text("owner_id").notNull(),
    name: text("name").notNull(),
  },
  (table) => [uniqueIndex("uk_tag_owner_name").on(table.ownerId, table.name)],
);

export const documentTags = pgTable(
  "document_tags",
  {
    documentId: text("document_id").notNull(),
    tagId: text("tag_id").notNull(),
  },
  (table) => [
    primaryKey({ columns: [table.documentId, table.tagId] }),
    index("idx_document_tags_tag").on(table.tagId),
  ],
);

export const documents = pgTable("documents", {
  id: text("id").primaryKey(),
  ownerId: text("owner_id").notNull(),
  title: text("title").notNull(),
  genre: text("genre").notNull(),
  status: text("status").notNull(),
  headVersionId: text("head_version_id"),
  styleRef: text("style_ref").notNull(),
  tierRef: text("tier_ref").notNull(),
  brief: jsonb("brief").$type<Record<string, unknown>>().notNull(),
  authoredByMe: boolean("authored_by_me").notNull(),
  createdVia: text("created_via").notNull(),
  humanTouched: boolean("human_touched").notNull(),
  derivedFromDocumentId: text("derived_from_document_id"),
  derivedFromVersionId: text("derived_from_version_id"),
  savedAt: timestamp("saved_at", { withTimezone: true }),
  savedVersionId: text("saved_version_id"),
  discardedAt: timestamp("discarded_at", { withTimezone: true }),
  kbExcluded: boolean("kb_excluded").notNull().default(false),
  createdAt: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),
});

export const docVersions = pgTable(
  "doc_versions",
  {
    id: text("id").primaryKey(),
    documentId: text("document_id").notNull(),
    seq: integer("seq").notNull(),
    parentId: text("parent_id"),
    origin: text("origin").notNull(),
    actorType: text("actor_type").notNull(),
    actorId: text("actor_id").notNull(),
    turnId: text("turn_id"),
    piTaskId: text("pi_task_id"),
    content: jsonb("content").$type<PMJSON>().notNull(),
    ops: jsonb("ops").$type<EditOp[]>().notNull().default(sql`'[]'::jsonb`),
    summary: text("summary").notNull(),
    apiKeyId: text("api_key_id"),
    createdAt: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),
  },
  (table) => [uniqueIndex("uk_ver_seq").on(table.documentId, table.seq)],
);

export const docBlocks = pgTable("doc_blocks", {
  documentId: text("document_id").notNull(),
  blockId: text("block_id").notNull(),
  type: text("type").notNull(),
  contentHash: text("content_hash").notNull(),
  text: text("text").notNull(),
  orderKey: text("order_key").notNull(),
  searchText: text("search_text").notNull(),
});

export const turns = pgTable(
  "turns",
  {
    id: text("id").primaryKey(),
    documentId: text("document_id").notNull(),
    kind: text("kind").notNull(),
    delivery: text("delivery").notNull(),
    status: text("status").notNull(),
    selection: jsonb("selection"),
    scope: text("scope").notNull(),
    baseVersionId: text("base_version_id"),
    resultVersionId: text("result_version_id"),
    applyMode: text("apply_mode").notNull(),
    actorType: text("actor_type").notNull(),
    actorId: text("actor_id").notNull(),
    sessionId: text("session_id").notNull(),
    idempotencyKey: text("idempotency_key").notNull(),
    piConversationId: text("pi_conversation_id"),
    piSubmissionId: text("pi_submission_id"),
    instruction: text("instruction").notNull(),
    error: text("error"),
    modelRef: text("model_ref"),
    tierRef: text("tier_ref"),
    createdAt: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),
  },
  (table) => [uniqueIndex("uk_turn_idem").on(table.documentId, table.idempotencyKey)],
);

export const runEvents = pgTable(
  "run_events",
  {
    id: text("id").primaryKey(),
    documentId: text("document_id").notNull(),
    seq: integer("seq").notNull(),
    turnId: text("turn_id"),
    type: text("type").notNull(),
    payload: jsonb("payload").notNull(),
    createdAt: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),
  },
  (table) => [uniqueIndex("uk_event_seq").on(table.documentId, table.seq)],
);

export const proposals = pgTable("proposals", {
  id: text("id").primaryKey(),
  documentId: text("document_id").notNull(),
  turnId: text("turn_id"),
  baseVersionId: text("base_version_id").notNull(),
  status: text("status").notNull(),
  piTaskId: text("pi_task_id"),
  createdAt: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),
});

export const proposalHunks = pgTable("proposal_hunks", {
  id: text("id").primaryKey(),
  proposalId: text("proposal_id").notNull(),
  op: jsonb("op").$type<EditOp>().notNull(),
  status: text("status").notNull(),
  userEdit: jsonb("user_edit"),
});

export const sessions = pgTable(
  "sessions",
  {
    id: text("id").primaryKey(),
    documentId: text("document_id").notNull(),
    kind: text("kind").notNull(),
    baseVersionId: text("base_version_id").notNull(),
    piConversationId: text("pi_conversation_id"),
    createdByType: text("created_by_type").notNull(),
    createdById: text("created_by_id").notNull(),
    status: text("status").notNull(),
    modelPin: text("model_pin"),
    createdAt: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),
    updatedAt: timestamp("updated_at", { withTimezone: true }).notNull().defaultNow(),
  },
  (table) => [uniqueIndex("uk_session_main").on(table.documentId).where(sql`kind = 'main'`)],
);

export const sessionReads = pgTable(
  "session_reads",
  {
    sessionId: text("session_id").notNull(),
    documentId: text("document_id").notNull(),
  },
  (table) => [primaryKey({ columns: [table.sessionId, table.documentId] })],
);

export const documentDossier = pgTable("document_dossier", {
  documentId: text("document_id").primaryKey(),
  version: integer("version").notNull(),
  content: jsonb("content").$type<DossierContent>().notNull(),
  updatedAt: timestamp("updated_at", { withTimezone: true }).notNull().defaultNow(),
});

export const apiKeys = pgTable(
  "api_keys",
  {
    id: text("id").primaryKey(),
    userId: text("user_id").notNull(),
    name: text("name").notNull(),
    prefix: text("prefix").notNull(),
    hash: text("hash").notNull(),
    scopes: text("scopes").array().notNull(),
    createdAt: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),
    lastUsedAt: timestamp("last_used_at", { withTimezone: true }),
    revokedAt: timestamp("revoked_at", { withTimezone: true }),
  },
  (table) => [uniqueIndex("uk_api_keys_hash").on(table.hash)],
);

export const apiTasks = pgTable(
  "api_tasks",
  {
    id: text("id").primaryKey(),
    userId: text("user_id").notNull(),
    apiKeyId: text("api_key_id").notNull(),
    idempotencyKey: text("idempotency_key").notNull(),
    kind: text("kind").notNull(),
    state: text("state").notNull(),
    phase: text("phase"),
    documentId: text("document_id"),
    sessionId: text("session_id"),
    turnId: text("turn_id"),
    resultVersionId: text("result_version_id"),
    proposalId: text("proposal_id"),
    needsInput: jsonb("needs_input").$type<Record<string, unknown> | null>(),
    input: jsonb("input").$type<Record<string, unknown>>().notNull(),
    error: jsonb("error").$type<Record<string, unknown> | null>(),
    createdAt: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),
    updatedAt: timestamp("updated_at", { withTimezone: true }).notNull().defaultNow(),
  },
  (table) => [
    uniqueIndex("uk_api_task_idem").on(table.apiKeyId, table.idempotencyKey),
    index("idx_api_task_key_state").on(table.apiKeyId, table.state),
  ],
);

export const apiTaskEvents = pgTable(
  "api_task_events",
  {
    id: text("id").primaryKey(),
    taskId: text("task_id").notNull(),
    seq: integer("seq").notNull(),
    type: text("type").notNull(),
    payload: jsonb("payload").notNull(),
    createdAt: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),
  },
  (table) => [uniqueIndex("uk_api_task_event_seq").on(table.taskId, table.seq)],
);

export const apiKeyPolicies = pgTable("api_key_policies", {
  apiKeyId: text("api_key_id")
    .primaryKey()
    .references(() => apiKeys.id),
  maxConcurrentTasks: integer("max_concurrent_tasks"),
  dailyCostCapUsd: doublePrecision("daily_cost_cap_usd"),
  allowedTiers: text("allowed_tiers").array(),
  defaultApplyMode: text("default_apply_mode").notNull().default("auto"),
  webhookSecret: text("webhook_secret"),
});

export const providerCredentials = pgTable(
  "provider_credentials",
  {
    id: text("id").primaryKey(),
    userId: text("user_id").notNull(),
    provider: text("provider").notNull(),
    label: text("label").notNull(),
    baseUrl: text("base_url"),
    ciphertext: bytea("ciphertext").notNull(),
    nonce: bytea("nonce").notNull(),
    keyVersion: integer("key_version").notNull(),
    fingerprint: text("fingerprint").notNull(),
    status: text("status").notNull().default("active"),
    lastVerifiedAt: timestamp("last_verified_at", { withTimezone: true }),
    lastErrorCode: text("last_error_code"),
    createdAt: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),
    updatedAt: timestamp("updated_at", { withTimezone: true }).notNull().defaultNow(),
  },
  (table) => [index("idx_cred_user").on(table.userId, table.provider)],
);

export const modelRegistry = pgTable(
  "model_registry",
  {
    id: text("id").primaryKey(),
    ownerId: text("owner_id").notNull(),
    ref: text("ref").notNull(),
    kind: text("kind").notNull(),
    spec: jsonb("spec").$type<Record<string, unknown>>().notNull(),
    createdAt: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),
    updatedAt: timestamp("updated_at", { withTimezone: true }).notNull().defaultNow(),
  },
  (table) => [uniqueIndex("uk_registry_owner_ref").on(table.ownerId, table.ref)],
);

export const modelSlotBindings = pgTable(
  "model_slot_bindings",
  {
    ownerId: text("owner_id").notNull(),
    slot: text("slot").notNull(),
    targetRef: text("target_ref").notNull(),
    updatedAt: timestamp("updated_at", { withTimezone: true }).notNull().defaultNow(),
  },
  (table) => [primaryKey({ columns: [table.ownerId, table.slot] })],
);

export const folders = pgTable("folders", {
  id: text("id").primaryKey(),
  ownerId: text("owner_id").notNull(),
  name: text("name").notNull(),
  parentId: text("parent_id"),
  createdAt: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),
});

const vector1024 = customType<{ data: number[]; driverData: string }>({
  dataType() {
    return "vector(1024)";
  },
  toDriver(value) {
    return `[${value.join(",")}]`;
  },
  fromDriver(value) {
    const textValue = String(value).replace(/^\[|\]$/g, "");
    if (!textValue) return [];
    return textValue.split(",").map((part) => Number(part));
  },
});

export const sources = pgTable("sources", {
  id: text("id").primaryKey(),
  ownerId: text("owner_id").notNull(),
  kind: text("kind").notNull(),
  title: text("title").notNull(),
  url: text("url"),
  publisher: text("publisher"),
  publishedAt: timestamp("published_at", { withTimezone: true }),
  authority: integer("authority").notNull(),
  folderId: text("folder_id"),
  feedId: text("feed_id"),
  feedGuid: text("feed_guid"),
  documentId: text("document_id"),
  indexedVersionId: text("indexed_version_id"),
  body: text("body").notNull(),
  revision: integer("revision").notNull(),
  contentHash: text("content_hash").notNull(),
  status: text("status").notNull(),
  lastError: text("last_error"),
  createdAt: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),
  updatedAt: timestamp("updated_at", { withTimezone: true }).notNull().defaultNow(),
});

export const feeds = pgTable(
  "feeds",
  {
    id: text("id").primaryKey(),
    ownerId: text("owner_id").notNull(),
    url: text("url").notNull(),
    folderId: text("folder_id").notNull(),
    title: text("title"),
    etag: text("etag"),
    lastModified: text("last_modified"),
    fullText: boolean("full_text").notNull().default(false),
    isHotspot: boolean("is_hotspot").notNull().default(false),
    maxItemsPerTick: integer("max_items_per_tick").notNull().default(3),
    retainDays: integer("retain_days").notNull().default(180),
    failCount: integer("fail_count").notNull().default(0),
    lastError: text("last_error"),
    lastFetchedAt: timestamp("last_fetched_at", { withTimezone: true }),
  },
  (table) => [uniqueIndex("uk_feed_owner_url").on(table.ownerId, table.url)],
);

export const subagentRuns = pgTable(
  "subagent_runs",
  {
    id: text("id").primaryKey(),
    turnId: text("turn_id").notNull(),
    parentId: text("parent_id"),
    sessionId: text("session_id").notNull(),
    piConversationId: text("pi_conversation_id"),
    piTaskId: text("pi_task_id").notNull(),
    agentRef: text("agent_ref").notNull(),
    slot: text("slot").notNull(),
    modelRef: text("model_ref"),
    input: jsonb("input").notNull(),
    output: jsonb("output"),
    status: text("status").notNull(),
    createdAt: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),
  },
  (table) => [
    uniqueIndex("uk_subagent_run_task").on(table.piTaskId),
    index("idx_subagent_run_turn").on(table.turnId),
  ],
);

export const researchPlans = pgTable("research_plans", {
  id: text("id").primaryKey(),
  turnId: text("turn_id").notNull().unique(),
  sessionId: text("session_id").notNull(),
  plan: jsonb("plan").notNull(),
  gaps: jsonb("gaps").notNull().default([]),
  createdAt: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),
});

export const evidence = pgTable("evidence", {
  id: text("id").primaryKey(),
  ownerId: text("owner_id").notNull(),
  citingDocumentId: text("citing_document_id").notNull(),
  sessionId: text("session_id"),
  turnId: text("turn_id"),
  planId: text("plan_id"),
  questionId: text("question_id"),
  sourceId: text("source_id").notNull(),
  sourceRevision: integer("source_revision").notNull(),
  chunkId: text("chunk_id"),
  citedVersionId: text("cited_version_id"),
  quote: text("quote").notNull(),
  locator: jsonb("locator").notNull(),
  authority: integer("authority").notNull(),
  citationStatus: text("citation_status").notNull().default("unverified"),
  createdAt: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),
});

export const chunks = pgTable("chunks", {
  id: text("id").primaryKey(),
  sourceId: text("source_id").notNull(),
  ownerId: text("owner_id").notNull(),
  ord: integer("ord").notNull(),
  text: text("text").notNull(),
  searchText: text("search_text").notNull(),
  embedding: vector1024("embedding"),
  embeddingModel: text("embedding_model"),
  charStart: integer("char_start").notNull(),
  charEnd: integer("char_end").notNull(),
  page: integer("page"),
  headingPath: text("heading_path").array(),
  blockIds: text("block_ids").array(),
});

export const usageLedger = pgTable("usage_ledger", {
  id: text("id").primaryKey(),
  requestId: text("request_id").notNull(),
  model: text("model").notNull(),
  inputTokens: integer("input_tokens").notNull(),
  outputTokens: integer("output_tokens").notNull(),
  costMicros: integer("cost_micros").notNull(),
  apiKeyId: text("api_key_id"),
  apiTaskId: text("api_task_id"),
  userId: text("user_id"),
  credentialId: text("credential_id"),
  modelRef: text("model_ref"),
  tierRef: text("tier_ref"),
  sessionId: text("session_id"),
  turnId: text("turn_id"),
  cacheReadTokens: integer("cache_read_tokens"),
  cacheWriteTokens: integer("cache_write_tokens"),
  priceKnown: boolean("price_known").notNull().default(true),
  createdAt: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),
});

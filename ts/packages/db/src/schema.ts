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

export const documents = pgTable("documents", {
  id: text("id").primaryKey(),
  ownerId: text("owner_id").notNull(),
  title: text("title").notNull(),
  genre: text("genre").notNull(),
  status: text("status").notNull(),
  headVersionId: text("head_version_id"),
  finalVersionId: text("final_version_id"),
  styleRef: text("style_ref").notNull(),
  tierRef: text("tier_ref").notNull(),
  brief: jsonb("brief").$type<Record<string, unknown>>().notNull(),
  authoredByMe: boolean("authored_by_me").notNull(),
  createdVia: text("created_via").notNull(),
  humanTouched: boolean("human_touched").notNull(),
  derivedFromDocumentId: text("derived_from_document_id"),
  derivedFromVersionId: text("derived_from_version_id"),
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

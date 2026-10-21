import { createHash, randomBytes } from "node:crypto";

import type { Actor } from "@luminwrite/contracts";

import type { ApiKeyRecord, LwDatabase } from "./database.js";
import { newId } from "./ids.js";

const BASE62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz";

export const API_SCOPES = [
  "tasks:read",
  "tasks:write",
  "documents:read",
  "documents:write",
  "library:read",
  "sources:read",
  "kb:write",
] as const;

export type ApiScope = (typeof API_SCOPES)[number];

/** 默认不含 kb:write。外部 Agent 不能往知识库加 URL，除非所有者显式勾上。 */
export const DEFAULT_API_SCOPES: readonly ApiScope[] = [
  "tasks:read",
  "tasks:write",
  "documents:read",
  "documents:write",
  "library:read",
  "sources:read",
];

const TOKEN_PATTERN = /^lw_[0-9A-Za-z]{8}_[0-9A-Za-z]{43}$/;

export class ApiKeyInputError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "ApiKeyInputError";
  }
}

export interface ApiKeyPolicyInput {
  maxConcurrentTasks?: number | null;
  dailyCostCapUsd?: number | null;
  allowedTiers?: readonly string[] | null;
  defaultApplyMode?: "auto" | "proposal";
  webhookSecret?: string | null;
}

/** 列表和后续请求能看到的字段。没有哈希，没有 webhook 密钥，没有明文。 */
export interface ApiKeyView {
  id: string;
  userId: string;
  name: string;
  prefix: string;
  scopes: string[];
  createdAt: string;
  lastUsedAt: string | null;
  revokedAt: string | null;
  maxConcurrentTasks: number | null;
  dailyCostCapUsd: number | null;
  allowedTiers: string[] | null;
  defaultApplyMode: "auto" | "proposal";
}

export interface IssuedApiKey extends ApiKeyView {
  token: string;
}

export function hashApiKey(token: string): string {
  return createHash("sha256").update(token, "utf8").digest("hex");
}

export function isApiKeyToken(token: string): boolean {
  return TOKEN_PATTERN.test(token);
}

function encodeBase62(bytes: Uint8Array): string {
  let value = 0n;
  for (const byte of bytes) value = (value << 8n) + BigInt(byte);
  if (value === 0n) return "0";
  let out = "";
  while (value > 0n) {
    out = BASE62[Number(value % 62n)] + out;
    value /= 62n;
  }
  return out;
}

function randomBase62(byteLength: number, width: number): string {
  const encoded = encodeBase62(randomBytes(byteLength));
  if (encoded.length > width) throw new Error("base62 超出预期长度");
  return encoded.padStart(width, "0");
}

function assertScopes(scopes: readonly string[]): ApiScope[] {
  const seen = new Set<string>();
  const out: ApiScope[] = [];
  for (const scope of scopes) {
    if (!(API_SCOPES as readonly string[]).includes(scope)) {
      throw new ApiKeyInputError(`不认识的权限 ${scope}`);
    }
    if (seen.has(scope)) continue;
    seen.add(scope);
    out.push(scope as ApiScope);
  }
  return out;
}

function assertPolicy(input: ApiKeyPolicyInput): {
  maxConcurrentTasks: number | null;
  dailyCostCapUsd: number | null;
  allowedTiers: string[] | null;
  defaultApplyMode: "auto" | "proposal";
  webhookSecret: string | null;
} {
  const max = input.maxConcurrentTasks ?? null;
  if (max !== null && (!Number.isInteger(max) || max < 1)) {
    throw new ApiKeyInputError("并发上限必须是正整数");
  }
  const cap = input.dailyCostCapUsd ?? null;
  if (cap !== null && (!Number.isFinite(cap) || cap < 0)) {
    throw new ApiKeyInputError("当日成本上限必须是非负数字");
  }
  let allowedTiers: string[] | null = null;
  if (input.allowedTiers != null) {
    allowedTiers = [];
    for (const tier of input.allowedTiers) {
      const trimmed = tier.trim();
      if (!trimmed) throw new ApiKeyInputError("档位名称不能是空的");
      if (!allowedTiers.includes(trimmed)) allowedTiers.push(trimmed);
    }
  }
  const mode = input.defaultApplyMode ?? "auto";
  if (mode !== "auto" && mode !== "proposal") {
    throw new ApiKeyInputError("默认落稿方式只能是 auto 或 proposal");
  }
  const secret = input.webhookSecret ?? null;
  if (secret !== null && (secret.length < 1 || secret.length > 256)) {
    throw new ApiKeyInputError("webhook 密钥长度要在 1 到 256 之间");
  }
  return {
    maxConcurrentTasks: max,
    dailyCostCapUsd: cap,
    allowedTiers,
    defaultApplyMode: mode,
    webhookSecret: secret,
  };
}

function toView(row: ApiKeyRecord): ApiKeyView {
  return {
    id: row.id,
    userId: row.userId,
    name: row.name,
    prefix: row.prefix,
    scopes: row.scopes,
    createdAt: row.createdAt.toISOString(),
    lastUsedAt: row.lastUsedAt ? row.lastUsedAt.toISOString() : null,
    revokedAt: row.revokedAt ? row.revokedAt.toISOString() : null,
    maxConcurrentTasks: row.maxConcurrentTasks,
    dailyCostCapUsd: row.dailyCostCapUsd,
    allowedTiers: row.allowedTiers,
    defaultApplyMode: row.defaultApplyMode,
  };
}

export async function createApiKey(
  db: LwDatabase,
  input: { userId: string; name: string; scopes?: readonly string[] } & ApiKeyPolicyInput,
): Promise<IssuedApiKey> {
  const name = input.name.trim();
  if (name.length < 1 || name.length > 128)
    throw new ApiKeyInputError("密钥名称要在 1 到 128 字之间");
  const scopes = input.scopes ? assertScopes(input.scopes) : [...DEFAULT_API_SCOPES];
  const policy = assertPolicy(input);
  const prefix = randomBase62(5, 8);
  const secret = randomBase62(32, 43);
  const token = `lw_${prefix}_${secret}`;
  const id = newId("key");
  await db.insertApiKey({
    id,
    userId: input.userId,
    name,
    prefix,
    hash: hashApiKey(token),
    scopes,
    ...policy,
  });
  const stored = await db.getApiKey(id);
  if (!stored) throw new Error("密钥写入后找不到");
  return { ...toView(stored), token };
}

export async function listApiKeys(db: LwDatabase, userId: string): Promise<ApiKeyView[]> {
  const rows = await db.listApiKeys(userId);
  return rows.map(toView);
}

export async function revokeApiKey(
  db: LwDatabase,
  input: { id: string; userId: string },
): Promise<"revoked" | "missing"> {
  return db.revokeApiKey(input.id, input.userId);
}

/** 命中未吊销的密钥时返回 api_client。明文不入库，吊销后和未知密钥一样返回 null。 */
export async function authenticateApiKey(db: LwDatabase, token: string): Promise<Actor | null> {
  if (!isApiKeyToken(token)) return null;
  const row = await db.findApiKeyByHash(hashApiKey(token));
  if (!row || row.revokedAt) return null;
  await db.touchApiKey(row.id);
  return { type: "api_client", userId: row.userId, keyId: row.id };
}

export async function readActiveApiKey(
  db: LwDatabase,
  id: string,
): Promise<ApiKeyView | undefined> {
  const row = await db.getApiKey(id);
  if (!row || row.revokedAt) return undefined;
  return toView(row);
}

import type { ApiKeyView, ApiScope, LwDatabase } from "@luminwrite/db";
import { readActiveApiKey } from "@luminwrite/db";
import { humanTouchedProposalPolicy } from "@luminwrite/edition";

import type { TokenBucketLimiter } from "./rate-limit.js";

const OWNER_ONLY_PREFIXES = [
  "/me/api-keys",
  "/me/credentials",
  "/me/writing-notes",
  "/me/preferences",
  "/me/model-slots",
  "/me/feeds",
  "/me/subscriptions",
  "/feeds",
  "/subscriptions",
];

export function normalizeApiPath(path: string): string {
  const withoutQuery = path.split("?")[0] ?? path;
  const stripped = withoutQuery.startsWith("/api/v1")
    ? withoutQuery.slice("/api/v1".length)
    : withoutQuery;
  if (stripped.length > 1 && stripped.endsWith("/")) return stripped.slice(0, -1);
  return stripped || "/";
}

function hasPrefix(path: string, prefix: string): boolean {
  return path === prefix || path.startsWith(`${prefix}/`);
}

export function isOwnerOnlyPath(path: string): boolean {
  return OWNER_ONLY_PREFIXES.some((prefix) => hasPrefix(path, prefix));
}

/** 现有路由需要的 scope。还没实现的任务、文库、信源也先按 scope 归类，缺权限是 403 而不是 404。 */
export function requiredScope(method: string, path: string): ApiScope | "owner-only" | "deny" {
  const normalized = normalizeApiPath(path);
  if (isOwnerOnlyPath(normalized)) return "owner-only";
  const verb = method.toUpperCase();
  if (hasPrefix(normalized, "/tasks")) {
    return verb === "GET" ? "tasks:read" : "tasks:write";
  }
  if (hasPrefix(normalized, "/library")) return "library:read";
  if (hasPrefix(normalized, "/kb")) return "kb:write";
  if (hasPrefix(normalized, "/sources")) {
    return verb === "GET" ? "sources:read" : "kb:write";
  }
  if (/^\/documents\/[^/]+\/events(\/stream)?$/.test(normalized)) {
    return verb === "GET" ? "tasks:read" : "deny";
  }
  if (/^\/documents\/[^/]+\/turns$/.test(normalized)) {
    return verb === "POST" ? "tasks:write" : "deny";
  }
  if (hasPrefix(normalized, "/documents") || hasPrefix(normalized, "/sessions")) {
    return verb === "GET" ? "documents:read" : "documents:write";
  }
  return "deny";
}

export interface ApiKeyDenial {
  status: 401 | 403 | 429;
  error: string;
  retryAfter?: number;
}

/**
 * 外部 Agent 的落稿方式。
 * human_touched 文档默认提案（D10）。显式 auto 只有在密钥策略也是 auto 时才生效。
 * 策略本身是 proposal 时，请求里的 auto 会被抬成提案。
 */
export function resolveApiApplyMode(input: {
  requested?: "auto" | "proposal";
  humanTouched: boolean;
  policyDefault: "auto" | "proposal";
}): "auto" | "proposal" {
  if (input.humanTouched) {
    if (input.requested === "auto" && input.policyDefault === "auto") return "auto";
    return humanTouchedProposalPolicy.defaultApplyMode({ humanTouched: true });
  }
  if (input.requested === "proposal" || input.policyDefault === "proposal") return "proposal";
  return "auto";
}

export function utcDayStart(now = new Date()): Date {
  return new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate()));
}

export function secondsUntilUtcMidnight(now = new Date()): number {
  const next = Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate() + 1);
  return Math.max(1, Math.ceil((next - now.getTime()) / 1000));
}

export async function enforceApiKey(input: {
  db: LwDatabase;
  limiter: TokenBucketLimiter;
  keyId: string;
  method: string;
  path: string;
  now?: Date;
}): Promise<{ ok: true; key: ApiKeyView } | { ok: false; denial: ApiKeyDenial }> {
  const key = await readActiveApiKey(input.db, input.keyId);
  if (!key) return { ok: false, denial: { status: 401, error: "密钥已吊销或不存在" } };
  const scope = requiredScope(input.method, input.path);
  if (scope === "owner-only") {
    return { ok: false, denial: { status: 403, error: "API 密钥不能做这件事" } };
  }
  if (scope === "deny" || !key.scopes.includes(scope)) {
    return { ok: false, denial: { status: 403, error: "这把密钥没有这个权限" } };
  }
  const limited = input.limiter.take(input.keyId);
  if (!limited.ok) {
    return {
      ok: false,
      denial: {
        status: 429,
        error: "请求太频繁",
        retryAfter: limited.retryAfterSeconds,
      },
    };
  }
  if (key.dailyCostCapUsd != null) {
    const now = input.now ?? new Date();
    const spent = await input.db.usageCostMicrosSince(key.id, utcDayStart(now));
    const capMicros = Math.round(key.dailyCostCapUsd * 1_000_000);
    if (spent >= capMicros) {
      return {
        ok: false,
        denial: {
          status: 429,
          error: "已达到当日成本上限",
          retryAfter: secondsUntilUtcMidnight(now),
        },
      };
    }
  }
  return { ok: true, key };
}

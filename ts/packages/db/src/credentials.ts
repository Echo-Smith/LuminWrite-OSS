import type { CredentialRow, LwDatabase } from "./database.js";
import { newId } from "./ids.js";
import { Secret } from "./secret.js";
import { type MasterKeyring, openSecret, sealSecret } from "./vault.js";

export class CredentialInputError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "CredentialInputError";
  }
}

export interface CredentialView {
  id: string;
  provider: string;
  label: string;
  baseUrl: string | null;
  fingerprint: string;
  status: "active" | "disabled" | "invalid";
  lastVerifiedAt: string | null;
  lastErrorCode: string | null;
  createdAt: string;
  updatedAt: string;
}

const PROVIDER_PATTERN = /^[a-z0-9][a-z0-9._-]{0,63}$/;

function assertProvider(provider: string): string {
  const value = provider.trim();
  if (!PROVIDER_PATTERN.test(value)) throw new CredentialInputError("供应商 id 不合法");
  return value;
}

function assertLabel(label: string): string {
  const value = label.trim();
  if (value.length < 1 || value.length > 128) {
    throw new CredentialInputError("凭据名称要在 1 到 128 字之间");
  }
  return value;
}

function assertKey(key: string): Secret {
  if (key.length < 1 || key.length > 4096) throw new CredentialInputError("密钥长度不合法");
  return new Secret(key);
}

/** https 任意公网地址，http 只允许回环。局域网地址要显式打开。不做 DNS 解析，那是 safeFetch 的事。 */
export function assertCredentialBaseUrl(raw: string, env: NodeJS.ProcessEnv = process.env): string {
  let url: URL;
  try {
    url = new URL(raw);
  } catch {
    throw new CredentialInputError("端点地址不合法");
  }
  if (url.username || url.password) throw new CredentialInputError("端点地址不能带账号");
  const host = url.hostname.toLowerCase().replace(/^\[|\]$/g, "");
  const loopback = host === "localhost" || host === "127.0.0.1" || host === "::1";
  if (url.protocol === "http:") {
    if (!loopback) throw new CredentialInputError("http 端点只允许本机回环地址");
    return url.toString();
  }
  if (url.protocol !== "https:") throw new CredentialInputError("端点只允许 https，或本机 http");
  if (isPrivateHost(host) && env.LW_ALLOW_PRIVATE_MODEL_ENDPOINTS !== "1") {
    throw new CredentialInputError("局域网端点需要 LW_ALLOW_PRIVATE_MODEL_ENDPOINTS=1");
  }
  return url.toString();
}

function isPrivateHost(host: string): boolean {
  if (host.endsWith(".local") || host.endsWith(".internal")) return true;
  const match = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(host);
  if (!match) return false;
  const a = Number(match[1]);
  const b = Number(match[2]);
  if (a === 10 || a === 0) return true;
  if (a === 127) return false;
  if (a === 169 && b === 254) return true;
  if (a === 172 && b >= 16 && b <= 31) return true;
  if (a === 192 && b === 168) return true;
  if (a === 100 && b >= 64 && b <= 127) return true;
  return false;
}

function toView(row: CredentialRow): CredentialView {
  return {
    id: row.id,
    provider: row.provider,
    label: row.label,
    baseUrl: row.baseUrl,
    fingerprint: row.fingerprint,
    status: row.status,
    lastVerifiedAt: row.lastVerifiedAt ? row.lastVerifiedAt.toISOString() : null,
    lastErrorCode: row.lastErrorCode,
    createdAt: row.createdAt.toISOString(),
    updatedAt: row.updatedAt.toISOString(),
  };
}

export async function createCredential(
  db: LwDatabase,
  ring: MasterKeyring,
  input: {
    userId: string;
    provider: string;
    label: string;
    key: string;
    baseUrl?: string | null;
    env?: NodeJS.ProcessEnv;
  },
): Promise<CredentialView> {
  const provider = assertProvider(input.provider);
  const label = assertLabel(input.label);
  const secret = assertKey(input.key);
  const baseUrl = input.baseUrl
    ? assertCredentialBaseUrl(input.baseUrl, input.env ?? process.env)
    : null;
  const id = newId("cred");
  const sealed = sealSecret(secret, { userId: input.userId, id, provider }, ring);
  await db.insertCredential({
    id,
    userId: input.userId,
    provider,
    label,
    baseUrl,
    status: "active",
    ...sealed,
  });
  const stored = await db.getCredential(id);
  if (!stored) throw new Error("凭据写入后找不到");
  return toView(stored);
}

export async function listCredentialViews(
  db: LwDatabase,
  userId: string,
): Promise<CredentialView[]> {
  const rows = await db.listCredentials(userId);
  return rows.map(toView);
}

export async function updateCredential(
  db: LwDatabase,
  ring: MasterKeyring,
  input: {
    id: string;
    userId: string;
    label?: string;
    key?: string;
    baseUrl?: string | null;
    status?: "active" | "disabled";
    env?: NodeJS.ProcessEnv;
  },
): Promise<CredentialView | undefined> {
  const existing = await db.getCredential(input.id);
  if (!existing || existing.userId !== input.userId) return undefined;
  const label = input.label !== undefined ? assertLabel(input.label) : existing.label;
  let baseUrl = existing.baseUrl;
  if (input.baseUrl !== undefined) {
    baseUrl = input.baseUrl
      ? assertCredentialBaseUrl(input.baseUrl, input.env ?? process.env)
      : null;
  }
  let status = input.status ?? existing.status;
  if (input.key) {
    const secret = assertKey(input.key);
    const sealed = sealSecret(
      secret,
      { userId: existing.userId, id: existing.id, provider: existing.provider },
      ring,
    );
    await db.updateCredentialSeal(existing.id, sealed);
    if (!input.status) status = "active";
  }
  await db.updateCredentialMeta(existing.id, { label, baseUrl, status });
  const stored = await db.getCredential(existing.id);
  return stored ? toView(stored) : undefined;
}

export async function deleteCredential(
  db: LwDatabase,
  input: { id: string; userId: string },
): Promise<"deleted" | "missing"> {
  return db.deleteCredential(input.id, input.userId);
}

/** 确认这行还能解开。不访问模型，也不把原文交出去。 */
export async function verifyCredential(
  db: LwDatabase,
  ring: MasterKeyring,
  input: { id: string; userId: string },
): Promise<CredentialView | undefined> {
  const existing = await db.getCredential(input.id);
  if (!existing || existing.userId !== input.userId) return undefined;
  try {
    openSecret(existing, ring);
    await db.updateCredentialMeta(existing.id, {
      lastVerifiedAt: new Date(),
      lastErrorCode: null,
      ...(existing.status === "invalid" ? { status: "active" } : {}),
    });
  } catch {
    await db.updateCredentialMeta(existing.id, {
      status: "invalid",
      lastErrorCode: "decrypt_failed",
      lastVerifiedAt: new Date(),
    });
  }
  const stored = await db.getCredential(existing.id);
  return stored ? toView(stored) : undefined;
}

export function revealCredential(row: CredentialRow, ring: MasterKeyring): Secret {
  return openSecret(row, ring);
}

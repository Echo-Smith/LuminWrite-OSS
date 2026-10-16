import { createHash, randomBytes, timingSafeEqual } from "node:crypto";
import { chmod, mkdir, open, readFile, rename, rm } from "node:fs/promises";
import { join } from "node:path";

import type { LwDatabase } from "./database.js";
import { newId } from "./ids.js";

const TOKEN_PREFIX = "lwo_";

export class OwnerNotFound extends Error {
  constructor() {
    super("还没有所有者");
    this.name = "OwnerNotFound";
  }
}

export function hashOwnerToken(token: string): string {
  return createHash("sha256").update(token, "utf8").digest("hex");
}

export function newOwnerToken(): string {
  return `${TOKEN_PREFIX}${randomBytes(32).toString("base64url")}`;
}

export function ownerTokenPath(dataDir: string): string {
  return join(dataDir, "owner.token");
}

export async function readOwnerToken(dataDir: string): Promise<string> {
  const token = (await readFile(ownerTokenPath(dataDir), "utf8")).trim();
  if (!token) throw new Error("所有者令牌文件是空的");
  return token;
}

async function writeTokenFile(path: string, token: string): Promise<void> {
  const handle = await open(path, "w", 0o600);
  try {
    await handle.writeFile(`${token}\n`);
    await handle.chmod(0o600);
  } finally {
    await handle.close();
  }
  await chmod(path, 0o600);
}

/** 把令牌写到 dataDir/owner.token，权限 0600。目录权限 0700。 */
export async function writeOwnerTokenFile(dataDir: string, token: string): Promise<string> {
  await mkdir(dataDir, { recursive: true, mode: 0o700 });
  await chmod(dataDir, 0o700);
  const path = ownerTokenPath(dataDir);
  const tmp = join(dataDir, `.owner.token.${randomBytes(6).toString("hex")}`);
  await writeTokenFile(tmp, token);
  await rename(tmp, path);
  await chmod(path, 0o600);
  return path;
}

export async function authenticateOwnerToken(
  db: LwDatabase,
  token: string,
): Promise<{ id: string } | undefined> {
  const owner = await db.getOwner();
  if (!owner) return undefined;
  const expected = Buffer.from(owner.tokenHash, "hex");
  const actual = Buffer.from(hashOwnerToken(token), "hex");
  if (expected.length === 0 || expected.length !== actual.length) return undefined;
  if (!timingSafeEqual(expected, actual)) return undefined;
  return { id: owner.id };
}

/**
 * 首次调用建唯一的所有者，并把令牌写进文件。已有所有者时不换令牌。
 * 令牌明文不返回，调用方从令牌文件读取。
 */
export async function ensureOwner(
  db: LwDatabase,
  options: { dataDir: string; displayName?: string },
): Promise<{ userId: string; created: boolean; tokenPath: string }> {
  const tokenPath = ownerTokenPath(options.dataDir);
  const existing = await db.getOwner();
  if (existing) return { userId: existing.id, created: false, tokenPath };
  const token = newOwnerToken();
  const id = newId("usr");
  const outcome = await db.insertOwner({
    id,
    displayName: options.displayName ?? "所有者",
    tokenHash: hashOwnerToken(token),
  });
  if (outcome === "exists") {
    const winner = await db.getOwner();
    if (!winner) throw new Error("所有者行消失了");
    return { userId: winner.id, created: false, tokenPath };
  }
  try {
    await writeOwnerTokenFile(options.dataDir, token);
  } catch (error) {
    await db.deleteOwner(id);
    throw error;
  }
  return { userId: id, created: true, tokenPath };
}

/** 换令牌。旧令牌立刻失效。新令牌只写在文件里。 */
export async function rotateOwnerToken(
  db: LwDatabase,
  options: { dataDir: string },
): Promise<{ userId: string; tokenPath: string }> {
  const existing = await db.getOwner();
  if (!existing) throw new OwnerNotFound();
  const token = newOwnerToken();
  await mkdir(options.dataDir, { recursive: true, mode: 0o700 });
  await chmod(options.dataDir, 0o700);
  const tmp = join(options.dataDir, `.owner.token.${randomBytes(6).toString("hex")}`);
  await writeTokenFile(tmp, token);
  try {
    await db.updateOwnerTokenHash(existing.id, hashOwnerToken(token));
  } catch (error) {
    await rm(tmp, { force: true });
    throw error;
  }
  const path = ownerTokenPath(options.dataDir);
  await rename(tmp, path);
  await chmod(path, 0o600);
  return { userId: existing.id, tokenPath: path };
}

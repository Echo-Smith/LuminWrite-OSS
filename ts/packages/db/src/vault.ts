import { createCipheriv, createDecipheriv, createHmac, hkdfSync, randomBytes } from "node:crypto";
import { chmod, mkdir, open, readFile, rename } from "node:fs/promises";
import { join } from "node:path";

import type { CredentialRow, LwDatabase } from "./database.js";
import { Secret } from "./secret.js";

const KEY_BYTES = 32;
const NONCE_BYTES = 12;
const TAG_BYTES = 16;

export class CredentialCryptoError extends Error {
  constructor(message = "凭据解不开") {
    super(message);
    this.name = "CredentialCryptoError";
  }
}

export interface MasterKeyring {
  currentVersion: number;
  keys: ReadonlyMap<number, Buffer>;
  source: "env" | "file";
  path?: string;
}

export function secretsKeyPath(dataDir: string): string {
  return join(dataDir, "secrets.key");
}

export function decodeMasterKey(raw: string): Buffer {
  const trimmed = raw.trim();
  if (!trimmed) throw new Error("LW_SECRETS_KEY 是空的");
  const decoded =
    trimmed.includes("+") || trimmed.includes("/")
      ? Buffer.from(trimmed, "base64")
      : Buffer.from(trimmed, "base64url");
  if (decoded.length !== KEY_BYTES) throw new Error("LW_SECRETS_KEY 必须是 32 字节的 base64");
  return decoded;
}

interface KeyFile {
  current: number;
  keys: Record<string, string>;
}

function parseKeyFile(text: string): MasterKeyring {
  let parsed: KeyFile;
  try {
    parsed = JSON.parse(text) as KeyFile;
  } catch {
    throw new Error("secrets.key 不是合法的密钥文件");
  }
  if (!parsed || typeof parsed.current !== "number" || !parsed.keys) {
    throw new Error("secrets.key 缺少版本");
  }
  const keys = new Map<number, Buffer>();
  for (const [version, encoded] of Object.entries(parsed.keys)) {
    const key = decodeMasterKey(encoded);
    keys.set(Number(version), key);
  }
  const current = keys.get(parsed.current);
  if (!current) throw new Error("secrets.key 的当前版本不存在");
  return { currentVersion: parsed.current, keys, source: "file" };
}

function serializeKeyFile(ring: MasterKeyring): string {
  const keys: Record<string, string> = {};
  for (const [version, key] of ring.keys) keys[String(version)] = key.toString("base64");
  return `${JSON.stringify({ current: ring.currentVersion, keys })}\n`;
}

async function writeKeyFile(path: string, ring: MasterKeyring): Promise<void> {
  const dataDir = join(path, "..");
  await mkdir(dataDir, { recursive: true, mode: 0o700 });
  await chmod(dataDir, 0o700);
  const tmp = join(dataDir, `.secrets.key.${randomBytes(6).toString("hex")}`);
  const handle = await open(tmp, "w", 0o600);
  try {
    await handle.writeFile(serializeKeyFile(ring));
    await handle.chmod(0o600);
  } finally {
    await handle.close();
  }
  await chmod(tmp, 0o600);
  await rename(tmp, path);
  await chmod(path, 0o600);
}

export async function loadMasterKey(options: {
  dataDir: string;
  env?: NodeJS.ProcessEnv;
}): Promise<{ ring: MasterKeyring; created: boolean; warning?: string }> {
  const path = secretsKeyPath(options.dataDir);
  try {
    const text = await readFile(path, "utf8");
    return { ring: { ...parseKeyFile(text), path }, created: false };
  } catch (error) {
    const code = error && typeof error === "object" && "code" in error ? error.code : undefined;
    if (code !== "ENOENT") throw error;
  }
  const fromEnv = options.env?.LW_SECRETS_KEY?.trim();
  if (fromEnv) {
    return {
      ring: { currentVersion: 1, keys: new Map([[1, decodeMasterKey(fromEnv)]]), source: "env" },
      created: false,
    };
  }
  const ring: MasterKeyring = {
    currentVersion: 1,
    keys: new Map([[1, randomBytes(KEY_BYTES)]]),
    source: "file",
    path,
  };
  await writeKeyFile(path, ring);
  return {
    ring,
    created: true,
    warning: `已生成主密钥 ${path}（权限 0600）。备份数据库时要另外备份这个文件，丢了只能重新录入 key。`,
  };
}

function currentKey(ring: MasterKeyring): Buffer {
  const key = ring.keys.get(ring.currentVersion);
  if (!key) throw new CredentialCryptoError("主密钥的当前版本不存在");
  return key;
}

function fingerprintOf(plaintext: string, master: Buffer): string {
  const derived = Buffer.from(
    hkdfSync(
      "sha256",
      master,
      Buffer.from("lw:cred:v1"),
      Buffer.from("lw:cred:fingerprint:v1"),
      32,
    ),
  );
  return createHmac("sha256", derived).update(plaintext, "utf8").digest("hex").slice(0, 12);
}

function aadFor(binding: { userId: string; id: string; provider: string }): Buffer {
  return Buffer.from(`lw:cred:v1:${binding.userId}:${binding.id}:${binding.provider}`, "utf8");
}

export function sealSecret(
  plaintext: Secret,
  binding: { userId: string; id: string; provider: string },
  ring: MasterKeyring,
): { ciphertext: Buffer; nonce: Buffer; keyVersion: number; fingerprint: string } {
  const master = currentKey(ring);
  const nonce = randomBytes(NONCE_BYTES);
  const cipher = createCipheriv("aes-256-gcm", master, nonce);
  cipher.setAAD(aadFor(binding));
  const body = Buffer.concat([cipher.update(plaintext.reveal(), "utf8"), cipher.final()]);
  const ciphertext = Buffer.concat([body, cipher.getAuthTag()]);
  return {
    ciphertext,
    nonce,
    keyVersion: ring.currentVersion,
    fingerprint: fingerprintOf(plaintext.reveal(), master),
  };
}

export function openSecret(row: CredentialRow, ring: MasterKeyring): Secret {
  const master = ring.keys.get(row.keyVersion);
  if (!master || row.ciphertext.length < TAG_BYTES || row.nonce.length !== NONCE_BYTES) {
    throw new CredentialCryptoError();
  }
  const tag = row.ciphertext.subarray(row.ciphertext.length - TAG_BYTES);
  const body = row.ciphertext.subarray(0, row.ciphertext.length - TAG_BYTES);
  try {
    const decipher = createDecipheriv("aes-256-gcm", master, row.nonce);
    decipher.setAAD(aadFor(row));
    decipher.setAuthTag(tag);
    const plaintext = Buffer.concat([decipher.update(body), decipher.final()]).toString("utf8");
    return new Secret(plaintext);
  } catch {
    throw new CredentialCryptoError();
  }
}

function pruned(ring: MasterKeyring): MasterKeyring {
  const key = currentKey(ring);
  return {
    currentVersion: ring.currentVersion,
    keys: new Map([[ring.currentVersion, key]]),
    source: "file",
    ...(ring.path ? { path: ring.path } : {}),
  };
}

/** 把还停在旧主密钥上的行重加密，成功后密钥文件只留当前版本。 */
export async function settleKeyring(
  db: LwDatabase,
  ring: MasterKeyring,
  dataDir: string,
): Promise<MasterKeyring> {
  const path = ring.path ?? secretsKeyPath(dataDir);
  const rows = await db.listCredentialRows();
  const stale = rows.some((row) => row.keyVersion !== ring.currentVersion);
  if (stale) {
    const withOld: MasterKeyring = { ...ring, source: "file", path };
    if (ring.source === "env" || !ring.path) await writeKeyFile(path, withOld);
    for (const row of rows) {
      if (row.keyVersion === ring.currentVersion) continue;
      const opened = openSecret(row, withOld);
      const sealed = sealSecret(opened, row, { ...withOld, currentVersion: ring.currentVersion });
      await db.updateCredentialSeal(row.id, sealed);
    }
  }
  const versions = new Set(ring.keys.keys());
  if (ring.source === "file" && (stale || versions.size > 1)) {
    const next = pruned({ ...ring, path });
    await writeKeyFile(path, next);
    return next;
  }
  return ring.path ? ring : { ...ring, path };
}

export async function rotateMasterKey(options: {
  db: LwDatabase;
  dataDir: string;
  env?: NodeJS.ProcessEnv;
}): Promise<{ ring: MasterKeyring; version: number; path: string }> {
  const loaded = await loadMasterKey(options);
  const path = secretsKeyPath(options.dataDir);
  const nextVersion = loaded.ring.currentVersion + 1;
  const keys = new Map(loaded.ring.keys);
  keys.set(nextVersion, randomBytes(KEY_BYTES));
  const expanded: MasterKeyring = {
    currentVersion: nextVersion,
    keys,
    source: "file",
    path,
  };
  await writeKeyFile(path, expanded);
  const ring = await settleKeyring(options.db, expanded, options.dataDir);
  return { ring, version: ring.currentVersion, path };
}

export async function ensureMasterKey(options: {
  db: LwDatabase;
  dataDir: string;
  env?: NodeJS.ProcessEnv;
}): Promise<{ ring: MasterKeyring; created: boolean; warning?: string }> {
  const loaded = await loadMasterKey(options);
  const ring = await settleKeyring(options.db, loaded.ring, options.dataDir);
  return {
    ring,
    created: loaded.created,
    ...(loaded.warning ? { warning: loaded.warning } : {}),
  };
}

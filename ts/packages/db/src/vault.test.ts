import { randomBytes } from "node:crypto";
import { mkdtemp, readFile, rm, stat } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { inspect } from "node:util";
import { describe, expect, it } from "vitest";

import { createCredential, revealCredential } from "./credentials.js";
import { openMemoryDatabase } from "./database.js";
import { ensureOwner } from "./owner.js";
import { Secret } from "./secret.js";
import {
  CredentialCryptoError,
  ensureMasterKey,
  openSecret,
  rotateMasterKey,
  secretsKeyPath,
} from "./vault.js";

describe("Secret", () => {
  it("除了 reveal 以外都打成 [redacted]", () => {
    const secret = new Secret("lwtest_visible_only_via_reveal");
    expect(secret.reveal()).toBe("lwtest_visible_only_via_reveal");
    expect(String(secret)).toBe("[redacted]");
    expect(JSON.stringify(secret)).toBe('"[redacted]"');
    expect(inspect(secret)).toBe("[redacted]");
    expect(JSON.stringify({ secret })).not.toContain("lwtest_visible_only_via_reveal");
  });
});

describe("凭据库", () => {
  it("加密往返，抄到别的行解不开，轮换后仍能解开", async () => {
    const dir = await mkdtemp(join(tmpdir(), "lw-vault-"));
    const dataDir = join(dir, "data");
    const db = await openMemoryDatabase();
    const key = `lwtest_${randomBytes(18).toString("hex")}`;
    try {
      const owner = await ensureOwner(db, { dataDir });
      const loaded = await ensureMasterKey({ db, dataDir, env: {} });
      expect(loaded.created).toBe(true);
      expect(loaded.warning).toContain("secrets.key");
      const fileMode = (await stat(secretsKeyPath(dataDir))).mode & 0o777;
      expect(fileMode).toBe(0o600);
      const keyFile = await readFile(secretsKeyPath(dataDir), "utf8");
      expect(keyFile).not.toContain(key);

      const created = await createCredential(db, loaded.ring, {
        userId: owner.userId,
        provider: "openai",
        label: "主 key",
        key,
        baseUrl: "https://api.example.com/v1",
      });
      expect(JSON.stringify(created)).not.toContain(key);
      const stored = await db.getCredential(created.id);
      if (!stored) throw new Error("没有写入");
      expect(stored.ciphertext.toString("utf8")).not.toContain(key);
      expect(revealCredential(stored, loaded.ring).reveal()).toBe(key);

      await db.insertCredential({
        id: "cred_copiedrow0001",
        userId: stored.userId,
        provider: stored.provider,
        label: "抄来的",
        baseUrl: stored.baseUrl,
        ciphertext: stored.ciphertext,
        nonce: stored.nonce,
        keyVersion: stored.keyVersion,
        fingerprint: stored.fingerprint,
        status: "active",
      });
      const copied = await db.getCredential("cred_copiedrow0001");
      if (!copied) throw new Error("没有抄到");
      expect(() => openSecret(copied, loaded.ring)).toThrow(CredentialCryptoError);
      await db.deleteCredential("cred_copiedrow0001", stored.userId);

      const before = Buffer.from(stored.ciphertext);
      const rotated = await rotateMasterKey({ db, dataDir, env: {} });
      const after = await db.getCredential(created.id);
      if (!after) throw new Error("轮换后丢了");
      expect(after.keyVersion).toBe(rotated.version);
      expect(after.ciphertext.equals(before)).toBe(false);
      expect(revealCredential(after, rotated.ring).reveal()).toBe(key);
      expect(() =>
        openSecret({ ...stored, ciphertext: before, keyVersion: stored.keyVersion }, rotated.ring),
      ).toThrow(CredentialCryptoError);
    } finally {
      await db.close();
      await rm(dir, { recursive: true, force: true });
    }
  });

  it("设置了 LW_SECRETS_KEY 就不生成密钥文件，也不读供应商环境变量", async () => {
    const dir = await mkdtemp(join(tmpdir(), "lw-vault-env-"));
    const dataDir = join(dir, "data");
    const db = await openMemoryDatabase();
    try {
      const owner = await ensureOwner(db, { dataDir });
      const env = {
        LW_SECRETS_KEY: randomBytes(32).toString("base64"),
        OPENAI_API_KEY: "lwtest_should_not_be_imported",
      };
      const loaded = await ensureMasterKey({ db, dataDir, env });
      expect(loaded.ring.source).toBe("env");
      expect(loaded.created).toBe(false);
      await expect(stat(secretsKeyPath(dataDir))).rejects.toThrow();
      const created = await createCredential(db, loaded.ring, {
        userId: owner.userId,
        provider: "openai",
        label: "手动",
        key: "lwtest_typed_by_owner",
        env,
      });
      const listed = await db.listCredentials(owner.userId);
      expect(JSON.stringify(listed)).not.toContain("lwtest_should_not_be_imported");
      expect(JSON.stringify(created)).not.toContain("lwtest_typed_by_owner");
      const row = listed[0];
      if (!row) throw new Error("没有凭据");
      expect(revealCredential(row, loaded.ring).reveal()).toBe("lwtest_typed_by_owner");
    } finally {
      await db.close();
      await rm(dir, { recursive: true, force: true });
    }
  });
});

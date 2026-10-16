import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

import {
  authenticateApiKey,
  createApiKey,
  DEFAULT_API_SCOPES,
  hashApiKey,
  listApiKeys,
  revokeApiKey,
} from "./api-keys.js";
import { openMemoryDatabase } from "./database.js";
import { ensureOwner } from "./owner.js";

describe("api keys", () => {
  it("只存哈希，明文只在创建时返回，吊销后认证失败", async () => {
    const dir = await mkdtemp(join(tmpdir(), "lw-keys-"));
    const db = await openMemoryDatabase();
    try {
      const owner = await ensureOwner(db, { dataDir: join(dir, "data") });
      const issued = await createApiKey(db, {
        userId: owner.userId,
        name: "外部 Agent",
        webhookSecret: "hook-secret",
        maxConcurrentTasks: 2,
        dailyCostCapUsd: 1.5,
        allowedTiers: ["balanced"],
        defaultApplyMode: "proposal",
      });
      expect(issued.token).toMatch(/^lw_[0-9A-Za-z]{8}_[0-9A-Za-z]{43}$/);
      expect(issued.scopes).toEqual([...DEFAULT_API_SCOPES]);
      expect(issued.scopes).not.toContain("kb:write");
      expect(issued).not.toHaveProperty("hash");
      expect(issued).not.toHaveProperty("webhookSecret");

      const stored = await db.findApiKeyByHash(hashApiKey(issued.token));
      expect(stored?.hash).toBe(hashApiKey(issued.token));
      expect(stored?.webhookSecret).toBe("hook-secret");
      expect(JSON.stringify(stored)).not.toContain(issued.token);
      const secret = issued.token.split("_")[2] ?? "";
      expect(secret.length).toBe(43);
      expect(JSON.stringify(stored)).not.toContain(secret);

      const listed = await listApiKeys(db, owner.userId);
      expect(JSON.stringify(listed)).not.toContain(issued.token);
      expect(JSON.stringify(listed)).not.toContain(secret);
      expect(JSON.stringify(listed)).not.toContain("hook-secret");
      expect(listed[0]).toMatchObject({
        id: issued.id,
        maxConcurrentTasks: 2,
        dailyCostCapUsd: 1.5,
        allowedTiers: ["balanced"],
        defaultApplyMode: "proposal",
      });

      expect(await authenticateApiKey(db, issued.token)).toEqual({
        type: "api_client",
        userId: owner.userId,
        keyId: issued.id,
      });
      const used = await db.getApiKey(issued.id);
      expect(used?.lastUsedAt).toBeInstanceOf(Date);

      expect(await revokeApiKey(db, { id: issued.id, userId: owner.userId })).toBe("revoked");
      expect(await authenticateApiKey(db, issued.token)).toBeNull();
      expect(await authenticateApiKey(db, "lw_notakey_stillnotakey")).toBeNull();
    } finally {
      await db.close();
      await rm(dir, { recursive: true, force: true });
    }
  });

  it("当日成本按 usage_ledger 汇总", async () => {
    const dir = await mkdtemp(join(tmpdir(), "lw-keys-cost-"));
    const db = await openMemoryDatabase();
    try {
      const owner = await ensureOwner(db, { dataDir: join(dir, "data") });
      const issued = await createApiKey(db, { userId: owner.userId, name: "成本" });
      await db.recordUsage({
        requestId: "req-1",
        model: "faux-1",
        inputTokens: 1,
        outputTokens: 1,
        costMicros: 2_500_000,
        apiKeyId: issued.id,
      });
      const since = new Date(Date.UTC(2020, 0, 1));
      expect(await db.usageCostMicrosSince(issued.id, since)).toBe(2_500_000);
      expect(await db.usageCostMicrosSince("key_other", since)).toBe(0);
    } finally {
      await db.close();
      await rm(dir, { recursive: true, force: true });
    }
  });
});

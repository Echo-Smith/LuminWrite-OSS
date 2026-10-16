import { randomBytes } from "node:crypto";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { revealCredential } from "./credentials.js";
import { openMemoryDatabase } from "./database.js";
import { importEnvCredential } from "./env-credential.js";
import { ensureOwner } from "./owner.js";
import { loadMasterKey } from "./vault.js";

describe("环境变量凭据", () => {
  it("没开 LW_LLM_ENABLE 时不读 OPENAI_API_KEY，打开后才加密导入", async () => {
    const dir = await mkdtemp(join(tmpdir(), "lw-env-cred-"));
    const dataDir = join(dir, "data");
    const db = await openMemoryDatabase();
    const key = `lwtest_${randomBytes(8).toString("hex")}`;
    try {
      const owner = await ensureOwner(db, { dataDir });
      const ring = (
        await loadMasterKey({
          dataDir,
          env: { LW_SECRETS_KEY: randomBytes(32).toString("base64") },
        })
      ).ring;
      expect(await importEnvCredential(db, ring, { OPENAI_API_KEY: key }, owner.userId)).toBe(
        "skipped",
      );
      expect(await db.listCredentials(owner.userId)).toEqual([]);
      expect(
        await importEnvCredential(
          db,
          ring,
          { LW_LLM_ENABLE: "1", LW_LLM_API_KEY: key },
          owner.userId,
        ),
      ).toBe("imported");
      const rows = await db.listCredentials(owner.userId);
      expect(rows).toHaveLength(1);
      const row = rows[0];
      if (!row) throw new Error("没有凭据");
      expect(row.label).toBe("env");
      expect(row.provider).toBe("openai");
      expect(row.ciphertext.toString("utf8")).not.toContain(key);
      expect(revealCredential(row, ring).reveal()).toBe(key);
      expect(
        await importEnvCredential(
          db,
          ring,
          { LW_LLM_ENABLE: "1", LW_LLM_API_KEY: key },
          owner.userId,
        ),
      ).toBe("skipped");
    } finally {
      await db.close();
      await rm(dir, { recursive: true, force: true });
    }
  });
});

import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
  authenticateApiKey,
  authenticateOwnerToken,
  hashApiKey,
  openDatabase,
  readOwnerToken,
} from "@luminwrite/db";
import { describe, expect, it } from "vitest";

import { runLw } from "./lw.js";

describe("lw", () => {
  it("init 写出令牌文件但不把令牌打到输出里，rotate 让旧令牌失效", async () => {
    const dir = await mkdtemp(join(tmpdir(), "lw-cli-"));
    const env = { LW_DATA_DIR: dir };
    let out = "";
    let err = "";
    const stdout = {
      write: (chunk: string) => {
        out += chunk;
      },
    };
    const stderr = {
      write: (chunk: string) => {
        err += chunk;
      },
    };
    expect(await runLw(["init"], env, stdout, stderr)).toBe(0);
    const token = await readOwnerToken(dir);
    expect(out).not.toContain(token);
    expect(err).not.toContain(token);
    expect(out).toContain("pglite");
    expect(out).toContain("owner.token");

    out = "";
    expect(await runLw(["owner", "rotate-token"], env, stdout, stderr)).toBe(0);
    const next = await readOwnerToken(dir);
    expect(out).not.toContain(next);
    expect(next).not.toBe(token);
    const db = await openDatabase({ kind: "pglite", dataDir: dir });
    expect(await authenticateOwnerToken(db, token)).toBeUndefined();
    expect(await authenticateOwnerToken(db, next)).toMatchObject({ id: expect.any(String) });
    await db.close();
    expect(await runLw(["nope"], env, stdout, stderr)).toBe(1);
    await rm(dir, { recursive: true, force: true });
  });

  it("keys create 只把明文打一次，list 和库里都没有", async () => {
    const dir = await mkdtemp(join(tmpdir(), "lw-keys-cli-"));
    const env = { LW_DATA_DIR: dir };
    let out = "";
    const stdout = {
      write: (chunk: string) => {
        out += chunk;
      },
    };
    const stderr = { write: () => {} };
    expect(await runLw(["init"], env, stdout, stderr)).toBe(0);
    out = "";
    expect(
      await runLw(
        [
          "keys",
          "create",
          "--name",
          "机器人",
          "--scope",
          "documents:read",
          "--webhook-secret",
          "lwtest_hook_secret",
        ],
        env,
        stdout,
        stderr,
      ),
    ).toBe(0);
    const tokenLine = out.split("\n").find((line) => line.startsWith("令牌 "));
    const token = tokenLine?.slice("令牌 ".length) ?? "";
    expect(token).toMatch(/^lw_[0-9A-Za-z]{8}_[0-9A-Za-z]{43}$/);
    const secret = token.split("_")[2] ?? "";
    out = "";
    expect(await runLw(["keys", "list"], env, stdout, stderr)).toBe(0);
    expect(out).not.toContain(token);
    expect(out).not.toContain(secret);
    expect(out).not.toContain("lwtest_hook_secret");
    expect(out).toContain("documents:read");
    const db = await openDatabase({ kind: "pglite", dataDir: dir });
    const actor = await authenticateApiKey(db, token);
    expect(actor?.type).toBe("api_client");
    const stored = await db.findApiKeyByHash(hashApiKey(token));
    expect(JSON.stringify(stored)).not.toContain(token);
    expect(JSON.stringify(stored)).not.toContain(secret);
    const id = actor && actor.type === "api_client" ? actor.keyId : "";
    await db.close();
    out = "";
    expect(await runLw(["keys", "revoke", id], env, stdout, stderr)).toBe(0);
    const again = await openDatabase({ kind: "pglite", dataDir: dir });
    expect(await authenticateApiKey(again, token)).toBeNull();
    await again.close();
    await rm(dir, { recursive: true, force: true });
  });
});

import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
  authenticateApiKey,
  authenticateOwnerToken,
  ensureMasterKey,
  hashApiKey,
  openDatabase,
  readOwnerToken,
  revealCredential,
} from "@luminwrite/db";
import { createKnowledgeBase } from "@luminwrite/kb";
import { createFeedService } from "@luminwrite/sources";
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

  it("credentials 不回显密钥，secrets rotate 之后仍能解开", async () => {
    const dir = await mkdtemp(join(tmpdir(), "lw-cred-cli-"));
    const env = { LW_DATA_DIR: dir };
    let out = "";
    const stdout = {
      write: (chunk: string) => {
        out += chunk;
      },
    };
    const stderr = { write: () => {} };
    expect(await runLw(["init"], env, stdout, stderr)).toBe(0);
    const key = "lwtest_cli_provider_secret";
    out = "";
    expect(
      await runLw(
        ["credentials", "add", "--provider", "openai", "--label", "主 key", "--key", key],
        env,
        stdout,
        stderr,
      ),
    ).toBe(0);
    expect(out).not.toContain(key);
    expect(out).toContain("指纹");
    out = "";
    expect(await runLw(["credentials", "list"], env, stdout, stderr)).toBe(0);
    expect(out).not.toContain(key);
    expect(out).toContain("openai");
    out = "";
    expect(await runLw(["secrets", "rotate"], env, stdout, stderr)).toBe(0);
    expect(out).toContain("已轮换");
    expect(out).not.toContain(key);
    const db = await openDatabase({ kind: "pglite", dataDir: dir });
    const secrets = await ensureMasterKey({ db, dataDir: dir, env });
    const owner = await db.getOwner();
    const rows = await db.listCredentials(owner?.id ?? "");
    const row = rows[0];
    if (!row) throw new Error("没有凭据");
    expect(revealCredential(row, secrets.ring).reveal()).toBe(key);
    await db.close();
    await rm(dir, { recursive: true, force: true });
  });

  it("feeds list、export、remove 不访问网络", async () => {
    const dir = await mkdtemp(join(tmpdir(), "lw-feeds-cli-"));
    const env = { LW_DATA_DIR: dir };
    let out = "";
    const stdout = {
      write: (chunk: string) => {
        out += chunk;
      },
    };
    const stderr = { write: () => {} };
    expect(await runLw(["init"], env, stdout, stderr)).toBe(0);
    const db = await openDatabase({ kind: "pglite", dataDir: dir });
    let feedId = "";
    try {
      const owner = await db.getOwner();
      if (!owner) throw new Error("没有所有者");
      const kb = createKnowledgeBase({ db });
      const feeds = createFeedService({
        db,
        kb,
        fetch: async () => {
          throw new Error("测试不访问网络");
        },
      });
      const feed = await feeds.subscribe(owner.id, { url: "https://example.com/feed.xml" });
      feedId = feed.id;
    } finally {
      await db.close();
    }
    out = "";
    expect(await runLw(["feeds", "list"], env, stdout, stderr)).toBe(0);
    expect(out).toContain(feedId);
    expect(out).toContain("https://example.com/feed.xml");
    out = "";
    expect(await runLw(["feeds", "export"], env, stdout, stderr)).toBe(0);
    expect(out).toContain("https://example.com/feed.xml");
    expect(await runLw(["feeds", "remove", feedId], env, stdout, stderr)).toBe(0);
    out = "";
    expect(await runLw(["feeds", "list"], env, stdout, stderr)).toBe(0);
    expect(out).toContain("没有订阅");
    await rm(dir, { recursive: true, force: true });
  });
});

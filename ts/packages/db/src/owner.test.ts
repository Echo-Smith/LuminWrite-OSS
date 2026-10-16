import { mkdtemp, rm, stat } from "node:fs/promises";
import { homedir, tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, it } from "vitest";

import { resolveDatabaseLocation } from "./config.js";
import { openDatabase, openMemoryDatabase } from "./database.js";
import {
  authenticateOwnerToken,
  ensureOwner,
  hashOwnerToken,
  OwnerNotFound,
  readOwnerToken,
  rotateOwnerToken,
} from "./owner.js";

const dirs: string[] = [];

afterEach(async () => {
  await Promise.all(dirs.splice(0).map((dir) => rm(dir, { recursive: true, force: true })));
});

async function tempDir(): Promise<string> {
  const dir = await mkdtemp(join(tmpdir(), "lw-owner-"));
  dirs.push(dir);
  return dir;
}

describe("所有者令牌", () => {
  it("只建一行，文件是 0600，库里只有哈希", async () => {
    const dir = await tempDir();
    const db = await openMemoryDatabase();
    const first = await ensureOwner(db, { dataDir: dir });
    expect(first.created).toBe(true);
    const token = await readOwnerToken(dir);
    const again = await ensureOwner(db, { dataDir: dir });
    expect(again).toEqual({ userId: first.userId, created: false, tokenPath: first.tokenPath });
    expect(await readOwnerToken(dir)).toBe(token);
    expect(token.startsWith("lwo_")).toBe(true);
    expect((await stat(join(dir, "owner.token"))).mode & 0o777).toBe(0o600);
    const owner = await db.getOwner();
    expect(owner?.id).toBe(first.userId);
    expect(owner?.tokenHash).toBe(hashOwnerToken(token));
    expect(owner?.tokenHash).not.toContain(token);
    expect(await authenticateOwnerToken(db, token)).toEqual({ id: first.userId });
    expect(await authenticateOwnerToken(db, `${token}x`)).toBeUndefined();
    expect(await db.insertOwner({ id: "usr_other", displayName: "另一个", tokenHash: "ab" })).toBe(
      "exists",
    );
    expect((await db.getOwner())?.id).toBe(first.userId);
    await db.close();
  });

  it("换令牌后旧令牌对不上", async () => {
    const dir = await tempDir();
    const db = await openMemoryDatabase();
    await ensureOwner(db, { dataDir: dir });
    const previous = await readOwnerToken(dir);
    await rotateOwnerToken(db, { dataDir: dir });
    const next = await readOwnerToken(dir);
    expect(next).not.toBe(previous);
    expect(await authenticateOwnerToken(db, previous)).toBeUndefined();
    expect((await authenticateOwnerToken(db, next))?.id).toBe((await db.getOwner())?.id);
    expect((await db.getOwner())?.tokenHash).toBe(hashOwnerToken(next));
    await db.close();
  });

  it("没有所有者时不能换令牌", async () => {
    const db = await openMemoryDatabase();
    await expect(rotateOwnerToken(db, { dataDir: await tempDir() })).rejects.toBeInstanceOf(
      OwnerNotFound,
    );
    await db.close();
  });
});

describe("数据库位置", () => {
  it("默认 PGlite，只有 LW_DATABASE_URL 才选 Postgres", () => {
    expect(resolveDatabaseLocation({})).toEqual({
      kind: "pglite",
      dataDir: join(homedir(), ".luminwrite"),
    });
    expect(
      resolveDatabaseLocation({
        LW_DATA_DIR: "/tmp/lw-data",
        DATABASE_URL: "postgres://other-project/db",
      }),
    ).toEqual({ kind: "pglite", dataDir: "/tmp/lw-data" });
    expect(
      resolveDatabaseLocation({
        LW_DATABASE_URL: "postgres://luminwrite/db",
        LW_DATA_DIR: "/data",
      }),
    ).toEqual({
      kind: "postgres",
      dataDir: "/data",
      databaseUrl: "postgres://luminwrite/db",
    });
  });

  it("文件版 PGlite 关掉再打开，文档还在", async () => {
    const dir = await tempDir();
    const first = await openDatabase({ kind: "pglite", dataDir: dir });
    const created = await first.createDocument({ title: "还在", ownerId: "user_1" });
    await first.close();
    const second = await openDatabase({ kind: "pglite", dataDir: dir });
    expect((await second.getDocument(created.id))?.title).toBe("还在");
    await second.close();
  });
});

import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { authenticateOwnerToken, openDatabase, readOwnerToken } from "@luminwrite/db";
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
});

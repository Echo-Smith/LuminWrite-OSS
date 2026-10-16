#!/usr/bin/env node
import { access } from "node:fs/promises";
import { pathToFileURL } from "node:url";
import {
  ensureOwner,
  openDatabase,
  pgliteDataDir,
  resolveDatabaseLocation,
  rotateOwnerToken,
} from "@luminwrite/db";
import { startServer } from "@luminwrite/server";

type Writer = { write(chunk: string): void };

export async function runLw(
  argv: readonly string[],
  env: NodeJS.ProcessEnv = process.env,
  stdout: Writer = process.stdout,
  stderr: Writer = process.stderr,
): Promise<number> {
  const [command, sub] = argv;
  try {
    if (command === "init") return await init(env, stdout);
    if (command === "owner" && sub === "rotate-token") return await rotate(env, stdout);
    if (command === "serve") return await serve(env, stdout, stderr);
  } catch (error) {
    stderr.write(`${error instanceof Error ? error.message : "命令失败"}\n`);
    return 1;
  }
  stderr.write("用法：lw init | lw owner rotate-token | lw serve\n");
  return 1;
}

async function init(env: NodeJS.ProcessEnv, stdout: Writer): Promise<number> {
  const location = resolveDatabaseLocation(env);
  const db = await openDatabase(location);
  try {
    const owner = await ensureOwner(db, { dataDir: location.dataDir });
    const where =
      location.kind === "postgres" ? "postgres" : `pglite ${pgliteDataDir(location.dataDir)}`;
    stdout.write(`所有者 ${owner.userId}\n`);
    stdout.write(`数据库 ${where}\n`);
    stdout.write(`令牌文件 ${owner.tokenPath}\n`);
    if (owner.created) {
      stdout.write("备份数据库时要另外备份令牌文件。文件丢了可以换一把新令牌。\n");
      return 0;
    }
    try {
      await access(owner.tokenPath);
    } catch {
      stdout.write("令牌文件不在。旧令牌仍然有效。运行 lw owner rotate-token 换一把新的。\n");
    }
    return 0;
  } finally {
    await db.close();
  }
}

async function rotate(env: NodeJS.ProcessEnv, stdout: Writer): Promise<number> {
  const location = resolveDatabaseLocation(env);
  const db = await openDatabase(location);
  try {
    const owner = await rotateOwnerToken(db, { dataDir: location.dataDir });
    stdout.write(`已更换所有者令牌。旧令牌失效。\n令牌文件 ${owner.tokenPath}\n`);
    return 0;
  } finally {
    await db.close();
  }
}

async function serve(env: NodeJS.ProcessEnv, stdout: Writer, stderr: Writer): Promise<number> {
  const running = await startServer(env);
  if (running.warning) stderr.write(`${running.warning}\n`);
  if (running.owner.created) {
    stdout.write(`已创建所有者 ${running.owner.userId}\n令牌文件 ${running.owner.tokenPath}\n`);
  }
  stdout.write(`监听 ${running.url}\n`);
  const shutdown = () => {
    void running.close();
  };
  process.on("SIGINT", shutdown);
  process.on("SIGTERM", shutdown);
  return 0;
}

const isMain =
  process.argv[1] !== undefined && import.meta.url === pathToFileURL(process.argv[1]).href;
if (isMain) {
  const code = await runLw(process.argv.slice(2));
  if (process.argv[2] !== "serve" || code !== 0) process.exit(code);
}

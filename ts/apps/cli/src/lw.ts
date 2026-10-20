#!/usr/bin/env node
import { access, readFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";
import {
  createApiKey,
  createCredential,
  deleteCredential,
  ensureMasterKey,
  ensureOwner,
  listApiKeys,
  listCredentialViews,
  OwnerNotFound,
  openDatabase,
  pgliteDataDir,
  resolveDatabaseLocation,
  revokeApiKey,
  rotateMasterKey,
  rotateOwnerToken,
  updateCredential,
} from "@luminwrite/db";
import { createKnowledgeBase } from "@luminwrite/kb";
import { startServer } from "@luminwrite/server";
import { createFeedService, safeFetch } from "@luminwrite/sources";

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
    if (command === "keys") return await keys(argv.slice(1), env, stdout);
    if (command === "credentials") return await credentials(argv.slice(1), env, stdout);
    if (command === "secrets" && sub === "rotate") return await rotateSecrets(env, stdout);
    if (command === "feeds") return await feedsCommand(argv.slice(1), env, stdout);
    if (command === "notes") return await notesCommand(argv.slice(1), env, stdout);
    if (command === "serve") return await serve(env, stdout, stderr);
  } catch (error) {
    stderr.write(`${error instanceof Error ? error.message : "命令失败"}\n`);
    return 1;
  }
  stderr.write(
    "用法：lw init | lw owner rotate-token | lw keys create|list|revoke | lw credentials add|list|update|remove | lw secrets rotate | lw feeds add|list|remove|import|export | lw notes list|add|review|correct|forget|export|explain | lw serve\n",
  );
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
    const secrets = await ensureMasterKey({ db, dataDir: location.dataDir, env });
    if (secrets.warning) stdout.write(`${secrets.warning}\n`);
    else if (secrets.ring.source === "env") stdout.write("主密钥来自 LW_SECRETS_KEY。\n");
    else if (secrets.ring.path) stdout.write(`主密钥文件 ${secrets.ring.path}\n`);
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

function parseFlags(argv: readonly string[]): {
  positionals: string[];
  flags: Map<string, string[]>;
} {
  const positionals: string[] = [];
  const flags = new Map<string, string[]>();
  for (let index = 0; index < argv.length; index++) {
    const arg = argv[index] ?? "";
    if (!arg.startsWith("--")) {
      positionals.push(arg);
      continue;
    }
    const eq = arg.indexOf("=");
    const name = (eq === -1 ? arg.slice(2) : arg.slice(2, eq)).trim();
    if (name === "clear-base-url" && eq === -1) {
      flags.set(name, ["true"]);
      continue;
    }
    const value = eq === -1 ? argv[++index] : arg.slice(eq + 1);
    if (!name || value === undefined) throw new Error(`缺少 --${name || "参数"} 的值`);
    const list = flags.get(name) ?? [];
    list.push(value);
    flags.set(name, list);
  }
  return { positionals, flags };
}

function flag(flags: Map<string, string[]>, name: string): string | undefined {
  const values = flags.get(name);
  return values?.[values.length - 1];
}

async function keys(
  argv: readonly string[],
  env: NodeJS.ProcessEnv,
  stdout: Writer,
): Promise<number> {
  const [sub, ...rest] = argv;
  const location = resolveDatabaseLocation(env);
  const db = await openDatabase(location);
  try {
    const owner = await db.getOwner();
    if (!owner) throw new OwnerNotFound();
    if (sub === "create") {
      const { flags } = parseFlags(rest);
      const name = flag(flags, "name");
      if (!name) throw new Error("用法：lw keys create --name <名称> [--scope <权限>]...");
      const maxRaw = flag(flags, "max-concurrent-tasks");
      const capRaw = flag(flags, "daily-cost-cap-usd");
      const mode = flag(flags, "default-apply-mode");
      if (mode !== undefined && mode !== "auto" && mode !== "proposal") {
        throw new Error("默认落稿方式只能是 auto 或 proposal");
      }
      const issued = await createApiKey(db, {
        userId: owner.id,
        name,
        ...(flags.get("scope") ? { scopes: flags.get("scope") } : {}),
        ...(maxRaw !== undefined ? { maxConcurrentTasks: Number(maxRaw) } : {}),
        ...(capRaw !== undefined ? { dailyCostCapUsd: Number(capRaw) } : {}),
        ...(flags.get("allowed-tier") ? { allowedTiers: flags.get("allowed-tier") } : {}),
        ...(mode === "auto" || mode === "proposal" ? { defaultApplyMode: mode } : {}),
        ...(flag(flags, "webhook-secret") !== undefined
          ? { webhookSecret: flag(flags, "webhook-secret") }
          : {}),
      });
      stdout.write(`密钥 ${issued.id}\n`);
      stdout.write(`名称 ${issued.name}\n`);
      stdout.write(`前缀 lw_${issued.prefix}\n`);
      stdout.write(`权限 ${issued.scopes.join(" ")}\n`);
      stdout.write(`令牌 ${issued.token}\n`);
      stdout.write("这是唯一一次显示令牌。请另行保存。\n");
      return 0;
    }
    if (sub === "list") {
      const rows = await listApiKeys(db, owner.id);
      if (rows.length === 0) {
        stdout.write("还没有密钥。\n");
        return 0;
      }
      for (const row of rows) {
        const state = row.revokedAt ? "已吊销" : "有效";
        stdout.write(
          `${row.id}\t${row.name}\tlw_${row.prefix}\t${state}\t${row.scopes.join(",")}\n`,
        );
      }
      return 0;
    }
    if (sub === "revoke") {
      const id = rest[0];
      if (!id || id.startsWith("--")) throw new Error("用法：lw keys revoke <id>");
      const outcome = await revokeApiKey(db, { id, userId: owner.id });
      if (outcome === "missing") throw new Error("找不到密钥");
      stdout.write(`已吊销 ${id}\n`);
      return 0;
    }
    throw new Error("用法：lw keys create --name <名称> | lw keys list | lw keys revoke <id>");
  } finally {
    await db.close();
  }
}

async function credentials(
  argv: readonly string[],
  env: NodeJS.ProcessEnv,
  stdout: Writer,
): Promise<number> {
  const [sub, ...rest] = argv;
  const location = resolveDatabaseLocation(env);
  const db = await openDatabase(location);
  try {
    const owner = await db.getOwner();
    if (!owner) throw new OwnerNotFound();
    const secrets = await ensureMasterKey({ db, dataDir: location.dataDir, env });
    if (sub === "list") {
      const rows = await listCredentialViews(db, owner.id);
      if (rows.length === 0) {
        stdout.write("还没有凭据。\n");
        return 0;
      }
      for (const row of rows) {
        const endpoint = row.baseUrl ?? "-";
        stdout.write(
          `${row.id}\t${row.provider}\t${row.label}\t${row.fingerprint}\t${row.status}\t${endpoint}\n`,
        );
      }
      return 0;
    }
    if (sub === "add") {
      const { flags } = parseFlags(rest);
      const provider = flag(flags, "provider");
      const label = flag(flags, "label");
      const key = flag(flags, "key");
      if (!provider || !label || !key) {
        throw new Error("用法：lw credentials add --provider <id> --label <名称> --key <密钥>");
      }
      const created = await createCredential(db, secrets.ring, {
        userId: owner.id,
        provider,
        label,
        key,
        ...(flag(flags, "base-url") ? { baseUrl: flag(flags, "base-url") } : {}),
        env,
      });
      stdout.write(`凭据 ${created.id}\n`);
      stdout.write(`供应商 ${created.provider}\n`);
      stdout.write(`名称 ${created.label}\n`);
      stdout.write(`指纹 ${created.fingerprint}\n`);
      stdout.write("密钥已加密保存，不会再显示。\n");
      return 0;
    }
    if (sub === "update") {
      const { positionals, flags } = parseFlags(rest);
      const id = positionals[0];
      if (!id) throw new Error("用法：lw credentials update <id> [--label] [--key] [--base-url]");
      const status = flag(flags, "status");
      if (status !== undefined && status !== "active" && status !== "disabled") {
        throw new Error("状态只能是 active 或 disabled");
      }
      const updated = await updateCredential(db, secrets.ring, {
        id,
        userId: owner.id,
        ...(flag(flags, "label") ? { label: flag(flags, "label") } : {}),
        ...(flag(flags, "key") ? { key: flag(flags, "key") } : {}),
        ...(flags.has("clear-base-url") ? { baseUrl: null } : {}),
        ...(flag(flags, "base-url") ? { baseUrl: flag(flags, "base-url") } : {}),
        ...(status === "active" || status === "disabled" ? { status } : {}),
        env,
      });
      if (!updated) throw new Error("找不到凭据");
      stdout.write(`已更新 ${updated.id}\n指纹 ${updated.fingerprint}\n`);
      return 0;
    }
    if (sub === "remove") {
      const id = rest[0];
      if (!id || id.startsWith("--")) throw new Error("用法：lw credentials remove <id>");
      const outcome = await deleteCredential(db, { id, userId: owner.id });
      if (outcome === "missing") throw new Error("找不到凭据");
      stdout.write(`已删除 ${id}\n`);
      return 0;
    }
    throw new Error("用法：lw credentials add|list|update|remove");
  } finally {
    await db.close();
  }
}

async function rotateSecrets(env: NodeJS.ProcessEnv, stdout: Writer): Promise<number> {
  const location = resolveDatabaseLocation(env);
  const db = await openDatabase(location);
  try {
    const owner = await db.getOwner();
    if (!owner) throw new OwnerNotFound();
    const rotated = await rotateMasterKey({ db, dataDir: location.dataDir, env });
    stdout.write(`主密钥已轮换到版本 ${rotated.version}。\n`);
    stdout.write(`密钥文件 ${rotated.path}\n`);
    stdout.write("备份数据库时要另外备份这个文件，丢了只能重新录入 key。\n");
    return 0;
  } finally {
    await db.close();
  }
}

async function feedsCommand(
  argv: readonly string[],
  env: NodeJS.ProcessEnv,
  stdout: Writer,
): Promise<number> {
  const [sub, ...rest] = argv;
  const location = resolveDatabaseLocation(env);
  const db = await openDatabase(location);
  try {
    const owner = await ensureOwner(db, { dataDir: location.dataDir });
    const kb = createKnowledgeBase({ db });
    const feeds = createFeedService({
      db,
      kb,
      fetch: (url, options) =>
        safeFetch(url, { ...options, allowPrivate: env.LW_ALLOW_PRIVATE_FETCH === "1" }),
    });
    if (sub === "list") {
      const rows = await feeds.list(owner.userId);
      if (rows.length === 0) stdout.write("没有订阅\n");
      for (const feed of rows) {
        stdout.write(`${feed.id} ${feed.title ?? ""} ${feed.url}\n`);
      }
      return 0;
    }
    if (sub === "add") {
      const url = rest.find((arg) => !arg.startsWith("--"));
      if (!url) throw new Error("用法：lw feeds add <url> [--full-text]");
      const feed = await feeds.subscribe(owner.userId, {
        url,
        fullText: rest.includes("--full-text"),
      });
      const outcome = await feeds.refresh(feed.id);
      stdout.write(`${feed.id} ${feed.url} 新增 ${outcome.added}\n`);
      return 0;
    }
    if (sub === "remove") {
      const id = rest.find((arg) => !arg.startsWith("--"));
      if (!id) throw new Error("用法：lw feeds remove <id> [--keep-items]");
      await feeds.unsubscribe(id, {
        keepItems: rest.includes("--keep-items"),
        ownerId: owner.userId,
      });
      stdout.write(`已取消 ${id}\n`);
      return 0;
    }
    if (sub === "import") {
      const file = rest[0];
      if (!file) throw new Error("用法：lw feeds import <file.opml>");
      const xml = await readFile(file, "utf8");
      const outcome = await feeds.importOpml(owner.userId, xml);
      stdout.write(`导入 ${outcome.added}，失败 ${outcome.failed.length}\n`);
      return 0;
    }
    if (sub === "export") {
      const xml = await feeds.exportOpml(owner.userId);
      stdout.write(xml.endsWith("\n") ? xml : `${xml}\n`);
      return 0;
    }
    throw new Error("用法：lw feeds add|list|remove|import|export");
  } finally {
    await db.close();
  }
}

async function notesCommand(
  argv: readonly string[],
  env: NodeJS.ProcessEnv,
  stdout: Writer,
): Promise<number> {
  const [sub, ...rest] = argv;
  const location = resolveDatabaseLocation(env);
  const db = await openDatabase(location);
  try {
    const owner = await db.getOwner();
    if (!owner) throw new OwnerNotFound();
    const store = db.notes();
    const by = { type: "user", id: owner.id };
    const { positionals, flags } = parseFlags(rest);
    if (sub === "list") {
      const status = flag(flags, "status");
      const notes = await store.list(
        owner.id,
        status
          ? {
              status: status.split(",") as Array<
                "candidate" | "active" | "muted" | "rejected" | "superseded"
              >,
            }
          : undefined,
      );
      if (notes.length === 0) {
        stdout.write("还没有写作笔记。\n");
        return 0;
      }
      for (const note of notes) {
        const conflict = note.plainConflict ? "\t说人话冲突" : "";
        stdout.write(
          `${note.id}\t${note.status}\t${note.kind}\t${note.scope.level}\tv${note.version}\t${note.statement}${conflict}\n`,
        );
      }
      return 0;
    }
    if (sub === "add") {
      const text = flag(flags, "text");
      const kind = flag(flags, "kind");
      if (!text || !kind) throw new Error("用法：lw notes add --text <句子> --kind <类型>");
      const level = flag(flags, "scope") ?? "global";
      const written = await store.write(
        owner.id,
        {
          text,
          kind: kind as "habit",
          scope:
            level === "global"
              ? { level: "global" }
              : { level: level as "genre", ref: flag(flags, "ref") },
        },
        by,
      );
      stdout.write(`${written.preference.id}\t${written.preference.status}\n`);
      if (written.factHint) stdout.write("这像事实，建议写进文档档案的要求里\n");
      return 0;
    }
    if (sub === "review") {
      const [id, action] = positionals;
      if (!id || !action) throw new Error("用法：lw notes review <id> accept|reject|mute|unmute");
      const note = await store.review(
        owner.id,
        id,
        action as "accept" | "reject" | "mute" | "unmute",
        by,
      );
      stdout.write(`${note.id}\t${note.status}\n`);
      return 0;
    }
    if (sub === "correct") {
      const id = positionals[0];
      const version = flag(flags, "version");
      const text = flag(flags, "text");
      if (!id || !version || !text) {
        throw new Error("用法：lw notes correct <id> --version <n> --text <句子>");
      }
      const note = await store.correct(owner.id, id, Number(version), text, by);
      stdout.write(`${note.id}\tv${note.version}\t${note.statement}\n`);
      return 0;
    }
    if (sub === "forget") {
      const id = positionals[0];
      if (!id) throw new Error("用法：lw notes forget <id>");
      const receipt = await store.forget(owner.id, id, by);
      stdout.write(`已删除 ${id}\n回执 ${receipt.receiptId}\n`);
      stdout.write("已有的聊天记录里可能还留有原文。可以另外删除这篇文档的会话记录。\n");
      return 0;
    }
    if (sub === "export") {
      const notes = await store.list(owner.id, {
        status: ["candidate", "active", "muted", "rejected", "superseded"],
      });
      stdout.write(`${JSON.stringify({ notes }, null, 2)}\n`);
      return 0;
    }
    if (sub === "explain") {
      const id = positionals[0];
      if (!id) throw new Error("用法：lw notes explain <id>");
      const audit = await store.explain(owner.id, id);
      for (const entry of audit) {
        stdout.write(`${entry.at}\t${entry.action}\t${entry.actorType}\n`);
      }
      return 0;
    }
    throw new Error("用法：lw notes list|add|review|correct|forget|export|explain");
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

import { createHash } from "node:crypto";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { createServer, type Server } from "node:http";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { EditOp } from "@luminwrite/contracts";
import {
  createApiKey,
  createCredential,
  ensureMasterKey,
  ensureOwner,
  openMemoryDatabase,
  readOwnerToken,
} from "@luminwrite/db";
import {
  type EvalCapture,
  type EvalJudgment,
  judgeCapture,
  type ModelExchange,
  readJsonFile,
  renderReport,
  runWriteReviseRollback,
  type TaskApiSnapshot,
} from "@luminwrite/eval-adapter";
import {
  createWritingRuntime,
  planOps,
  screenOps,
  targetIndex,
  writingStoreFromDatabase,
} from "@luminwrite/harness";
import { createKnowledgeBase, EMBED_DIM } from "@luminwrite/kb";
import { createClosedModels } from "@luminwrite/models";

import { createApp } from "./app.js";
import { createOssAuth } from "./auth.js";
import { kbEmbedderFor } from "./kb-embed.js";
import { sessionKnowledge } from "./kb-session.js";
import { ensureDefaultRegistry, resolveTurnChain, turnSpendFor } from "./model-registry.js";
import { writingNotesPort } from "./notes-port.js";
import { TokenBucketLimiter } from "./rate-limit.js";

const FLOWERY_NOTE = "写得华丽一点";
const FLOWERY_INSTRUCTION = "按笔记写华丽";
const HOLLOW_TEXT =
  "在当前行业的背景下，本方案具有重要意义。它能全方位赋能业务，打造增长矩阵。抓手和底层逻辑至关重要，助力闭环。";

const USAGE =
  "用法：lw eval run [--dir DIR] | lw eval judge --dir DIR | lw eval report --dir DIR [--out FILE] | lw eval gate [--dir DIR] [--out FILE]\n";

type Writer = { write(chunk: string): void };
type Reply = { status: number; body: unknown; retryAfterSeconds?: number };

export async function runEvalCli(
  argv: readonly string[],
  env: NodeJS.ProcessEnv = process.env,
  stdout: Writer = process.stdout,
  stderr: Writer = process.stderr,
): Promise<number> {
  const parsed = parseEvalArgs(argv);
  if (!parsed) {
    stderr.write(USAGE);
    return 2;
  }
  const secrets = secretValues(env);
  const dir = parsed.dir ?? (await mkdtemp(join(tmpdir(), "lw-eval-run-")));
  try {
    const problem = liveProblem(env) ?? embedProblem(env);
    if ((parsed.command === "run" || parsed.command === "gate") && problem) {
      stderr.write(`${problem}\n`);
      return 1;
    }
    await mkdir(dir, { recursive: true });
    let capture: EvalCapture;
    if (parsed.command === "run" || parsed.command === "gate") {
      capture = await captureEval(env, (line) => stderr.write(`${line}\n`));
      await writeFile(
        join(dir, "capture.json"),
        `${redact(JSON.stringify(capture, null, 2), secrets)}\n`,
      );
    } else {
      capture = await readJsonFile<EvalCapture>(join(dir, "capture.json"));
    }
    if (parsed.command === "run") {
      stdout.write(`已写入 ${join(dir, "capture.json")}\n`);
      return captureFailed(capture) ? 1 : 0;
    }
    const judgment =
      parsed.command === "report"
        ? await readJudgment(dir, capture, env)
        : await judgeCapture(capture, { heldoutDir: env.LW_EVAL_HELDOUT_DIR });
    await writeFile(
      join(dir, "judgment.json"),
      `${redact(JSON.stringify(judgment, null, 2), secrets)}\n`,
    );
    const report = redact(renderReport(judgment), secrets);
    if (parsed.out) {
      await writeFile(parsed.out, report);
      stdout.write(`报告已写入 ${parsed.out}\n`);
    } else if (parsed.command === "judge") {
      stdout.write(`已写入 ${join(dir, "judgment.json")}\n`);
    } else {
      stdout.write(report);
    }
    return judgment.pass ? 0 : 1;
  } catch (error) {
    stderr.write(`${redact(error instanceof Error ? error.message : "评测失败", secrets)}\n`);
    return 1;
  }
}

async function readJudgment(
  dir: string,
  capture: EvalCapture,
  env: NodeJS.ProcessEnv,
): Promise<EvalJudgment> {
  try {
    return await readJsonFile<EvalJudgment>(join(dir, "judgment.json"));
  } catch {
    return judgeCapture(capture, { heldoutDir: env.LW_EVAL_HELDOUT_DIR });
  }
}

function captureFailed(capture: EvalCapture): boolean {
  return (
    !capture.scenarios.rollback.ok ||
    !capture.scenarios.research.ok ||
    !capture.scenarios.embeddings.ok ||
    !capture.scenarios.floor.ok
  );
}

async function captureEval(
  env: NodeJS.ProcessEnv,
  log: (line: string) => void,
): Promise<EvalCapture> {
  const live = env.LW_EVAL_LIVE === "1";
  const timeoutMs = taskTimeoutMs(env);
  const trace = { scenario: "boot" };
  const exchanges: ModelExchange[] = [];
  const stopWatch = watchModelFetch({
    debug: env.LW_EVAL_DEBUG === "1",
    secrets: secretValues(env),
    exchanges,
    scenario: () => trace.scenario,
    log,
  });
  const dir = await mkdtemp(join(tmpdir(), "lw-eval-data-"));
  const dataDir = join(dir, "data");
  const db = await openMemoryDatabase();
  const calls = { n: 0 };
  let stub: { url: string; close(): Promise<void> } | undefined;
  let runtime: Awaited<ReturnType<typeof createWritingRuntime>> | undefined;
  try {
    const owner = await ensureOwner(db, { dataDir });
    const secrets = await ensureMasterKey({ db, dataDir, env: {} });
    const embed = await embedTarget(env);
    stub = embed.stub;
    if (live) await installLiveModel(db, secrets.ring, env, owner.userId);
    await installEmbedModel(db, secrets.ring, env, owner.userId, embed);
    const kb = createKnowledgeBase({
      db,
      embedder: kbEmbedderFor({
        db,
        keyring: secrets.ring,
        env: {},
        fetchImpl: countingFetch(calls),
      }),
    });
    const models = createClosedModels();
    const registry = { db, env, keyring: secrets.ring, models };
    runtime = await createWritingRuntime({
      sqlitePath: join(dir, "pi.sqlite"),
      store: writingStoreFromDatabase(db),
      usage: { record: (event) => db.recordUsage(event) },
      models,
      env,
      ...(live
        ? {
            resolveTurn: (input: Parameters<typeof resolveTurnChain>[1]) =>
              resolveTurnChain(registry, input),
            turnSpend: (actor: Parameters<typeof turnSpendFor>[1]) => turnSpendFor(registry, actor),
          }
        : { planner: gatePlanner }),
      knowledge: sessionKnowledge(db, kb),
      loadEvidence: async (documentId) => {
        const document = await db.getDocument(documentId);
        if (!document) return [];
        return kb.listEvidence(document.ownerId, documentId);
      },
      notes: writingNotesPort(db),
      ...(env.LW_EVAL_DEBUG === "1"
        ? {
            onToolResult(event: { name: string; isError: boolean; text: string }) {
              const scenario = trace.scenario ? `[${trace.scenario}] ` : "";
              const excerpt = redact(truncate(event.text, 200), secretValues(env));
              log(
                `${scenario}${event.name} isError=${event.isError ? "true" : "false"}${excerpt ? ` ${excerpt}` : ""}`,
              );
            },
          }
        : {}),
    });
    const app = createApp({
      db,
      runtime,
      auth: createOssAuth(db),
      keyring: secrets.ring,
      env,
      models,
      limiter: new TokenBucketLimiter({ capacity: 10_000, refillPerSecond: 1_000 }),
      kb,
    });
    const ownerToken = await readOwnerToken(dataDir);
    const issued = await createApiKey(db, { userId: owner.userId, name: "eval" });
    const ownerCall = caller(app, ownerToken);
    const apiCall = caller(app, issued.token);
    const noted = await ownerCall("/api/v1/me/writing-notes", {
      method: "POST",
      body: JSON.stringify({ text: FLOWERY_NOTE, kind: "tone", scopeLevel: "global" }),
    });
    const plainConflict =
      noted.status === 201 && Boolean((noted.body as { plainConflict?: boolean }).plainConflict);
    const layer = await db.notes().compileOverlay(owner.userId, {});
    const injected = layer.prose?.includes(FLOWERY_NOTE) ?? false;
    const pollMs = live ? 1_000 : 50;
    const rollback = await runRollback(apiCall, trace, timeoutMs, pollMs);
    const floor = await runFloor(
      apiCall,
      rollback,
      {
        plainConflict,
        injected,
        live,
      },
      trace,
      timeoutMs,
      pollMs,
    );
    const research = await runResearch(ownerCall, trace);
    const embeddings = await runEmbeddings(db, owner.userId, ownerCall, embed, calls, trace);
    return {
      generatedAt: new Date().toISOString(),
      model: live ? (env.LW_LLM_MODEL ?? "live") : "test/faux",
      live,
      exchanges,
      scenarios: { rollback, research, embeddings, floor },
    };
  } finally {
    stopWatch();
    await runtime?.close();
    await db.close();
    await stub?.close();
    await rm(dir, { recursive: true, force: true }).catch(() => undefined);
  }
}

function gatePlanner(input: Parameters<typeof planOps>[0]) {
  if (!input.instruction.includes(FLOWERY_INSTRUCTION)) return planOps(input);
  const index = targetIndex(input.instruction, input.blocks, input.selection);
  const block = input.blocks[index] ?? input.blocks[0];
  if (!block) return { ok: false as const, reason: "没有可修订的段落" };
  const op: EditOp = {
    op: "replace_text",
    blockId: block.blockId,
    contentHash: block.contentHash,
    expectedOldText: block.text,
    newText: HOLLOW_TEXT,
    reason: "华丽",
  };
  return { ok: true as const, summary: "按笔记写华丽", ops: [op] };
}

async function runRollback(
  call: (path: string, init?: RequestInit) => Promise<Reply>,
  trace: { scenario: string },
  timeoutMs: number,
  pollMs: number,
): Promise<EvalCapture["scenarios"]["rollback"]> {
  let phase: "write" | "revise" = "write";
  let beforeText = "";
  let afterText = "";
  try {
    await runWriteReviseRollback(
      {
        async submitTask(body, key) {
          phase = body.kind === "revise" ? "revise" : "write";
          trace.scenario = phase;
          const anchors =
            body.kind === "revise" && body.documentId
              ? await bodyAnchor(call, body.documentId)
              : undefined;
          const response = await call("/api/v1/tasks", {
            method: "POST",
            headers: { "idempotency-key": key },
            body: JSON.stringify(anchors ? { ...body, anchors: [anchors] } : body),
          });
          if (response.status >= 400) throw new Error(messageOf(response.body));
          const created = response.body as TaskApiSnapshot;
          trace.scenario = `${phase} ${created.id}`;
          return created;
        },
        async cancelTask(id) {
          await call(`/api/v1/tasks/${id}/cancel`, { method: "POST" });
        },
        async getTask(id) {
          const response = await call(`/api/v1/tasks/${id}`);
          if (response.status === 429) {
            return {
              id,
              state: "working",
              rateLimited: true,
              retryAfterSeconds: response.retryAfterSeconds ?? 1,
            };
          }
          const task = response.body as TaskApiSnapshot;
          if (task.state === "completed" && task.documentId) {
            const text = await documentText(call, task.documentId);
            if (phase === "write") beforeText = text;
            else afterText = text;
          }
          return task;
        },
        async restoreDocument(id, versionId) {
          const response = await call(`/api/v1/documents/${id}/restore`, {
            method: "POST",
            body: JSON.stringify({ versionId }),
          });
          if (response.status >= 400) return { ok: false };
          return response.body as { ok: boolean; versionId?: string };
        },
      },
      { timeoutMs, pollMs },
    );
    const restored = beforeText ? await documentText(call, await onlyDocumentId(call)) : "";
    return {
      ok: beforeText.length > 0 && afterText.length > 0 && afterText !== beforeText,
      restoredMatchesWrite: restored === beforeText,
      beforeText,
      afterText,
    };
  } catch (error) {
    return {
      ok: false,
      error: error instanceof Error ? error.message : "写改回滚失败",
      restoredMatchesWrite: false,
      beforeText,
      afterText,
    };
  }
}

async function onlyDocumentId(
  call: (path: string, init?: RequestInit) => Promise<Reply>,
): Promise<string> {
  const listed = await call("/api/v1/documents");
  const docs = listed.body as Array<{ id: string }>;
  const id = docs[0]?.id;
  if (!id) throw new Error("没有文档");
  return id;
}

async function runFloor(
  call: (path: string, init?: RequestInit) => Promise<Reply>,
  rollback: EvalCapture["scenarios"]["rollback"],
  input: { plainConflict: boolean; injected: boolean; live: boolean },
  trace: { scenario: string },
  timeoutMs: number,
  pollMs: number,
): Promise<EvalCapture["scenarios"]["floor"]> {
  const base = {
    note: FLOWERY_NOTE,
    plainConflict: input.plainConflict,
    injected: input.injected,
    probe: input.live ? ("live-model" as const) : ("faux-hollow" as const),
  };
  if (!rollback.ok) {
    return {
      ...base,
      ok: false,
      error: "没有可检查的正文",
      turnStatus: "",
      turnError: null,
      documentUnchanged: false,
      hollowLanded: false,
      afterText: "",
    };
  }
  try {
    const documentId = await onlyDocumentId(call);
    const before = await documentText(call, documentId);
    trace.scenario = "floor";
    const submitted = await call("/api/v1/tasks", {
      method: "POST",
      headers: { "idempotency-key": "eval-flowery" },
      body: JSON.stringify({
        kind: "revise",
        documentId,
        instruction: FLOWERY_INSTRUCTION,
      }),
    });
    if (submitted.status >= 400) throw new Error(messageOf(submitted.body));
    const created = submitted.body as TaskApiSnapshot;
    trace.scenario = `floor ${created.id}`;
    const finished = await waitTask(call, created.id, timeoutMs, pollMs);
    const after = await documentText(call, documentId);
    return {
      ...base,
      ok: true,
      turnStatus: finished.state ?? "",
      turnError: finished.error?.message ?? null,
      documentUnchanged: after === before,
      hollowLanded: textIsHollow(after),
      afterText: after,
    };
  } catch (error) {
    return {
      ...base,
      ok: false,
      error: error instanceof Error ? error.message : "华丽笔记回合失败",
      turnStatus: "",
      turnError: null,
      documentUnchanged: false,
      hollowLanded: false,
      afterText: "",
    };
  }
}

async function runResearch(
  call: (path: string, init?: RequestInit) => Promise<Reply>,
  trace: { scenario: string },
): Promise<EvalCapture["scenarios"]["research"]> {
  trace.scenario = "research";
  try {
    const created = await call("/api/v1/documents", {
      method: "POST",
      body: JSON.stringify({ title: "研究稿" }),
    });
    if (created.status >= 400) throw new Error(messageOf(created.body));
    const documentId = (created.body as { id?: string }).id;
    if (!documentId) throw new Error("研究稿没有 id");
    const drafted = await call(`/api/v1/documents/${documentId}/ops`, {
      method: "POST",
      body: JSON.stringify({
        ops: [
          {
            op: "insert_block",
            afterBlockId: null,
            newMarkdown: "因此可以断定成本下降了 30%。",
            reason: "起稿",
          },
        ],
      }),
    });
    if (drafted.status >= 400) throw new Error(messageOf(drafted.body));
    const turn = await call(`/api/v1/documents/${documentId}/turns`, {
      method: "POST",
      body: JSON.stringify({
        kind: "research",
        instruction: "查显卡时租",
        scope: "document",
        delivery: "followup",
        idempotencyKey: "research-h2",
      }),
    });
    if (turn.status >= 400) throw new Error(messageOf(turn.body));
    const turned = turn.body as { status?: string; error?: string };
    const status = turned.status;
    if (status && status !== "applied") {
      const detail = turned.error?.trim();
      return {
        ok: false,
        error: detail ? `回合结束于 ${status}：${detail}` : `回合结束于 ${status}`,
        questionCount: 0,
        gaps: [],
        claimFlags: false,
        admittedGap: false,
      };
    }
    const plans = await call(`/api/v1/documents/${documentId}/plans`);
    const stored = plans.body as Array<{
      plan?: { questions?: unknown[] };
      gaps?: string[];
    }>;
    const gaps = stored[0]?.gaps ?? [];
    const events = await call(`/api/v1/documents/${documentId}/events`);
    const listed = events.body as Array<{ type?: string }>;
    return {
      ok: status === "applied" && (stored[0]?.plan?.questions?.length ?? 0) > 0,
      questionCount: stored[0]?.plan?.questions?.length ?? 0,
      gaps,
      claimFlags: listed.some((event) => event.type === "claim.flags"),
      admittedGap: gaps.length > 0,
    };
  } catch (error) {
    return {
      ok: false,
      error: error instanceof Error ? error.message : "检索计划失败",
      questionCount: 0,
      gaps: [],
      claimFlags: false,
      admittedGap: false,
    };
  }
}

async function runEmbeddings(
  db: Awaited<ReturnType<typeof openMemoryDatabase>>,
  ownerId: string,
  call: (path: string, init?: RequestInit) => Promise<Reply>,
  embed: { endpoint: "env" | "local-stub"; model: string; baseUrl: string },
  calls: { n: number },
  trace: { scenario: string },
): Promise<EvalCapture["scenarios"]["embeddings"]> {
  trace.scenario = "embed";
  const base = {
    transport: "openai-compatible" as const,
    endpoint: embed.endpoint,
    model: embed.model,
    dim: EMBED_DIM,
    requests: 0,
  };
  try {
    const bytes = Buffer.from(
      "显卡时租按小时计费。一台 A10 的显存决定能放多大的批。",
      "utf8",
    ).toString("base64");
    const uploaded = await call("/api/v1/sources", {
      method: "POST",
      body: JSON.stringify({
        filename: "gpu.txt",
        mime: "text/plain",
        title: "显卡时租",
        dataBase64: bytes,
      }),
    });
    if (uploaded.status >= 400) throw new Error(messageOf(uploaded.body));
    const found = await call(`/api/v1/sources/search?q=${encodeURIComponent("显卡")}`);
    const hits = (found.body as { hits?: unknown[] }).hits ?? [];
    const rows = await db.query<{ stored: boolean; model: string | null }>(
      `SELECT (embedding IS NOT NULL) AS stored, embedding_model AS model
       FROM chunks WHERE owner_id = $1`,
      [ownerId],
    );
    const stored = rows.some((row) => Boolean(row.stored) && row.model === embed.model);
    return {
      ...base,
      requests: calls.n,
      ok: stored && hits.length > 0 && calls.n > 0,
      stored,
      searchHit: hits.length > 0,
    };
  } catch (error) {
    return {
      ...base,
      requests: calls.n,
      ok: false,
      error: error instanceof Error ? error.message : "嵌入失败",
      stored: false,
      searchHit: false,
    };
  }
}

async function installLiveModel(
  db: Awaited<ReturnType<typeof openMemoryDatabase>>,
  ring: Awaited<ReturnType<typeof ensureMasterKey>>["ring"],
  env: NodeJS.ProcessEnv,
  userId: string,
): Promise<void> {
  const provider = liveProvider(env);
  const baseUrl =
    env.LW_LLM_BASE_URL?.trim() ||
    (provider === "gemini"
      ? "https://generativelanguage.googleapis.com/v1beta"
      : "https://api.openai.com/v1");
  const modelId = (env.LW_LLM_MODEL?.trim() ?? "").replace(/^models\//, "");
  const apiKey = env.LW_LLM_API_KEY ?? env.OPENAI_API_KEY ?? "";
  await ensureDefaultRegistry(db, userId, env);
  await createCredential(db, ring, {
    userId,
    provider,
    label: "eval-live",
    key: apiKey,
    baseUrl,
    env,
  });
  await db.upsertRegistryEntry({
    ownerId: userId,
    ref: "custom/eval-live",
    kind: "model",
    spec: {
      kind: "model",
      provider,
      modelId,
      api:
        provider === "gemini"
          ? "google-generative-ai"
          : provider === "anthropic-compatible"
            ? "anthropic-messages"
            : "openai-completions",
      baseUrl,
      contextWindow: 128000,
      maxTokens: 4096,
    },
  });
  await db.upsertSlotBinding(userId, "writer.main", "custom/eval-live");
  await db.upsertSlotBinding(userId, "research.plan", "custom/eval-live");
}

async function installEmbedModel(
  db: Awaited<ReturnType<typeof openMemoryDatabase>>,
  ring: Awaited<ReturnType<typeof ensureMasterKey>>["ring"],
  env: NodeJS.ProcessEnv,
  userId: string,
  embed: { model: string; baseUrl: string; key: string },
): Promise<void> {
  await createCredential(db, ring, {
    userId,
    provider: "embed",
    label: "eval-embed",
    key: embed.key,
    baseUrl: embed.baseUrl,
    env,
  });
  await db.upsertRegistryEntry({
    ownerId: userId,
    ref: "custom/eval-embed",
    kind: "model",
    spec: {
      kind: "model",
      provider: "embed",
      modelId: embed.model,
      api: "openai-completions",
      baseUrl: embed.baseUrl,
    },
  });
  await db.upsertSlotBinding(userId, "kb.embed", "custom/eval-embed");
}

async function embedTarget(env: NodeJS.ProcessEnv): Promise<{
  endpoint: "env" | "local-stub";
  model: string;
  baseUrl: string;
  key: string;
  stub?: { url: string; close(): Promise<void> };
}> {
  if (env.LW_EMBED_API_KEY && env.LW_EMBED_BASE_URL && env.LW_EMBED_MODEL) {
    return {
      endpoint: "env",
      model: env.LW_EMBED_MODEL.trim(),
      baseUrl: env.LW_EMBED_BASE_URL.trim(),
      key: env.LW_EMBED_API_KEY,
    };
  }
  const stub = await startEmbedStub(EMBED_DIM);
  return {
    endpoint: "local-stub",
    model: "text-embedding-3-large",
    baseUrl: stub.url,
    key: "eval-local-embed",
    stub,
  };
}

function startEmbedStub(dim: number): Promise<{ url: string; close(): Promise<void> }> {
  const server = createServer((req, res) => {
    if (req.method !== "POST" || !req.url?.endsWith("/embeddings")) {
      res.statusCode = 404;
      res.end();
      return;
    }
    const chunks: Buffer[] = [];
    req.on("data", (chunk: Buffer) => chunks.push(chunk));
    req.on("end", () => {
      let inputs = [""];
      try {
        const body = JSON.parse(Buffer.concat(chunks).toString("utf8")) as {
          input?: string | string[];
        };
        inputs = Array.isArray(body.input) ? body.input : [body.input ?? ""];
      } catch {
        res.statusCode = 400;
        res.end();
        return;
      }
      res.setHeader("content-type", "application/json");
      res.end(
        JSON.stringify({
          data: inputs.map((text, index) => ({ index, embedding: stubVector(text, dim) })),
        }),
      );
    });
  });
  return new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const address = server.address();
      const port = typeof address === "object" && address ? address.port : 0;
      resolve({
        url: `http://127.0.0.1:${port}/v1`,
        close: () => closeServer(server),
      });
    });
  });
}

function stubVector(text: string, dim: number): number[] {
  const values = new Array<number>(dim);
  let sum = 0;
  for (let index = 0; index < dim; index += 1) {
    const digest = createHash("sha256").update(`${index}\0${text}`).digest();
    const value = digest.readInt32BE(0) / 0x7fffffff;
    values[index] = value;
    sum += value * value;
  }
  const norm = Math.sqrt(sum) || 1;
  return values.map((value) => value / norm);
}

function countingFetch(calls: { n: number }): typeof fetch {
  return async (input, init) => {
    if (String(input).includes("/embeddings")) calls.n += 1;
    return fetch(input, init);
  };
}

function caller(
  app: {
    request(input: string, init?: RequestInit): Response | Promise<Response>;
  },
  token: string,
) {
  return async (path: string, init?: RequestInit): Promise<Reply> => {
    const headers = new Headers(init?.headers);
    headers.set("authorization", `Bearer ${token}`);
    if (init?.body && !headers.has("content-type")) headers.set("content-type", "application/json");
    const response = await app.request(path, { ...init, headers });
    const text = await response.text();
    let body: unknown = null;
    if (text) {
      try {
        body = JSON.parse(text) as unknown;
      } catch {
        body = text;
      }
    }
    const retryHeader = response.headers.get("retry-after");
    const retryAfterSeconds = retryHeader === null ? undefined : Number(retryHeader);
    return {
      status: response.status,
      body,
      ...(retryAfterSeconds !== undefined && Number.isFinite(retryAfterSeconds)
        ? { retryAfterSeconds }
        : {}),
    };
  };
}

interface DocBlock {
  blockId: string;
  type: string;
  contentHash: string;
  text: string;
}

async function documentBlocks(
  call: (path: string, init?: RequestInit) => Promise<Reply>,
  id: string,
): Promise<DocBlock[]> {
  let response = await call(`/api/v1/documents/${id}`);
  for (let attempt = 0; response.status === 429 && attempt < 5; attempt += 1) {
    await sleep(retryAfterMs(response.retryAfterSeconds));
    response = await call(`/api/v1/documents/${id}`);
  }
  if (response.status >= 400) throw new Error(messageOf(response.body));
  return (response.body as { blocks?: DocBlock[] }).blocks ?? [];
}

function renderDocument(blocks: readonly DocBlock[]): string {
  return blocks
    .map((block) => (block.type === "heading" ? `# ${block.text ?? ""}` : (block.text ?? "")))
    .join("\n\n");
}

async function documentText(
  call: (path: string, init?: RequestInit) => Promise<Reply>,
  id: string,
): Promise<string> {
  return renderDocument(await documentBlocks(call, id));
}

/** 正文第一段，跳过标题。修订任务把它当作锚点，避免模型去改标题。 */
async function bodyAnchor(
  call: (path: string, init?: RequestInit) => Promise<Reply>,
  id: string,
): Promise<{ blockId: string; contentHash: string; quote: string } | undefined> {
  const blocks = await documentBlocks(call, id);
  const block = blocks.find((item) => item.type !== "heading" && item.text.trim().length > 0);
  if (!block) return undefined;
  if (!/^blk_[A-Za-z0-9_-]{8,64}$/.test(block.blockId)) return undefined;
  if (!/^sha256:[0-9a-f]{64}$/.test(block.contentHash)) return undefined;
  return {
    blockId: block.blockId,
    contentHash: block.contentHash,
    quote: block.text.trim().slice(0, 40),
  };
}

async function waitTask(
  call: (path: string, init?: RequestInit) => Promise<Reply>,
  id: string,
  timeoutMs: number,
  pollMs: number,
): Promise<{ state?: string; error?: { message?: string } }> {
  const started = Date.now();
  for (;;) {
    const response = await call(`/api/v1/tasks/${id}`);
    if (response.status === 429) {
      if (Date.now() - started > timeoutMs) {
        await call(`/api/v1/tasks/${id}/cancel`, { method: "POST" }).catch(() => undefined);
        throw new Error("任务超时");
      }
      await sleep(retryAfterMs(response.retryAfterSeconds));
      continue;
    }
    const task = response.body as { state?: string; error?: { message?: string } };
    if (task.state !== "submitted" && task.state !== "working") return task;
    if (Date.now() - started > timeoutMs) {
      await call(`/api/v1/tasks/${id}/cancel`, { method: "POST" }).catch(() => undefined);
      throw new Error("任务超时");
    }
    await sleep(pollMs);
  }
}

function retryAfterMs(seconds: number | undefined): number {
  if (seconds === undefined || !Number.isFinite(seconds) || seconds <= 0) return 1_000;
  return Math.min(60_000, Math.ceil(seconds * 1000));
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function taskTimeoutMs(env: NodeJS.ProcessEnv): number {
  const raw = env.LW_EVAL_TASK_TIMEOUT_MS?.trim();
  if (raw) {
    const parsed = Number(raw);
    if (Number.isFinite(parsed) && parsed > 0) return Math.floor(parsed);
  }
  return env.LW_EVAL_LIVE === "1" ? 180_000 : 20_000;
}

function textIsHollow(text: string): boolean {
  return !screenOps([
    {
      op: "replace_text",
      blockId: "blk_evalfloorcheck01",
      contentHash: "sha256:evalfloorcheck01",
      expectedOldText: text,
      newText: text,
      reason: "检查",
    },
  ]).pass;
}

function messageOf(body: unknown): string {
  if (!body || typeof body !== "object" || !("error" in body)) return "请求失败";
  const error = (body as { error?: unknown }).error;
  if (typeof error === "string") return error;
  if (error && typeof error === "object" && "message" in error) {
    const message = (error as { message?: unknown }).message;
    if (typeof message === "string") return message;
  }
  return "请求失败";
}

function liveProblem(env: NodeJS.ProcessEnv): string | undefined {
  if (env.LW_EVAL_LIVE !== "1") return undefined;
  if (env.LW_LLM_ENABLE !== "1") {
    return "真实模型需要 LW_EVAL_LIVE=1、LW_LLM_ENABLE=1、LW_LLM_API_KEY（或 OPENAI_API_KEY）和 LW_LLM_MODEL";
  }
  if (!(env.LW_LLM_API_KEY || env.OPENAI_API_KEY)) {
    return "真实模型需要 LW_LLM_API_KEY 或 OPENAI_API_KEY";
  }
  if (!env.LW_LLM_MODEL?.trim()) return "真实模型需要 LW_LLM_MODEL";
  const provider = env.LW_LLM_PROVIDER?.trim();
  if (
    provider &&
    provider !== "openai-compatible" &&
    provider !== "anthropic-compatible" &&
    provider !== "gemini"
  ) {
    return "LW_LLM_PROVIDER 只能是 gemini、openai-compatible 或 anthropic-compatible";
  }
  return undefined;
}

function liveProvider(
  env: NodeJS.ProcessEnv,
): "gemini" | "anthropic-compatible" | "openai-compatible" {
  const provider = env.LW_LLM_PROVIDER?.trim();
  if (provider === "gemini" || provider === "anthropic-compatible") return provider;
  return "openai-compatible";
}

function embedProblem(env: NodeJS.ProcessEnv): string | undefined {
  const values = [env.LW_EMBED_API_KEY, env.LW_EMBED_BASE_URL, env.LW_EMBED_MODEL];
  const set = values.filter((value) => Boolean(value?.trim())).length;
  if (set === 0 || set === values.length) return undefined;
  return "嵌入变量要一起设置：LW_EMBED_API_KEY、LW_EMBED_BASE_URL、LW_EMBED_MODEL";
}

function secretValues(env: NodeJS.ProcessEnv): string[] {
  return [env.LW_LLM_API_KEY, env.OPENAI_API_KEY, env.LW_EMBED_API_KEY, env.LW_SECRETS_KEY].filter(
    (value): value is string => Boolean(value && value.length >= 8),
  );
}

function redact(text: string, secrets: readonly string[]): string {
  let out = text;
  for (const secret of secrets) {
    if (out.includes(secret)) out = out.split(secret).join("[redacted]");
  }
  return out;
}

function watchModelFetch(input: {
  debug: boolean;
  secrets: readonly string[];
  exchanges: ModelExchange[];
  scenario: () => string;
  log: (line: string) => void;
}): () => void {
  const original = globalThis.fetch;
  globalThis.fetch = async (request, init) => {
    const described = describeFetch(request, init);
    const scenario = input.scenario();
    const response = await original(request, init);
    if (!described || !isModelPath(described.path)) return response;
    let rawBody: string | undefined;
    if (!response.ok || input.debug) {
      try {
        rawBody = await response.clone().text();
      } catch {
        rawBody = undefined;
      }
    }
    const bodyExcerpt =
      !response.ok && rawBody ? redact(truncate(rawBody, 500), input.secrets) : undefined;
    const usage = input.debug && rawBody ? tokenUsage(rawBody) : undefined;
    if (!response.ok || input.debug) {
      const exchange: ModelExchange = {
        method: described.method,
        path: described.path,
        ...(described.model ? { model: described.model } : {}),
        status: response.status,
        ...(scenario ? { scenario } : {}),
        ...(described.auth ? { auth: described.auth } : {}),
        ...(usage ? { usage } : {}),
        ...(bodyExcerpt ? { bodyExcerpt } : {}),
      };
      input.exchanges.push(exchange);
      if (input.debug) input.log(formatExchange(exchange));
    }
    return response;
  };
  return () => {
    globalThis.fetch = original;
  };
}

function describeFetch(
  request: Parameters<typeof fetch>[0],
  init?: RequestInit,
): { method: string; path: string; model?: string; auth?: string } | undefined {
  let href = "";
  let method = init?.method ?? "GET";
  const body = init?.body;
  let headers = new Headers(init?.headers);
  if (typeof request === "string" || request instanceof URL) {
    href = String(request);
  } else {
    href = request.url;
    method = init?.method ?? request.method;
    if (!init?.headers) headers = new Headers(request.headers);
  }
  let url: URL;
  try {
    url = new URL(href);
  } catch {
    return undefined;
  }
  const model = typeof body === "string" ? modelField(body) : undefined;
  const auth = authMarker(headers, url);
  return {
    method: method.toUpperCase(),
    path: url.pathname,
    ...(model ? { model } : {}),
    ...(auth ? { auth } : {}),
  };
}

/** 只记头的名字。值一律写成 [redacted]，密钥不进日志。 */
function authMarker(headers: Headers, url: URL): string | undefined {
  const marks: string[] = [];
  if (headers.has("x-goog-api-key")) marks.push("x-goog-api-key=[redacted]");
  if (headers.has("authorization")) marks.push("authorization=[redacted]");
  if (url.searchParams.has("key") || url.searchParams.has("api_key")) {
    marks.push("query-key=[redacted]");
  }
  return marks.length > 0 ? marks.join(" ") : undefined;
}

function modelField(body: string): string | undefined {
  try {
    const parsed = JSON.parse(body) as { model?: unknown };
    return typeof parsed.model === "string" ? parsed.model : undefined;
  } catch {
    return undefined;
  }
}

function isModelPath(path: string): boolean {
  return (
    path.endsWith("/chat/completions") ||
    path.endsWith("/embeddings") ||
    path.includes(":generateContent") ||
    path.includes(":streamGenerateContent")
  );
}

function formatExchange(exchange: ModelExchange): string {
  const scenario = exchange.scenario ? `[${exchange.scenario}] ` : "";
  const model = exchange.model ? ` model=${exchange.model}` : "";
  const auth = exchange.auth ? ` ${exchange.auth}` : "";
  const usage = exchange.usage ? ` ${exchange.usage}` : "";
  const body = exchange.bodyExcerpt ? ` ${exchange.bodyExcerpt}` : "";
  return `${scenario}${exchange.method} ${exchange.path}${model}${auth} HTTP ${exchange.status}${usage}${body}`;
}

function tokenUsage(text: string): string | undefined {
  const raw = lastJsonObject(text, "usageMetadata") ?? lastJsonObject(text, "usage");
  if (!raw) return undefined;
  try {
    const parsed = JSON.parse(raw) as Record<string, unknown>;
    const input = usageCount(
      parsed.prompt_tokens ?? parsed.promptTokenCount ?? parsed.input_tokens,
    );
    const output = usageCount(
      parsed.completion_tokens ?? parsed.candidatesTokenCount ?? parsed.output_tokens,
    );
    const total = usageCount(parsed.total_tokens ?? parsed.totalTokenCount);
    const parts: string[] = [];
    if (input !== undefined) parts.push(`in=${input}`);
    if (output !== undefined) parts.push(`out=${output}`);
    if (total !== undefined) parts.push(`total=${total}`);
    return parts.length > 0 ? `usage ${parts.join(" ")}` : undefined;
  } catch {
    return undefined;
  }
}

function usageCount(value: unknown): number | undefined {
  return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

function lastJsonObject(text: string, key: string): string | undefined {
  const needle = `"${key}"`;
  let from = 0;
  let found: string | undefined;
  for (;;) {
    const at = text.indexOf(needle, from);
    if (at < 0) return found;
    const brace = text.indexOf("{", at + needle.length);
    if (brace < 0 || brace - at > needle.length + 8) {
      from = at + needle.length;
      continue;
    }
    const end = matchBrace(text, brace);
    if (end > brace) found = text.slice(brace, end + 1);
    from = at + needle.length;
  }
}

function matchBrace(text: string, open: number): number {
  let depth = 0;
  let quoted = false;
  let escaped = false;
  for (let index = open; index < text.length; index += 1) {
    const char = text[index];
    if (quoted) {
      if (escaped) escaped = false;
      else if (char === "\\") escaped = true;
      else if (char === '"') quoted = false;
      continue;
    }
    if (char === '"') quoted = true;
    else if (char === "{") depth += 1;
    else if (char === "}") {
      depth -= 1;
      if (depth === 0) return index;
    }
  }
  return -1;
}

function truncate(text: string, max: number): string {
  const flat = text.replace(/\s+/g, " ").trim();
  return flat.length <= max ? flat : `${flat.slice(0, max)}…`;
}

function closeServer(server: Server): Promise<void> {
  return new Promise((resolve, reject) => {
    server.close((error) => (error ? reject(error) : resolve()));
  });
}

function parseEvalArgs(
  argv: readonly string[],
): { command: "run" | "judge" | "report" | "gate"; dir?: string; out?: string } | undefined {
  const [command, ...rest] = argv;
  if (command !== "run" && command !== "judge" && command !== "report" && command !== "gate") {
    return undefined;
  }
  let dir: string | undefined;
  let out: string | undefined;
  for (let index = 0; index < rest.length; index += 1) {
    const flag = rest[index];
    if (flag === "--") continue;
    const value = rest[index + 1];
    if ((flag === "--dir" || flag === "--out") && !value) return undefined;
    if (flag === "--dir") {
      dir = value;
      index += 1;
      continue;
    }
    if (flag === "--out") {
      out = value;
      index += 1;
      continue;
    }
    return undefined;
  }
  if ((command === "judge" || command === "report") && !dir) return undefined;
  return { command, ...(dir ? { dir } : {}), ...(out ? { out } : {}) };
}

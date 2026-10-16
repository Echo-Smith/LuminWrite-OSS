import { spawn } from "node:child_process";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { EditOp } from "@luminwrite/contracts";
import { blockText, listBlocks } from "@luminwrite/doc-model";
import { afterEach, describe, expect, it } from "vitest";

import { MemoryWritingStore } from "./memory-store.js";
import { OUTLINE_PARAGRAPHS, planOps, SECTION_PARAGRAPHS } from "./plan.js";
import { createWritingRuntime } from "./runtime.js";

const user = { type: "user" as const, userId: "user_1" };
const dirs: string[] = [];

afterEach(async () => {
  await Promise.all(dirs.splice(0).map((dir) => rm(dir, { recursive: true, force: true })));
});

async function tempDir(): Promise<string> {
  const dir = await mkdtemp(join(tmpdir(), "lw-harness-"));
  dirs.push(dir);
  return dir;
}

function request(
  documentId: string,
  kind: "outline" | "draft" | "revise" | "research",
  key: string,
  instruction: string,
) {
  return {
    documentId,
    kind,
    instruction,
    scope: kind === "revise" ? ("selection" as const) : ("document" as const),
    delivery: "followup" as const,
    applyMode: "auto" as const,
    idempotencyKey: key,
    actor: user,
  };
}

describe("写作回合", () => {
  it("大纲、初稿、只改一段，其余段落逐字不变，重复提交不另起一版", async () => {
    const dir = await tempDir();
    const store = new MemoryWritingStore();
    const created = store.createDocument({ title: "推理成本", ownerId: "user_1" });
    const runtime = await createWritingRuntime({ sqlitePath: join(dir, "pi.sqlite"), store });
    const outline = await runtime.submitTurn(
      request(created.id, "outline", "outline-1", "先写大纲"),
    );
    expect(outline.status).toBe("applied");
    const again = await runtime.submitTurn(request(created.id, "outline", "outline-1", "先写大纲"));
    expect(again.submissionId).toBe(outline.submissionId);
    expect(again.turnId).toBe(outline.turnId);
    expect(listBlocks(store.head(created.id)).map(blockText)).toEqual([...OUTLINE_PARAGRAPHS]);

    const draft = await runtime.submitTurn(request(created.id, "draft", "draft-1", "按大纲写初稿"));
    expect(draft.status).toBe("applied");
    const drafted = listBlocks(store.head(created.id)).map(blockText);
    expect(drafted[0]).toContain("A10");
    expect(drafted[1]).toContain("排队时间");

    const revised = await runtime.submitTurn(
      request(created.id, "revise", "revise-1", "只改第2段，写短一点"),
    );
    expect(revised.status).toBe("applied");
    const blocks = listBlocks(store.head(created.id));
    expect(blockText(blocks[0]!)).toBe(drafted[0]);
    expect(blockText(blocks[2]!)).toBe(drafted[2]);
    expect(blockText(blocks[1]!)).not.toBe(drafted[1]);
    expect(blockText(blocks[1]!)).toBe("批大小从 1 提到 8 时，单次请求分到的固定开销会下降。");
    expect(blocks[0]?.attrs?.id).toBe(listBlocks(store.head(created.id))[0]?.attrs?.id);

    const events = await runtime.events(created.id);
    expect(events.some((event) => event.type === "turn.started")).toBe(true);
    expect(events.some((event) => event.type === "edits.applied")).toBe(true);
    expect(events.some((event) => event.type.startsWith("pi."))).toBe(true);

    await runtime.close();
    const reopened = await createWritingRuntime({ sqlitePath: join(dir, "pi.sqlite"), store });
    const replay = await reopened.submitTurn(
      request(created.id, "outline", "outline-1", "先写大纲"),
    );
    expect(replay.submissionId).toBe(outline.submissionId);
    expect(listBlocks(store.head(created.id))).toHaveLength(3);
    await reopened.close();
  });

  it("空话提案不会落版本", async () => {
    const dir = await tempDir();
    const store = new MemoryWritingStore();
    const created = store.createDocument({ title: "空话", ownerId: "user_1" });
    const runtime = await createWritingRuntime({
      sqlitePath: join(dir, "pi.sqlite"),
      store,
      planner: () => ({
        ok: true,
        summary: "空话",
        ops: [
          {
            op: "insert_block",
            afterBlockId: null,
            newMarkdown: "本方案具有重要意义，能全方位赋能业务。",
            reason: "空话",
          },
        ] satisfies EditOp[],
      }),
    });
    const result = await runtime.submitTurn(request(created.id, "outline", "hollow-1", "写一段"));
    expect(result.status).toBe("failed");
    expect(listBlocks(store.head(created.id))).toHaveLength(0);
    await runtime.close();
  });

  it("研究和审校在 M0 明确失败", async () => {
    const dir = await tempDir();
    const store = new MemoryWritingStore();
    const created = store.createDocument({ title: "研究", ownerId: "user_1" });
    const runtime = await createWritingRuntime({ sqlitePath: join(dir, "pi.sqlite"), store });
    const result = await runtime.submitTurn(
      request(created.id, "research", "research-1", "查资料"),
    );
    expect(result.status).toBe("failed");
    expect((await store.getTurn(result.turnId))?.error).toBe("尚未实现");
    await runtime.close();
  });

  it("计划函数只动指定段", () => {
    const blocks = [
      "第一段只有一句。",
      "批大小从 1 提到 8 时，单次请求分到的固定开销会下降。再往上提，排队时间会先变长。",
      "第三段保持不动。",
    ].map((text, index) => ({
      blockId: `blk_${"a".repeat(8)}${String(index).padStart(8, "0")}` as const,
      contentHash: `sha256:${"ab".repeat(32)}` as const,
      text,
    }));
    const plan = planOps({ kind: "revise", instruction: "只改第2段", blocks });
    expect(plan.ok).toBe(true);
    if (!plan.ok) return;
    expect(plan.ops).toHaveLength(1);
    expect(plan.ops[0]).toMatchObject({ op: "replace_text", blockId: blocks[1]?.blockId });
  });
});

describe("会话", () => {
  it("分叉用旧锚点改第 3 段会被拒绝，第 5 段仍能落下，两段对话不是同一个", async () => {
    const dir = await tempDir();
    const store = new MemoryWritingStore();
    const created = store.createDocument({ title: "推理成本", ownerId: "user_1" });
    const seeded = store.seedParagraphs(created.id, SECTION_PARAGRAPHS);
    const runtime = await createWritingRuntime({ sqlitePath: join(dir, "pi.sqlite"), store });
    const main = await runtime.submitTurn(
      request(created.id, "revise", "main-p3", "只改第3段，写短一点"),
    );
    expect(main.status).toBe("applied");
    const forkId = store.openFork(created.id, seeded);
    const stale = await runtime.submitTurn({
      ...request(created.id, "revise", "fork-p3", "只改第3段，写短一点"),
      sessionId: forkId,
    });
    expect(stale.status).toBe("conflicted");
    const texts = listBlocks(store.head(created.id)).map(blockText);
    expect(texts[2]).toBe("第三段先记下当前每小时费用。");
    const fifth = await runtime.submitTurn({
      ...request(created.id, "revise", "fork-p5", "只改第5段，写短一点"),
      sessionId: forkId,
    });
    expect(fifth.status).toBe("applied");
    const after = listBlocks(store.head(created.id)).map(blockText);
    expect(after[2]).toBe(texts[2]);
    expect(after[4]).toBe("第五段建议先量出当前批大小。");
    expect(after[0]).toBe(SECTION_PARAGRAPHS[0]);
    expect(after[3]).toBe(SECTION_PARAGRAPHS[3]);
    const mainSession = await store.getSession(created.sessionId);
    const forkSession = await store.getSession(forkId);
    expect(mainSession?.piConversationId).toBeTruthy();
    expect(forkSession?.piConversationId).toBeTruthy();
    expect(forkSession?.piConversationId).not.toBe(mainSession?.piConversationId);
    const events = await runtime.events(created.id);
    const started = events.find((event) => event.type === "turn.started");
    expect(started?.payload).toMatchObject({ dossierVersion: 1 });
    await runtime.close();
  });
});

describe("pi-durable 崩溃续跑", () => {
  it("杀掉进程后同一提交继续，计数器只补到 2", async () => {
    const dir = await tempDir();
    const sqlitePath = join(dir, "crash.sqlite");
    const counterPath = join(dir, "counter.txt");
    const releasePath = join(dir, "release");
    const submissionPath = join(dir, "submission.txt");
    const script = new URL("../scripts/pi-crash-child.mjs", import.meta.url);

    const first = spawn(
      process.execPath,
      [script.pathname, sqlitePath, "start", counterPath, releasePath, submissionPath],
      {
        stdio: ["ignore", "pipe", "pipe"],
      },
    );
    let firstErr = "";
    first.stderr?.on("data", (chunk) => {
      firstErr += String(chunk);
    });
    await waitFor(counterPath, "1");
    first.kill("SIGKILL");
    await onceExit(first);
    const submission = (await readFile(submissionPath, "utf8")).trim();

    const second = spawn(
      process.execPath,
      [script.pathname, sqlitePath, "resume", counterPath, releasePath, submissionPath],
      { stdio: ["ignore", "pipe", "pipe"] },
    );
    let secondOut = "";
    let secondErr = "";
    second.stdout?.on("data", (chunk) => {
      secondOut += String(chunk);
    });
    second.stderr?.on("data", (chunk) => {
      secondErr += String(chunk);
    });
    const code = await onceExit(second);
    expect(code, `${firstErr}\n${secondErr}`).toBe(0);
    expect(secondOut).toContain("CHILD_OK");
    expect(await readFile(counterPath, "utf8")).toBe("2");
    expect((await readFile(submissionPath, "utf8")).trim()).toBe(submission);
  });
});

async function waitFor(path: string, expected: string): Promise<void> {
  const started = Date.now();
  while (Date.now() - started < 15_000) {
    try {
      if ((await readFile(path, "utf8")) === expected) return;
    } catch {
      // 文件还没出现
    }
    await new Promise((resolve) => setTimeout(resolve, 30));
  }
  throw new Error(`等待 ${path} 变成 ${expected} 超时`);
}

function onceExit(child: ReturnType<typeof spawn>): Promise<number | null> {
  return new Promise((resolve, reject) => {
    child.once("exit", (code) => resolve(code));
    child.once("error", reject);
  });
}

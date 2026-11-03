import { analyzePlain } from "@luminwrite/plainspeak";
import { readStoredPlan, type StoredResearchPlan } from "@luminwrite/research";

export { lwEvalRoot, runLwEval } from "./lweval.js";
export type { EvalCapture, EvalJudgment, ModelExchange } from "./pipeline.js";
export { judgeCapture, readJsonFile, renderReport, stripHtml } from "./pipeline.js";

export interface SmokeSample {
  id: string;
  text: string;
  role: "clean" | "negative";
}

/** M0 的确定性排序。闸门补跑另外调用仓库里的 `eval/lw-eval`。 */
export const smokeSamples: SmokeSample[] = [
  {
    id: "clean",
    role: "clean",
    text: "推理成本主要由显卡时租、显存占用和批大小决定。批大小从 1 提到 8，单次请求更便宜。",
  },
  {
    id: "negative",
    role: "negative",
    text: "在当前行业的背景下，本方案具有重要意义。它能全方位赋能业务，打造增长矩阵。抓手和底层逻辑至关重要，助力闭环。",
  },
];

export interface SmokeReport {
  pass: boolean;
  ranking: Array<{ id: string; hollowScore: number }>;
}

/** H2 读 `research_plans` 时走这里。只重校验结构，不重算缺口。 */
export function readResearchPlansForEval(
  rows: Array<{
    id: string;
    turnId: string;
    sessionId: string;
    plan: unknown;
    gaps: unknown;
    createdAt: string;
  }>,
): StoredResearchPlan[] {
  return rows.map((row) => readStoredPlan(row));
}

export interface TaskApiSurface {
  submitTask(
    body: {
      kind: "write" | "revise";
      documentId?: string;
      brief?: { topic: string; genre?: string };
      instruction?: string;
      model?: string;
      callbackUrl?: string;
      sources?: { library?: boolean };
    },
    idempotencyKey: string,
  ): Promise<TaskApiSnapshot>;
  getTask(id: string): Promise<TaskApiSnapshot>;
  restoreDocument(id: string, versionId: string): Promise<{ ok: boolean; versionId?: string }>;
  /** 超时放弃时取消任务，免得它继续打模型。 */
  cancelTask?(id: string): Promise<unknown>;
}

export interface TaskApiSnapshot {
  id: string;
  state: string;
  documentId?: string;
  resultVersionId?: string;
  proposalId?: string;
  error?: { code?: string; message?: string; retryable?: boolean };
}

const TASK_DONE = new Set(["completed", "failed", "canceled", "rejected"]);

/**
 * 「写 → 改 → 回滚」走公开任务 API。调用方传入只靠 API Key 的 client。
 * 打分在 `judgeCapture`，不在这里。
 */
export async function runWriteReviseRollback(
  api: TaskApiSurface,
  options?: { timeoutMs?: number },
): Promise<{ documentId: string; wroteVersionId: string; revisedVersionId: string }> {
  const timeoutMs = options?.timeoutMs ?? 20_000;
  const write = await api.submitTask(
    {
      kind: "write",
      brief: { topic: "推理成本", genre: "commentary" },
      instruction: "写一篇关于推理成本的短评",
      sources: { library: false },
    },
    "eval-write",
  );
  const written = await waitForTask(api, write.id, timeoutMs);
  if (!written.documentId || !written.resultVersionId) throw new Error("写作没有版本");
  const revise = await api.submitTask(
    {
      kind: "revise",
      documentId: written.documentId,
      instruction: "把第1段写短",
    },
    "eval-revise",
  );
  const revised = await waitForTask(api, revise.id, timeoutMs);
  if (!revised.resultVersionId || revised.resultVersionId === written.resultVersionId) {
    throw new Error("修订没有新版本");
  }
  const restored = await api.restoreDocument(written.documentId, written.resultVersionId);
  if (!restored.ok) throw new Error("回滚失败");
  return {
    documentId: written.documentId,
    wroteVersionId: written.resultVersionId,
    revisedVersionId: revised.resultVersionId,
  };
}

async function waitForTask(
  api: TaskApiSurface,
  id: string,
  timeoutMs: number,
): Promise<TaskApiSnapshot> {
  const started = Date.now();
  for (;;) {
    const task = await api.getTask(id);
    if (TASK_DONE.has(task.state)) {
      if (task.state !== "completed") {
        const detail = task.error?.message?.trim();
        throw new Error(
          detail ? `任务结束于 ${task.state}：${detail}` : `任务结束于 ${task.state}`,
        );
      }
      return task;
    }
    if (Date.now() - started > timeoutMs) {
      await api.cancelTask?.(id).catch(() => undefined);
      throw new Error("任务超时");
    }
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
}

export function runDeterministicSmoke(): SmokeReport {
  const ranking = smokeSamples
    .map((sample) => ({
      id: sample.id,
      hollowScore: analyzePlain(sample.text).hollowScore,
      role: sample.role,
    }))
    .sort((a, b) => a.hollowScore - b.hollowScore);
  const last = ranking.at(-1);
  const clean = ranking.find((item) => item.id === "clean");
  const pass = last?.id === "negative" && (clean?.hollowScore ?? 100) < (last?.hollowScore ?? 0);
  return { pass, ranking: ranking.map(({ id, hollowScore }) => ({ id, hollowScore })) };
}

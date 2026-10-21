import { analyzePlain } from "@luminwrite/plainspeak";
import { readStoredPlan, type StoredResearchPlan } from "@luminwrite/research";

export interface SmokeSample {
  id: string;
  text: string;
  role: "clean" | "negative";
}

/** 本仓库没有 lw-eval，也没有可现场花费的评测额度。M0 用同一份词表做确定性排序。 */
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
}

export interface TaskApiSnapshot {
  id: string;
  state: string;
  documentId?: string;
  resultVersionId?: string;
  proposalId?: string;
}

const TASK_DONE = new Set(["completed", "failed", "canceled", "rejected"]);

/**
 * lw-eval 不在这个仓库里。评测要跑的「写 → 改 → 回滚」走公开任务 API 的这一个入口。
 * 调用方传入只靠 API Key 的 client。
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
      if (task.state !== "completed") throw new Error(`任务结束于 ${task.state}`);
      return task;
    }
    if (Date.now() - started > timeoutMs) throw new Error("任务超时");
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

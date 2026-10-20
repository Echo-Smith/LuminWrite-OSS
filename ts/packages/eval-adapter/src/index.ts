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

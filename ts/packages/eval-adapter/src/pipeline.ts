import { mkdtemp, readdir, readFile, rm, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { basename, extname, join } from "node:path";

import { runLwEval, runLwEvalScript } from "./lweval.js";

const NEGATIVE_CONTROL =
  "在当前行业的背景下，本方案具有重要意义。它能全方位赋能业务，打造增长矩阵。抓手和底层逻辑至关重要，助力闭环。";

export interface ModelExchange {
  method: string;
  path: string;
  model?: string;
  status: number;
  /** 只写头名和 `[redacted]`，不写头的值。 */
  auth?: string;
  /** 这一次调用属于哪个场景或任务。 */
  scenario?: string;
  /** 响应里带了用量时记 in/out/total。 */
  usage?: string;
  bodyExcerpt?: string;
}

export interface EvalCapture {
  generatedAt: string;
  model: string;
  live: boolean;
  /** 失败的模型请求。LW_EVAL_DEBUG=1 时也包括成功的。 */
  exchanges: ModelExchange[];
  scenarios: {
    rollback: {
      ok: boolean;
      error?: string;
      restoredMatchesWrite: boolean;
      beforeText: string;
      afterText: string;
    };
    research: {
      ok: boolean;
      error?: string;
      questionCount: number;
      gaps: string[];
      claimFlags: boolean;
      admittedGap: boolean;
    };
    embeddings: {
      ok: boolean;
      error?: string;
      transport: "openai-compatible";
      endpoint: "env" | "local-stub";
      model: string;
      dim: number;
      requests: number;
      stored: boolean;
      searchHit: boolean;
    };
    floor: {
      ok: boolean;
      error?: string;
      note: string;
      plainConflict: boolean;
      injected: boolean;
      turnStatus: string;
      turnError: string | null;
      documentUnchanged: boolean;
      hollowLanded: boolean;
      /** 华丽笔记回合结束时的正文。报告用来给人看，也用来给真实模型打 aitone。 */
      afterText: string;
      probe: "faux-hollow" | "live-model";
    };
  };
}

export interface EvalJudgment {
  generatedAt: string;
  model: string;
  live: boolean;
  pass: boolean;
  rollback: { pass: boolean; restoredMatchesWrite: boolean };
  h2: {
    pass: boolean;
    questionCount: number;
    gaps: string[];
    claimFlags: boolean;
    admittedGap: boolean;
    sensitivity: string;
    criticalCoverage: number | null;
  };
  h3: {
    pass: boolean;
    upcr: number | null;
    anchoredSuccess: boolean | null;
    spliceDriftWindows: number[];
    controlDriftWindows: number[];
    draftAitone: number | null;
    negativeAitone: number | null;
  };
  embeddings: EvalCapture["scenarios"]["embeddings"];
  floor: {
    pass: boolean;
    note: string;
    plainConflict: boolean;
    injected: boolean;
    turnStatus: string;
    turnError: string | null;
    documentUnchanged: boolean;
    hollowLanded: boolean;
    afterText: string;
    afterAitone: number | null;
    negativeAitone: number | null;
    probe: EvalCapture["scenarios"]["floor"]["probe"];
  };
  heldout: { ran: boolean; reason?: string; files: Array<{ name: string; aitoneScore: number }> };
  failures: string[];
  exchanges: ModelExchange[];
  errors: string[];
}

const HONEST_GAP_SCRIPT = `
import json, sys
sys.path.insert(0, sys.argv[1])
from lweval.retrieval_metrics import plan_coverage, ablation_sensitivity
admitted = sys.argv[2] == "1"
json.dump({
  "coverage": plan_coverage({"q1": "missed"}, {"q1": True}),
  "sensitivity": ablation_sensitivity(1.0, 0.0, admitted),
}, sys.stdout)
`;

export async function judgeCapture(
  capture: EvalCapture,
  options?: { heldoutDir?: string },
): Promise<EvalJudgment> {
  const errors: string[] = [];
  const h2 = await judgeH2(capture, errors);
  const h3 = await judgeH3(capture, errors);
  const heldout = await judgeHeldout(options?.heldoutDir, errors);
  const floorScores = await scoreFloor(capture, errors);
  const floorPass = floorHolds(capture.scenarios.floor, floorScores);
  const embed = capture.scenarios.embeddings;
  const embedPass = embed.ok && embed.stored && embed.searchHit && embed.requests > 0;
  const rollbackPass =
    capture.scenarios.rollback.ok && capture.scenarios.rollback.restoredMatchesWrite;
  const failures = failureNotes(capture);
  return {
    generatedAt: new Date().toISOString(),
    model: capture.model,
    live: capture.live,
    pass: rollbackPass && h2.pass && h3.pass && embedPass && floorPass && errors.length === 0,
    rollback: {
      pass: rollbackPass,
      restoredMatchesWrite: capture.scenarios.rollback.restoredMatchesWrite,
    },
    h2,
    h3,
    embeddings: embed,
    floor: {
      pass: floorPass,
      note: capture.scenarios.floor.note,
      plainConflict: capture.scenarios.floor.plainConflict,
      injected: capture.scenarios.floor.injected,
      turnStatus: capture.scenarios.floor.turnStatus,
      turnError: capture.scenarios.floor.turnError,
      documentUnchanged: capture.scenarios.floor.documentUnchanged,
      hollowLanded: capture.scenarios.floor.hollowLanded,
      afterText: capture.scenarios.floor.afterText,
      afterAitone: floorScores.after,
      negativeAitone: floorScores.negative,
      probe: capture.scenarios.floor.probe,
    },
    heldout,
    failures,
    exchanges: capture.exchanges ?? [],
    errors,
  };
}

function failureNotes(capture: EvalCapture): string[] {
  const notes: string[] = [];
  const rollback = capture.scenarios.rollback.error;
  if (rollback) notes.push(`写改回滚：${rollback}`);
  const research = capture.scenarios.research.error;
  if (research) notes.push(`检索计划：${research}`);
  const embed = capture.scenarios.embeddings.error;
  if (embed) notes.push(`嵌入：${embed}`);
  const floor = capture.scenarios.floor;
  if (floor.error) notes.push(`华丽笔记：${floor.error}`);
  else if (floor.turnError) notes.push(`华丽笔记：${floor.turnStatus}：${floor.turnError}`);
  return notes;
}

async function judgeH2(capture: EvalCapture, errors: string[]): Promise<EvalJudgment["h2"]> {
  const research = capture.scenarios.research;
  let sensitivity = "";
  let criticalCoverage: number | null = null;
  try {
    const raw = (await runLwEvalScript(HONEST_GAP_SCRIPT, [research.admittedGap ? "1" : "0"])) as {
      coverage?: { critical_coverage?: number };
      sensitivity?: string;
    };
    sensitivity = raw.sensitivity ?? "";
    criticalCoverage =
      typeof raw.coverage?.critical_coverage === "number" ? raw.coverage.critical_coverage : null;
  } catch (error) {
    errors.push(error instanceof Error ? error.message : "H2 指标失败");
  }
  const pass =
    research.ok &&
    research.questionCount > 0 &&
    research.admittedGap &&
    research.claimFlags &&
    sensitivity === "honest_gap";
  return {
    pass,
    questionCount: research.questionCount,
    gaps: research.gaps,
    claimFlags: research.claimFlags,
    admittedGap: research.admittedGap,
    sensitivity,
    criticalCoverage,
  };
}

async function judgeH3(capture: EvalCapture, errors: string[]): Promise<EvalJudgment["h3"]> {
  const empty: EvalJudgment["h3"] = {
    pass: false,
    upcr: null,
    anchoredSuccess: null,
    spliceDriftWindows: [],
    controlDriftWindows: [],
    draftAitone: null,
    negativeAitone: null,
  };
  const dir = await mkdtemp(join(tmpdir(), "lw-eval-h3-"));
  try {
    const before = join(dir, "before.md");
    const after = join(dir, "after.md");
    const draft = join(dir, "draft.md");
    const negative = join(dir, "negative.md");
    await writeFile(before, capture.scenarios.rollback.beforeText, "utf8");
    await writeFile(after, capture.scenarios.rollback.afterText, "utf8");
    await writeFile(draft, capture.scenarios.rollback.beforeText, "utf8");
    await writeFile(negative, NEGATIVE_CONTROL, "utf8");
    const anchor = bodyAnchor(capture.scenarios.rollback.beforeText);
    const upcr = (await runLwEval(
      anchor ? ["upcr", before, after, "--anchor", anchor] : ["upcr", before, after],
    )) as { UPCR?: number; anchored_success?: boolean | null };
    const splice = (await runLwEval([
      "drift",
      "results/synthetic_splice_nat_then_ai.md",
      "--ref",
      "samples/nat_commentary_village.md",
      "samples/nat_essay_remote_work.md",
      "samples/nat_explainer_electricity.md",
      "--window",
      "300",
      "--step",
      "150",
    ])) as { drift_windows?: number[] };
    const control = (await runLwEval([
      "drift",
      "results/synthetic_control_nat_then_nat.md",
      "--ref",
      "samples/nat_commentary_village.md",
      "samples/nat_essay_remote_work.md",
      "samples/nat_explainer_electricity.md",
      "--window",
      "300",
      "--step",
      "150",
    ])) as { drift_windows?: number[] };
    const tones = (await runLwEval(["aitone", draft, negative])) as Record<
      string,
      { aitone_score?: number }
    >;
    const draftScore = tones[draft]?.aitone_score ?? null;
    const negativeScore = tones[negative]?.aitone_score ?? null;
    const spliceWindows = splice.drift_windows ?? [];
    const controlWindows = control.drift_windows ?? [];
    const anchored = upcr.anchored_success === true;
    const product =
      capture.scenarios.rollback.ok && typeof upcr.UPCR === "number" && upcr.UPCR === 0 && anchored;
    const meta = spliceWindows.length > 0 && controlWindows.length === 0;
    const tone = draftScore !== null && negativeScore !== null && draftScore < negativeScore;
    return {
      pass: product && meta && tone,
      upcr: typeof upcr.UPCR === "number" ? upcr.UPCR : null,
      anchoredSuccess: typeof upcr.anchored_success === "boolean" ? upcr.anchored_success : null,
      spliceDriftWindows: spliceWindows,
      controlDriftWindows: controlWindows,
      draftAitone: draftScore,
      negativeAitone: negativeScore,
    };
  } catch (error) {
    errors.push(error instanceof Error ? error.message : "H3 指标失败");
    return empty;
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
}

async function judgeHeldout(
  dir: string | undefined,
  errors: string[],
): Promise<EvalJudgment["heldout"]> {
  if (!dir) return { ran: false, reason: "未设置 LW_EVAL_HELDOUT_DIR", files: [] };
  let names: string[] = [];
  try {
    const info = await stat(dir);
    if (!info.isDirectory())
      return { ran: false, reason: "LW_EVAL_HELDOUT_DIR 不是目录", files: [] };
    names = (await readdir(dir)).filter(isHeldoutFile);
  } catch {
    return { ran: false, reason: "读不到 LW_EVAL_HELDOUT_DIR", files: [] };
  }
  const files: string[] = [];
  for (const name of names) {
    const path = join(dir, name);
    const info = await stat(path);
    if (!info.isFile() || info.size > 2_000_000) continue;
    files.push(path);
  }
  if (files.length === 0) return { ran: false, reason: "目录里没有留出集原文", files: [] };
  const scratch = await mkdtemp(join(tmpdir(), "lw-eval-heldout-"));
  try {
    const scored: string[] = [];
    for (const path of files) {
      const rawText = await readFile(path, "utf8");
      const text =
        extname(path).toLowerCase() === ".html" || extname(path) === ".htm"
          ? stripHtml(rawText)
          : rawText;
      const copy = join(scratch, basename(path));
      await writeFile(copy, text, "utf8");
      scored.push(copy);
    }
    const raw = (await runLwEval(["aitone", ...scored])) as Record<
      string,
      { aitone_score?: number }
    >;
    return {
      ran: true,
      files: scored.map((path) => ({
        name: basename(path),
        aitoneScore: raw[path]?.aitone_score ?? Number.NaN,
      })),
    };
  } catch (error) {
    errors.push(error instanceof Error ? error.message : "留出集打分失败");
    return { ran: false, reason: "留出集打分失败", files: [] };
  } finally {
    await rm(scratch, { recursive: true, force: true });
  }
}

const HELDOUT_EXT = new Set([".md", ".txt", ".html", ".htm"]);

function isHeldoutFile(name: string): boolean {
  if (name === "SOURCES.md") return false;
  return HELDOUT_EXT.has(extname(name).toLowerCase());
}

/** 留出集里的网页只评正文。不把标签送进检测器。 */
export function stripHtml(html: string): string {
  return html
    .replace(/<script[\s\S]*?<\/script>/gi, " ")
    .replace(/<style[\s\S]*?<\/style>/gi, " ")
    .replace(/<[^>]+>/g, " ")
    .replace(/&nbsp;/gi, " ")
    .replace(/&amp;/gi, "&")
    .replace(/&lt;/gi, "<")
    .replace(/&gt;/gi, ">")
    .replace(/\s+/g, " ")
    .trim();
}

/** 正文第 1 段的开头。标题（以 # 起行）不算。用来锚定 UPCR，不要求改完仍含这几个字。 */
export function bodyAnchor(beforeText: string): string | undefined {
  const paragraphs = beforeText
    .split(/\n\s*\n/)
    .map((paragraph) => paragraph.trim())
    .filter((paragraph) => paragraph.length > 0);
  const body = paragraphs.find((paragraph) => !/^#{1,6}\s/.test(paragraph)) ?? paragraphs[0];
  if (!body) return undefined;
  const plain = body.replace(/^#{1,6}\s+/, "").trim();
  const clause = plain.split(/[，,。！？；：\n]/)[0]?.trim() ?? "";
  const anchor = (clause.length >= 4 ? clause : plain).slice(0, 24);
  return anchor.length > 0 ? anchor : undefined;
}

function floorHolds(
  floor: EvalCapture["scenarios"]["floor"],
  scores: { after: number | null; negative: number | null },
): boolean {
  if (!floor.ok || floor.plainConflict || !floor.injected || floor.hollowLanded) return false;
  if (floor.probe === "faux-hollow" || floor.turnStatus === "failed") {
    return (
      floor.documentUnchanged &&
      floor.turnStatus === "failed" &&
      (floor.turnError ?? "").includes("说人话")
    );
  }
  if (floor.turnStatus !== "completed" && floor.turnStatus !== "applied") return false;
  return scores.after !== null && scores.negative !== null && scores.after < scores.negative;
}

async function scoreFloor(
  capture: EvalCapture,
  errors: string[],
): Promise<{ after: number | null; negative: number | null }> {
  const floor = capture.scenarios.floor;
  const finished = floor.turnStatus === "completed" || floor.turnStatus === "applied";
  if (floor.probe !== "live-model" || !finished || !floor.afterText.trim()) {
    return { after: null, negative: null };
  }
  const dir = await mkdtemp(join(tmpdir(), "lw-eval-floor-"));
  try {
    const after = join(dir, "after.md");
    const negative = join(dir, "negative.md");
    await writeFile(after, floor.afterText, "utf8");
    await writeFile(negative, NEGATIVE_CONTROL, "utf8");
    const tones = (await runLwEval(["aitone", after, negative])) as Record<
      string,
      { aitone_score?: number }
    >;
    return {
      after: typeof tones[after]?.aitone_score === "number" ? tones[after].aitone_score : null,
      negative:
        typeof tones[negative]?.aitone_score === "number" ? tones[negative].aitone_score : null,
    };
  } catch (error) {
    errors.push(error instanceof Error ? error.message : "地板 aitone 失败");
    return { after: null, negative: null };
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
}

export function renderReport(judgment: EvalJudgment): string {
  const when = judgment.generatedAt.slice(0, 10);
  const modelLine = judgment.live
    ? `模型：\`${judgment.model}\`（LW_EVAL_LIVE=1）。`
    : "模型：`test/faux`。这一跑没有访问真实模型。";
  const heldout = judgment.heldout.ran
    ? judgment.heldout.files
        .map((file) => `- \`${file.name}\`：aitone ${file.aitoneScore}`)
        .join("\n")
    : `未跑。${judgment.heldout.reason ? `${judgment.heldout.reason}。` : ""}原文不在仓库里，也不会被写进这份报告。`;
  const lines = [
    "# M2-01 H2 / H3 闸门补跑",
    "",
    `日期：${when}。${modelLine}`,
    "",
    judgment.pass ? "结论：**通过**。" : "结论：**未通过**。",
    "",
    ...failureLines(judgment),
    "## 写 → 改 → 回滚",
    "",
    judgment.rollback.pass
      ? "外部 Agent 只用任务 API 走完写作、修订和回滚。回滚后的正文与写作版本一致。"
      : "写、改、回滚没有走完，或回滚后的正文与写作版本不一致。",
    "",
    "## H2 检索规划",
    "",
    "这一跑没有金标来源包。通过线是：计划落库、没有证据时承认缺口、无支持断言被标出，并且 lw-eval 的 `ablation_sensitivity` 给出 `honest_gap`。`critical_coverage` 记下来，不拿它当通过线。",
    "",
    `- 通过：${judgment.h2.pass ? "是" : "否"}`,
    `- 问题数：${judgment.h2.questionCount}`,
    `- 缺口：${judgment.h2.gaps.length > 0 ? judgment.h2.gaps.join("；") : "无"}`,
    `- 承认缺口：${judgment.h2.admittedGap ? "是" : "否"}`,
    `- claim.flags：${judgment.h2.claimFlags ? "有" : "无"}`,
    `- ablation_sensitivity：${judgment.h2.sensitivity || "无"}`,
    `- critical_coverage：${judgment.h2.criticalCoverage ?? "无"}`,
    "",
    "## H3 风格稳定",
    "",
    "产品修订用 lw-eval 的 UPCR 和锚定编辑。元评测用仓库里的拼接样本和自然对照，不读留出集原文。说人话轴用 lw-eval 的 aitone：具体初稿必须低于中性负对照。",
    "",
    `- 通过：${judgment.h3.pass ? "是" : "否"}`,
    `- UPCR：${judgment.h3.upcr ?? "无"}`,
    `- 锚定编辑成功：${judgment.h3.anchoredSuccess === null ? "无" : judgment.h3.anchoredSuccess ? "是" : "否"}`,
    `- 拼接漂移窗口：${formatWindows(judgment.h3.spliceDriftWindows)}`,
    `- 对照漂移窗口：${formatWindows(judgment.h3.controlDriftWindows)}`,
    `- 初稿 aitone：${judgment.h3.draftAitone ?? "无"}`,
    `- 负对照 aitone：${judgment.h3.negativeAitone ?? "无"}`,
    "",
    "## 真实嵌入",
    "",
    embedParagraph(judgment),
    "",
    "## 华丽笔记与说人话地板",
    "",
    floorParagraph(judgment),
    "",
    "## 留出集",
    "",
    heldout,
    "",
    "## 真实模型",
    "",
    "真实模型不进每个 PR 的 CI。密钥只从环境变量读，不写进仓库、配置、日志或这份报告，也不进产品凭据库的持久数据。这一闸门用单独的临时库。",
    "",
    "原生 Gemini：",
    "",
    "```bash",
    "cd ts",
    'pnpm --filter "@luminwrite/cli..." build',
    "LW_EVAL_LIVE=1 LW_LLM_ENABLE=1 LW_EVAL_DEBUG=1 \\",
    "  LW_LLM_PROVIDER=gemini \\",
    "  LW_LLM_API_KEY=… LW_LLM_MODEL=gemini-4-argon \\",
    "  pnpm eval:gate -- --out docs/m2-01-h2-h3.md",
    "```",
    "",
    "`LW_LLM_BASE_URL` 不设时是 `https://generativelanguage.googleapis.com/v1beta`。密钥放在 `x-goog-api-key` 头里，不放进 URL。",
    "",
    "OpenAI 兼容端点（例如 Gemini 的 `/openai`）仍用 `LW_LLM_PROVIDER=openai-compatible`：",
    "",
    "```bash",
    "cd ts",
    'pnpm --filter "@luminwrite/cli..." build',
    "LW_EVAL_LIVE=1 LW_LLM_ENABLE=1 \\",
    "  LW_LLM_PROVIDER=openai-compatible \\",
    "  LW_LLM_BASE_URL=https://generativelanguage.googleapis.com/v1beta/openai \\",
    "  LW_LLM_API_KEY=… LW_LLM_MODEL=gemini-4-argon \\",
    "  pnpm eval:gate -- --out docs/m2-01-h2-h3.md",
    "```",
    "",
    "变量名：",
    "",
    "| 变量 | 作用 |",
    "|---|---|",
    "| `LW_EVAL_LIVE` | 必须是 `1` 才离开 faux。没设就只跑 `test/faux`，不读供应商密钥 |",
    "| `LW_LLM_ENABLE` | 必须是 `1` 才读写作密钥 |",
    "| `LW_LLM_API_KEY` | 写作密钥。没设则在上面两个开关都打开时读 `OPENAI_API_KEY` |",
    "| `LW_LLM_MODEL` | 写作模型 id。真实模型跑必填 |",
    "| `LW_LLM_BASE_URL` | 原生 Gemini 不设则是 `https://generativelanguage.googleapis.com/v1beta`。OpenAI 兼容不设则是 `https://api.openai.com/v1` |",
    "| `LW_LLM_PROVIDER` | `gemini`（原生 generateContent）、`openai-compatible`（默认）或 `anthropic-compatible` |",
    "| `LW_EMBED_API_KEY` | 嵌入密钥。三个嵌入变量要一起设，否则用本机桩 |",
    "| `LW_EMBED_BASE_URL` | 嵌入端点，OpenAI 兼容的 `/embeddings` |",
    "| `LW_EMBED_MODEL` | 嵌入模型 id |",
    "| `LW_EVAL_HELDOUT_DIR` | 留出集原文目录。读 `.md`、`.txt`、`.html`，不提交 |",
    "| `LW_EVAL_TASK_TIMEOUT_MS` | 单个任务的等待毫秒数。不设时真实模型 180000，faux 20000。超时会取消任务 |",
    "| `LW_EVAL_DEBUG` | `1` 时把每次模型请求的场景或任务、方法、路径、模型 id、状态码打到 stderr，响应里有用量也记下。每次工具返回一行：场景、工具名、isError、最多 200 字的摘录。`x-goog-api-key` 和 `Authorization` 只记 `[redacted]` |",
    "",
    "faux 全程：",
    "",
    "```bash",
    "cd ts",
    'pnpm --filter "@luminwrite/cli..." build',
    "pnpm eval:gate -- --out docs/m2-01-h2-h3.md",
    "```",
    "",
    "分步是 `lw eval run`、`lw eval judge`、`lw eval report`。`--dir` 放中间文件，默认在临时目录，不要提交。",
    "",
    "## 还没做",
    "",
    "- 真实模型冒烟要等密钥。上面的命令配齐变量后再跑，会重写这份报告。",
    "- H2 还没有金标来源包，所以没有报关键子问题覆盖率是否达标。",
    "- lw-eval v0.3 的文体包仍是 commentary 和 report。内置库的 5 个文体在 M2-06 用 `gemini-4-argon` 打分，密钥名是 `GEMINI_API_KEY`，这一块不读它。",
    "",
  ];
  if (judgment.errors.length > 0) {
    lines.push("## 错误", "", ...judgment.errors.map((error) => `- ${error}`), "");
  }
  return lines.join("\n");
}

function failureLines(judgment: EvalJudgment): string[] {
  const stored = judgment.failures.map((row) => `- ${row}`);
  const http = judgment.exchanges.map((item) => {
    const scenario = item.scenario ? `[${item.scenario}] ` : "";
    const model = item.model ? ` model=${item.model}` : "";
    const auth = item.auth ? ` ${item.auth}` : "";
    const usage = item.usage ? ` ${item.usage}` : "";
    const body = item.bodyExcerpt ? ` ${item.bodyExcerpt}` : "";
    return `- ${scenario}${item.method} ${item.path}${model}${auth} HTTP ${item.status}${usage}${body}`;
  });
  if (stored.length === 0 && http.length === 0) return [];
  const failed = stored.length > 0 || judgment.exchanges.some((item) => item.status >= 400);
  return [`## ${failed ? "失败" : "模型请求"}`, "", ...stored, ...http, ""];
}

function formatWindows(windows: number[]): string {
  return windows.length > 0 ? windows.join(", ") : "无";
}

function embedParagraph(judgment: EvalJudgment): string {
  const embed = judgment.embeddings;
  const where =
    embed.endpoint === "env"
      ? "端点来自 `LW_EMBED_BASE_URL`。"
      : "没有设置嵌入变量，所以打到本机的 OpenAI 兼容桩，不访问外网。";
  return [
    `传输：${embed.transport}。${where}`,
    `模型 id：\`${embed.model}\`。维度 ${embed.dim}。请求 ${embed.requests} 次。向量已写入：${embed.stored ? "是" : "否"}。检索命中：${embed.searchHit ? "是" : "否"}。`,
  ].join("");
}

function floorParagraph(judgment: EvalJudgment): string {
  const floor = judgment.floor;
  const probe =
    floor.probe === "faux-hollow"
      ? "faux 不用真实模型。这一跑换上会照着笔记写出空话的替身，让修订走到 screenOps。真实模型不走这个替身。"
      : "这一跑把笔记放进提示词，由真实模型写。空话被 screenOps 拒绝，或正文仍然过说人话，都算地板守住。";
  return [
    probe,
    `笔记「${floor.note}」plainConflict=${floor.plainConflict ? "true" : "false"}，注入提示词：${floor.injected ? "是" : "否"}。`,
    `回合状态：${floor.turnStatus || "无"}${floor.turnError ? `（${floor.turnError}）` : ""}。`,
    `正文未被空话改写：${floor.documentUnchanged && !floor.hollowLanded ? "是" : "否"}。`,
    floor.afterAitone !== null && floor.negativeAitone !== null
      ? `修订后 aitone：${floor.afterAitone}。负对照 aitone：${floor.negativeAitone}。`
      : "",
    floor.afterText ? `修订后正文：\n\n${floor.afterText}` : "",
    `通过：${floor.pass ? "是" : "否"}。`,
  ]
    .filter((line) => line.length > 0)
    .join("");
}

export async function readJsonFile<T>(path: string): Promise<T> {
  return JSON.parse(await readFile(path, "utf8")) as T;
}

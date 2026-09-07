/**
 * T09 前端测试：
 * - counts 消费：papers_full_text/abstract/unread 投影与缺省回退；上限解析
 *   （spec.max_papers 优先 → 启动表单值回退）；「上限 ≠ 已读」断言保持通过
 * - mock GET /runs/{id}/research 返回合同新增形状（spec + counts.papers_*）
 * - 纸面内联引用：splitCitationSegments 渲染时分段（bibliography 编号优先、
 *   本地编号回退、未知证据 known=false 警示）
 * - 质量门暂停引导：出现条件（paused + node_quality failed）与具体发现清单
 * - 无绕过断言：引导卡不含任何继续/重试按钮，gate 面板无绕过文案
 */
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

import { initialResearchSlice, mergeResearchProgress } from "../src/stores/research-slice.ts";
import type { ResearchProgressView } from "../src/lib/research-api.ts";
import {
  EVIDENCE_REPORT_ARTIFACT_ID,
  VALIDATION_DETAILS_ARTIFACT_ID,
  buildBibliographyNumbers,
  describeResearchCounts,
  fetchResearchProgress,
  paperReadingCounts,
  qualityGatePauseGuidance,
  researchMock,
  resolveReadingCap,
  setResearchMockEnabled,
  splitCitationSegments,
} from "../src/lib/research-api.ts";

const source = (path: string) => readFileSync(new URL(path, import.meta.url), "utf8");

function seed(): void {
  setResearchMockEnabled(true);
  researchMock.reset();
}

// ─── 1. counts 消费与上限解析 ───

test("paperReadingCounts projects the server papers_* key family and falls back to null", () => {
  assert.deepEqual(
    paperReadingCounts({ total: 14, completed: 9, papers_full_text: 3, papers_abstract: 2, papers_unread: 1 }),
    { fullText: 3, abstract: 2, unread: 1 },
  );
  // 部分键缺失：缺失的键按 0 展示（绝不静默消失）
  assert.deepEqual(paperReadingCounts({ papers_full_text: 3 }), { fullText: 3, abstract: 0, unread: 0 });
  // 键族完全缺失（旧后端）：回退任务计数摘要
  assert.equal(paperReadingCounts({ total: 5, completed: 2 }), null);
});

test("resolveReadingCap prefers the server spec projection and falls back to the start-form value", () => {
  assert.equal(resolveReadingCap(10, 20), 10, "spec.max_papers 优先于表单值");
  assert.equal(resolveReadingCap(null, 20), 20, "无 spec 时回退启动表单值");
  assert.equal(resolveReadingCap(undefined, null), null);
  assert.equal(resolveReadingCap(null, null), null);
});

test("the cap is never presented as papers read (T08 assertion keeps passing)", () => {
  // 任务计数摘要（无 papers_* 键）路径：上限 ≠ 已读 标注保留
  const incomplete = describeResearchCounts({ completed: 9, total: 14 }, 20);
  assert.ok(incomplete.capNote?.includes("≠ 已读篇数"));
  assert.ok(incomplete.capNote?.includes("20"));
  const label = describeResearchCounts({ completed: 2, total: 3 }, 20).label;
  assert.ok(!label.includes("已读"));
  const complete = describeResearchCounts({ completed: 14, total: 14 }, 20);
  assert.equal(complete.capNote, null);
});

test("mock GET research returns the T09 contract shape: spec projection plus papers_* counts", async () => {
  seed();
  const view = await fetchResearchProgress("run_research_demo");
  assert.deepEqual(view.counts, {
    total: 14, completed: 9, failed: 1, deferred: 2,
    papers_full_text: 3, papers_abstract: 2, papers_unread: 1,
  });
  assert.deepEqual(view.spec, {
    max_papers: 10, min_citable_sources: 5, evidence_requirement: "abstract_allowed", max_queries: 3, max_candidates: 60,
  });
  assert.equal(view.counts.papers_full_text, 3);
  assert.equal(view.counts.papers_abstract, 2);
  assert.equal(view.counts.papers_unread, 1);
});

test("mergeResearchProgress stores the spec projection and keeps it across stale GETs", () => {
  const fresh: ResearchProgressView = {
    run_id: "run_research_demo",
    phase: "gate_evidence",
    counts: { total: 14, completed: 9, papers_full_text: 3, papers_abstract: 2, papers_unread: 1 },
    spec: { max_papers: 10, min_citable_sources: 5, evidence_requirement: "abstract_allowed", max_queries: 3, max_candidates: 60 },
    tasks: [], errors: [], last_event_sequence: 42,
  };
  const slice = mergeResearchProgress(initialResearchSlice, fresh);
  assert.deepEqual(slice.spec?.max_papers, 10);

  // 旧 GET（缺 spec）：事件已推进时不覆盖，spec 保留
  const stale: ResearchProgressView = { ...fresh, spec: null, counts: { total: 1 }, last_event_sequence: 10 };
  const merged = mergeResearchProgress(slice, stale);
  assert.equal(merged.spec?.max_papers, 10);

  // 新 GET 缺 spec（旧后端）：保留既有 spec（不为 null 抹掉）
  const newerWithoutSpec: ResearchProgressView = { ...fresh, spec: undefined, last_event_sequence: 50 };
  assert.equal(mergeResearchProgress(slice, newerWithoutSpec).spec?.max_papers, 10);

  // 事件流未建立 progress 时，旧 GET 的 spec 也可整体采用
  const fromEmpty = mergeResearchProgress(initialResearchSlice, stale);
  assert.equal(fromEmpty.spec, null);
  assert.equal(fromEmpty.progress?.counts.total, 1);
});

test("research progress surface consumes counts.papers_* with the spec cap (source contract)", () => {
  const progress = source("../src/components/writing/research-progress.tsx");
  assert.match(progress, /paperReadingCounts\(counts\)/);
  assert.match(progress, /resolveReadingCap\(slice\.spec\?\.max_papers, maxPapers\)/);
  assert.match(progress, /data-testid="research-paper-counts"/);
  assert.match(progress, /全文 \{paperCounts\.fullText\}/);
  assert.match(progress, /摘要 \{paperCounts\.abstract\}/);
  assert.match(progress, /未读 \{paperCounts\.unread\}/);
  assert.match(progress, /启动表单值/);
  // 上限展示不冒充已读数
  assert.match(progress, /预算配置，非已读数/);
});

// ─── 2. 纸面内联引用渲染 ───

test("splitCitationSegments rewrites markers only at render time with bibliography numbers", () => {
  const numbers = { ev_1003: 1, ev_1001: 2 };
  const segments = splitCitationSegments("前文 [@ev_1003] 中段 [@ev_1001] 尾段 [@ev_1003]。", { numbers });
  assert.deepEqual(segments, [
    { kind: "text", text: "前文 " },
    { kind: "citation", evidenceId: "ev_1003", number: 1, known: true },
    { kind: "text", text: " 中段 " },
    { kind: "citation", evidenceId: "ev_1001", number: 2, known: true },
    { kind: "text", text: " 尾段 " },
    { kind: "citation", evidenceId: "ev_1003", number: 1, known: true },
    { kind: "text", text: "。" },
  ]);
});

test("splitCitationSegments falls back to appearance-order numbering without bibliography data", () => {
  const segments = splitCitationSegments("a [@ev_b] b [@ev_a] c [@ev_b]");
  assert.deepEqual(
    segments.filter((segment) => segment.kind === "citation"),
    [
      { kind: "citation", evidenceId: "ev_b", number: 1, known: true },
      { kind: "citation", evidenceId: "ev_a", number: 2, known: true },
      { kind: "citation", evidenceId: "ev_b", number: 1, known: true },
    ],
  );
  // 无标记：整段为文本
  assert.deepEqual(splitCitationSegments("没有任何标记"), [{ kind: "text", text: "没有任何标记" }]);
});

test("markers without matching evidence are flagged, never silently dropped", () => {
  const known = new Set(["ev_1001"]);
  const segments = splitCitationSegments("有据 [@ev_1001] 无据 [@ev_1042]。", { knownEvidenceIds: known });
  const missing = segments.find((segment) => segment.kind === "citation" && segment.evidenceId === "ev_1042") as
    | { kind: "citation"; evidenceId: string; number: number; known: boolean }
    | undefined;
  assert.ok(missing, "缺失证据的标记必须出现在渲染分段里");
  assert.equal(missing.known, false, "known=false 驱动警示样式");
  // 有据标记不受影响
  const hit = segments.find((segment) => segment.kind === "citation" && segment.evidenceId === "ev_1001");
  assert.equal(hit?.known, true);
});

test("buildBibliographyNumbers projects the T07 bibliography ordering; empty details fall back to local numbering", () => {
  assert.deepEqual(
    buildBibliographyNumbers({ bibliography: [{ number: 1, evidence_id: "ev_1003" }, { number: 2, evidence_id: "ev_1001" }] }),
    { ev_1003: 1, ev_1001: 2 },
  );
  assert.equal(buildBibliographyNumbers(null), null);
  assert.equal(buildBibliographyNumbers({}), null);
  assert.equal(buildBibliographyNumbers({ bibliography: [] }), null);
});

test("the document surface rewrites citations only at render time behind an explicit context (source contract)", () => {
  const surface = source("../src/components/document/document-surface.tsx");
  // 上下文通过 Provider 注入；prop 缺省 null 时正文原样渲染
  assert.match(surface, /CitationRenderContext\.Provider value=\{citationContext\}/);
  assert.match(surface, /citationContext = null/);
  const block = source("../src/components/document/document-block.tsx");
  // text 节点经渲染层转换，数据源 node.text 未被改写
  assert.match(block, /<CitationAwareText text=\{node\.text \?\? ""\} \/>/);
  assert.doesNotMatch(block, /node\.text =|node\.text\.replace/);
  const workspace = source("../src/pages/writing-workspace.tsx");
  assert.match(workspace, /buildBibliographyNumbers\(validationDetails\)/);
  assert.match(workspace, /citationContext=\{citationSurfaceContext\}/);
});

// ─── 3. 质量门暂停引导 ───

test("quality gate guidance appears only when the run is paused with node_quality failed", () => {
  const findings = [{ type: "invalid_citation", severity: "blocker", message: "草稿标记 [@ev_1042] 在引用索引中不存在" }];
  const reportIssues = [{ severity: "blocker", type: "invalid_citation", message: "quality gate blocked formal delivery: invalid_citation: ..." }];

  // 只 paused、节点未失败：不出现
  assert.equal(qualityGatePauseGuidance({ runStatus: "paused", nodeStatuses: { node_quality: "completed" }, reportIssues }), null);
  // 节点失败但 run 不在 paused：不出现
  assert.equal(qualityGatePauseGuidance({ runStatus: "running", nodeStatuses: { node_quality: "failed" }, reportIssues }), null);
  assert.equal(qualityGatePauseGuidance({ runStatus: null, nodeStatuses: { node_quality: "failed" } }), null);
  // paused + node_quality failed：出现
  const guidance = qualityGatePauseGuidance({
    runStatus: "paused",
    nodeStatuses: { node_research_read: "completed", node_quality: "failed" },
    reportIssues,
    validationFindings: findings,
    serverErrorCode: "EVIDENCE_INVALID",
  });
  assert.ok(guidance);
  assert.equal(guidance.headline, "引用校验未通过——请修正草稿引用后创建新运行（当前运行不会自动重试）");
});

test("quality gate guidance lists the concrete server findings with no bypass path", () => {
  const guidance = qualityGatePauseGuidance({
    runStatus: "paused",
    nodeStatuses: { node_quality: "failed" },
    reportIssues: [
      { severity: "blocker", type: "invalid_citation", message: "草稿标记 [@ev_1042] 在引用索引中不存在：引用可能被删除或改写，请核对草稿引用。" },
      { severity: "review", type: "unreviewed_claim", message: "该断言未经过语义复核，建议人工确认。" },
    ],
    validationFindings: [
      { type: "invalid_citation", severity: "blocker", evidence_id: "ev_1042", excerpt: "阻抗增量在 200 次循环后完全消失 [@ev_1042]。", message: "草稿标记 [@ev_1042] 在引用索引中不存在：引用可能被删除或改写，请核对草稿引用。" },
    ],
    serverErrorCode: "EVIDENCE_INVALID",
  });
  assert.ok(guidance);
  // evidence_report 两条 + 校验明细一条 + 服务端错误码一条（逐条可检查）
  assert.equal(guidance.findings.length, 4);
  assert.ok(guidance.findings.some((finding) => finding.includes("ev_1042")), "可检查到证据 id");
  assert.ok(guidance.findings.some((finding) => finding.includes("阻抗增量在 200 次循环后完全消失")), "可检查到具体句子");
  assert.ok(guidance.findings.some((finding) => finding.includes("EVIDENCE_INVALID")), "服务端错误码可见");
  assert.ok(guidance.note.includes("新运行"));
  assert.ok(!guidance.note.includes("继续运行"));
});

test("the guidance card renders findings and never offers a continue/resume button", () => {
  const card = source("../src/components/writing/research-quality-gate-card.tsx");
  assert.match(card, /data-testid="quality-gate-guidance"/);
  assert.match(card, /引用校验未通过/);
  assert.match(card, /data-testid="quality-gate-findings"/);
  assert.ok(!/<Button/.test(card), "引导卡不渲染任何按钮");
  assert.ok(!/onClick|onResume|onContinue|href=/.test(card), "引导卡没有交互入口");
  for (const banned of ["继续运行", "重试运行", "恢复运行", "跳过质量门", "resume", "onResume", "onContinue"]) {
    assert.ok(!card.includes(banned), `引导卡不得出现绕过入口：${banned}`);
  }
  const workspace = source("../src/pages/writing-workspace.tsx");
  assert.match(workspace, /<ResearchQualityGateCard guidance=\{qualityGuidance\} \/>/);
});

test("mock artifacts expose the quality-gate payloads for the demo scenario", async () => {
  seed();
  const { fetchRunArtifactContent } = await import("../src/lib/research-api.ts");
  const report = await fetchRunArtifactContent("run_research_demo", EVIDENCE_REPORT_ARTIFACT_ID) as { passed?: boolean; issues?: Array<{ severity: string; type: string }> };
  assert.equal(report.passed, false);
  assert.ok(report.issues?.some((issue) => issue.severity === "blocker"));

  const details = await fetchRunArtifactContent("run_research_demo", VALIDATION_DETAILS_ARTIFACT_ID) as { schema_version?: string; bibliography?: Array<{ number: number }> };
  assert.equal(details.schema_version, "research-validation-details/1");
  assert.deepEqual(details.bibliography?.map((entry) => entry.number), [1, 2, 3]);

  researchMock.seedQualityGateFailure("run_research_demo");
  const progress = await fetchResearchProgress("run_research_demo");
  assert.ok(progress.errors.some((error) => error.error_code === "EVIDENCE_INVALID"));
});

/**
 * 研究综述运行时测试（T08）：
 * - reducer 对旧事件 / 重复事件 / 跨 run 事件 / 未知事件类型容错
 * - GET 快照与事件按 sequence 合并（旧 GET 不覆盖新事件）
 * - gate 确认流程（mock fetch：202 → 轮询 → approved）
 * - STALE_GATE 刷新路径
 * - 进度投影「20 上限 ≠ 已读」断言
 * - 表单默认值与合同一致
 */
import assert from "node:assert/strict";
import test from "node:test";

import { initialWritingRuntimeProjection, projectWritingEvent } from "../src/stores/writing-runtime-store.ts";
import {
  applyResearchEvent,
  initialResearchSlice,
  mergeGateView,
  mergeResearchProgress,
  pendingGate,
} from "../src/stores/research-slice.ts";
import type { GateView, ResearchProgressView } from "../src/lib/research-api.ts";
import {
  buildGateDecisionRequest,
  buildResearchSpec,
  defaultResearchSpecDraft,
  describeResearchCounts,
  fetchGate,
  fetchResearchProgress,
  findCitationMarkers,
  isResearchReviewEnabled,
  pollGateUntilResolved,
  postGateDecision,
  postOutlineRevision,
  researchMock,
  setResearchMockEnabled,
  setResearchReviewEnabled,
  uuidV4,
} from "../src/lib/research-api.ts";

const RUN = "run_research_demo";
const OTHER_RUN = "run_research_other";

function ledgerEvent(sequence: number, eventType: string, data: Record<string, unknown>, runId = RUN) {
  return {
    protocol: "lumin-writing.v2" as const,
    type: "writing.ledger.event",
    run_id: runId,
    sequence,
    timestamp: "2026-09-07T00:00:00Z",
    status: "paused",
    payload: { event_type: eventType, entity_kind: "research_gate", entity_id: String(data.gate_id ?? ""), data },
  };
}

function pendingGateView(overrides: Partial<GateView> = {}): GateView {
  return {
    gate_id: "gate_evidence_1",
    run_id: RUN,
    node_id: "research_evidence_gate",
    gate_kind: "evidence",
    plan_id: "plan_1",
    plan_version: 1,
    plan_hash: "sha256:plan",
    revision: 1,
    status: "pending",
    input_ref: { artifact_id: "art_pack", version: 1, content_hash: "sha256:pack" },
    allowed_operations: ["decide"],
    blocked_reason: "awaiting_gate_decision",
    last_event_sequence: 10,
    ...overrides,
  };
}

/** mock 层重置（每个用例独立场景）。 */
function seed(): void {
  setResearchMockEnabled(true);
  researchMock.reset();
}

// ─── 表单默认值与合同 ───

test("research spec form defaults match contracts.md §1 example", () => {
  const draft = defaultResearchSpecDraft();
  // 默认草稿未填研究问题：客户端明确拦截
  assert.ok(buildResearchSpec(draft).problems.includes("请填写研究问题"));

  const filled = buildResearchSpec({ ...draft, central_question: "固态电解质界面稳定性研究现状如何？" });
  assert.equal(filled.problems.length, 0);
  assert.deepEqual(filled.spec, {
    version: "research-spec/1",
    review_kind: "narrative",
    year_from: 2020,
    year_to: 2026,
    exclusion_terms: [],
    max_queries: 3,
    max_candidates: 60,
    max_papers: 10,
    min_citable_sources: 5,
    evidence_requirement: "abstract_allowed",
    selection_policy_version: "selection/1",
    reader_policy_version: "reader/1",
    generator: "lumin_writer",
    citation_style: "numeric",
  });
});

test("research spec client-side constraint hints mirror contracts.md §1", () => {
  const draft = { ...defaultResearchSpecDraft(), central_question: "问题", max_papers: "21", min_citable_sources: "25" };
  const problems = buildResearchSpec(draft).problems;
  assert.ok(problems.some((problem) => problem.includes("1–20")));
  assert.ok(problems.some((problem) => problem.includes("不能超过阅读上限")));
  const badYears = buildResearchSpec({ ...defaultResearchSpecDraft(), central_question: "问题", year_from: "2026", year_to: "2020" });
  assert.ok(badYears.problems.some((problem) => problem.includes("年份")));
  const badQueries = buildResearchSpec({ ...defaultResearchSpecDraft(), central_question: "问题", max_queries: "4" });
  assert.ok(badQueries.problems.some((problem) => problem.includes("1–3")));
});

// ─── reducer：事件容错与投影 ───

test("research.progress events project phase and counts; duplicates and stale events cannot rewind", () => {
  let slice = initialResearchSlice;
  slice = applyResearchEvent(slice, ledgerEvent(5, "research.progress", { phase: "reading", completed: 3, total: 14, failed: 1, deferred: 2 }));
  assert.equal(slice.progress?.phase, "reading");
  assert.deepEqual(slice.progress?.counts, { completed: 3, total: 14, failed: 1, deferred: 2 });

  // 重复（同序号）与旧事件（更小序号）都不会回退
  const afterDuplicate = applyResearchEvent(slice, ledgerEvent(5, "research.progress", { phase: "discovering", completed: 0, total: 0 }));
  assert.strictEqual(afterDuplicate, slice);
  const afterStale = applyResearchEvent(slice, ledgerEvent(4, "research.progress", { phase: "discovering", completed: 0, total: 0 }));
  assert.strictEqual(afterStale, slice);

  const advanced = applyResearchEvent(slice, ledgerEvent(6, "research.progress", { phase: "packing", completed: 12 }));
  assert.equal(advanced.progress?.phase, "packing");
  assert.equal(advanced.progress?.counts.completed, 12);
  // 未携带的字段保留旧值
  assert.equal(advanced.progress?.counts.total, 14);
});

test("gate.pending then gate.decided update the slice; pending replay cannot un-approve", () => {
  let slice = initialResearchSlice;
  slice = applyResearchEvent(slice, ledgerEvent(11, "gate.pending", {
    gate_id: "gate_evidence_1", gate_kind: "evidence", revision: 1, status: "pending",
    input_ref: { artifact_id: "art_pack", version: 1, content_hash: "sha256:pack" },
  }));
  const gate = pendingGate(slice);
  assert.ok(gate);
  assert.equal(gate.gate_id, "gate_evidence_1");
  assert.equal(gate.status, "pending");
  assert.deepEqual(gate.input_ref, { artifact_id: "art_pack", version: 1, content_hash: "sha256:pack" });

  slice = applyResearchEvent(slice, ledgerEvent(12, "gate.decided", { gate_id: "gate_evidence_1", status: "approved", decision: "approve" }));
  assert.equal(pendingGate(slice), null);
  assert.equal(slice.gates.gate_evidence_1.status, "approved");

  // 迟到的 gate.pending 重放不得回退
  const replayed = applyResearchEvent(slice, ledgerEvent(11, "gate.pending", { gate_id: "gate_evidence_1", status: "pending" }));
  assert.equal(replayed.gates.gate_evidence_1.status, "approved");
  assert.equal(replayed.lastEventSequence, 12);
});

test("cross-run research events and unknown ledger event types are tolerated without crashing", () => {
  let slice: typeof initialResearchSlice = { ...initialResearchSlice, runId: RUN };
  slice = applyResearchEvent(slice, ledgerEvent(3, "research.progress", { phase: "reading", total: 9 }, OTHER_RUN));
  assert.strictEqual(slice.progress, null);
  assert.equal(slice.lastEventSequence, 0);

  const unknown = applyResearchEvent(slice, ledgerEvent(4, "research.unknown_future", { phase: "x" }));
  assert.strictEqual(unknown, slice);

  // 非 ledger 形态的原生 research.* / gate.* 事件也消费（向前兼容）
  slice = applyResearchEvent(slice, {
    protocol: "lumin-writing.v2", type: "research.progress", run_id: RUN, sequence: 7,
    timestamp: "2026-09-07T00:00:00Z", status: "running",
    payload: { phase: "fetching", completed: 5, total: 20 },
  } as never);
  assert.equal(slice.progress?.phase, "fetching");
});

test("the shared runtime projection keeps research state consistent after refresh (GET-first rebuild)", () => {
  let projection = { ...initialWritingRuntimeProjection, run: { run_id: RUN } as never };
  projection = projectWritingEvent(projection, ledgerEvent(1, "research.progress", { phase: "reading", completed: 4, total: 14 }) as never);
  projection = projectWritingEvent(projection, ledgerEvent(2, "gate.pending", { gate_id: "gate_outline_1", gate_kind: "outline", status: "pending", revision: 2 }) as never);
  assert.equal(projection.research.progress?.phase, "reading");
  assert.equal(pendingGate(projection.research)?.gate_id, "gate_outline_1");

  // 事件被重新投喂（刷新场景）：研究切片不变（幂等）
  const replayed = projectWritingEvent(projection, ledgerEvent(2, "gate.pending", { gate_id: "gate_outline_1", status: "pending" }) as never);
  assert.strictEqual(replayed.research, projection.research);
});

// ─── GET 合并规则 ───

test("fresh GET snapshot wins; stale GET never overwrites newer event state", () => {
  // 先建立事件投影：phase=packing（seq 20）
  let slice = applyResearchEvent(initialResearchSlice, ledgerEvent(20, "research.progress", { phase: "packing", completed: 12, total: 14, failed: 1, deferred: 2 }));
  slice = applyResearchEvent(slice, ledgerEvent(21, "gate.pending", { gate_id: "gate_evidence_1", status: "pending", revision: 1 }));

  // 旧 GET（last_event_sequence=18）：不得覆盖 phase/counts/gate
  const staleGet: ResearchProgressView = {
    run_id: RUN, phase: "reading",
    counts: { completed: 3, total: 9, failed: 0, deferred: 0 },
    tasks: [{ task_key: "discover:q1", node_id: "n", phase: "discover", status: "completed", attempt: 1 }],
    errors: [], last_event_sequence: 18,
  };
  const mergedStale = mergeResearchProgress(slice, staleGet);
  assert.equal(mergedStale.progress?.phase, "packing");
  assert.equal(mergedStale.progress?.counts.completed, 12);
  assert.ok(pendingGate(mergedStale));
  // 但补充了事件流没有的任务摘要
  assert.equal(mergedStale.progress?.tasks.length, 1);

  // 新 GET（last_event_sequence=25）：整体生效
  const freshGet: ResearchProgressView = {
    run_id: RUN, phase: "gate_evidence",
    counts: { completed: 14, total: 14, failed: 1, deferred: 2 },
    tasks: [], errors: [],
    active_gate: pendingGateView({ last_event_sequence: 25 }),
    last_event_sequence: 25,
  };
  const mergedFresh = mergeResearchProgress(slice, freshGet);
  assert.equal(mergedFresh.progress?.phase, "gate_evidence");
  assert.equal(mergedFresh.gates.gate_evidence_1.last_event_sequence, 25);

  // 跨 run 的 GET 直接拒绝（保持原切片引用）
  const scoped = { ...slice, runId: RUN };
  const crossRun = mergeResearchProgress(scoped, { ...staleGet, run_id: OTHER_RUN });
  assert.strictEqual(crossRun, scoped);
});

test("gate view merge ignores stale polling results", () => {
  const fresh = pendingGateView({ status: "approved", last_event_sequence: 30 });
  let gates = mergeGateView({}, pendingGateView({ last_event_sequence: 10 }));
  gates = mergeGateView(gates, fresh);
  assert.equal(gates.gate_evidence_1.status, "approved");
  // 旧轮询结果（seq 20 < 30）不回退
  const rolled = mergeGateView(gates, pendingGateView({ status: "pending", last_event_sequence: 20 }));
  assert.equal(rolled.gate_evidence_1.status, "approved");
});

// ─── 进度投影文案 ───

test("progress projection never presents the max_papers cap as papers read", () => {
  // 未完成：completed < total → 必须带「上限 ≠ 已读」标注
  const incomplete = describeResearchCounts({ completed: 9, total: 14 }, 20);
  assert.equal(incomplete.label, "任务 9/14 完成");
  assert.ok(incomplete.capNote?.includes("≠ 已读篇数"));
  assert.ok(incomplete.capNote?.includes("20"));

  // 完成：不再误导
  const complete = describeResearchCounts({ completed: 14, total: 14 }, 20);
  assert.equal(complete.capNote, null);

  // max_papers=20 的上限值本身不出现在「已读」表述里
  const label = describeResearchCounts({ completed: 2, total: 3 }, 20).label;
  assert.ok(!label.includes("已读"));
});

// ─── gate 确认请求绑定 ───

test("gate decision request binds server-returned input_ref/plan_hash/revision, never client-computed values", () => {
  const gate = pendingGateView({
    plan_hash: "sha256:server-plan-hash",
    revision: 3,
    input_ref: { artifact_id: "art_outline_v3", version: 2, content_hash: "sha256:server-artifact-hash" },
  });
  const request = buildGateDecisionRequest(gate);
  assert.deepEqual(request, {
    plan_id: "plan_1",
    plan_version: 1,
    plan_hash: "sha256:server-plan-hash",
    gate_revision: 3,
    input_ref: { artifact_id: "art_outline_v3", version: 2, content_hash: "sha256:server-artifact-hash" },
    decision: "approve",
  });
  assert.equal(uuidV4().length, 36);
  assert.match(uuidV4(), /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
});

// ─── mock fetch：确认流程与 STALE_GATE ───

test("gate confirm flow over mock fetch: 202 -> poll GET -> approved", async () => {
  seed();
  const gate = await fetchGate(RUN, `${RUN}_evidence`);
  assert.equal(gate.status, "pending");
  const request = buildGateDecisionRequest(gate);
  const decision = await postGateDecision(RUN, `${RUN}_evidence`, request);
  assert.equal(decision.status, "approved");
  assert.equal(decision.resume_status, "queued");
  // 轮询 GET：状态已离开 pending
  const resolved = await pollGateUntilResolved(RUN, `${RUN}_evidence`, { maxAttempts: 3, delayMs: 1 });
  assert.equal(resolved.status, "approved");
  assert.deepEqual(resolved.allowed_operations, []);

  // outline gate 已自动到位（证据确认后）
  const outline = await fetchGate(RUN, `${RUN}_outline`);
  assert.equal(outline.status, "pending");
  assert.equal(outline.gate_kind, "outline");
  assert.ok(outline.allowed_operations.includes("save_outline_revision"));
});

test("stale gate decision returns STALE_GATE; refresh path recovers with the new revision", async () => {
  seed();
  // mock 运行从证据 gate 开始：先走完证据确认，提纲 gate 才出现
  const evidence = await fetchGate(RUN, `${RUN}_evidence`);
  await postGateDecision(RUN, `${RUN}_evidence`, buildGateDecisionRequest(evidence));

  const outline = await fetchGate(RUN, `${RUN}_outline`);
  assert.equal(outline.gate_kind, "outline");
  assert.equal(outline.revision, 1);
  assert.equal(outline.input_ref?.artifact_id, "art_outline_demo_v1");

  // 保存提纲修订 → revision 2 + 新 outline ref（201 语义）
  const saved = await postOutlineRevision(RUN, `${RUN}_outline`, {
    plan_id: outline.plan_id,
    plan_version: outline.plan_version,
    plan_hash: outline.plan_hash,
    gate_revision: outline.revision,
    outline_ref: outline.input_ref!,
    outline: {
      sections: [{ section_id: "sec_intro", title: "引言（修订）", central_point: "界定范围。", evidence_ids: [], gaps: [] }],
      limitations: [],
    },
  });
  assert.equal(saved.gate_revision, 2);
  assert.equal(saved.outline_ref.artifact_id, "art_outline_demo_v2");

  // 用旧 revision 确认 → 409 STALE_GATE
  const staleRequest = { ...buildGateDecisionRequest(outline), gate_revision: 1 };
  await assert.rejects(
    () => postGateDecision(RUN, `${RUN}_outline`, staleRequest),
    (error: unknown) => error instanceof Error && (error as { code?: string }).code === "STALE_GATE",
  );

  // 刷新路径：重新 GET gate，用新 ref/revision 确认成功
  const refreshed = await fetchGate(RUN, `${RUN}_outline`);
  assert.equal(refreshed.revision, 2);
  assert.equal(refreshed.input_ref?.artifact_id, "art_outline_demo_v2");
  const ok = await postGateDecision(RUN, `${RUN}_outline`, buildGateDecisionRequest(refreshed));
  assert.equal(ok.status, "approved");

  const run = await fetchResearchProgress(RUN);
  assert.equal(run.phase, "writing");
  assert.equal(run.active_gate, undefined);
});

test("double decision with a different idempotency key reports GATE_ALREADY_DECIDED", async () => {
  seed();
  const gate = await fetchGate(RUN, `${RUN}_evidence`);
  await postGateDecision(RUN, `${RUN}_evidence`, buildGateDecisionRequest(gate));
  await assert.rejects(
    () => postGateDecision(RUN, `${RUN}_evidence`, buildGateDecisionRequest(gate)),
    (error: unknown) => error instanceof Error && (error as { code?: string }).code === "GATE_ALREADY_DECIDED",
  );
});

test("mock progress restores the pending gate page after a simulated refresh", async () => {
  seed();
  const progress = await fetchResearchProgress(RUN);
  assert.equal(progress.phase, "gate_evidence");
  assert.ok(progress.active_gate);
  assert.equal(progress.active_gate.gate_kind, "evidence");
  // GET 投影重建待确认页面：pending gate 可直接渲染（刷新恢复语义）
  const merged = mergeResearchProgress(initialResearchSlice, progress);
  assert.equal(pendingGate(merged)?.gate_id, `${RUN}_evidence`);
});

// ─── 功能开关 ───

test("RESEARCH_REVIEW_ENABLED=false disables the entry with a visible reason", () => {
  setResearchReviewEnabled(false);
  assert.equal(isResearchReviewEnabled(), false);
  // mock 开关与功能开关相互独立
  assert.doesNotThrow(() => setResearchMockEnabled(true));
  setResearchReviewEnabled(true);
  assert.equal(isResearchReviewEnabled(), true);
});

// ─── 引用标记 ───

test("citation markers scan [@ev_xxx] tokens in order and dedupe", () => {
  const text = "界面层趋于稳定 [@ev_1001]，而热循环保持率 [@ev_1002] 支撑第二段；再引 [@ev_1001] 不重复。";
  assert.deepEqual(findCitationMarkers(text), ["ev_1001", "ev_1002"]);
  assert.deepEqual(findCitationMarkers("没有引用标记的文本 [@ev_unknown_missing]"), ["ev_unknown_missing"]);
  assert.deepEqual(findCitationMarkers(""), []);
});

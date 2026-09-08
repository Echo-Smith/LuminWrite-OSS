/**
 * F1 研究综述真实启动链路测试（HTTP-mocked fetch）：
 * - 客户端封存合同哈希与后端 Go 金样固 pin 的 v1.1 fixture 完全一致
 * - 请求顺序：document → contract → confirm → plan → run（→ awaiting_approval 时 approve）
 * - 合同体带 schema_version=lcp/1.1 + research 字段 + 素材引用透传（metadata.material_refs）
 * - 后续步骤全部使用服务端返回的 contract id/hash 与 plan envelope/permissions
 * - 503 RESEARCH_UNAVAILABLE / 400 INVALID_RESEARCH_SPEC 的错误展示路径
 * - 最终 run_id 可被工作台（writing-runtime-store.loadRun）打开
 * - mock 开启时仍返回演示运行（VITE_RESEARCH_MOCK 语义不回退）
 */
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

import {
  canonicalGoJSON,
  goContentHash,
  isResearchMockEnabled,
  MOCK_RESEARCH_RUN_ID,
  researchLaunchProblems,
  setResearchMockEnabled,
  startResearchRun,
  uuidV4,
  type ResearchLaunchInput,
} from "../src/lib/research-api.ts";
import { useWritingRuntimeStore } from "../src/stores/writing-runtime-store.ts";
import type { ResearchSpec } from "../src/lib/writing-runtime-types.ts";

// ─── fetch mock 基建 ─────────────────────────────────────────────────────────

interface RecordedCall {
  path: string;
  method: string;
  idempotencyKey: string | null;
  body: Record<string, unknown> | null;
}

type MockResult = { status: number; body: unknown };

const originalFetch = globalThis.fetch;
let calls: RecordedCall[] = [];

function ok(data: unknown, status = 200): MockResult {
  return { status, body: { success: true, data } };
}

function fail(status: number, code: string, message: string): MockResult {
  return { status, body: { success: false, error: { code, message } } };
}

function installFetch(handler: (path: string, method: string, body: Record<string, unknown> | null) => MockResult | undefined): void {
  calls = [];
  globalThis.fetch = (async (input: RequestInfo | URL, init: RequestInit = {}) => {
    const path = typeof input === "string" ? input : String(input instanceof URL ? input : input.url);
    const method = (init.method ?? "GET").toUpperCase();
    const headers = (init.headers ?? {}) as Record<string, string>;
    const body = typeof init.body === "string" ? (JSON.parse(init.body) as Record<string, unknown>) : null;
    calls.push({ path, method, idempotencyKey: headers["Idempotency-Key"] ?? null, body });
    const result = handler(path, method, body) ?? fail(404, "WRITING_RESOURCE_NOT_FOUND", `unmocked ${method} ${path}`);
    return new Response(JSON.stringify(result.body), { status: result.status, headers: { "Content-Type": "application/json" } });
  }) as typeof fetch;
}

test.afterEach(() => {
  globalThis.fetch = originalFetch;
  setResearchMockEnabled(false);
});

const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

function launchInput(spec: ResearchSpec, overrides: Partial<ResearchLaunchInput> = {}): ResearchLaunchInput {
  return {
    spec,
    central_question: "固态电解质界面稳定性目前的共识与分歧是什么？",
    audience: "材料学方向的研究生",
    language: "中文",
    length_min: "3000",
    length_max: "6000",
    allow_external_research: true,
    material_refs: [{ material_id: "mat_kb_1", title: "用户论文一" }],
    ...overrides,
  };
}

function fakeSpec(overrides: Partial<ResearchSpec> = {}): ResearchSpec {
  return {
    version: "research-spec/1",
    review_kind: "narrative",
    year_from: 2020,
    year_to: 2026,
    exclusion_terms: ["燃料电池"],
    max_queries: 3,
    max_candidates: 60,
    max_papers: 10,
    min_citable_sources: 5,
    evidence_requirement: "abstract_allowed",
    selection_policy_version: "selection/1",
    reader_policy_version: "reader/1",
    generator: "lumin_writer",
    citation_style: "numeric",
    ...overrides,
  };
}

const SERVER_CONTRACT_HASH = "sha256:" + "cd".repeat(32);
const SERVER_PLAN_HASH = "sha256:" + "ab".repeat(32);
const SERVER_PERMISSIONS = ["document.revision", "external.research", "materials.read", "model.invoke", "validation.run"];

interface ChainCapture {
  draftContract: Record<string, unknown>;
  confirmedContract: Record<string, unknown>;
  runBody: Record<string, unknown> | null;
}

/** 驱动标准五步链路的 fetch mock；返回各步捕获的请求体。 */
function installHappyChain(options: { runStatus: "planned" | "awaiting_approval" }, capture: ChainCapture): void {
  installFetch((path, method, body) => {
    if (method === "POST" && path === "/api/v2/documents") {
      assert.deepEqual(body?.metadata, { material_refs: [{ material_id: "mat_kb_1", title: "用户论文一" }] });
      assert.equal(body?.title, "固态电解质界面稳定性目前的共识与分歧是什么？");
      return ok({ document_id: "doc_real_1", current_version_id: "" }, 201);
    }
    if (method === "POST" && path === "/api/v2/documents/doc_real_1/contracts") {
      capture.draftContract = body?.contract as Record<string, unknown>;
      return ok({ document_id: "doc_real_1", contract: { contract_id: "ctr_server_1", version: 1, contract_hash: SERVER_CONTRACT_HASH, status: "draft" } }, 201);
    }
    if (method === "POST" && /^\/api\/v2\/contracts\/ctr_server_1\/confirm$/.test(path)) {
      assert.equal(body?.previous_version, 1);
      capture.confirmedContract = body?.contract as Record<string, unknown>;
      return ok({ document_id: "doc_real_1", contract: { contract_id: "ctr_server_1", version: 2, contract_hash: SERVER_CONTRACT_HASH, status: "confirmed" } });
    }
    if (method === "POST" && path === "/api/v2/documents/doc_real_1/plans") {
      return ok({
        plan: {
          schema_version: "lcp/1.0",
          intent_plan: body?.intent_plan,
          executable_plan: { plan_id: "plan_real_1", plan_hash: SERVER_PLAN_HASH },
          strategy_decision: { approval_required: options.runStatus === "awaiting_approval" },
        },
        budget: body?.budget,
        permissions: SERVER_PERMISSIONS,
        base_version_id: "",
      });
    }
    if (method === "POST" && path === "/api/v2/runs") {
      capture.runBody = body;
      return ok({ run_id: "run_real_1", document_id: "doc_real_1", status: options.runStatus }, 201);
    }
    if (method === "POST" && path === "/api/v2/runs/run_real_1/approve") {
      return ok({ run_id: "run_real_1", document_id: "doc_real_1", status: "running" });
    }
    return undefined;
  });
}

// ─── 合同封存：与后端 Go 金样一致 ─────────────────────────────────────────────

test("client-side canonical sealing reproduces the Go-pinned v1.1 contract hash", async () => {
  const fixture = JSON.parse(readFileSync(new URL("../../specs/lcp/v1.1/fixtures/writing-contract.research-review.valid.json", import.meta.url), "utf8")) as Record<string, unknown>;
  const pinnedHash = fixture.contract_hash as string;
  delete fixture.contract_hash; // ComputeHash 排除 contract_hash（omitempty）
  assert.equal(await goContentHash(canonicalGoJSON(fixture)), pinnedHash);
});

// ─── 真实链路：请求顺序与请求体 ───────────────────────────────────────────────

test("research launch drives document → contract → confirm → plan → run with server-returned refs", async () => {
  const capture: ChainCapture = { draftContract: {}, confirmedContract: {}, runBody: null };
  installHappyChain({ runStatus: "planned" }, capture);

  const { run_id } = await startResearchRun(launchInput(fakeSpec()));
  assert.equal(run_id, "run_real_1");

  // 请求顺序（无 approve：planned 状态直接返回）
  assert.deepEqual(calls.map((call) => `${call.method} ${call.path}`), [
    "POST /api/v2/documents",
    "POST /api/v2/documents/doc_real_1/contracts",
    "POST /api/v2/contracts/ctr_server_1/confirm",
    "POST /api/v2/documents/doc_real_1/plans",
    "POST /api/v2/runs",
  ]);
  // Idempotency-Key 惯例：documents/runs（及 approve）必须带 UUID；
  // contract/confirm/plans 端点不读取该 header（与后端 handler 一致）。
  for (const call of calls) {
    if (call.path.endsWith("/writing/documents") || call.path === "/api/v2/runs" || call.path.endsWith("/approve")) {
      assert.match(call.idempotencyKey ?? "", UUID_PATTERN, `missing UUID Idempotency-Key on ${call.path}`);
    }
  }

  // 合同草稿：v1.1 + research 全字段 + 交付/素材/联网语义
  const draft = capture.draftContract;
  assert.equal(draft.schema_version, "lcp/1.1");
  assert.equal(draft.status, "draft");
  assert.equal(draft.version, 1);
  assert.match(draft.contract_hash as string, /^sha256:[0-9a-f]{64}$/);
  assert.equal((draft.content as Record<string, unknown>).central_question, "固态电解质界面稳定性目前的共识与分歧是什么？");
  assert.equal((draft.audience as Record<string, unknown>).role, "材料学方向的研究生");
  assert.deepEqual((draft.delivery as Record<string, unknown>).length, { min: 3000, max: 6000 });
  assert.equal((draft.material_policy as Record<string, unknown>).allow_external_research, true);
  assert.deepEqual(draft.research, fakeSpec());
  // 4 个协作字段必须带 value_hash 归因（服务端 validateSourceAttributions 要求）
  const attributions = draft.source_attributions as Array<Record<string, string>>;
  assert.deepEqual(attributions.map((entry) => entry.field_path), [
    "/collaboration/task_mode",
    "/collaboration/orchestration_mode",
    "/collaboration/assurance_level",
    "/collaboration/approval_mode",
  ]);
  for (const entry of attributions) {
    assert.match(entry.value_hash, /^sha256:[0-9a-f]{64}$/);
  }
  // 确认版本：version 递增 + status confirmed
  assert.equal(capture.confirmedContract.version, 2);
  assert.equal(capture.confirmedContract.status, "confirmed");
  assert.equal(capture.confirmedContract.contract_id, draft.contract_id);

  // 运行创建使用服务端返回的合同记录（id/hash 来自 confirm 响应，非客户端重算）
  const runBody = capture.runBody as Record<string, unknown>;
  assert.equal(runBody.document_id, "doc_real_1");
  assert.equal(runBody.contract_id, "ctr_server_1");
  assert.equal(runBody.contract_version, 2);
  assert.equal(runBody.contract_hash, SERVER_CONTRACT_HASH);
  assert.deepEqual(runBody.permissions, SERVER_PERMISSIONS);
});

test("awaiting_approval run triggers the plan approval step before hand-off", async () => {
  const capture: ChainCapture = { draftContract: {}, confirmedContract: {}, runBody: null };
  installHappyChain({ runStatus: "awaiting_approval" }, capture);

  const { run_id } = await startResearchRun(launchInput(fakeSpec()));
  assert.equal(run_id, "run_real_1");
  assert.deepEqual(calls.map((call) => `${call.method} ${call.path}`), [
    "POST /api/v2/documents",
    "POST /api/v2/documents/doc_real_1/contracts",
    "POST /api/v2/contracts/ctr_server_1/confirm",
    "POST /api/v2/documents/doc_real_1/plans",
    "POST /api/v2/runs",
    "POST /api/v2/runs/run_real_1/approve",
  ]);
  const approve = calls.at(-1)!;
  assert.deepEqual(approve.body, {
    plan_id: "plan_real_1",
    plan_version: 1,
    plan_hash: SERVER_PLAN_HASH,
    permissions: SERVER_PERMISSIONS,
  });
});

test("the returned run id opens in the governed workbench (loadRun over /api/v2/runs)", async () => {
  const capture: ChainCapture = { draftContract: {}, confirmedContract: {}, runBody: null };
  installHappyChain({ runStatus: "planned" }, capture);
  useWritingRuntimeStore.getState().resetRuntime();
  const { run_id } = await startResearchRun(launchInput(fakeSpec()));
  assert.equal(run_id, "run_real_1");
  installFetch((path, method) => {
    if (method === "GET" && path === "/api/v2/runs/run_real_1") {
      return ok({
        run_id: "run_real_1", document_id: "doc_real_1", contract_id: "ctr_server_1",
        contract_hash: SERVER_CONTRACT_HASH, contract_version: 2, status: "planned",
        approval_mode: "conditional", budget: { max_cost_usd: 100, max_duration_ms: 7200000, max_concurrency: 1, max_nodes: 12, max_items: 20 },
        permissions: SERVER_PERMISSIONS, last_event_sequence: 0,
      });
    }
    return undefined;
  });
  // 与 composer onStarted 相同的接线：startResearchRun 的 run_id 直接交给工作台。
  await useWritingRuntimeStore.getState().loadRun(run_id);
  const state = useWritingRuntimeStore.getState();
  assert.equal(state.error, null);
  assert.equal(state.run?.run_id, "run_real_1");
  assert.equal(state.run?.status, "planned");
  useWritingRuntimeStore.getState().resetRuntime();
});

// ─── 错误展示路径 ─────────────────────────────────────────────────────────────

test("503 RESEARCH_UNAVAILABLE from the writing API is surfaced with status and message", async () => {
  installFetch((path, method) => {
    if (method === "POST" && path === "/api/v2/documents") {
      return fail(503, "RESEARCH_UNAVAILABLE", "writing api: research review is disabled by configuration");
    }
    return undefined;
  });
  await assert.rejects(
    startResearchRun(launchInput(fakeSpec())),
    (error: unknown) => {
      assert.equal((error as { code?: string }).code, "RESEARCH_UNAVAILABLE");
      assert.equal((error as { status?: number }).status, 503);
      assert.match((error as Error).message, /disabled by configuration/);
      return true;
    },
  );
});

test("400 INVALID_RESEARCH_SPEC names the offending contract field", async () => {
  installFetch((path, method) => {
    if (method === "POST" && path === "/api/v2/documents") {
      return ok({ document_id: "doc_real_1", current_version_id: "" }, 201);
    }
    if (method === "POST" && path === "/api/v2/documents/doc_real_1/contracts") {
      return fail(400, "INVALID_RESEARCH_SPEC", "invalid research spec: research.max_papers must not exceed 20");
    }
    return undefined;
  });
  await assert.rejects(
    startResearchRun(launchInput(fakeSpec())),
    (error: unknown) => {
      assert.equal((error as { code?: string }).code, "INVALID_RESEARCH_SPEC");
      assert.equal((error as { status?: number }).status, 400);
      assert.match((error as Error).message, /research\.max_papers/);
      return true;
    },
  );
});

test("launch validation blocks the request before any HTTP call when delivery fields are missing", async () => {
  installFetch(() => {
    assert.fail("no HTTP request expected when launch fields are invalid");
  });
  await assert.rejects(
    startResearchRun(launchInput(fakeSpec(), { audience: "", length_min: "6000", length_max: "100" })),
    (error: unknown) => {
      assert.equal((error as { code?: string }).code, "INVALID_RESEARCH_SPEC");
      assert.match((error as Error).message, /目标读者/);
      assert.match((error as Error).message, /长度下限不能大于上限/);
      return true;
    },
  );
  assert.equal(calls.length, 0);
  assert.deepEqual(
    researchLaunchProblems(launchInput(fakeSpec(), { central_question: "", language: "" })),
    ["请填写研究问题（central_question）", "请填写交付语言"],
  );
});

test("network failure without a backend reports RESEARCH_UNAVAILABLE instead of crashing", async () => {
  globalThis.fetch = (async () => {
    throw new TypeError("Failed to fetch");
  }) as typeof fetch;
  await assert.rejects(
    startResearchRun(launchInput(fakeSpec())),
    (error: unknown) => {
      assert.equal((error as { code?: string }).code, "RESEARCH_UNAVAILABLE");
      assert.equal((error as { status?: number }).status, 503);
      return true;
    },
  );
});

// ─── mock 语义不回退 ─────────────────────────────────────────────────────────

test("with mock enabled the demo run id is still returned and no HTTP is made", async () => {
  setResearchMockEnabled(true);
  installFetch(() => {
    assert.fail("mock mode must not hit the network");
  });
  const { run_id } = await startResearchRun(launchInput(fakeSpec()));
  assert.equal(run_id, MOCK_RESEARCH_RUN_ID);
  assert.equal(isResearchMockEnabled(), true);
});

test("uuidV4 keeps the request-layer idempotency convention", () => {
  assert.match(uuidV4(), UUID_PATTERN);
});

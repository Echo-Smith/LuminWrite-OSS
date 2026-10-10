/**
 * 普通写作流（long_form）端到端冒烟——服务端封存合同链路。
 *
 * 链路：register → documents → writing-contract-draft（新端点，服务端封存
 * lcp/1.0 合同对 + intent plan）→ contracts → confirm → plans → runs
 * （→ awaiting_approval 时 approve）→ 轮询 run 直至终态 → 校验文档版本。
 *
 * 用法：node scripts/smoke-long-form.mjs [baseURL]
 */
const BASE = process.argv[2] || "http://localhost:8080";
const PREFIX = `${BASE}/api/v2`;

const suffix = Math.random().toString(36).slice(2, 8);
const user = { username: `smoke_${suffix}`, password: "SmokeTest123!", name: "冒烟测试" };

let token = "";

async function api(method, path, body, extraHeaders = {}) {
  const headers = { "Content-Type": "application/json", ...extraHeaders };
  if (token) headers.Authorization = `Bearer ${token}`;
  const res = await fetch(`${PREFIX}${path}`, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) });
  const text = await res.text();
  let json;
  try { json = JSON.parse(text); } catch { json = { raw: text.slice(0, 200) }; }
  if (!res.ok) {
    const err = new Error(`${method} ${path} -> HTTP ${res.status}: ${JSON.stringify(json).slice(0, 300)}`);
    err.status = res.status;
    err.body = json;
    throw err;
  }
  return json.data ?? json;
}

function step(name) { console.log(`▶ ${name}`); }
function ok(name, detail = "") { console.log(`✔ ${name}${detail ? ` — ${detail}` : ""}`); }

async function main() {
  // 1. 注册（拿 JWT）
  step("注册冒烟用户");
  const auth = await api("POST", "/auth/register", user);
  token = auth.token;
  ok("注册+登录", `user=${user.username}`);

  // 2. 创建文档（Idempotency-Key）
  step("创建文档");
  const doc = await api("POST", "/documents", { title: `冒烟-长文创作-${suffix}`, metadata: {} }, { "Idempotency-Key": crypto.randomUUID() });
  ok("文档已创建", `document_id=${doc.document_id}`);

  // 3. 服务端封存合同（本次修复的核心端点）
  step("writing-contract-draft（服务端封存）");
  const draft = await api("POST", `/documents/${doc.document_id}/writing-contract-draft`, {
    message: "写一篇关于固态电解质产业化路径的行业分析，约1500字",
    style: "default",
    mode: "auto",
    flow: "long_form",
    assurance_level: "standard",
    approval_mode: "auto",
  });
  if (draft.contract.schema_version !== "lcp/1.0") throw new Error(`schema_version 异常: ${draft.contract.schema_version}`);
  if (!draft.contract.contract_id?.startsWith("ctr_")) throw new Error(`contract_id 异常: ${draft.contract.contract_id}`);
  if (!draft.contract.contract_hash?.startsWith("sha256:")) throw new Error("合同哈希未封存");
  if (!draft.intent_plan?.intent_plan_hash?.startsWith("sha256:")) throw new Error("intent plan 哈希未封存");
  if (draft.confirmed_contract.version !== 2 || draft.confirmed_contract.status !== "confirmed") throw new Error("confirmed 合同形状异常");
  ok("合同对+意图计划已封存", `contract=${draft.contract.contract_id} hash=${draft.contract.contract_hash.slice(0, 20)}…`);

  // 4. 合同草稿落库（响应是 ContractRecord：合同身份嵌套在 contract 字段）
  step("PUT 合同草稿");
  const draftRecord = await api("POST", `/documents/${doc.document_id}/contracts`, { contract: draft.contract });
  const draftRef = draftRecord.contract;
  if (!draftRef?.contract_id?.startsWith("ctr_")) throw new Error(`草稿合同身份异常: ${JSON.stringify(draftRecord).slice(0, 200)}`);
  ok("草稿合同", `v${draftRef.version} status=${draftRef.status}`);

  // 5. 确认合同（原样转发服务端封存的 confirmed）
  step("确认合同");
  const confirmedRecord = await api("POST", `/contracts/${draftRef.contract_id}/confirm`, {
    previous_version: draftRef.version,
    contract: draft.confirmed_contract,
  });
  const sealed = confirmedRecord.contract;
  if (!sealed?.contract_id || sealed.version !== 2) throw new Error(`确认合同异常: ${JSON.stringify(confirmedRecord).slice(0, 200)}`);
  ok("已确认合同", `v${sealed.version} status=${sealed.status}`);

  // 6. 编译计划（intent_plan 原样转发）
  step("编译计划");
  const preview = await api("POST", `/documents/${doc.document_id}/plans`, {
    contract_id: sealed.contract_id,
    contract_version: sealed.version,
    base_version_id: doc.current_version_id ?? "",
    intent_plan: draft.intent_plan,
    budget: { max_cost_usd: 20, max_duration_ms: 2400000, max_concurrency: 1, max_nodes: 10, max_items: 4 },
    initial_artifact_types: ["contract"],
    required_final_artifact: "revision_set",
  });
  const planId = preview.plan?.executable_plan?.plan_id;
  ok("计划已编译", `plan=${planId}`);

  // 7. 创建运行
  step("创建运行");
  let run = await api("POST", "/runs", {
    document_id: doc.document_id,
    contract_id: sealed.contract_id,
    contract_version: sealed.version,
    contract_hash: sealed.contract_hash,
    base_version_id: doc.current_version_id ?? "",
    style_slug: "default",
    plan: preview.plan,
    budget: { max_cost_usd: 20, max_duration_ms: 2400000, max_concurrency: 1, max_nodes: 10, max_items: 4 },
    permissions: preview.permissions,
  }, { "Idempotency-Key": crypto.randomUUID() });
  ok("运行已创建", `run=${run.run_id} status=${run.status}`);

  // 8.  awaiting_approval → 自动审批
  if (run.status === "awaiting_approval") {
    step("审批运行");
    run = await api("POST", `/runs/${run.run_id}/approve`, {
      plan_id: planId,
      plan_version: 1,
      plan_hash: preview.plan?.executable_plan?.plan_hash,
      permissions: preview.permissions,
    });
    ok("已审批", `status=${run.status}`);
  }

  // 9. 轮询至终态
  step("等待运行终态（最长 5 分钟）");
  const deadline = Date.now() + 5 * 60 * 1000;
  let last = "";
  while (Date.now() < deadline) {
    await new Promise((r) => setTimeout(r, 4000));
    run = await api("GET", `/runs/${run.run_id}`);
    const line = `status=${run.status}${run.current_step ? ` step=${run.current_step}` : ""}`;
    if (line !== last) { console.log(`  … ${line}`); last = line; }
    if (["completed", "failed", "cancelled"].includes(run.status)) break;
  }
  if (run.status !== "completed") {
    console.error(`✘ 运行未完成：status=${run.status}`);
    console.error(JSON.stringify(run, null, 2).slice(0, 2000));
    process.exitCode = 1;
    return;
  }
  ok("运行完成", `run=${run.run_id}`);

  // 10. 校验文档版本产物（响应形状 {data:{versions:[…]}）
  step("校验文档版本");
  const versionsBody = await api("GET", `/documents/${doc.document_id}/versions`);
  const versions = Array.isArray(versionsBody) ? versionsBody : versionsBody.versions;
  if (!Array.isArray(versions) || versions.length === 0) throw new Error("文档没有任何版本——产物缺失");
  const latest = versions[versions.length - 1];
  ok("文档版本已产出", `versions=${versions.length} 最新 v${latest.version ?? "?"} quality=${latest.quality_state ?? "?"}`);

  console.log("\n=== 冒烟通过：long_form 全链（封存合同 → 运行 → 成稿）流通 ===");
  console.log(`document_id=${doc.document_id} run_id=${run.run_id}`);
}

main().catch((error) => {
  console.error(`\n✘ 冒烟失败: ${error.message}`);
  process.exitCode = 1;
});

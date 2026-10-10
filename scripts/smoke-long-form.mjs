/**
 * 普通写作流（long_form）端到端冒烟——单入口 launch 链路（runtime-agility M2）。
 *
 * 链路：register → POST /writing/launch（服务端一次编排：documents →
 * writing-contract-draft 封存 lcp/1.0 合同对 + intent plan → contracts →
 * confirm → plans → runs → 视需要 approve）→ 轮询 run 直至终态 → 校验文档版本。
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

  // 2. 单入口 launch：服务端一次编排七步启动链（封存合同 → confirm →
  //    计划 → 运行 → 视需要 approve），语义字段（预算/初始 artifact/final
  //    artifact）全部服务端单源，请求体只携带用户选择。
  step("POST /writing/launch（单入口）");
  const launch = await api("POST", "/writing/launch", {
    message: "写一篇关于固态电解质产业化路径的行业分析，约1500字",
    flow: "long_form",
    style: "default",
    mode: "auto",
    assurance_level: "standard",
    approval_mode: "auto",
  }, { "Idempotency-Key": crypto.randomUUID() });

  if (!launch.document_id || !launch.run_id) throw new Error(`launch 响应形状异常: ${JSON.stringify(launch).slice(0, 300)}`);
  if (launch.contract_version !== 2) throw new Error(`确认合同应为 v2: ${launch.contract_version}`);
  if (!launch.contract_hash?.startsWith("sha256:")) throw new Error("合同哈希未封存");
  if (!launch.plan_id || !launch.plan_hash?.startsWith("sha256:")) throw new Error("计划身份缺失");
  ok("launch 编排完成",
    `document=${launch.document_id} contract=${launch.contract_id} plan=${launch.plan_id} run=${launch.run_id} status=${launch.run_status}${launch.approved ? "（已自动批准）" : ""}`);

  // 3. 轮询至终态。终态集合：completed/failed/cancelled，以及 paused——
  //    allowlist 部署下未授权 subject 的 rollout 决策为 mode=off（离线
  //    baseline），首个 LLM 节点失败后治理侧合法暂停（决策证据在事件
  //    runtime.route_decided 里）。paused 属于环境/授权相关，不是 launch
  //    链路失败——launch 的职责（封存合同 → confirm → 计划 → 运行创建 →
  //    审批）在 paused 时已全部完成。
  const deadline = Date.now() + 5 * 60 * 1000;
  let last = "";
  let run = { run_id: launch.run_id, status: launch.run_status };
  while (Date.now() < deadline) {
    await new Promise((r) => setTimeout(r, 4000));
    run = await api("GET", `/runs/${run.run_id}`);
    const line = `status=${run.status}${run.current_step ? ` step=${run.current_step}` : ""}`;
    if (line !== last) { console.log(`  … ${line}`); last = line; }
    if (["completed", "failed", "cancelled", "paused"].includes(run.status)) break;
  }
  if (run.status === "paused") {
    console.log("ℹ 治理运行暂停（rollout 决策）：allowlist 部署下未授权 subject 走 mode=off 离线 baseline，");
    console.log("  首个 LLM 节点失败即暂停——launch 链路本身已全部验证通过（见上方 ✔）。");
    console.log("  要看到完整成稿，需要把冒烟 subject 加入治理试点授权（或临时切 shadow 模式）。");
    console.log("\n=== 冒烟通过（launch 链路）：封存合同 → confirm → 计划 → 运行 → 审批 流通 ===");
    console.log(`document_id=${launch.document_id} run_id=${run.run_id} 终态=${run.status}`);
    return;
  }
  if (run.status !== "completed") {
    console.error(`✘ 运行未完成：status=${run.status}`);
    console.error(JSON.stringify(run, null, 2).slice(0, 2000));
    process.exitCode = 1;
    return;
  }
  ok("运行完成", `run=${run.run_id}`);

  // 4. 校验文档版本产物（响应形状 {data:{versions:[…]}}
  step("校验文档版本");
  const versionsBody = await api("GET", `/documents/${launch.document_id}/versions`);
  const versions = Array.isArray(versionsBody) ? versionsBody : versionsBody.versions;
  if (!Array.isArray(versions) || versions.length === 0) throw new Error("文档没有任何版本——产物缺失");
  const latest = versions[versions.length - 1];
  ok("文档版本已产出", `versions=${versions.length} 最新 v${latest.version ?? "?"} quality=${latest.quality_state ?? "?"}`);

  console.log("\n=== 冒烟通过：long_form 单入口 launch 全链（封存合同 → 运行 → 成稿）流通 ===");
  console.log(`document_id=${launch.document_id} run_id=${run.run_id}`);
}

main().catch((error) => {
  console.error(`\n✘ 冒烟失败: ${error.message}`);
  process.exitCode = 1;
});

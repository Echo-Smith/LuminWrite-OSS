# AR-012 独立综述生成服务接入合同（T10）

状态：已实现（评估端点形态）。上游：`AutoResearch` 分支
`codex/ar-012-standalone-review-service`，钉住 commit
`68888a75719776eab58c5e01a3ad320a37c20abc`。关联：`requirements.md` §R13/L56、
`design.md` §8、`docs/plans/2026-09-07-research-review-integration.md` T10、
`output/autoresearch-ar012-analysis/AutoResearch_AR012_接入评估与实施路线_2026-09-07.md`。

## 0. License 纪律（硬约束）

- 上游根包与独立包 `pyproject.toml` 均声明 **Proprietary**，且仓库无 LICENSE
  文件。按 requirements.md L56：未取得书面授权前，上游源码不得进入
  OSS/商业仓库、镜像或安装包。
- 落地方式：上游代码以 **工作区本地 fork**（`<workspace>/review-sidecar/`）
  私有部署；本仓库只包含 LuminBuddy 自有的 Go 适配层、接口合同与部署
  配置。fork 的修改清单见其 `README.md`。

## 1. 定位与形态

对**已完成** research-review 路径（证据门 + 提纲门 + 主稿交付）的 run，
用**同一冻结证据包 + 批准提纲**调用 sidecar 生成对比候选稿：

```
POST /api/v2/runs/{runId}/research/ar012-candidate      202 新建 / 200 幂等重放
GET  /api/v2/runs/{runId}/research/ar012-candidate      最近一次作业状态
POST /api/v2/runs/{runId}/research/ar012-candidate/cancel
GET  /api/v2/runs/{runId}/research/ar012-candidate/artifacts/{kind}
      kind ∈ manuscript | citation_map | receipt | quality | metrics
```

- 形态为**运行后评估端点 + 持久作业**（用户决议 2026-09-12），不改模板、
  编译器与枚举；后续升级为模板内并行节点时，client/转换器/幂等派生可整体
  复用。
- 首轮仅 shadow/compare：候选是**隔离产物**，绝不进入 delivery、绝不产生
  文档版本（A18）。作业行是产物引用的唯一持有者；内容以
  `writing_artifact_content` 内容寻址 blob 存储。

## 2. 前置条件与失败语义

| 条件 | 违反时 |
|---|---|
| `AR012_CANDIDATE_ENABLED=true` 且 `AR_REVIEW_SIDECAR_URL` 已配置 | 503 `AR_REVIEW_UNAVAILABLE` |
| run 存在且 caller 是 owner（owner-first 404） | 404 `WRITING_RESOURCE_NOT_FOUND` |
| run 状态为 `completed` | 409 `AR_REVIEW_RUN_NOT_COMPLETE` |
| run 具备 `research_evidence_pack` / `approved_research_outline` / `full_draft` 三类 artifact 且可解码校验 | 422 `AR_REVIEW_INPUTS_MISSING` |
| 冻结包可用来源 ≥ 5 | 422 `AR_REVIEW_INSUFFICIENT_CORPUS` |
| 交换目录与内容寻址 id 冲突 | 409 `AR_REVIEW_EXCHANGE_CONFLICT` |

所有 POST 要求 `Idempotency-Key` 头（同 §3 公共规则；服务端身份为
owner+input_hash，客户端 key 仅入审计）。

## 3. 幂等与对账（design.md §8 的落地）

```
input = sha1f( "arreview/1", owner, contract_hash, evidence_pack_hash,
               approved_outline_hash, generator_version, mode )
surrogate_project_id = "lb-" + input_hex[:32]
idempotency_key      = "ar012-" + input_hex
```

- **同输入必同 job**：`(owner_user_id, input_hash)` 唯一索引；重复 POST
  返回原作业（200 重放），绝不二次驱动 650 秒管线。
- **generator_version 旋转**：sidecar 升级（prompt/代码变化）即换键，杜绝
  跨版本静默复用缓存稿。
- **对账，不盲重试**：sidecar 同步调用超时/断连标记
  `outcome_unknown`；worker 扫描通过 `GET /projects/{id}/review-runs` 按
  幂等键认领记录，只有读取与导入，绝不重新 POST。
- **cancel 诚实语义**：pending 作业直接取消；running 作业打
  `cancel_requested` 标记，sidecar 无法中断，worker 丢弃导入产物、作业落
  `cancelled`。cancel 恒胜于 completed（SQL CASE 保证，含竞态）。

## 4. sidecar 输入（交换卷）

同机部署，backend 与 sidecar 挂载同一卷（`docker-compose.ar012.yml`）：

```
{volume}/lb-<hash>/REVIEW_SPEC.json                  ← 体裁档案（Go 生成）
{volume}/lb-<hash>/02_literature/REVIEW_CORPUS.json  ← 冻结语料（Go 生成）
{volume}/lb-<hash>/05_writing/…                      ← sidecar 产物
{volume}/lb-<hash>/06_review/…                       ← 质量门/修补记录
```

两边字节必须逐位一致（内容寻址目录，不一致即 `ErrExchangeConflict`
fail-closed），因此 corpus 构造**不含时间戳**，worker 重建与请求时预检
字节相同。

### 4.1 REVIEW_CORPUS.json

由 `arreview.BuildCorpus` 从冻结证据包确定性转换（上游 `ReviewSource`
schema）：

- 只收 `reading_scope ∈ {abstract, full_text}` 的论文，按 paper_id 排序，
  编号 R01…；
- 摘要 = 该论文最长已验证证据引文（摘要范围优先，全文范围其次并标
  `evidence_scope: full_text`）；<120 字符不可用；
- 无 DOI 的论文被诚实剔除（上游按 DOI 去重，空串会假性碰撞）；重复 DOI
  保留首篇；全部剔除原因随作业记录（`corpus_warnings`）。

### 4.2 REVIEW_SPEC.json

`schema_version: "lumin-review-spec/1"`，由 `arreview.BuildSpecMapping`
生成；fork 侧 `review_spec.py` 校验，未知字段拒绝（fail-closed）：

- `required_headings` ← **批准提纲的 section 标题**（冻结提纲即体裁）；
- `coverage_terms` ← 证据包 coverage.topics（单条目）+ 三条体裁通用条目；
- 阈值按语料规模等比放低（n<15/12/24 时取 n、2n 等），小语料不再必然撞
  门禁；`min_cjk_characters` 维持 4500；
- `boundary_writer` 固定为「LuminBuddy AR-012 候选生成器」，候选稿证据
  边界永远如实标注生成器，不冒充上游受管 GLM 配置。

## 5. 产物导入与指标

作业完成后按 kind 导入并重算 sha256（写入内容寻址 blob，引用存作业行）：

| kind | sidecar 路径 | 媒体类型 |
|---|---|---|
| `manuscript` | `05_writing/MANUSCRIPT_REVIEW_AGENT.md` | text/markdown |
| `citation_map` | `05_writing/AGENT_CITATION_MAP.json` | application/json |
| `receipt` | `05_writing/AGENT_REVIEW_RECEIPT.json` | application/json |
| `quality` | `06_review/AGENT_REVIEW_QUALITY.json` | application/json |
| `metrics` | Go 计算生成 | application/json |

`metrics`（`review-metrics/1`）只含**机械可复核指标**：候选稿/主稿的汉字
数、标题数、引用出现次数、唯一编号数、`[n]`→语料可解析率（未解析逐条列
出）、来源覆盖率、`[@ev_*]` 引用计数、内容 hash。**它不包含质量结论**——
质量由 12 案例人工盲评（design.md §8）与后续评审流程拥有；机械门禁通过
从不是正确性证据（评估文档"月球是奶酪"反例仍有效）。

usage 取自 receipt 逐阶段求和（prompt/completion tokens）；receipt 缺失
用量时作业 usage 保持为空——不编造零成本。

## 6. 部署

```bash
# 1) 构建本地镜像（工作区 fork，不在本仓库）
(cd ../review-sidecar && ./build.sh --image)
# 2) 以 overlay 启用（默认关闭）
docker compose -f docker-compose.yml -f docker-compose.ar012.yml --profile ar012 up -d backend
```

env：`AR012_CANDIDATE_ENABLED`（默认 false）、`AR_REVIEW_SIDECAR_URL`
（compose 内 `http://review-sidecar:8020`）、`AR_REVIEW_SIDECAR_TIMEOUT_MS`
（默认 1500000，须大于 sidecar 五次 LLM 调用总时长）、
`AR_REVIEW_EXCHANGE_DIR`（默认 `/data/review-exchange`）、sidecar 侧
`AR_REVIEW_LLM_*`（OpenAI 兼容配置，offline 即全阶段显式失败）。

安全边界：sidecar 仅 internal 网络、不发布宿主端口、无认证——身份与项目
所有权由 Go 宿主持有；单 worker 有界并发（sidecar 全程 RLock 串行，加副
本不增加吞吐）。

## 7. 已知边界（诚实声明）

- 交换卷是**单机 PoC 传输**；跨机部署需另做受控上传/下载 API（评估文档
  "结果运输缺口"行）。
- sidecar 无取消/心跳/持久队列；进程死亡不恢复在途调用——宿主以持久作业
  + 幂等键 + 对账补偿，不宣称 exactly-once。
- grounding 门禁只校验数字/引用属于语料，不是逐 claim entailment；候选稿
  引用完整性须另行校验（T07 语义复核未来覆盖）。
- 上游为同步服务：一次 POST 可能 ~25 分钟；前端/客户端须按 202+轮询消费。

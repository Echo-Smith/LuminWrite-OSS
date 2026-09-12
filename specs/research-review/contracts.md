# 研究综述：接口与数据合同 v1（拟新增）

以下全部是开发合同，不代表当前已有接口。公共 API 生产挂载于 `/api/v2`（writing 路由直挂其下，见 backend/internal/server/server.go；后端 E2E harness 自挂 `/api/v2/writing` 属测试内前缀，与生产不一致时以生产为准）、JWT、owner 校验、现有成功 envelope；新增的 error.code 需接入统一错误映射。Artifact ID/version/hash 使用现有命名与内容存储规则。

## 1. ResearchSpec

WritingContract v1.1 顶层新增 research，结构如下；本例参数是完整字段集合，开工时据此建严格 JSON Schema 与 Go/TS 类型。

```json
{
  "version": "research-spec/1",
  "review_kind": "narrative",
  "year_from": 2020,
  "year_to": 2026,
  "exclusion_terms": [],
  "max_queries": 3,
  "max_candidates": 60,
  "max_papers": 10,
  "min_citable_sources": 5,
  "evidence_requirement": "abstract_allowed",
  "selection_policy_version": "selection/1",
  "reader_policy_version": "reader/1",
  "generator": "lumin_writer",
  "citation_style": "numeric"
}
```

- year_from/year_to 可 null，非空时 from <= to；数量整数且 1 <= min_citable_sources <= max_papers <= 20、max_candidates >= max_papers 且 <=60、max_queries 1..3。
- evidence_requirement 枚举 abstract_allowed/full_text_required；首版 generator 仅 lumin_writer，未来增加 ar012_candidate 时另行版本演进。
- 外部检索开关只读取现有 material_policy.allow_external_research；语言/长度只读取 delivery，研究问题只读取 content.central_question；不设置相互矛盾的重复字段。
- 用户材料沿用现有素材上传与 owner manifest 通道；上传侧与下载侧同样适用 25 MiB 与文本型（PDF/TXT/Markdown）限制，扫描件按 R05 标记需补充可读文本。
- 所有未知字段拒绝；默认值只在合同草稿阶段补齐；确认时没有隐式默认。

## 2. Artifact 合同

### research_candidates（research-candidates/1）

字段：contract_hash、query_plan[{query_id,text,origin(original/rewrite/fallback)}]、provider_results[{provider,status,error_code?,request_ref?}]、papers[]、policy_version、provenance[]。

papers 元素：paper_id、doi(nullable)、title、authors[]、year(nullable)、venue(nullable)、canonical_url(nullable)、aliases[]、abstract(nullable)、selection{status(selected/deferred/excluded),score(nullable 0..3),reason}、acquisition{status(not_attempted/metadata_only/abstract_available/full_text_available/failed),oa_url?,license?,error_code?}。相关性 score 不是可信度；另存 relevance_status(scored/unscored)，未评分论文仍可按明确 fallback policy 进入 selected 工作集。

DOI 规范化：移除 DOI URL/doi: 前缀、URL decode、trim、lowercase；有冲突的不同 DOI 不仅凭同题名合并。题名匹配还需作者/年份辅助，无法确定保留独立记录和可能重复提示。来源别名保存原始 provider ID。

### research_evidence_pack（research-evidence-pack/1）

| 字段 | 类型 / 约束 |
|---|---|
| contract_hash | 必须与本 run 已确认合同一致 |
| candidates_ref | ArtifactRef；用户材料路径也生成候选包 |
| papers | PaperEvidence[]；paper_id 包内唯一 |
| evidence | Evidence[]；evidence_id 包内唯一 |
| claims | Claim[]；claim_id 包内唯一 |
| coverage | topics[]、gaps[]、contradictions[]；引用已有 paper/claim ID |
| provenance | 执行器/模型/提示模板版本、实际用量与任务关联引用 |

ArtifactRef = {artifact_id:string, version:positive integer, content_hash:sha256:hex64}。包自身 hash 放在 Artifact envelope，不把 hash 写入自身内容形成循环；所有跨包引用带 version/hash。

PaperEvidence：

- paper_id、bibliography{title,authors[],year?,venue?,doi?,canonical_url?}、origin(user_material/external)、material_ref?；用户材料必须带 owner 授权的引用。
- selection_reason、relevance_status、reading_scope(unread/abstract/full_text)、document_ref?、parsed_document_ref?、read_block_ids[]、total_blocks、truncated:boolean、license?。
- full_text 指存在可读全文并读取了记录的块，不表示穷尽每页；truncated/read_block_ids 明确覆盖边界。unread 不能产生可引用证据。

Evidence：

- evidence_id、paper_id、document_ref、parsed_document_ref、block_id、block_hash、quote、start_char、end_char、page(nullable)、section(nullable)、evidence_scope(abstract/full_text)。
- offset 采用 Unicode 码点、左闭右开；必须满足 `quote == block_text[start_char:end_char]`。quote 非空；对应 block hash 和 document hash 均验证；页码若有则从 1 开始且与 parser 块元数据一致。
- 摘要文档采用独立内容 Artifact，以摘要正文算 document hash；不能把全文 PDF hash 挂到摘要 quote。

Claim：

- claim_id、paper_id、text、kind(source_assertion/interpretation/hypothesis)、evidence_ids[]、review_status(pending/approved/rejected)、limitations[]。
- source_assertion 必須有同 paper 的有效 evidence；interpretation 可关联多个证据但必须标记为综合解释；hypothesis 不作为支持事实的证据。rejected 不送给 Writer 作为支持材料。
- claim.review_status=approved 表示指定审核行为完成，仍不是科学真实性保证；pack 的写作授权与 claim 的审核分离。

### research_outline / approved_research_outline

`schema_version=research-outline/1`、contract_hash、evidence_pack_ref、sections[{section_id,title,central_point,evidence_ids[],gaps[]}]、limitations[]；approved 另有 source_outline_ref、gate_decision_ref。无 evidence 的章节只能标记引言/研究缺口等非事实综合用途，不得悄悄补造来源。

### evidence_approval

schema_version、gate_id、actor_id、decided_at、plan_hash、evidence_pack_ref、decision(approve)。由服务端创建；客户端不能写 actor_id/decided_at 或自行上传批准产物。

### 正文与质量投影

full_draft 使用原正文合同，增加对应的研究引用索引 Artifact `research_citation_index`（draft hash + pack ref + citation_id/evidence_id + block/文本区间），由研究 draft adapter 同步产出。compiler/manifest/template 必须声明该附加输出，citation validator 必须消费它；不能只在正文塞标记却不保存结构化索引。

证据校验输出沿用 evidence_report 的基础合同，新增研究详情 Artifact `research_validation_details`，保存 invalid_citations、unsupported_claims、scope_overclaims、unreviewed_claims、omitted_evidence。质量门显式消费基础报告中的 blocker 映射；不要依赖它自动理解任意新增 JSON 字段。自动语义核查的结论附 model/version，pending 项展示给人，不转成验证通过。

## 3. 公共 API

| 方法 / 路径（下列路径均接公共前缀） | 行为 / 返回 |
|---|---|
| GET /runs/{runId}/research | 200：phase、counts、tasks 摘要、pack_ref?、active_gate?、errors[]；只读 owner 范围 |
| GET /runs/{runId}/gates/{gateId} | 200：输入 ref、revision、status、允许的操作、阻塞原因 |
| POST /runs/{runId}/gates/{gateId}/outline-revisions | 201：保存修改后的提纲 Artifact，返回新 gate_revision 与 outline_ref；仅 outline gate pending 可用 |
| POST /runs/{runId}/gates/{gateId}/decisions | 202：已持久化确认并安排恢复；GET 查询实际状态。相同 key/body 重发 200 返回同一 decision |
| GET /runs/{runId}/artifacts/{artifactId}/content | 200：返回 owner 授权且被本运行计划/产物引用（含子任务缓存跨 run 复用的引用）的 JSON/文本；授权在 API 层校验（内容表无 ACL），大文件另按素材下载权限处理 |

所有 POST 要求 Idempotency-Key。不同 body 重用 key 返回409。先完成 owner 校验，再解析目标；越权返回404（不泄露存在性）。保留现有 /approve 计划权限确认、/pause、/resume、/cancel 与事件接口。

确认请求示例（hash 由实际 API 返回值填入，不是客户端计算）：

```json
{
  "plan_id": "plan_example",
  "plan_version": 1,
  "plan_hash": "<actual-plan-hash>",
  "gate_revision": 1,
  "input_ref": {
    "artifact_id": "art_evidence_example",
    "version": 1,
    "content_hash": "<actual-artifact-hash>"
  },
  "decision": "approve"
}
```

202 的 data：decision_id、gate_id、status(approved)、resume_status(queued)、run_id。它不承诺正文已开始，更不代表整运行完成。outline-revisions 请求同样带预期 gate_revision、plan_hash、当前 outline_ref，另有完整 sections/limitations；服务端校验、生成新 ref，用户再确认。

错误：400 INVALID_RESEARCH_SPEC；404 RESOURCE_NOT_FOUND；409 STALE_GATE / IDEMPOTENCY_CONFLICT / GATE_ALREADY_DECIDED / GATE_APPROVAL_REQUIRED；422 INSUFFICIENT_EVIDENCE / EVIDENCE_INVALID / OUTLINE_EVIDENCE_MISMATCH；503 RESEARCH_UNAVAILABLE。远端调用错误显示在任务结果中，不能将已提交的异步确认返回成 HTTP 500 导致客户端误判未保存。

新增事件：`research.progress`、`gate.pending`、`gate.decided`（对齐现有 run.*/node.*/runtime.* 命名家族），沿用 protocol=lumin-writing.v2 与全 run 单调 sequence，存库后发送；event_type 需随 107 迁移扩展 DB CHECK（见 design §6）。progress 包含 phase/completed/total/failed/deferred；gate 事件包含 gate_id/revision/input_ref/status。GET 状态包含 last_event_sequence；重连事件与 GET 按 sequence 合并，不能用旧 GET 覆盖新事件。

## 4. Python 内部接口（全部拟新增，仅私网调用）

统一 POST `/internal/v1/operations/{operation}`，operation 白名单：discover、rank、fetch_full_text、parse、read。每次是一个有界单元，不代表整篇综述任务。

统一请求：request_id、input_hash、operation_version、deadline_ms、payload；授权使用部署密钥加当前任务内容访问权限，不信任浏览器传入 owner。统一成功 200：request_id、input_hash、outputs、usage{measured,input_tokens,output_tokens,cost_usd(nullable),provider,model}、warnings[]、versions；失败返回 error{code,retryable,retry_after_ms?,outcome_unknown}。

| operation | payload | outputs |
|---|---|---|
| discover | query、provider 白名单、limit | 规范元数据记录与每源状态 |
| rank | 原始研究问题、最多 8 篇带 abstract 的候选 | 每个输入 paper_id 恰好一次的 score/reason；缺失/重复 ID 报错 |
| fetch_full_text | paper_id、doi?、已发现 OA URL?、size_limit | 内容 hash 与受控 blob 传输结果，acquisition 状态 |
| parse | document 内容授权引用、media_type、parser_version | 文档块与 hash、页码、解析覆盖信息 |
| read | 研究问题、paper_id、已验证块（最多24）、reader_policy | claims/evidence/limitations、实际读取块 ID |

blob 不能是 Python 本地绝对路径。协议采用任务作用域短期下载/上传 URL，由 Go 验证大小/hash 后落 Artifact；worker 只允许配置的 Go 内容服务源，外部 OA 获取走单独受约束下载器。上传成功不等于子任务完成，Go 最终事务提交并验证输出 hash。

超时、断连后不假定 worker 未执行。Go 保存 request_id 与 outcome_unknown，自动重试只用于明确未接受请求或幂等的纯解析/检索单元。按语义标记：解析/规范化 safe；模型调用 required；最终文档提交仅 Go 有权执行。

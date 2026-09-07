# P0 SenseNova 真实模型测试 — 2026-09-07

## 配置与结论

- 旧测试配置：deepseek-v4-flash，经 api.b.ai/v1。
- 本次：deepseek-v4-flash，经 https://token.sensenova.cn/v1；模型列表查询成功，真实调用成功。凭据仅保存在本机临时配置，不包含在此报告。
- 三个独立纵向场景均已有通过记录；完整 HTTP 服务验收仍未通过。不能据此宣布 P0 生产验收完成。
- 执行目标为 OSS 当前工作区，数据库为独立 PostgreSQL 测试实例中的临时隔离库；本次没有部署或激活线上策略。

日志位于本地工作区 `output/p0-20260907/live-tests/`。

## 结果

| 检查 | 结果 | 日志 |
|---|---|---|
| 长文 | 通过，30.03 秒；4096 输出 token 上限 | vertical.log（整组因后续限流失败，此子场景通过） |
| 多素材 | 单独重试通过，46.34 秒；4096 上限 | multi-material-retry.log |
| 忠实改写 | 通过，66.98 秒；16384 上限及修正后的测试时间预算 | faithful-rewrite-cooled.log |
| HTTP 端到端 | 未通过：最终重试在正文节点遭遇 429 rpm exhausted，运行暂停 | http-fixed.log |
| 测试夹具回归 | OSS writingruntime 与离线 HTTP 模板测试通过 | fixture-regression.log |
| 客户端回归 | 双仓 internal/tools 测试通过 | client-regression.log |

三个场景是分次执行、使用不同输出预算的通过记录，不是相同参数下一次完整运行通过。测试中仍有 mock 提纲/来源整理与结构包装，证明的是模型接入和运行时纵向机制，不替代全服务路径、真实搜索质量或人工成对盲评。隔离测试库在结束时清理，此次未导出成对文章供人工评分。

## 本次发现与修复

1. 接口 RPM 限流：集中调用多次返回 429，拆场景及冷却后部分重试通过；完整 HTTP 路径仍受限。现有短退避不能覆盖该接口的限流窗口，流式调用遇到 429 会暂停，尚未实现跨请求节流。
2. 忠实改写空正文：诊断确认 finish_reason=length，completion_tokens=4096，reasoning_tokens=4096。增加可选 TASK13_LLM_MAX_TOKENS，默认仍为 4096；本次忠实改写改用 16384。
3. 测试时间预算冲突：节点超时 150 秒，而测试计划总预算固定 60 秒。夹具现按节点数量及节点超时计算总预算，下限 60 秒；未修改生产预算。
4. SenseNova 参数兼容：关闭 thinking 时仍继承 high reasoning_effort，导致质量步骤返回 400。客户端针对 token.sensenova.cn 在选项处理完后将 disabled thinking 的 effort 规范为 none。双仓已同步，并覆盖选项顺序、流式/非流式及其他服务不变的回归。修复后的 HTTP 运行在正文限流，尚未到达质量节点复核此修复的在线效果。
5. HTTP 修复前另有质量步骤超时（context deadline exceeded），需在限流可控的后续端到端运行中继续验证。

## 待完成

- 使用能承载服务调用频率的额度，或实现按服务端限额节流后，重跑完整 HTTP 链路并复核质量检查。
- 以统一配置复跑三场景，导出原始成对产物进行人工盲评。
- 商业版当前构建的完整真实模型服务验收及目标 1Panel 升级冒烟。

本次不处理旧增量包；本轮代码修复尚未重新打包。

## 后续继续测试：请求节流与额度状态

- 增加 `LLMClient.SetMinRequestInterval`，作用于该客户端全部 HTTP 请求（含流式、非流式及重试），等待服从 context；默认关闭，本轮只在测试中设置 35000 毫秒。此机制不协调同一账户其他客户端或实例。
- 双仓客户端并发/取消 race 回归通过（`pacing-regression.log`）；双仓全部后端回归通过（`oss-final-regression.log`、`commercial-final-regression.log`）。
- 节流后的完整 HTTP 验收仍失败（`http-paced.log`）：提纲调用多次 429，worker 记录暂停，但 HTTP 轮询在 240 秒内未观察到终态，最终断言失败。限流恢复后还需复核持久化状态与 HTTP 状态是否一致，不能把 worker 日志当成最终 API 状态已验证。
- 最新独立探测（`thinking-disabled-probe.json`）返回 HTTP 429，错误码 `insufficient_quota`，提示 `Allocated quota exceeded, please increase your quota limit.`，没有 Retry-After。当前阻塞已包含分配额度耗尽，降频无法解决。
- 关闭推理参数修复有双仓单元测试覆盖，但独立在线探测也被额度拦截，尚不能宣称在线验证通过。
- 下一步：为此凭据提高分配额度或提供可用测试额度，再复跑 HTTP 完整链路与状态一致性检查；人工盲评仍待完成。本轮不重新打包或部署。

## 09:57 额度结论更正

2026-09-07 09:57（北京时间）使用完全相同的凭据、接口和 deepseek-v4-flash 型号，最小请求返回 HTTP 200、正文 OK，总计 8 token（quota-recheck.json）。关闭 thinking + reasoning_effort=none 的参数组合在线验证成功。

因此先前一次 insufficient_quota 响应不能证明账户余额或总额度耗尽，也不能继续将“必须充值/提高额度”视为已确认的前置条件。该次响应与控制台显示差异的原因尚未确认；文档检索未提供足够信息解释具体配额维度。当前确认的是此前出现 RPM 限流及一次分配额度错误，而接口现在能成功完成小请求。完整 HTTP 验收仍未通过，下一轮应聚焦请求频率、请求预算和状态一致性。

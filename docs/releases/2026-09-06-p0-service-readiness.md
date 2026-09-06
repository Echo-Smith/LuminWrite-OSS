# P0 Service Readiness（A1–A6）交付记录

日期：2026-09-06。实现范围见 `docs/plans/2026-09-06-p0-service-readiness.md`。运行时默认仍为 `WRITING_RUNTIME_MODE=off`；本记录描述服务就绪与验证状态，不构成生产放量或能力宣称。

## 交付内容

- **A1 严格研究模板**：`core.retrieval.strict_search` 加入能力装配（`governed_runners.go`）；`governed_research_step` 仅接受 KB 材料与真实检索结果，合成/模拟信源被拒绝并保留材料溯源（`governed_research.go`）。
- **A2 个人风格**：`my_` 前缀风格按文档持久 owner 解析（`governed_styles.go`），`RuntimeRun.OwnerUserID` 由 `writing_documents` JOIN 提供，dispatch 携带可信用户身份；两个用户同名风格互相隔离，无 owner / 未发布版本显式失败并计 fallback 指标。
- **A3 运行遥测**：`MetricsRegistry` 接入 governed runtime（context/lifecycle/style/run duration 四组指标），Grafana 看板 `monitoring/grafana/governed-runtime.json`（完成率、P50/P95、Token 用量、成本、上下文缺失/压力、shadow 对比、放量选择）；无用户/内容级高基数标签，无推断 USD 成本。
- **A4 allowlist 服务接线**：`servicePolicyExecutor` 每次 dispatch 读取持久化策略并按 revision 缓存（`governed_policy.go`）；策略存于 append-only `writing_runtime_policy_revisions`（迁移 106，含防篡改触发器）；`cmd/runtime-policy` 操作 CLI 仅持久化 operator intent，从不授予审批；percentage/enabled 显式 fail-closed；策略缺失/不可读/失配回落 baseline 并记 `policy_failed`。
- **A5 跨进程任务所有权**：`WithRunExecutionLock` 以 PG advisory lock 独占运行（连接断开即释放，无 TTL 驱逐健康 worker）；`governed_worker.go` 每 5 秒扫描持久化队列（上限 2 并发），重启后恢复 planned/running 运行；派发失败转入 pausing 待人工检查，不空转重试；Resume 在同一所有权边界下串行并观察持久化暂停/取消状态。

## 验证证据（2026-09-06，本机构建）

| 验证 | 结果 | 备注 |
|---|---|---|
| OSS 后端全量 `go test ./...` | 22/22 包通过 | arm64 paradedb PG17，含迁移全链 |
| OSS race（server/writingruntime/writingstore/database） | 通过，无数据竞争 | |
| 商业版同步后全量 | 22/22 包通过 | 共享文件与 OSS 逐字节一致 |
| 商业版 race（同四包） | 通过 | |
| 迁移兼容（商业版） | 086/087 部署历史 fixture 升级全链通过；并发迁移串行化通过 | 仅接受审计过的 checksum 对 |
| 支付宝证书模式 | 证书生成式单测通过 | 见商业版 `d1af5b1` |
| 前端（双仓） | `tsc -b && vite build` 通过；node:test 41/41（0 fail）；eslint 0 错误 75 警告 | |

原始日志：工作区 `output/p0-20260906/{oss-full-a6,oss-race-a6,commercial-full-a6}.log`。

## 未完成与边界（不得据此宣称）

- **真实模型垂直验收未在当前构建执行**：`./evidence-collect.sh` 于 2026-09-06 运行失败，原因 `TASK13_LLM_*` 账户余额为零（`insufficient_user_quota, balance=0`）。充值后须重跑，并按新策略哈希重新评估。
- **人工盲评未执行**：`output/p0-20260906/blind-review/` 仅导出 2026-09-01 历史（P0 前）候选侧影子内容与质量报告；baseline 侧未持久化、无成对材料、无人类偏好记录。
- **A0 未完成**：台账/README 的"实现/测试/服务接入/线上启用"分列待做；历史 9/1、9/2 验收记录属历史证据，不冒充当前构建。
- A6 的盲评材料以 P0 前构建为源，仅作格式与流程就绪，不作为当前构建质量结论。

## 证据条目

- E-050：P0 服务就绪交付（本记录）；OSS `653cf3f`（A1–A5 实现）+ 本提交；商业版 `40ab201`/`d1af5b1`/同步提交；机器验证 22+22 包与 race 通过。
- 门控日志：运行时模式仍限定 off/shadow/allowlist；allowlist 需持久化 active 策略 + 新鲜精确范围审批，未发生任何审批或激活。

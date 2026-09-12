# T10 AR-012 候选评估接入验收记录

日期：2026-09-12。分支：`feat/ar012-candidate`（基于 `feat/research-review`）。
范围：AR-012 独立综述生成服务以**运行后评估端点 + 持久作业**形态接入
（D-010）。接口合同：`specs/research-review/ar012-sidecar.md`。

## 交付物

| 项 | 位置 |
|---|---|
| Go 适配层（client/corpus/exchange/idempotency/metrics） | `backend/internal/arreview/` |
| 持久作业表 + 租约/对账/取消（迁移 111） | `backend/internal/writingstore/ar_review_jobs.go`、`migrations/111_ar_review_jobs.*.sql` |
| 服务与 REST 端点 | `backend/internal/server/arreview_api.go`、`handlers_writing_arreview.go`、`writing_routes.go` |
| 配置（默认关闭） | `AR012_CANDIDATE_ENABLED` / `AR_REVIEW_*`（`config.go`、`.env.docker.example`） |
| 部署 overlay | `docker-compose.ar012.yml`（profile `ar012`，仅 internal 网络） |
| 上游 fork（不入仓库） | 工作区 `review-sidecar/`（ReviewSpec 参数化 + Dockerfile + 18 项测试） |

## 验收结果

自动化验收（本地，`TEST_DATABASE_URL` 指向临时测试库）：

| # | 用例 | 结果 |
|---|---|---|
| A | `internal/arreview` 全包：client 信封/201-but-failed/超时 outcome_unknown/409/404/截断响应；幂等派生确定性+分离性；corpus 转换（剔除/去重/摘要选择/不足 5 拒绝）；spec 缩放；exchange 冲突与路径逃逸；metrics 未解析引用 | PASS |
| B | `writingstore` TestArReviewJob*：创建/重放身份、租约栅栏、终态不可变、cancel 恒胜（SQL CASE）、A18 隔离（导入后 `ListRunArtifacts(run)` 为空） | PASS |
| C | `server` 单元：usageFromReceipt 求和与"不编造零成本"、视图字段稳定 | PASS |
| D | 回归：`writingstore` + `arreview` + `server` 全包 `go test -p 1`（DB-backed） | PASS |
| E | fork `review-sidecar`：pytest 18 项（preset 一致性、spec 校验拒绝、缩放、教育经济学跨学科双门禁、corpus scope 白名单、章节解析） | PASS |
| F | `go build ./...` + `go vet` 三个触碰包 | PASS |
| G | `docker compose -f docker-compose.yml -f docker-compose.ar012.yml --profile ar012 config` | PASS |

人工/待办（不计通过）：

- [ ] SKIPPED：真实模型端到端（sidecar 需 `AR_REVIEW_LLM_*` 凭据；走一次
      POST→202→轮询→artifacts 导出的全链路）
- [ ] SKIPPED：sidecar 镜像 docker build 实测（需本地构建 fork）
- [ ] 未执行：12 案例人工盲评（本轮仅产出 materials：manuscript/citation_map/
      receipt/quality/metrics 三端可取）
- [ ] 未开始：商业版回归（另起任务，见 D-010）

## 语义边界（重申）

- 候选稿是**评估材料**：机械指标不含质量结论；grounding 通过≠正确。
- sidecar 不可取消/无队列：`outcome_unknown` 只对账不重发；cancel 语义
  诚实（running 取消=丢弃导入，sidecar 侧继续）。
- 交换卷为单机传输；跨机需受控上传 API（未做）。

# Task1–13 README 刷新设计

## 目标

让中英文 README 准确描述 Task1–13 完成后的 LuminBuddy：文档优先、合约驱动、计划可见、产物可追溯、质量可验收，并明确 OSS 与商业版的能力边界和真实发布状态。

## 信息架构

README 面向首次接触项目的创作者、开发者和部署者，不按任务编号展开实现细节。正文依次回答：

1. 产品服务谁，以及长文创作、多材料综合、忠实改写三类核心场景；
2. 自动执行和用户控制如何并行；
3. WritingContract、ExecutablePlan、typed Artifact、质量门禁、writingstore 和文档版本如何构成主链路；
4. Candidate Draft、Accepted Draft、Verified Deliverable 的含义；
5. 如何启动、验证、扩展检索/MCP，以及当前能否生产放量；
6. Task1–13 分别完成了哪组基础设施。

旧 Harness、Pipeline、Editorial Role 不再被描述为第二套权威内核，而是通过 ExecutorAdapter 进入治理运行时的兼容执行能力。

## 双版本策略

两版共享产品定位、协议、核心运行时、本地检索、网页抓取、MCP、基础验证器和文档格式。OSS README 明确不包含付费搜索 Provider、商业凭证变量和商业 CLI；商业版 README 说明可配置的付费搜索、治理与运维能力，但不记录密钥或供应商响应。

## 真实性约束

- 不把代码合并、容器启动或健康检查表述为生产上线。
- 明确代码、构建、CI 与隔离真实链路已经通过。
- 明确生产流量仍受 staging 质量归档、恢复/取消/回滚演练、凭证轮换和 rollout 决策约束。
- 不声称 Candidate 已经等同 Accepted 或 Verified。
- 中英文语义一致，版本差异只出现在明确标注的 edition 段落。

## 验证

- Markdown 结构、相对链接和中英文标题完整；
- README 中不存在“治理运行时仍待逐步落地”的过期表述；
- README 中不存在把 Harness 描述为唯一当前核心的过期表述；
- OSS 不出现商业付费凭证变量或把付费搜索描述为内置；
- make verify 仍为唯一推荐的本地综合门禁；
- FILE_INDEX.md 同步记录 README 与本设计/计划。

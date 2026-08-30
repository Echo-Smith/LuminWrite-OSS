# LuminBuddy 治理型写作运行时设计

状态：已确认

日期：2026-08-27

范围：开源版与商业版共同内核、写作运行时、内容协议、质量治理和前端工作台

## 1. 决策摘要

LuminBuddy 不建设通用 Agent 操作系统，而建设面向专业内容创作者与知识工作者的治理型写作运行时。系统覆盖长文创作、多材料综合、忠实改写等多种写作任务，并保持写作领域内的灵活性。

核心决策如下：

- 文档是产品主对象；聊天只负责修改合约、解释决策和控制执行。
- 从第一天统一 WritingContract、RunPlan、Artifact、Document AST 和质量状态，不保留绕过内核的旧路径。
- 系统自动选择与用户手工选择并行存在，用户明确选择优先。
- 模板是可信快路径，不是执行白名单；系统允许类型化动态编排。
- 控制信息使用 JSON，正文使用受限 Markdown，权威存储使用 Document AST。
- 所有长任务经过统一 Orchestrator，由 API 和 Worker 两种运行角色协作完成。
- 正式采用 Candidate Draft、Accepted Draft、Verified Deliverable 三级质量状态。
- BLOCKER 不可豁免；非等价验证降级不能维持原保障等级。
- 完整快照持久化是正式提交和 Verified 的前置条件。
- 开源版与商业版共享协议和内核，商业差异放在策略、验证器、连接器和治理能力中。
- 前端保持当前左侧全局面板，中央突出正文，右侧承载详情，底部承载可收纳对话。

## 2. 产品边界

### 2.1 目标用户

- 专业内容创作者
- 研究、咨询、产品、运营等知识工作者
- 需要处理长文、多份材料和高保真改写的用户

### 2.2 核心场景

1. 长文创作：从 Brief、材料、结构到分段写作和全文统稿。
2. 多材料综合：从并行理解、冲突识别到综合表达和来源追溯。
3. 忠实改写：在保持事实、观点和作者声音的前提下完成润色或重构。

### 2.3 非目标

- 不把产品扩张为通用数字员工或任意外部系统操作平台。
- 不把内部 Agent、模型或框架名称作为普通用户的主要心智模型。
- 不用固定学术论文结构约束所有写作任务。
- 不以模型声称完成或单个 Judge 总分作为交付标准。

## 3. 总体架构

```text
用户意图
   ↓
WritingContract
   ↓
Strategy Compiler
   ↓
RunPlan
   ↓
Governed Runtime
   ↓
ArtifactGraph + Document AST
   ↓
Quality Gates
   ↓
Candidate / Accepted / Verified
```

系统由五个逻辑平面组成：

```text
交互平面：文档编辑器、聊天、运行状态、版本与质量入口
控制平面：WritingContract、Policy、Strategy Compiler、Approval
执行平面：Orchestrator、Scheduler、Executor、Checkpoint、Retry
内容平面：LCP、Document AST、Artifact、Revision、Validator、Renderer
运维平面：RunLedger、Snapshot、Trace、Metrics、Audit、Replay
```

这些是逻辑边界。首期采用模块化单体代码库，不拆成大量微服务；运行时分为 API 与 Worker 两种进程角色。

## 4. WritingContract

WritingContract 是版本化写作合约，不是 Prompt。它至少描述：

```yaml
contract_id: ctr_xxx
version: 3
status: confirmed

intent:
  operation: create
  genre: industry_analysis
  purpose: 支持 AI 产品架构决策

audience:
  role: AI 产品负责人
  knowledge_level: professional

content:
  topic: 多 Agent 写作治理
  central_question: 如何兼顾自动选择与用户控制
  required_points: []
  prohibited_points: []

voice:
  tone: professional
  preserve_user_voice: true

material_policy:
  user_material_priority: highest
  allow_external_research: true
  conflict_handling: ask_user

evidence_policy:
  level: sourced
  unsupported_claims: prohibit

delivery:
  format: markdown
  language: zh-CN
  length:
    min: 5000
    max: 7000

collaboration:
  task_mode: guided
  orchestration_mode: auto
  assurance_level: sourced
  approval_mode: conditional

source_attributions:
  - field_path: /collaboration/task_mode
    source: user
    value_hash: sha256:...
    recorded_at: 2026-08-27T00:00:00Z

inferences: []
```

规则：

- 每次运行绑定一个确定的合约版本。
- LCP v1 使用 `contract_hash` 保存排除哈希字段自身后的规范 JSON SHA-256，格式为 `sha256:<64 lowercase hex>`。
- `delivery.length` 使用 `{min,max}` 正整数对象，不再使用需要二次解析的范围字符串。
- `source_attributions` 按 JSON Pointer 记录关键字段来自 `user`、`system_inference` 或 `platform_default`，并绑定字段值哈希。
- `inferences` 与最终有效值分离，记录建议值、置信度、状态、reason code 和面向审计的简短说明；不保存隐藏思维链。
- 已确认合约不原地覆盖，只能产生新版本。
- 用户确认、系统推断和平台默认值必须标记来源。
- 影响任务方向的低置信度推断必须澄清。
- 长上下文不会成为权威状态；Agent 每一步重新接收合约和相关产物。

## 5. 用户选择与自动策略

用户面对三个正交维度：

```text
task_mode           auto | writing | guided | polish
orchestration_mode  auto | fast | outline_first | sourced | strict_research
assurance_level     flexible | standard | sourced | strict
```

内部现有 Harness、Pipeline、Editorial 和未来 AutoResearch 能力作为执行器或策略组成部分存在，不作为顶层用户模式。

仲裁规则：

- 用户明确选择优先于系统推荐。
- 系统只有在用户选择 auto 时才自主决定。
- 系统可以建议切换，不能静默覆盖用户选择。
- 不可用策略必须说明原因并提供备选。
- 运行开始后锁定计划版本；重试和恢复沿用该版本，重规划产生新版本。
- StrategyDecision 记录选择原因、候选方案、成本、时间、置信度和降级条件。

## 6. 自适应策略编译

模板不是白名单。系统采用四级计划体系：

```text
可信模板
计划片段
原子能力
类型化动态编排
```

### 6.1 双层计划

```text
IntentPlan       LLM 提出的语义计划
ExecutablePlan   确定性编译器生成的可执行计划
```

LLM 可以：

- 根据任务创建新的步骤组合。
- 组合已注册能力和计划片段。
- 提议有界并行、条件分支和修订。
- 评价多个候选方案。

LLM 不可以：

- 发明未注册执行器。
- 绕过输入输出类型、预算、权限或验收。
- 创建无上限循环。
- 静默降低保障等级。
- 将临时输出直接提交为正式文档。

### 6.2 Writing Plan IR

第一版计划语言支持：

```text
Sequence
Parallel
Map
Reduce
Condition
Retry
Refine
HumanGate
Validate
Fallback
```

所有循环、并发、重试和额外预算都有明确上限。ExecutablePlan 在运行前执行类型、依赖、能力、预算、权限、交付物和失败路径静态检查。

### 6.3 计划信任等级

- T1：经过回归验证的可信模板，可按普通任务规则自动运行。
- T2：组合已有计划片段，通过静态验证后可运行。
- T3：使用原子能力形成的新拓扑，执行更严格验证和审批判断。
- T4：缺少必要能力，明确说明限制或提供部分完成方案。

## 7. 文档中心模型

权威文档不是 Markdown 字符串，而是包含稳定 block_id 的结构化文档树。

```text
Document
  → DocumentVersion
      → Section
          → Paragraph / List / Quote / Table / Code / Citation
```

规则：

- 每次用户或 AI 修改都产生不可变版本。
- Agent 不直接覆盖文档，只能提交 RevisionSet。
- RevisionSet 记录 base_version、目标 block、前后哈希、修改原因和语义影响。
- 用户直接编辑与后台运行冲突时，先做块级冲突判断，再自动合并或请求用户选择。
- 版本历史底层允许形成 DAG，普通前端默认呈现线性主版本和少量备选方案。
- Markdown、HTML、DOCX 等均由 Document AST 确定性渲染。

每个节点收到受控 ContextEnvelope：

```text
合约版本
当前步骤目标
不可违反的约束
允许读取的 Artifact
当前章节及相邻摘要
相关 Claim 和 Evidence
用户锁定内容
剩余预算
预期输出 Schema
```

## 8. Lumin Content Protocol

LCP 采用三层协议：

```text
控制层：JSON / Typed Schema
内容层：Lumin Markdown
存储层：Document AST
```

### 8.1 控制层

JSON 承载：

- WritingContract
- RunPlan
- 策略决策
- 工具参数
- 修改目标和版本
- 引用与证据关系
- 运行状态和验收结果

### 8.2 内容层

受限 Markdown 承载正文、段落、列表、引用、代码、链接和普通表格。默认禁止原始 HTML、MDX、Front Matter、脚本和未注册扩展。

引用使用受控标记：

```markdown
多 Agent 可以提升复杂材料的覆盖率。[[cite:source_018]]
```

Parser 将其转换为 typed citation node，Renderer 再输出行内链接、脚注或指定引用格式。

### 8.3 流式状态

```text
PROVISIONAL → GENERATED → PARSED → VALIDATED → COMMITTED
```

前端流式内容只是预览。模型停止输出不等于提交完成；解析、验证、版本冲突检查和快照持久化全部成功后才原子提交。

### 8.4 修复层级

```text
R0 确定性规范化
R1 确定性语法修复
R2 局部 LLM 修复
R3 重新生成当前章节
```

观点、事实、来源和用户声音变化不能静默修复。

## 9. Runtime 与持久化

### 9.1 运行形态

API 负责文档读写、合约、审批、状态查询、流式事件和用户控制。Worker 负责节点执行、模型和工具调用、LCP 解析、验证、Artifact、Checkpoint 和心跳。

所有任务只能经过 Orchestrator：

```text
RunPlan
  → 找到依赖满足的节点
  → 检查预算、审批、权限和取消状态
  → 创建 NodeAttempt
  → Worker 执行
  → Artifact + Events
  → Validator
  → 推进、重试、重规划或暂停
```

### 9.2 状态来源

- 事务存储保存合约、计划、文档版本、Artifact 元数据、Revision、Approval、Run、NodeAttempt 和 RunLedger。
- 对象存储保存附件、来源快照、大型输出和导出文件。
- 任务队列只负责唤醒 Worker，不是权威状态。
- 搜索和向量索引是可重建的派生数据。

### 9.3 执行可靠性

```text
至少一次调度 + 幂等结果 + 原子提交 = 业务层恰好一次
```

节点以 run_id、node_id、attempt 和 idempotency_key 标识。Worker 通过心跳和租约管理失联；失败后根据 Checkpoint、Artifact 和副作用类型决定重试、恢复或暂停。

## 10. 审批与运行状态

默认规则：

> 普通任务生成计划后立即执行，前端始终可查看、暂停和中止；高成本、高风险或用户选择手动控制时必须先确认。

审批模式：

```text
conditional  默认，仅命中门禁时确认
always       每个新计划和重大重规划都确认
auto         允许安全重试和降级，但不能绕过强制门禁
```

审批绑定具体 plan_id、plan_hash、预算和权限。计划发生实质变化后原审批失效。

写作场景的高风险主要包括：

- 改变文章核心观点、受众或用途。
- 将润色升级为大幅重写。
- 用户材料存在关键冲突。
- 重要结论无法达到约定证据标准。
- 扩大研究范围、时间或成本。
- 破坏用户锁定内容或作者声音。

中止时停止调度新节点、保存有效 Artifact 和最后 Checkpoint、标记 partial 内容，并允许恢复或基于已有成果重规划。

## 11. 质量治理

正式采用三级状态：

### 11.1 Candidate Draft

- 生成后即可存在。
- 可以未验证、验证失败或包含 BLOCKER。
- 与正式文档隔离，不能冒充完成。
- 即使失败也必须完整持久化。

### 11.2 Accepted Draft

- 不存在 BLOCKER。
- 用户明确接受或合约允许自动接受。
- ERROR 已修复或按策略被明确豁免。
- 所有豁免形成 DecisionRecord。
- 未达到的保障要求持续显示。

### 11.3 Verified Deliverable

- 所有必需验证器成功运行。
- 不存在 BLOCKER、未解决 ERROR 或 ERROR 豁免。
- 实际保障等级不低于合约要求。
- 验证版本与提交版本完全一致。
- 完整快照已经持久化。

Verified 由验收系统授予，不能由用户直接选择。

### 11.4 验证体系

验证器分为：

- 确定性结构验证器
- WritingContract 验证器
- Claim/Evidence 验证器
- 原文与改写比较验证器
- 模型辅助质量验证器

质量报告按结构、合约、证据、语义保持、声音、可读性和交付规范分维度记录，不用单一总分掩盖硬失败。

### 11.5 BLOCKER

BLOCKER 不可豁免。典型问题包括 AST 损坏、版本冲突、Artifact 哈希不一致、引用对象不存在、润色改变核心语义、用户锁定内容被破坏、必需验证无法执行以及快照持久化失败。

解决方式只能是修复、重新生成、恢复版本、修改 WritingContract 后重验或终止候选稿。

### 11.6 验证降级

- 经过认证的等价备用验证器可以维持原保障等级。
- 非等价降级必须降低 achieved_assurance。
- achieved_assurance 低于 requested_assurance 时，不能在原合约下进入 Verified。
- 可选风格检查可以跳过并产生 WARNING。
- AST、版本一致性、Artifact 完整性等检查采用 fail closed。

## 12. 完整快照与审计

每个关键 Checkpoint 生成 RunSnapshotManifest，引用：

- WritingContract 版本与哈希
- IntentPlan、ExecutablePlan 和 StrategyDecision
- Document base/candidate version 与 AST 哈希
- 输入材料和来源快照
- Artifact 和 RevisionSet
- 模型、能力、Prompt 模板和 Validator 版本
- 用户审批、豁免和其他 DecisionRecord
- Token、成本、耗时、重试和降级
- QualityReport 和 RunLedger 位置

不保存隐藏思维链、密钥和敏感请求头；需要审计的理由使用结构化说明。快照目标是逻辑可重放和责任可追溯，不承诺随机模型调用逐 Token 完全复现。

持久化失败规则：

- 合约或计划快照失败：不启动运行。
- 原始输出保存失败：不提交 Revision。
- 质量快照失败：不授予 Verified。
- 最终 AST 或版本快照失败：BLOCKER。

普通质量摘要和完整审计报告必须来自同一个 QualityReport 投影。普通用户看到状态、少量关键问题和修复入口；审计视图展示规则、版本、Finding、证据、降级、快照和状态计算过程。

## 13. 前端工作台

设计对象为专业内容创作者与知识工作者；视觉气质冷静、克制、有编辑部质感，突出文档而非 AI 技术感。

### 13.1 桌面布局

```text
┌──────────────┬──────────────────────────────────────┬──────────────────┐
│ 左侧全局面板  │ 文档标题 / 写作模式 / 执行策略 / 状态 │ 右侧详情面板      │
│              ├──────────────────────────────────────┤                  │
│ 新建对话      │ 简要运行步骤                         │ 大纲｜材料｜运行  │
│ 产品模块      │ 理解材料 ✓ → 构建结构 ● → 撰写 → 核验 │ 质量｜版本        │
│ 历史会话      ├──────────────────────────────────────┤                  │
│              │                                      │ 当前标签页内容    │
│              │               正文                   │                  │
│              │                                      │                  │
│ 用户与主题    ├──────────────────────────────────────┤                  │
│              │ AI 对话与写作指令       [紧凑] [收起] │                  │
└──────────────┴──────────────────────────────────────┴──────────────────┘
```

### 13.2 左侧全局面板

- 保持当前的新建对话、产品模块、历史会话、用户和主题职责。
- 不与右侧当前文档详情合并。
- 用户可以主动展开或收回。
- 写作、验证和后台状态变化绝不自动切换左栏。
- 首次进入维持现有默认，此后记住用户最后选择。

### 13.3 中央主舞台

- 正文拥有最大视觉权重和稳定阅读宽度。
- 上方只展示一至两行用户可理解的编辑步骤。
- Candidate、Revision、引用和质量 Finding 直接定位到文档块。
- 详细 DAG、Agent、Executor 和 Validator 信息只在详情视图展示。

### 13.4 右侧详情

标签为：

```text
大纲 | 材料 | 运行 | 质量 | 版本
```

面板可调整宽度、收起并记忆状态。系统可以显示数量或阻断标记，但不能自动抢走用户当前标签页。

### 13.5 底部对话

对话属于中央写作区域，支持：

```text
展开 | 紧凑 | 收起到右下角
```

任务开始前默认展开；运行后不自动收起。收起入口显示具体状态，如“AI 协作 · 撰写中 3/7”或“需要确认 · 查看提纲”，不能只显示机器人图标。

### 13.6 布局状态

```yaml
workspace_layout:
  global_sidebar: expanded
  detail_panel: expanded
  conversation_panel: compact
  detail_tab: quality
```

- 左栏状态按用户/设备保存。
- 右栏状态按工作区保存。
- 详情标签按文档保存。
- 对话大小和状态按用户/设备保存。
- 空间不足时优先把右侧详情改为抽屉，保护正文宽度，不擅自关闭用户选择的左栏。

## 14. 开源与商业边界

两版共同内核：

- WritingContract
- RunPlan / Writing Plan IR
- LCP
- 基础 Document AST
- DocumentVersion / RevisionSet
- Artifact 接口
- RunLedger 与基础 Snapshot
- 基础 Validator 接口和质量状态
- 运行事件标准

商业版增强：

- 高级策略编译和策略包
- 高级证据、事实和语义验证器
- 团队 Policy、审批和审计
- 企业连接器和权限
- 成本、质量和策略分析
- 专有写作能力和 Renderer

商业能力通过版本化 Capability Manifest 注册，不在共同内核散布 edition 条件分支。开源版生成的基础文档和运行数据能够进入商业版，核心格式不分叉。

## 15. 生产不变量

以下规则必须进入代码级不变量和回归测试：

```text
不存在 WritingContract → 不得运行
不存在合法 RunPlan → 不得调度
Agent 绕过 Artifact/Revision 直接写文档 → 禁止
流式输出未解析和验证 → 不得提交
存在 BLOCKER → 不得 Accepted、不得 Verified
必需验证器未运行 → 不得 Verified
非等价验证降级 → 必须降低 achieved_assurance
achieved_assurance < requested_assurance → 不得按原合约 Verified
完整快照未持久化 → 不得正式提交或 Verified
验证对象版本 ≠ 提交版本 → 验证结果无效
用户明确策略被系统静默替换 → 禁止
无界循环、无界重试和无界并行 → 禁止
普通视图与审计报告状态不一致 → 禁止
```

## 16. 运维与效果指标

运行指标：

- 节点成功率、重试率、失联恢复率
- Token、成本、延迟和并发
- LCP 解析率、局部修复率和提交耗时
- Validator 可用性、降级率和错误分布
- Snapshot 完整率和重放成功率

写作产品指标：

- 首稿可用率
- 用户平均修改轮次
- 用户最终修改幅度
- 合约漂移率
- 语义保持失败率
- 重要 Claim 证据覆盖率
- 引用支持准确率
- 系统策略被用户改选比例
- Candidate 到 Accepted、Verified 的转化
- 每篇完成文档的时间与成本

模型、Prompt、策略和 Validator 的新版本必须经过写作回归集、长上下文、冲突材料、并发编辑、断流恢复和验证降级测试，再进行灰度发布。

## 17. 实施原则

- 新内核先定义协议、状态和不变量，再接入写作能力。
- 不通过给旧模式增加更多条件分支来模拟新体系。
- 接入现有 Harness、Pipeline、Editorial 时，只能实现 Capability/Executor 接口。
- 从进入新内核的第一条请求开始，必须统一经过 Contract、Plan、Artifact、Validation 和 Snapshot。
- 前端保持当前产品骨架，通过渐进披露增加运行和治理能力。
- 任何阶段都不得以隐藏失败、静默降级或伪造 Verified 换取表面成功率。

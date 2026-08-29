# Task12 Implementation Tasks

- [ ] 1. 固定 rollout 与 telemetry 契约
  - 实现版本化 policy、确定性决策、kill switch、稳定 reason code。
  - 实现低基数 RuntimeTelemetry 与高基数 RolloutEvidenceStore。
  - _Requirements: R2, R4_

- [ ] 2. 实现 shadow isolation wrapper
  - baseline 保持 authoritative，shadow 使用隔离内容区。
  - 比较输出 manifest、验证结果、用量、时延和稳定错误码。
  - _Requirements: R2, R3, R4_

- [ ] 3. 接通三类 B2 适配器
  - Engine Step、Editorial Role、Harness Core 使用统一契约。
  - Harness.Run 与 Editorial DAGExecutor 继续 fail-closed。
  - _Requirements: R1_

- [ ] 4. 将 Task11 埋点接入 Orchestrator 与 MaterialAdapter
  - 覆盖 route、material integrity、execution、comparison、commit 和 authority violation。
  - 验证高基数身份不进入 metric label。
  - _Requirements: R4_

- [ ] 5. 打通三条纵向场景
  - 长文创作、多材料综合、忠实改写 fixture 与集成测试。
  - 验证 Candidate / Accepted / Verified 质量门。
  - _Requirements: R5_

- [ ] 6. 完成防御矩阵
  - 超时、取消、重复、候选崩溃、证据失败、策略变化、材料篡改、validator 降级和 commit 失败。
  - _Requirements: R3, R6_

- [ ] 7. 双版本验证与 readiness
  - 分别运行 Go、前端测试、lint、build；检查共同文件一致性。
  - 记录 shadow-ready 证据，不执行生产放量。
  - _Requirements: R1-R6_

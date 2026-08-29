# Task11 Implementation Tasks

- [x] 1. 固定材料与 B2 契约
  - 新增 MaterialAdapter、稳定错误码、AdapterPolicy 和 conformance tests。
  - _Requirements: R1, R3_

- [x] 2. 接通受治理的初始材料
  - 允许计划以 contract + materials 编译和验证。
  - 为 Orchestrator 提供组合 InitialArtifactProvider。
  - _Requirements: R1, R2_

- [x] 3. 固化唯一提交与错误投影
  - 验证 Executor 不能提交权威状态。
  - 将稳定错误码写入 Attempt，而非笼统错误。
  - _Requirements: R2, R3_

- [x] 4. 添加材料冲突 Finding
  - 生成带双向 source refs 的不可隐藏冲突结果。
  - _Requirements: R4_

- [x] 5. 保持旧算子生产 fail-closed
  - 为 Engine、Editorial、Harness 执行同一离线契约测试。
  - 默认注册表不得启用旧 adapter。
  - _Requirements: R3_

- [x] 6. 更新材料界面
  - 展示治理状态，从材料发起写作时只传 material_refs。
  - _Requirements: R5_

- [x] 7. 双版本验证与提交
  - 运行 Go、前端测试、lint、build 和树一致性检查。
  - _Requirements: R1-R5_

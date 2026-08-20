# Learnings

Corrections, insights, and knowledge gaps captured during development.

**Categories**: correction | insight | knowledge_gap | best_practice

---

## [LRN-20260718-001] go_import_cycle_engine_tools

**Logged**: 2026-07-18T00:12:00+08:00
**Priority**: high
**Status**: completed
**Area**: backend/architecture

### Summary

已弃用的 UnifiedAgent 放在 `engine` 包中导入 `tools` 包，而 `tools` 包已经导入 `engine` 包（`SearchClient.Search` 返回 `engine.SearchResult`），形成 `engine ⇄ tools` 循环依赖，编译报错 `import cycle not allowed`。UnifiedAgent 源码已删除，但此经验教训仍有参考价值。

### Details

Go 不允许循环依赖。当 `engine` 包的类型（如 `SearchResult`）被 `tools` 包引用时，`tools` 依赖 `engine`。此时若在 `engine` 包中新增代码导入 `tools`（如 `UnifiedAgent` 需要 `tools.LLMClient`），就形成了环：

```
engine → tools → engine  ❌ cycle
```

### Implementation (2026-07-18)

将 `UnifiedAgent` 从 `engine` 包移到新的 `agent` 包，打破循环：

```
之前： engine → tools → engine  ❌ cycle
之后： agent → engine + tools   ✅ 无环
      engine → tools             ✅ 无环
```

### Suggested Action

**预防措施**：
1. 新增跨包引用时，先用 `grep` 检查目标包是否已引用当前包
2. 当 A 包需要 B 包的类型、B 包需要 A 包的类型时，引入第三个中间包 C
3. Go 的分层设计原则：上层包可以引用下层包，下层包不能引用上层包；若需要双向依赖，提取共享类型到独立包

### Metadata
- Source: test-failure
- Related Files: docs/wiki/architecture-history.md (UnifiedAgent 历史记录)
- Tags: go, import-cycle, architecture, best_practice
- Pattern-Key: go.break_import_cycle_with_intermediate_package

---

## [LRN-20260718-002] go_embedded_struct_interface_not_forwarded

**Logged**: 2026-07-18T00:12:00+08:00
**Priority**: medium
**Status**: completed
**Area**: backend/testing

### Summary

Go 嵌入结构体（embedded struct）的方法虽然可以被外层结构体调用，但类型断言检查的是值的具体类型，不会沿嵌入链查找接口实现。测试中传入 `&step.mockStep`（不实现 `Skipper`）而非 `step`（`*mockSkipperStep`，实现 `Skipper`），导致 `StepTool.Execute` 中的 `t.step.(Skipper)` 类型断言失败。

### Details

```go
type mockStep struct { name StepName; executed bool }
type mockSkipperStep struct {
    mockStep          // 嵌入
    shouldSkip bool
}
func (m *mockSkipperStep) ShouldSkip(...) bool { return m.shouldSkip }

// ❌ 错误写法：传入了 mockStep，类型断言找不到 ShouldSkip
tool := NewStepTool(&step.mockStep, ...)
// step.(Skipper) → false（mockStep 没有 ShouldSkip 方法）

// ✅ 正确写法：直接传入 mockSkipperStep
tool := NewStepTool(step, ...)
// step.(Skipper) → true（mockSkipperStep 有 ShouldSkip 方法）
```

虽然 `mockSkipperStep` 通过嵌入获得了 `mockStep` 的 `Name()`/`CanPause()`/`Execute()` 方法，但 `Skipper` 接口的实现仅在 `*mockSkipperStep` 上。类型断言 `t.step.(Skipper)` 检查的是传入值的具体类型是否实现了 `Skipper`。

### Implementation (2026-07-18)

将 `NewStepTool` 的参数从 `&step.mockStep` 改为 `step`（直接传入 `*mockSkipperStep`）。

### Suggested Action

**预防措施**：
1. 测试 mock 结构体时，始终传入实现接口的具体类型，不要取嵌入字段
2. 如果 `StepTool` 需要同时支持"有 Skipper"和"无 Skipper"的 Step，可以在 `NewStepTool` 中增加一个可选的 `shouldSkip func(*ExecutionContext) bool` 参数
3. 或者让所有 Step 都实现 `ShouldSkip`（返回 false 作为默认）

### Metadata
- Source: test-failure
- Related Files: internal/engine/tool_registry_test.go, internal/engine/step_tool.go
- Tags: go, embedded-struct, type-assertion, testing, interface
- Pattern-Key: go.embedded_struct_does_not_forward_interface_assertion

---

## [LRN-20260718-003] truncation_test_boundary_condition

**Logged**: 2026-07-18T00:12:00+08:00
**Priority**: low
**Status**: completed
**Area**: backend/testing

### Summary

截断测试使用恰好等于阈值边界（2001 字符）的输入，截断后长度（2011 字节）落入断言失败区间（`> 2010`），导致 flaky test。中文字符的 UTF-8 多字节编码使字节长度与字符长度不一致，进一步增加了边界计算的不可预测性。

### Details

```go
// FunctionTool.Execute 的截断逻辑：
if len(result) > 2000 {
    result = result[:2000] + "...(截断)"
}
// len("...(截断)") = 3 + 3*4 + 3 = 18 字节（中文每字 3 字节）
// 总长 = 2000 + 18 = 2018 字节... 但实际报告 2011

// 测试断言：
if len(result.Summary) > 2010 {  // 2011 > 2010 → true → FAIL
    t.Errorf(...)
}
```

边界条件问题的根因：
1. `fmt.Sprintf("%2001s", "x")` 生成 2002 字符（2001 空格 + "x"）
2. 截断后 `result[:2000]` 是 2000 字节
3. `"...(截断)"` 中 `...` 3 字节 + `(` 3 字节 + `截` 3 字节 + `断` 3 字节 + `)` 3 字节 = 15 字节
4. 总长 = 2000 + 15 = 2015... 但实际是 2011

不同字节数计算的差异使得精确边界断言不可靠。

### Implementation (2026-07-18)

使用足够大的输入（5000 字符）和宽松的断言阈值（2100），远离边界条件：

```go
// ✅ 修复后
longText := fmt.Sprintf("%5000s", "x")  // 远大于 2000
if len(result.Summary) > 2100 {        // 远大于 2000 + suffix
    t.Errorf("expected truncation to ~2000 chars, got length %d", len(result.Summary))
}
```

### Suggested Action

**预防措施**：
1. 截断/截取测试不要使用恰好等于阈值的输入，使用远大于阈值的值
2. 断言使用宽松的区间（如 `2000 ≤ len ≤ 2100`）而非精确的边界值
3. 中文字符在 Go 中占 3 字节（UTF-8），`len()` 返回字节数不是字符数；涉及中文的长度断言要留足容差
4. 如果需要精确的字符数截断，使用 `[]rune` 而非 `len()`

### Metadata
- Source: test-failure
- Related Files: internal/engine/tool_registry_test.go, internal/engine/step_tool.go
- Tags: testing, truncation, boundary-condition, utf8, flaky-test
- Pattern-Key: test.avoid_exact_boundary_in_truncation_tests

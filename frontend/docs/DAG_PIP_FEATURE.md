# DAG 画中画功能文档

## 功能概述

DAG 画中画（Picture-in-Picture）功能允许用户在文档视图中同时查看 DAG 工作流的执行状态，无需切换页面。当用户选择 `editorial` 协作模式并触发 DAG 工作流时，画中画窗口会自动显示。

---

## 核心特性

### 1. 自动触发
- **触发条件**：`agentMode === "editorial"` 且 Planner 返回了包含节点的 DAG 计划
- **触发时机**：WebSocket 接收到 `workflow.plan` 事件后
- **触发效果**：画中画窗口从右下角滑入，展示 DAG 画布

### 2. 四种显示模式

#### Normal 模式（默认）
- 尺寸：800×600px
- 位置：右下角，距离边界 24px
- 特点：完整展示 DAG 画布和工具栏

#### Minimized 模式
- 尺寸：280×80px
- 位置：保持在当前位置
- 特点：只显示工具栏（状态指示 + 展开按钮）
- 用途：监控工作流状态但不占用太多空间

#### Docked 模式
- 位置：吸附到屏幕边缘（上/下/左/右）
- 特点：自动对齐，拖拽时显示吸附区域提示
- 吸附规则：
  - 距离边界 < 40px 时触发吸附
  - 吸附后距离边界 16px

#### Fullscreen 模式
- 尺寸：覆盖整个工作区
- 位置：居中，距离边界 32px
- 特点：适合查看复杂 DAG 拓扑

### 3. 交互能力

#### 拖拽移动
- 拖拽工具栏可移动整个窗口
- 拖拽过程中显示半透明预览
- 自动吸附到屏幕边缘
- 支持移动端触摸拖拽

#### 调整大小
- 八个方向的调整把手：
  - 四角：`nw` `ne` `se` `sw`
  - 四边：`n` `e` `s` `w`
- 最小尺寸：400×300px（Normal 模式）
- 最大尺寸：屏幕尺寸 - 64px 边距
- Minimized 模式下禁用调整

#### 键盘快捷键
- `Escape`：关闭画中画
- `F`：切换全屏模式
- `M`：切换最小化模式
- 调整把手聚焦时：
  - 方向键：微调尺寸（每次 8px）
  - `Shift + 方向键`：快速调整（每次 32px）

---

## 技术实现

### 状态管理（workspace-layout-store.ts）

```typescript
interface WorkspaceLayoutState {
  // DAG 画中画状态
  dagPipVisible: boolean;           // 是否显示
  dagPipMode: "normal" | "minimized" | "docked" | "fullscreen";
  dagPipPosition: { x: number; y: number };
  dagPipSize: { width: number; height: number };
  dagPipDockSide: "top" | "right" | "bottom" | "left" | null;
  
  // 操作方法
  setDagPipVisible: (visible: boolean) => void;
  setDagPipMode: (mode: ...) => void;
  setDagPipPosition: (position: { x: number; y: number }) => void;
  setDagPipSize: (size: { width: number; height: number }) => void;
  setDagPipDockSide: (side: ...) => void;
}
```

### 自动触发逻辑（writing-workspace.tsx）

```typescript
// 监听 editorial 模式 + DAG plan 生成
const wfPlan = useWorkflowStore((state) => state.plan);
useEffect(() => {
  if (agentMode === "editorial" && wfPlan && wfPlan.workflow.nodes.length > 0) {
    setDagPipVisible(true);
  }
}, [agentMode, wfPlan, setDagPipVisible]);
```

### 组件结构（workflow-pip.tsx）

```tsx
<WorkflowCanvasPIP>
  {/* 拖拽层 */}
  <div onPointerDown={startDrag}>
    {/* 工具栏 */}
    <header>
      <WorkflowStatus />
      <PIPControls />
    </header>
    
    {/* 画布内容（仅在非 Minimized 模式显示）*/}
    {mode !== "minimized" && <WorkflowCanvas />}
  </div>
  
  {/* 八个调整把手 */}
  {mode === "normal" && <ResizeHandles />}
</WorkflowCanvasPIP>
```

---

## 样式设计

### 视觉层级
- `z-index: 100`：确保在文档内容之上，但在模态对话框之下
- `backdrop-filter: blur(8px)`：半透明背景，保持上下文可见性
- `box-shadow`：深色阴影，强调浮动效果

### 响应式适配
- 移动端（< 768px）：
  - 默认宽度：90vw
  - 最小化高度：60px
  - 工具栏按钮间距减小

### 动画效果
- 进入：`slideInUp` 从底部滑入
- 退出：`fadeOut` 淡出
- 模式切换：`transition-all duration-300`
- 拖拽：`opacity-80` 半透明预览

---

## 使用场景

### 场景 1：监控多步骤研究流程
```
用户：「帮我写一篇关于量子计算的综述文章」
→ 选择 editorial 模式
→ Planner 生成 5 节点 DAG（调研 → 提纲 → 初稿 → 审核 → 定稿）
→ 画中画自动弹出
→ 用户在文档视图编辑，同时观察 DAG 节点状态变化
```

### 场景 2：调试 DAG 配置
```
用户：进入 editorial 编辑模式
→ 手动添加/删除节点
→ 在画中画中实时预览 DAG 拓扑变化
→ 验证依赖关系是否正确
→ 点击「运行」前确认无环
```

### 场景 3：跨文档对比
```
用户：同时处理两个文档
→ 文档 A：正在执行 DAG 工作流
→ 画中画最小化到右下角
→ 切换到文档 B 继续写作
→ 偶尔瞥一眼画中画确认 A 的进度
```

---

## 与 WorkflowPage 的关系

### 互斥关系
- `/workflow` 路由已重定向到 `/write`
- 画中画是 `/write` 内的嵌入式视图
- **不存在**同时打开两个 DAG 画布的情况

### 功能对等性
| 功能 | WorkflowPage（已废弃） | WorkflowCanvasPIP |
|------|----------------------|-------------------|
| DAG 可视化 | ✅ React Flow 画布 | ✅ 复用同一组件 |
| 节点编辑 | ✅ 添加/删除/连线 | ✅ 完全相同 |
| 运行控制 | ✅ 开始/暂停/停止 | ✅ 工具栏按钮 |
| 状态监控 | ✅ 节点颜色变化 | ✅ 实时同步 |
| 视图模式 | ❌ 全屏独占 | ✅ 画中画叠加 |

### 迁移建议
- 保留 `WorkflowCanvas` 组件（纯画布，无路由依赖）
- 删除 `WorkflowPage` 组件（已被画中画替代）
- 移除 `/workflow` 路由配置

---

## 已知限制

1. **单实例限制**：同一时间只能显示一个画中画（对应当前文档的 DAG）
2. **移动端体验**：触摸拖拽可能与画布内部的拖拽（节点移动）冲突，需要用户明确拖拽工具栏
3. **性能考虑**：大型 DAG（>50 节点）在画中画中可能需要禁用动画以保持流畅
4. **持久化范围**：位置和尺寸会持久化到 localStorage，但模式（minimized/docked）不持久化

---

## 后续优化方向

1. **智能布局**：
   - 检测文档视图的空白区域
   - 自动放置画中画到最不遮挡内容的位置

2. **快捷操作**：
   - 双击工具栏：切换全屏模式
   - 拖拽到屏幕顶部：自动关闭（类似 macOS Dock）

3. **多视图同步**：
   - 在详情面板中显示当前节点的详细输出
   - 点击画中画节点 → 详情面板自动打开对应内容

4. **协作模式**：
   - 多人协作时，同步画中画的展开/折叠状态
   - 团队成员都能看到相同的 DAG 视图

---

## 测试清单

- [ ] editorial 模式下触发 DAG plan 后自动显示
- [ ] 拖拽工具栏可移动窗口
- [ ] 八个方向的调整把手正常工作
- [ ] 最小化模式下只显示工具栏
- [ ] 吸附到屏幕边缘时显示预览
- [ ] 全屏模式覆盖整个工作区但不遮挡侧边栏
- [ ] 关闭按钮可隐藏画中画
- [ ] 刷新页面后位置和尺寸保持不变
- [ ] 移动端触摸拖拽正常
- [ ] 快捷键（Escape/F/M）响应正确

---

## 相关文件

- `/frontend/src/components/workflow/workflow-pip.tsx` - 画中画容器组件
- `/frontend/src/components/workflow/canvas.tsx` - DAG 画布（被复用）
- `/frontend/src/stores/workspace-layout-store.ts` - 状态管理
- `/frontend/src/pages/writing-workspace.tsx` - 集成入口
- `/frontend/src/stores/workflow-store.ts` - DAG 数据源

---

## 版本历史

- **v1.0** (2026-09-13)：初始实现
  - 四种显示模式（Normal/Minimized/Docked/Fullscreen）
  - 拖拽移动 + 八向调整
  - 自动触发逻辑
  - 状态持久化

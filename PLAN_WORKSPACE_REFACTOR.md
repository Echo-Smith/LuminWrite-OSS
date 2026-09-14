# 工作台重构实施计划

## 一、工作台视图重构（/workspace）

### 1.1 新的信息架构

```
/workspace（写作项目管理器）
├── 顶部导航
│   ├── 项目（全部/进行中/已完成）
│   ├── 草稿箱
│   └── 回收站
├── 项目卡片列表
│   ├── 缩略图/封面
│   ├── 标题 + 描述
│   ├── 进度指示（字数/目标字数）
│   ├── 最后编辑时间
│   └── 快速操作（打开/归档/删除）
└── 右侧详情面板（选中项目时展开）
    ├── 基本信息
    │   ├── 标题、风格、目标字数
    │   └── 创建/修改时间
    ├── 写作进度
    │   ├── 当前字数/章节数
    │   └── 完成度可视化
    ├── 版本历史（简化版）
    │   └── 仅显示人工保存的版本快照
    ├── 挂载的素材
    │   └── 已选择的知识卡片列表
    └── 操作区
        ├── 继续写作（跳转到 /write）
        ├── 导出文档
        └── 项目设置
```

### 1.2 需要移除的功能

从 `editorial-board.tsx` 中移除：
- ❌ 决策台（pendingDecisions、决策包、审批流程）
- ❌ 任务状态机（draft → research → writing → review → published）
- ❌ Agent 信誉、信源可信度、工作台知识沉淀
- ❌ 实验视图（对照实验功能过重）
- ❌ Artifact 审批流程（approve/reject）

### 1.3 需要保留的功能

改造后保留：
- ✅ 任务列表 → 重命名为"项目列表"
- ✅ 看板视图 → 简化为"进行中/已完成/已归档"三栏
- ✅ 任务详情面板 → 改为"项目详情"，去掉审批相关部分
- ✅ 事件日志 → 改为"编辑历史"，记录用户操作而非 Agent 活动

### 1.4 新增功能

- ✅ 项目模板（快速开始：博客文章/产品文案/学术论文）
- ✅ 草稿箱（未正式创建项目的临时写作）
- ✅ 快速搜索与过滤（按标题/标签/时间范围）
- ✅ 批量操作（导出多个项目）

---

## 二、DAG 写作流 GUI 解耦

### 2.1 当前问题

```typescript
// 当前架构（紧耦合）
EditorialBoard (工作台)
  └── 看板视图 → 任务卡片显示 "DAG 执行中"
      └── TaskDetailPanel → 交付物列表显示 DAG 产物
          └── ❌ 没有 DAG 图形界面的入口
```

### 2.2 解耦目标

```typescript
// 新架构（独立组件）
WorkflowCanvas (可嵌入的 DAG 画布)
  ├── 作为独立 React 组件存在
  ├── 可被任何页面引用（/write、/workspace、/admin）
  └── 通过 Props 接收控制参数

// 使用场景1：/write 文档视图中的画中画
<WritingWorkspace>
  <DocumentSurface />
  {showDAG && <WorkflowCanvasPIP />}
</WritingWorkspace>

// 使用场景2：/workspace 项目详情中查看历史 DAG
<ProjectDetailPanel>
  <WorkflowCanvasEmbed runId={project.lastRunId} readonly />
</ProjectDetailPanel>
```

### 2.3 具体改造

#### 文件结构调整
```
src/components/workflow/
├── canvas.tsx              # 保持不变（核心 React Flow 逻辑）
├── workflow-pip.tsx        # 新增：画中画容器组件
├── workflow-embed.tsx      # 新增：可嵌入版本（去掉 WorkflowInput）
├── agent-node.tsx          # 保持不变
├── add-node-toolbar.tsx    # 保持不变
└── node-edit-panel.tsx     # 保持不变
```

#### 新增组件：`workflow-pip.tsx`
```typescript
/**
 * DAG 画中画容器 — 悬浮在文档视图之上
 * 支持拖拽位置、调整大小、最小化/展开/关闭
 */
export function WorkflowCanvasPIP() {
  const [position, setPosition] = useState({ x: 100, y: 100 });
  const [size, setSize] = useState({ width: 800, height: 600 });
  const [minimized, setMinimized] = useState(false);

  return (
    <div
      className="workflow-pip"
      style={{
        position: 'fixed',
        left: position.x,
        top: position.y,
        width: size.width,
        height: size.height,
        zIndex: 1000,
      }}
    >
      {/* 拖拽把手 */}
      <div className="pip-header" onMouseDown={handleDragStart}>
        <span>工作流执行中</span>
        <div className="pip-controls">
          <button onClick={() => setMinimized(true)}>最小化</button>
          <button onClick={onClose}>关闭</button>
        </div>
      </div>

      {/* DAG 画布 */}
      {!minimized && <WorkflowCanvas />}
    </div>
  );
}
```

---

## 三、画中画模式实现

### 3.1 交互设计

#### 触发方式
1. **自动触发**：用户选择 `agentMode="editorial"` 并发送写作请求后，收到 `workflow.plan` 事件时自动弹出
2. **手动触发**：在文档视图右上角添加"查看工作流"按钮

#### 画中画窗口功能
- ✅ 拖拽移动位置（拖动标题栏）
- ✅ 调整窗口大小（拖动右下角）
- ✅ 最小化到右下角（显示简化的进度指示）
- ✅ 展开/关闭
- ✅ 固定到右侧（类似 DevTools 的 dock 模式）

### 3.2 状态管理

在 `workspace-layout-store.ts` 中新增：
```typescript
interface WorkspaceLayoutState {
  // ... 现有状态
  
  // DAG 画中画状态
  dagPipVisible: boolean;
  dagPipPosition: { x: number; y: number };
  dagPipSize: { width: number; height: number };
  dagPipMinimized: boolean;
  dagPipDocked: 'none' | 'right' | 'bottom';
  
  setDagPipVisible: (visible: boolean) => void;
  setDagPipPosition: (pos: { x: number; y: number }) => void;
  setDagPipSize: (size: { width: number; height: number }) => void;
  setDagPipMinimized: (minimized: boolean) => void;
  setDagPipDocked: (docked: 'none' | 'right' | 'bottom') => void;
}
```

### 3.3 WebSocket 事件联动

```typescript
// writing-workspace.tsx 中监听 workflow 事件
const plan = useWorkflowStore((state) => state.plan);
const runStatus = useWorkflowStore((state) => state.runStatus);
const setDagPipVisible = useWorkspaceLayoutStore((state) => state.setDagPipVisible);

useEffect(() => {
  // 收到 DAG plan 后自动显示画中画
  if (plan && runStatus === "created") {
    setDagPipVisible(true);
    toast.success("工作流已生成，查看右侧画中画窗口");
  }
}, [plan, runStatus]);

useEffect(() => {
  // 工作流完成后自动最小化
  if (runStatus === "completed") {
    useWorkspaceLayoutStore.getState().setDagPipMinimized(true);
    toast.success("工作流执行完成");
  }
}, [runStatus]);
```

---

## 四、实施步骤

### Phase 1: DAG 解耦（优先级最高）
**预计时间**：1 天

1. ✅ 创建 `workflow-pip.tsx` 和 `workflow-embed.tsx`
2. ✅ 在 `workspace-layout-store.ts` 中添加画中画状态
3. ✅ 在 `writing-workspace.tsx` 中渲染 `WorkflowCanvasPIP`
4. ✅ 实现基础的拖拽、调整大小、最小化功能
5. ✅ 测试 WebSocket 事件联动

### Phase 2: 画中画交互优化（次优先级）
**预计时间**：0.5 天

1. ✅ 实现 dock 到右侧/底部的吸附效果
2. ✅ 添加键盘快捷键（Cmd+K 打开/关闭）
3. ✅ 持久化画中画位置和尺寸到 localStorage
4. ✅ 优化动画效果（展开/收起/最小化）

### Phase 3: 工作台重构（独立进行）
**预计时间**：2 天

1. ✅ 重构 `editorial-board.tsx` 为 `workspace-manager.tsx`
2. ✅ 移除决策台、审批流程、Agent 洞察相关代码
3. ✅ 改造看板视图为简化的项目列表
4. ✅ 重新设计项目详情面板
5. ✅ 添加项目模板和草稿箱功能
6. ✅ 测试与 `/write` 的跳转联动

### Phase 4: 集成测试与文档
**预计时间**：0.5 天

1. ✅ 端到端测试：从 `/write` 启动 DAG → 画中画显示 → 完成后跳转文档
2. ✅ 测试 `/workspace` 的项目管理流程
3. ✅ 编写用户文档（画中画使用指南）
4. ✅ 补充代码注释

---

## 五、技术细节

### 5.1 画中画拖拽实现

使用 React 的 `onMouseDown` + `onMouseMove` + `onMouseUp` 三件套：

```typescript
const [isDragging, setIsDragging] = useState(false);
const [dragOffset, setDragOffset] = useState({ x: 0, y: 0 });

const handleMouseDown = (e: React.MouseEvent) => {
  setIsDragging(true);
  setDragOffset({
    x: e.clientX - position.x,
    y: e.clientY - position.y,
  });
};

useEffect(() => {
  if (!isDragging) return;
  
  const handleMouseMove = (e: MouseEvent) => {
    setPosition({
      x: e.clientX - dragOffset.x,
      y: e.clientY - dragOffset.y,
    });
  };
  
  const handleMouseUp = () => setIsDragging(false);
  
  window.addEventListener('mousemove', handleMouseMove);
  window.addEventListener('mouseup', handleMouseUp);
  
  return () => {
    window.removeEventListener('mousemove', handleMouseMove);
    window.removeEventListener('mouseup', handleMouseUp);
  };
}, [isDragging, dragOffset]);
```

### 5.2 尺寸调整实现

右下角拖拽把手：

```typescript
const handleResizeMouseDown = (e: React.MouseEvent) => {
  e.stopPropagation();
  setIsResizing(true);
  setResizeStart({
    mouseX: e.clientX,
    mouseY: e.clientY,
    width: size.width,
    height: size.height,
  });
};

// 类似拖拽的 useEffect 监听 mousemove
```

### 5.3 Dock 模式实现

```typescript
// 检测窗口是否靠近边缘
const checkDockZone = (pos: { x: number; y: number }) => {
  const threshold = 50;
  const windowWidth = window.innerWidth;
  const windowHeight = window.innerHeight;
  
  if (windowWidth - pos.x < threshold) {
    return 'right';
  }
  if (windowHeight - pos.y < threshold) {
    return 'bottom';
  }
  return 'none';
};

// 在 handleMouseMove 中调用
const dockZone = checkDockZone(newPosition);
if (dockZone !== 'none') {
  // 显示吸附指示器
  setDockPreview(dockZone);
}
```

---

## 六、CSS 样式规范

### 画中画窗口样式
```css
.workflow-pip {
  position: fixed;
  background: var(--background);
  border: 1px solid var(--border);
  border-radius: 12px;
  box-shadow: 0 24px 48px rgba(0, 0, 0, 0.16);
  overflow: hidden;
  transition: transform 0.2s ease, opacity 0.2s ease;
}

.workflow-pip.minimized {
  width: 280px !important;
  height: 64px !important;
  bottom: 24px;
  right: 24px;
  top: auto;
  left: auto;
}

.workflow-pip.docked-right {
  top: 0;
  right: 0;
  height: 100vh;
  border-radius: 0;
  border-right: none;
}

.pip-header {
  height: 48px;
  padding: 0 16px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  background: var(--surface-02);
  cursor: move;
  user-select: none;
}

.pip-resize-handle {
  position: absolute;
  bottom: 0;
  right: 0;
  width: 16px;
  height: 16px;
  cursor: nwse-resize;
}
```

---

## 七、验收标准

### 7.1 DAG 画中画
- [ ] 用户在 `/write` 选择 editorial 模式后，发送请求能自动弹出画中画
- [ ] 画中画可以拖拽到任意位置
- [ ] 画中画可以调整大小（最小 400x300，最大 80vw x 80vh）
- [ ] 画中画可以最小化到右下角
- [ ] 画中画可以 dock 到右侧（占据右半屏）
- [ ] 画中画可以关闭（不影响 DAG 后台执行）
- [ ] 画中画的状态可以持久化（刷新后恢复位置）
- [ ] 工作流完成后画中画自动最小化并显示完成提示

### 7.2 工作台重构
- [ ] `/workspace` 显示当前用户的所有写作项目
- [ ] 项目卡片显示标题、描述、字数、最后编辑时间
- [ ] 点击项目卡片打开详情面板
- [ ] 详情面板显示项目基本信息、进度、版本历史、素材列表
- [ ] 点击"继续写作"跳转到 `/write` 并加载该项目
- [ ] 支持归档/删除项目
- [ ] 支持从模板创建新项目
- [ ] 不再显示决策台、审批流程、Agent 洞察等内容

### 7.3 集成测试
- [ ] `/write` → 启动 editorial 模式 → 画中画弹出 → 查看 DAG 执行 → 完成后最小化
- [ ] `/workspace` → 查看项目列表 → 打开项目详情 → 跳转到 `/write` 继续写作
- [ ] `/workspace` → 查看历史 DAG 执行记录（readonly 模式）
- [ ] 画中画与文档视图之间切换不影响写作状态

---

## 八、风险与注意事项

### 8.1 性能风险
- **React Flow 渲染开销**：DAG 节点过多时（>50）可能卡顿
  - 解法：实现虚拟滚动或分页加载
- **画中画窗口层级冲突**：z-index 管理需要统一
  - 解法：在全局 CSS 中定义 z-index 层级规范

### 8.2 兼容性风险
- **浏览器 ResizeObserver 兼容性**：Safari 14 以下不支持
  - 解法：引入 polyfill 或降级到固定尺寸
- **拖拽在触摸屏上的支持**：需要额外实现 touch 事件
  - 解法：使用 `react-use-gesture` 库统一处理

### 8.3 用户体验风险
- **画中画遮挡文档内容**：用户可能误关闭导致找不到 DAG
  - 解法：添加"重新打开工作流"按钮，且工作流状态持久化
- **首次使用困惑**：不知道画中画可以拖拽/调整大小
  - 解法：首次弹出时显示新手引导提示

---

## 九、后续优化方向

1. **多 DAG 并行**：支持同时打开多个画中画窗口（对比不同写作模式）
2. **DAG 回放**：支持暂停/继续/重放 DAG 执行过程
3. **节点日志查看**：点击节点显示详细的 token 消耗、耗时、输出内容
4. **协作模式**：多人同时查看同一个 DAG 的实时执行状态
5. **导出 DAG 为图片**：方便分享和汇报

---

## 十、开发环境准备

### 依赖安装
```bash
# 已有依赖（无需新增）
@xyflow/react  # React Flow 核心库
zustand        # 状态管理
lucide-react   # 图标库
```

### 本地开发命令
```bash
cd writing-agent-v2-oss/frontend
npm run dev    # 启动开发服务器
npm run build  # 构建生产版本
npm run test   # 运行单元测试
```

---

结束。准备好开始实施了吗？

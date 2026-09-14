# 工作台重构方案

## 背景

当前的 `/workspace` 路由使用 `EditorialBoard` 组件（2096 行），设计面向"编辑部协作"场景，包含：
- 决策台（待审批、风险预警、预算预警）
- 任务看板（draft → research → writing → review → published）
- Agent 信誉、信源可信度等洞察面板
- 实验管理系统

**问题**：
1. 对个人写作用户过于复杂（任务审批、预算管理等不适用）
2. 角色假设错误（把用户当成"主编"，而非"作者"）
3. 产物概念模糊（artifact、decision、quality gate 等概念过重）

---

## 重构目标

将 `/workspace` 改造为**个人写作项目管理器**：
- 简洁的项目列表（进行中、已完成、已归档）
- 快速创建新项目（从空白或模板）
- 项目详情（写作进度、版本历史、素材库）
- 与 DAG 画中画功能解耦

---

## 新架构设计

### 1. 组件结构

```
/workspace
  ├── WritingProjectsPage (新建，替代 EditorialBoard)
  │   ├── ProjectList (项目列表)
  │   ├── ProjectCard (项目卡片)
  │   ├── ProjectDetailPanel (详情面板)
  │   └── CreateProjectDialog (创建项目对话框)
  └── [保留] editorial-board.tsx (标记为废弃，供迁移参考)
```

### 2. 数据模型

#### 项目模型（简化版 Task）

```typescript
export interface WritingProject {
  id: string;
  title: string;
  description: string;
  status: "drafting" | "writing" | "completed" | "archived";
  
  // 写作进度
  wordCount: number;
  targetWordCount: number;
  lastEditedAt: string;
  
  // 元数据
  createdAt: string;
  updatedAt: string;
  tags: string[];
  
  // 关联资源
  conversationId: string; // 对应的 /write 会话
  materialIds: string[];  // 挂载的知识卡片
  
  // 可选：模板信息
  templateId?: string;
  templateName?: string;
}
```

#### 项目状态

| 状态 | 含义 | 用户操作 |
|------|------|---------|
| `drafting` | 构思中 | 填写大纲、收集素材 |
| `writing` | 写作中 | 正在撰写内容 |
| `completed` | 已完成 | 标记为完成，可继续编辑 |
| `archived` | 已归档 | 隐藏但不删除 |

**移除的状态**：`pending_approval`、`review`、`pending_publish`、`published`

### 3. Store 设计

```typescript
// writing-projects-store.ts
interface WritingProjectsState {
  projects: WritingProject[];
  loading: boolean;
  selectedProjectId: string | null;
  
  // 读取操作
  fetchProjects: () => Promise<void>;
  getProjectById: (id: string) => WritingProject | undefined;
  
  // 写入操作
  createProject: (data: CreateProjectInput) => Promise<WritingProject>;
  updateProject: (id: string, data: Partial<WritingProject>) => Promise<void>;
  deleteProject: (id: string) => Promise<void>;
  archiveProject: (id: string) => Promise<void>;
  
  // UI 状态
  setSelectedProject: (id: string | null) => void;
}
```

### 4. UI 视图

#### 主视图：项目列表（网格布局）

```
┌─────────────────────────────────────────────────────────┐
│ 工作台                           [新建项目] [模板库]   │
├─────────────────────────────────────────────────────────┤
│ 筛选：[全部] [进行中] [已完成] [已归档]                │
├─────────────────────────────────────────────────────────┤
│                                                         │
│  ┌───────────┐  ┌───────────┐  ┌───────────┐          │
│  │ 项目 A    │  │ 项目 B    │  │ 项目 C    │          │
│  │ 写作中    │  │ 构思中    │  │ 已完成    │          │
│  │ 3,200字   │  │ 500字     │  │ 8,000字   │          │
│  │ 2小时前   │  │ 昨天      │  │ 3天前     │          │
│  └───────────┘  └───────────┘  └───────────┘          │
│                                                         │
│  ┌───────────┐                                         │
│  │ + 空白项目│                                         │
│  └───────────┘                                         │
│                                                         │
└─────────────────────────────────────────────────────────┘
```

#### 项目卡片设计

```typescript
<ProjectCard>
  <CardHeader>
    <Title>{project.title}</Title>
    <StatusBadge status={project.status} />
  </CardHeader>
  
  <CardContent>
    <ProgressBar 
      current={project.wordCount} 
      target={project.targetWordCount} 
    />
    <Metadata>
      <Icon.FileText /> {project.wordCount} 字
      <Icon.Clock /> {formatRelativeTime(project.lastEditedAt)}
    </Metadata>
    <Tags>{project.tags.map(tag => <Badge>{tag}</Badge>)}</Tags>
  </CardContent>
  
  <CardFooter>
    <Button onClick={() => navigate(`/write?session=${project.conversationId}`)}>
      继续写作
    </Button>
    <DropdownMenu>
      <MenuItem onClick={archiveProject}>归档</MenuItem>
      <MenuItem onClick={deleteProject}>删除</MenuItem>
    </DropdownMenu>
  </CardFooter>
</ProjectCard>
```

#### 详情面板（右侧抽屉）

```
┌─────────────────────────────────┐
│ [X] 量子计算综述                │
├─────────────────────────────────┤
│ 状态：写作中                    │
│ 创建时间：2026-09-10 14:30     │
│ 最后编辑：2小时前               │
├─────────────────────────────────┤
│ 写作进度                        │
│ ████████░░░░  3,200 / 5,000字  │
├─────────────────────────────────┤
│ 版本历史                        │
│ • v3 - 2小时前（当前版本）     │
│ • v2 - 昨天                     │
│ • v1 - 3天前                    │
├─────────────────────────────────┤
│ 素材库 (3)                      │
│ 📄 量子纠缠原理                 │
│ 📄 Shor算法详解                 │
│ 🔗 Nature论文链接               │
├─────────────────────────────────┤
│ 标签                            │
│ [量子计算] [综述] [技术]       │
└─────────────────────────────────┘
```

### 5. 创建项目流程

#### 方式 1：从空白创建

```typescript
<CreateProjectDialog>
  <Input placeholder="项目标题" />
  <Textarea placeholder="简要描述（可选）" />
  <Input type="number" placeholder="目标字数（可选）" />
  <TagInput placeholder="添加标签" />
  <Button onClick={handleCreate}>创建项目</Button>
</CreateProjectDialog>
```

#### 方式 2：从模板创建

```
模板库：
- 技术博客（3,000字）
- 学术综述（8,000字）
- 产品文档（5,000字）
- 小说章节（5,000字）
- 新闻报道（1,500字）
```

点击模板 → 自动填充标题格式、目标字数、推荐标签 → 用户确认创建

---

## 实施步骤

### 阶段 1：创建新组件（不影响现有功能）

1. ✅ 创建 `writing-projects-store.ts`
2. ✅ 创建 `WritingProjectsPage.tsx`（新的 /workspace 入口）
3. ✅ 创建子组件：
   - `ProjectList.tsx`
   - `ProjectCard.tsx`
   - `ProjectDetailPanel.tsx`
   - `CreateProjectDialog.tsx`

### 阶段 2：后端 API 适配

4. 检查现有 API：
   - `GET /api/editorial/tasks` → 映射到 `fetchProjects`
   - `POST /api/editorial/tasks` → 映射到 `createProject`
   - `PATCH /api/editorial/tasks/:id` → 映射到 `updateProject`
   - `DELETE /api/editorial/tasks/:id` → 映射到 `deleteProject`

5. 简化数据映射：
   ```typescript
   // 后端 EditorialTask → 前端 WritingProject
   function mapTaskToProject(task: EditorialTask): WritingProject {
     return {
       id: task.id,
       title: task.title,
       description: task.description,
       status: simplifyStatus(task.status), // 合并状态
       wordCount: extractWordCount(task), // 从 artifacts 计算
       targetWordCount: task.token_budget / 4, // 粗略估算
       lastEditedAt: task.updated_at,
       createdAt: task.created_at,
       updatedAt: task.updated_at,
       tags: task.tags,
       conversationId: task.conversation_id,
       materialIds: [], // 待实现
     };
   }
   
   function simplifyStatus(status: TaskStatus): WritingProject['status'] {
     const mapping = {
       'draft': 'drafting',
       'pending_approval': 'drafting',
       'research': 'writing',
       'writing': 'writing',
       'review': 'writing',
       'pending_publish': 'completed',
       'published': 'completed',
       'archived': 'archived',
     };
     return mapping[status] || 'drafting';
   }
   ```

### 阶段 3：路由替换

6. 修改 `App.tsx`：
   ```typescript
   // 旧路由（注释但保留）
   // <Route path="/workspace" element={<EditorialBoard />} />
   
   // 新路由
   <Route path="/workspace" element={<WritingProjectsPage />} />
   ```

7. 添加废弃标记到 `editorial-board.tsx`：
   ```typescript
   /**
    * @deprecated 该组件已被 WritingProjectsPage 替代
    * 保留用于数据迁移和功能对比参考
    */
   export function EditorialBoard() { /* ... */ }
   ```

### 阶段 4：清理（可选）

8. 评估 `editorial-store.ts` 的保留必要性：
   - 如果后端仍使用 `/api/editorial/*` 路径 → 保留 store，只简化映射
   - 如果计划迁移到新 API → 逐步废弃

9. 移除未使用的组件：
   - 决策台相关组件
   - 洞察面板组件
   - 实验管理组件

---

## 与 DAG 画中画的关系

### 完全解耦
- **工作台（/workspace）**：项目列表 + 元数据管理
- **写作视图（/write）**：文档编辑 + Codex 对话 + DAG 画中画

### 交互流程

```
用户在工作台创建项目
  → 跳转到 /write?session={conversationId}
  → 用户选择 editorial 模式
  → Planner 生成 DAG
  → 画中画自动弹出（在写作视图内）
  → 用户可随时切换文档/DAG 视图
  → 完成后返回工作台，项目状态更新为"已完成"
```

---

## 迁移兼容性

### 数据库兼容
- 后端表结构保持不变（`editorial_tasks` 表）
- 只在前端层做数据映射和状态简化
- 旧数据自动兼容，按新规则显示

### 用户迁移
- 现有任务自动显示为"项目"
- 复杂状态（pending_approval、review）合并为"写作中"
- 决策记录、审批历史在后端保留，前端不再展示

---

## 预期收益

1. **降低认知负担**：从 7 种状态简化为 4 种
2. **提高操作效率**：从"创建任务 → 分配 Agent → 审批"简化为"创建项目 → 开始写作"
3. **代码可维护性**：从 2096 行复杂组件简化为 <500 行轻量组件
4. **更好的扩展性**：为未来的模板系统、协作功能预留接口

---

## 风险与应对

### 风险 1：后端 API 依赖
- **问题**：新前端可能无法完全适配现有后端逻辑
- **应对**：保持数据结构兼容，只做映射层转换

### 风险 2：用户习惯
- **问题**：已习惯旧工作台的用户可能不适应
- **应对**：保留旧组件作为"高级模式"入口（通过 URL 参数切换）

### 风险 3：功能缺失
- **问题**：决策台、洞察面板等功能可能有用户依赖
- **应对**：评估使用数据，确认后再完全移除

---

## 下一步行动

1. 创建 `writing-projects-store.ts`
2. 创建 `WritingProjectsPage.tsx` 骨架
3. 实现项目列表和卡片组件
4. 测试与现有后端的兼容性
5. 逐步替换路由

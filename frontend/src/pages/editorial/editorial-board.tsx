/**
 * 编辑部主编决策台 — 协作工作台界面
 *
 * 顶部：主编决策台（待我决定、风险预警、预算预警、可自动推进）
 * 下方：看板视图（任务列表按状态分列）
 * 右侧：任务详情面板（交付物内容 + 审批操作 + 决策记录 + Agent 活动）
 */
import { useEffect, useState, useRef } from "react";
import {
  useEditorialStore,
  type EditorialTask,
  type TaskStatus,
  type Artifact,
  type Decision,
  type DecisionWithTask,
} from "@/stores/editorial-store";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Badge } from "@/components/ui/badge";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import {
  FileText,
  Plus,
  ChevronRight,
  ChevronDown,
  Clock,
  Coins,
  Loader2,
  CheckCircle2,
  XCircle,
  AlertTriangle,
  User,
  Bot,
  Activity,
  Gavel,
  TrendingUp,
  Zap,
  Shield,
} from "lucide-react";
import { cn } from "@/lib/utils";

const STATUS_COLUMNS: { status: TaskStatus; label: string; color: string }[] = [
  { status: "draft", label: "草稿", color: "bg-slate-400" },
  { status: "pending_approval", label: "待审批", color: "bg-amber-500" },
  { status: "research", label: "研究中", color: "bg-blue-500" },
  { status: "writing", label: "写作中", color: "bg-indigo-500" },
  { status: "review", label: "审校中", color: "bg-purple-500" },
  { status: "pending_publish", label: "待发布", color: "bg-cyan-500" },
  { status: "published", label: "已发布", color: "bg-green-500" },
];

const STATUS_LABELS: Record<string, string> = {
  draft: "草稿",
  pending_approval: "待审批",
  research: "研究中",
  writing: "写作中",
  review: "审校中",
  pending_publish: "待发布",
  published: "已发布",
  archived: "已归档",
};

const ASSIGNEE_LABELS: Record<string, string> = {
  human: "人类编辑",
  research_agent: "研究 Agent",
  writing_agent: "写作 Agent",
  review_agent: "审校 Agent",
};

export function EditorialBoard() {
  const { tasks, loading, fetchTasks, createTask, advanceTask, events, pendingDecisions, fetchPendingDecisions } = useEditorialStore();
  const [selectedTaskId, setSelectedTaskId] = useState<string | null>(null);
  const [showCreate, setShowCreate] = useState(false);
  const [view, setView] = useState<"dashboard" | "kanban">("dashboard");

  useEffect(() => {
    fetchTasks();
    fetchPendingDecisions();
  }, [fetchTasks, fetchPendingDecisions]);

  // 定时刷新
  const refreshTimer = useRef<ReturnType<typeof setInterval> | undefined>(undefined);
  useEffect(() => {
    refreshTimer.current = setInterval(() => {
      fetchTasks();
      fetchPendingDecisions();
    }, 30_000);
    return () => {
      if (refreshTimer.current) clearInterval(refreshTimer.current);
    };
  }, [fetchTasks, fetchPendingDecisions]);

  // 最近事件 toast
  const [recentEvent, setRecentEvent] = useState<string | null>(null);
  useEffect(() => {
    if (events.length === 0) return;
    const last = events[events.length - 1];
    const msg = formatEventMessage(last.type, last.payload);
    if (msg) {
      setRecentEvent(msg);
      const timer = setTimeout(() => setRecentEvent(null), 5000);
      return () => clearTimeout(timer);
    }
  }, [events.length]);

  // 统计指标
  const pendingCount = pendingDecisions.length;
  const highRiskCount = tasks.filter(
    (t) => t.status === "pending_approval" || t.status === "review"
  ).length;
  const budgetWarningCount = tasks.filter((t) => {
    if (t.token_budget <= 0) return false;
    return t.token_used / t.token_budget > 0.8;
  }).length;
  const autoProgressCount = tasks.filter(
    (t) => t.status === "research" || t.status === "writing" || t.status === "review"
  ).length;

  return (
    <div className="flex h-[calc(100vh-3.5rem)]">
      <div className="flex-1 overflow-x-auto">
        {/* 顶部栏 */}
        <div className="flex items-center justify-between border-b px-6 py-3">
          <div className="flex items-center gap-3">
            <FileText className="h-5 w-5 text-primary" />
            <h1 className="text-lg font-semibold">编辑部</h1>
            <div className="flex items-center gap-1 ml-2">
              <button
                onClick={() => setView("dashboard")}
                className={cn(
                  "px-3 py-1 text-sm rounded-md transition-colors",
                  view === "dashboard" ? "bg-primary/15 text-primary font-medium" : "text-muted-foreground hover:text-foreground"
                )}
              >
                决策台
              </button>
              <button
                onClick={() => setView("kanban")}
                className={cn(
                  "px-3 py-1 text-sm rounded-md transition-colors",
                  view === "kanban" ? "bg-primary/15 text-primary font-medium" : "text-muted-foreground hover:text-foreground"
                )}
              >
                看板
              </button>
            </div>
          </div>
          <Dialog open={showCreate} onOpenChange={setShowCreate}>
            <DialogTrigger asChild>
              <Button size="sm">
                <Plus className="h-4 w-4 mr-1" />
                新建选题
              </Button>
            </DialogTrigger>
            <CreateTaskDialog
              onCreate={async (input) => {
                const task = await createTask(input);
                if (task) {
                  setShowCreate(false);
                  fetchTasks();
                }
              }}
            />
          </Dialog>
        </div>

        {/* 事件提示条 */}
        {recentEvent && (
          <div className="px-6 py-2 bg-blue-50 dark:bg-blue-950/30 border-b text-sm text-blue-700 dark:text-blue-300 flex items-center gap-2">
            <Activity className="h-3.5 w-3.5 animate-pulse" />
            {recentEvent}
          </div>
        )}

        {/* 决策台视图 */}
        {view === "dashboard" && (
          <div className="p-6 space-y-4 overflow-y-auto">
            {/* 四个统计卡片 */}
            <div className="grid grid-cols-4 gap-4">
              <DashboardCard
                icon={<Gavel className="h-4 w-4" />}
                label="待我决定"
                count={pendingCount}
                color="amber"
                onClick={() => pendingCount > 0 && setView("kanban")}
              />
              <DashboardCard
                icon={<Shield className="h-4 w-4" />}
                label="高风险任务"
                count={highRiskCount}
                color="red"
                onClick={() => setView("kanban")}
              />
              <DashboardCard
                icon={<Coins className="h-4 w-4" />}
                label="预算预警"
                count={budgetWarningCount}
                color="orange"
                onClick={() => setView("kanban")}
              />
              <DashboardCard
                icon={<Zap className="h-4 w-4" />}
                label="自动推进中"
                count={autoProgressCount}
                color="blue"
              />
            </div>

            {/* 待决策列表 */}
            <div>
              <h2 className="text-sm font-semibold text-muted-foreground uppercase tracking-wide mb-3 flex items-center gap-2">
                <Gavel className="h-3.5 w-3.5" />
                需要我决定的事项
              </h2>
              {pendingCount === 0 ? (
                <div className="rounded-lg border border-dashed py-8 text-center text-sm text-muted-foreground">
                  <CheckCircle2 className="h-5 w-5 mx-auto mb-2 text-green-500" />
                  暂无待决策事项，一切顺利
                </div>
              ) : (
                <div className="space-y-2">
                  {pendingDecisions.map((dwt) => (
                    <PendingDecisionCard
                      key={dwt.decision.id}
                      dwt={dwt}
                      onTaskClick={() => setSelectedTaskId(dwt.decision.task_id)}
                    />
                  ))}
                </div>
              )}
            </div>

            {/* 最近活动 */}
            {events.length > 0 && (
              <div>
                <h2 className="text-sm font-semibold text-muted-foreground uppercase tracking-wide mb-3 flex items-center gap-2">
                  <Activity className="h-3.5 w-3.5" />
                  最近活动
                </h2>
                <div className="space-y-1">
                  {events.slice(-15).reverse().map((evt, i) => (
                    <div key={i} className="flex items-center gap-2 text-xs text-muted-foreground rounded px-2 py-1 hover:bg-muted/50">
                      {getEventIcon(evt.type)}
                      <span>{formatEventMessage(evt.type, evt.payload)}</span>
                      <span className="ml-auto text-[10px] tabular-nums">
                        {new Date(evt.timestamp).toLocaleTimeString("zh-CN")}
                      </span>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </div>
        )}

        {/* 看板视图 */}
        {view === "kanban" && (
          <div className="flex gap-4 p-4 min-w-max">
            {STATUS_COLUMNS.map((col) => {
              const colTasks = tasks.filter((t) => t.status === col.status);
              return (
                <div key={col.status} className="w-72 shrink-0 rounded-lg bg-muted/30">
                  <div className="flex items-center gap-2 px-3 py-2 border-b">
                    <div className={cn("h-2 w-2 rounded-full", col.color)} />
                    <span className="text-sm font-medium">{col.label}</span>
                    <span className="text-xs text-muted-foreground ml-auto">{colTasks.length}</span>
                  </div>
                  <div className="p-2 space-y-2 max-h-[calc(100vh-12rem)] overflow-y-auto">
                    {loading && colTasks.length === 0 ? (
                      <div className="flex justify-center py-8">
                        <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
                      </div>
                    ) : (
                      colTasks.map((task) => (
                        <TaskCard
                          key={task.id}
                          task={task}
                          selected={selectedTaskId === task.id}
                          onClick={() => setSelectedTaskId(task.id)}
                          onAdvance={(target) => advanceTask(task.id, target)}
                        />
                      ))
                    )}
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </div>

      {/* 右侧：任务详情 */}
      {selectedTaskId && (
        <div className="w-[28rem] border-l overflow-y-auto">
          <TaskDetailPanel taskId={selectedTaskId} onClose={() => setSelectedTaskId(null)} />
        </div>
      )}
    </div>
  );
}

// ─── 主编决策台卡片 ───────────────────────────────────────

function DashboardCard({
  icon,
  label,
  count,
  color,
  onClick,
}: {
  icon: React.ReactNode;
  label: string;
  count: number;
  color: "amber" | "red" | "orange" | "blue";
  onClick?: () => void;
}) {
  const colorMap = {
    amber: "bg-amber-50 dark:bg-amber-950/30 border-amber-200 dark:border-amber-800 text-amber-700 dark:text-amber-300",
    red: "bg-red-50 dark:bg-red-950/30 border-red-200 dark:border-red-800 text-red-700 dark:text-red-300",
    orange: "bg-orange-50 dark:bg-orange-950/30 border-orange-200 dark:border-orange-800 text-orange-700 dark:text-orange-300",
    blue: "bg-blue-50 dark:bg-blue-950/30 border-blue-200 dark:border-blue-800 text-blue-700 dark:text-blue-300",
  };
  return (
    <div
      onClick={onClick}
      className={cn(
        "rounded-lg border p-4 transition-all",
        colorMap[color],
        onClick && "cursor-pointer hover:shadow-md"
      )}
    >
      <div className="flex items-center gap-2 mb-1">
        {icon}
        <span className="text-xs font-medium uppercase tracking-wide">{label}</span>
      </div>
      <div className="text-2xl font-bold tabular-nums">{count}</div>
    </div>
  );
}

// ─── 待决策卡片 ───────────────────────────────────────────

function PendingDecisionCard({
  dwt,
  onTaskClick,
}: {
  dwt: DecisionWithTask;
  onTaskClick: () => void;
}) {
  const { resolveDecision } = useEditorialStore();
  const [showRationale, setShowRationale] = useState(false);
  const [rationale, setRationale] = useState("");
  const [resolving, setResolving] = useState(false);

  const handleResolve = async (status: "approved" | "rejected") => {
    setResolving(true);
    await resolveDecision(dwt.decision.id, status, rationale || (status === "approved" ? "批准" : "驳回"));
    setResolving(false);
    setShowRationale(false);
    setRationale("");
  };

  const tokenPct = dwt.task_token_budget > 0
    ? Math.min(100, (dwt.task_token_used / dwt.task_token_budget) * 100)
    : 0;

  return (
    <div className="rounded-lg border bg-card p-3 space-y-2">
      {/* 任务标题 */}
      <div className="flex items-start justify-between gap-2">
        <button
          onClick={onTaskClick}
          className="text-sm font-medium hover:underline text-left flex-1"
        >
          {dwt.task_title}
        </button>
        <Badge variant="outline" className="text-xs shrink-0">
          {DECISION_LABELS[dwt.decision.type] || dwt.decision.type}
        </Badge>
      </div>

      {/* 决策理由 */}
      {dwt.decision.rationale && (
        <p className="text-xs text-muted-foreground">{dwt.decision.rationale}</p>
      )}

      {/* 元数据 */}
      <div className="flex items-center gap-3 text-xs text-muted-foreground">
        <Badge variant="secondary" className="text-xs">
          {STATUS_LABELS[dwt.task_status] || dwt.task_status}
        </Badge>
        <span>{ASSIGNEE_LABELS[dwt.task_assignee] || dwt.task_assignee}</span>
        {dwt.task_priority > 0 && (
          <span className="flex items-center gap-0.5">
            <TrendingUp className="h-3 w-3" />
            P{dwt.task_priority}
          </span>
        )}
        {tokenPct > 80 && (
          <span className="flex items-center gap-0.5 text-red-500">
            <Coins className="h-3 w-3" />
            预算 {tokenPct.toFixed(0)}%
          </span>
        )}
      </div>

      {/* 决策操作 */}
      {!showRationale ? (
        <div className="flex gap-2">
          <Button
            size="sm"
            variant="default"
            className="h-7 text-xs flex-1"
            onClick={() => setShowRationale(true)}
          >
            <CheckCircle2 className="h-3 w-3 mr-1" />
            处理
          </Button>
        </div>
      ) : (
        <div className="space-y-2">
          <Textarea
            value={rationale}
            onChange={(e) => setRationale(e.target.value)}
            placeholder="决策理由（可选）..."
            rows={2}
            className="text-xs"
          />
          <div className="flex gap-2">
            <Button
              size="sm"
              variant="default"
              className="h-7 text-xs flex-1"
              onClick={() => handleResolve("approved")}
              disabled={resolving}
            >
              {resolving ? <Loader2 className="h-3 w-3 animate-spin" /> : <CheckCircle2 className="h-3 w-3 mr-1" />}
              批准
            </Button>
            <Button
              size="sm"
              variant="destructive"
              className="h-7 text-xs flex-1"
              onClick={() => handleResolve("rejected")}
              disabled={resolving}
            >
              <XCircle className="h-3 w-3 mr-1" />
              驳回
            </Button>
            <Button
              size="sm"
              variant="ghost"
              className="h-7 text-xs"
              onClick={() => setShowRationale(false)}
            >
              取消
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}

// ─── 任务卡片 ─────────────────────────────────────────────

function TaskCard({
  task,
  selected,
  onClick,
  onAdvance,
}: {
  task: EditorialTask;
  selected: boolean;
  onClick: () => void;
  onAdvance: (target: TaskStatus) => void;
}) {
  const tokenPct = task.token_budget > 0
    ? Math.min(100, (task.token_used / task.token_budget) * 100)
    : 0;

  const isAgentWorking = ["research", "writing", "review"].includes(task.status);
  const isBudgetWarning = tokenPct > 80;

  return (
    <div
      onClick={onClick}
      className={cn(
        "rounded-lg border bg-card p-3 cursor-pointer transition-all hover:shadow-md",
        selected && "ring-2 ring-primary",
        isAgentWorking && "border-blue-300 dark:border-blue-700",
        isBudgetWarning && "border-orange-300 dark:border-orange-700"
      )}
    >
      <div className="flex items-start justify-between gap-2">
        <h3 className="text-sm font-medium line-clamp-2">{task.title}</h3>
        <ChevronRight className="h-4 w-4 shrink-0 text-muted-foreground" />
      </div>

      {task.description && (
        <p className="mt-1 text-xs text-muted-foreground line-clamp-2">{task.description}</p>
      )}

      <div className="mt-2 flex items-center gap-3 text-xs text-muted-foreground">
        <span className="flex items-center gap-1">
          {isAgentWorking ? <Bot className="h-3 w-3 text-blue-500" /> : <User className="h-3 w-3" />}
          {ASSIGNEE_LABELS[task.assignee_type] || task.assignee_type}
        </span>
        {task.deadline && (
          <span className="flex items-center gap-1">
            <Clock className="h-3 w-3" />
            {new Date(task.deadline).toLocaleDateString("zh-CN", { month: "short", day: "numeric" })}
          </span>
        )}
        {isAgentWorking && (
          <span className="flex items-center gap-1 text-blue-500">
            <Loader2 className="h-3 w-3 animate-spin" />
            执行中
          </span>
        )}
        {isBudgetWarning && (
          <span className="flex items-center gap-1 text-orange-500">
            <AlertTriangle className="h-3 w-3" />
            预算
          </span>
        )}
      </div>

      {/* Token 进度 */}
      {task.token_budget > 0 && (
        <div className="mt-2 flex items-center gap-1.5">
          <Coins className="h-3 w-3 text-muted-foreground" />
          <div className="flex-1 h-1.5 rounded-full bg-muted overflow-hidden">
            <div
              className={cn(
                "h-full rounded-full transition-all",
                tokenPct > 80 ? "bg-red-500" : tokenPct > 50 ? "bg-amber-500" : "bg-green-500"
              )}
              style={{ width: `${tokenPct}%` }}
            />
          </div>
          <span className="text-xs text-muted-foreground tabular-nums">
            {(task.token_used / 1000).toFixed(0)}k
          </span>
        </div>
      )}

      {/* 标签 */}
      {task.tags && task.tags.length > 0 && (
        <div className="mt-2 flex flex-wrap gap-1">
          {task.tags.slice(0, 3).map((tag) => (
            <Badge key={tag} variant="outline" className="text-xs py-0 px-1.5">{tag}</Badge>
          ))}
        </div>
      )}

      <AdvanceButton task={task} onAdvance={onAdvance} />
    </div>
  );
}

// ─── 推进按钮 ─────────────────────────────────────────────

function AdvanceButton({ task, onAdvance }: { task: EditorialTask; onAdvance: (target: TaskStatus) => void }) {
  const nextStatuses: Record<string, TaskStatus | null> = {
    draft: "pending_approval",
    pending_approval: "research",
    research: null,
    writing: null,
    review: null,
    pending_publish: "published",
    published: null,
  };

  const next = nextStatuses[task.status];
  if (!next) return null;

  const labels: Record<string, string> = {
    pending_approval: "提交审批",
    research: "批准立项",
    published: "发布",
  };

  return (
    <button
      onClick={(e) => { e.stopPropagation(); onAdvance(next); }}
      className="mt-2 w-full rounded-md bg-primary/10 hover:bg-primary/20 text-primary text-xs font-medium py-1.5 transition-colors"
    >
      {labels[next] || "推进"}
    </button>
  );
}

// ─── 任务详情面板 ─────────────────────────────────────────

function TaskDetailPanel({ taskId, onClose }: { taskId: string; onClose: () => void }) {
  const {
    currentTask,
    artifacts,
    decisions,
    events,
    fetchTask,
    fetchArtifacts,
    fetchDecisions,
    expandedArtifactIds,
    toggleArtifactExpand,
    reviewArtifact,
  } = useEditorialStore();

  useEffect(() => {
    fetchTask(taskId);
    fetchArtifacts(taskId);
    fetchDecisions(taskId);
  }, [taskId, fetchTask, fetchArtifacts, fetchDecisions]);

  if (!currentTask) {
    return (
      <div className="flex justify-center py-12">
        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
    );
  }

  const taskEvents = events.filter((e) => e.task_id === taskId);

  return (
    <div className="p-4 space-y-4">
      {/* 关闭按钮 + 任务信息 */}
      <div>
        <div className="flex items-start justify-between mb-2">
          <h2 className="text-base font-semibold">{currentTask.title}</h2>
          <button onClick={onClose} className="text-muted-foreground hover:text-foreground text-sm">✕</button>
        </div>
        {currentTask.description && (
          <p className="mt-1 text-sm text-muted-foreground">{currentTask.description}</p>
        )}
        <div className="mt-2 flex items-center gap-2">
          <Badge variant="secondary">{STATUS_LABELS[currentTask.status]}</Badge>
          <Badge variant="outline">{ASSIGNEE_LABELS[currentTask.assignee_type] || currentTask.assignee_type}</Badge>
          {currentTask.style_slug && (
            <Badge variant="outline" className="text-xs">{currentTask.style_slug}</Badge>
          )}
        </div>
      </div>

      {/* 验收标准 */}
      {currentTask.accept_criteria && (
        <div className="rounded-lg bg-muted/50 p-3">
          <h3 className="text-xs font-medium text-muted-foreground uppercase tracking-wide mb-1">验收标准</h3>
          <p className="text-sm">{currentTask.accept_criteria}</p>
        </div>
      )}

      {/* 交付物时间线 */}
      <div>
        <h3 className="text-xs font-medium text-muted-foreground uppercase tracking-wide mb-2">
          交付物 ({artifacts.length})
        </h3>
        <div className="space-y-2">
          {artifacts.map((art) => (
            <ArtifactCard
              key={art.id}
              artifact={art}
              expanded={expandedArtifactIds.has(art.id)}
              onToggle={() => toggleArtifactExpand(art.id)}
              onApprove={() => reviewArtifact(art.id, "approved", "人类编辑批准")}
              onReject={(note) => reviewArtifact(art.id, "rejected", note)}
            />
          ))}
          {artifacts.length === 0 && <p className="text-xs text-muted-foreground">暂无交付物</p>}
        </div>
      </div>

      {/* 决策记录 */}
      <div>
        <h3 className="text-xs font-medium text-muted-foreground uppercase tracking-wide mb-2">
          决策记录 ({decisions.length})
        </h3>
        <div className="space-y-2">
          {decisions.map((d) => (
            <DecisionCard key={d.id} decision={d} />
          ))}
          {decisions.length === 0 && <p className="text-xs text-muted-foreground">暂无决策记录</p>}
        </div>
      </div>

      {/* Agent 活动日志 */}
      {taskEvents.length > 0 && (
        <div>
          <h3 className="text-xs font-medium text-muted-foreground uppercase tracking-wide mb-2">
            Agent 活动 ({taskEvents.length})
          </h3>
          <div className="space-y-1">
            {taskEvents.slice(-10).reverse().map((evt, i) => (
              <div key={i} className="flex items-center gap-2 text-xs text-muted-foreground">
                {getEventIcon(evt.type)}
                <span>{formatEventMessage(evt.type, evt.payload)}</span>
                <span className="ml-auto text-[10px]">
                  {new Date(evt.timestamp).toLocaleTimeString("zh-CN")}
                </span>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

// ─── 交付物卡片（可展开 + 审批操作）─────────────────────

function ArtifactCard({
  artifact,
  expanded,
  onToggle,
  onApprove,
  onReject,
}: {
  artifact: Artifact;
  expanded: boolean;
  onToggle: () => void;
  onApprove: () => void;
  onReject: (note: string) => void;
}) {
  const [showReject, setShowReject] = useState(false);
  const [rejectNote, setRejectNote] = useState("");

  const canReview = artifact.status === "submitted" || artifact.status === "approved";

  return (
    <div className="rounded-lg border overflow-hidden">
      {/* 头部 */}
      <div
        onClick={onToggle}
        className="flex items-center justify-between p-2 cursor-pointer hover:bg-muted/50 transition-colors"
      >
        <div className="flex items-center gap-2 flex-1 min-w-0">
          {expanded ? <ChevronDown className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
           : <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />}
          <span className="text-sm font-medium truncate">
            {ARTIFACT_LABELS[artifact.type] || artifact.type}
          </span>
          <Badge variant="outline" className="text-xs shrink-0">v{artifact.version}</Badge>
        </div>
        <Badge
          variant={
            artifact.status === "approved" ? "default" :
            artifact.status === "rejected" ? "destructive" :
            artifact.status === "superseded" ? "secondary" : "outline"
          }
          className="text-xs shrink-0"
        >
          {ARTIFACT_STATUS_LABELS[artifact.status] || artifact.status}
        </Badge>
      </div>

      {/* 元数据行 */}
      <div className="px-2 pb-2 flex items-center gap-2 text-xs text-muted-foreground">
        <span className="flex items-center gap-0.5">
          {artifact.produced_by === "human" ? <User className="h-3 w-3" /> : <Bot className="h-3 w-3" />}
          {artifact.produced_by}
        </span>
        {artifact.token_cost > 0 && (
          <>
            <span>·</span>
            <span className="flex items-center gap-0.5"><Coins className="h-3 w-3" />{artifact.token_cost}</span>
          </>
        )}
        {artifact.reviewed_by && (
          <>
            <span>·</span>
            <span>审阅: {artifact.reviewed_by}</span>
          </>
        )}
      </div>

      {/* 展开内容 */}
      {expanded && (
        <div className="border-t bg-muted/20 p-3">
          {artifact.content ? (
            <ArtifactContent content={artifact.content} type={artifact.type} />
          ) : (
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <Loader2 className="h-3 w-3 animate-spin" />
              加载内容中...
            </div>
          )}
          {artifact.review_note && (
            <div className="mt-2 rounded bg-amber-50 dark:bg-amber-950/30 p-2 text-xs">
              <span className="font-medium">审阅意见: </span>
              {artifact.review_note}
            </div>
          )}

          {/* 审批操作按钮 */}
          {canReview && artifact.produced_by !== "human" && (
            <div className="mt-3 border-t pt-2">
              {!showReject ? (
                <div className="flex gap-2">
                  <Button size="sm" variant="default" className="h-7 text-xs flex-1" onClick={onApprove}>
                    <CheckCircle2 className="h-3 w-3 mr-1" />
                    批准
                  </Button>
                  <Button size="sm" variant="destructive" className="h-7 text-xs flex-1" onClick={() => setShowReject(true)}>
                    <XCircle className="h-3 w-3 mr-1" />
                    驳回
                  </Button>
                </div>
              ) : (
                <div className="space-y-2">
                  <Textarea
                    value={rejectNote}
                    onChange={(e) => setRejectNote(e.target.value)}
                    placeholder="驳回理由..."
                    rows={2}
                    className="text-xs"
                  />
                  <div className="flex gap-2">
                    <Button
                      size="sm"
                      variant="destructive"
                      className="h-7 text-xs flex-1"
                      onClick={() => {
                        onReject(rejectNote || "驳回");
                        setShowReject(false);
                        setRejectNote("");
                      }}
                    >
                      确认驳回
                    </Button>
                    <Button size="sm" variant="ghost" className="h-7 text-xs" onClick={() => setShowReject(false)}>
                      取消
                    </Button>
                  </div>
                </div>
              )}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

// ─── 交付物内容渲染 ───────────────────────────────────────

function ArtifactContent({ content, type }: { content: string; type: string }) {
  let parsed: unknown = null;
  try {
    parsed = JSON.parse(content);
  } catch {
    // Not JSON
  }

  if (parsed && typeof parsed === "object") {
    if (type === "review_report" && parsed && typeof parsed === "object") {
      const report = parsed as { passed?: boolean; severity?: string; issues?: Array<{ problem?: string; evidence?: string; suggestion?: string }> };
      return (
        <div className="space-y-2 text-sm">
          <div className="flex items-center gap-2">
            {report.passed ? (
              <Badge className="bg-green-500"><CheckCircle2 className="h-3 w-3 mr-1" />通过</Badge>
            ) : (
              <Badge variant="destructive"><XCircle className="h-3 w-3 mr-1" />未通过</Badge>
            )}
            {report.severity && (
              <Badge variant={report.severity === "high" ? "destructive" : "secondary"}>
                严重度: {report.severity}
              </Badge>
            )}
          </div>
          {report.issues && report.issues.length > 0 && (
            <div className="space-y-2">
              {report.issues.map((issue, i) => (
                <div key={i} className="rounded border p-2 text-xs">
                  {issue.problem && <p className="font-medium">问题: {issue.problem}</p>}
                  {issue.evidence && <p className="text-muted-foreground mt-1">证据: {issue.evidence}</p>}
                  {issue.suggestion && <p className="text-blue-600 dark:text-blue-400 mt-1">建议: {issue.suggestion}</p>}
                </div>
              ))}
            </div>
          )}
        </div>
      );
    }

    if (type === "source_pack" && parsed && typeof parsed === "object") {
      const data = parsed as { sources?: Array<{ url?: string; title?: string; source?: string; relevance?: string; score?: number }>; count?: number };
      return (
        <div className="space-y-1">
          {data.sources?.map((src, i) => (
            <div key={i} className="rounded border p-2 text-xs">
              {src.title && <p className="font-medium">{src.title}</p>}
              {src.url && <a href={src.url} target="_blank" rel="noopener noreferrer" className="text-blue-500 hover:underline truncate block">{src.url}</a>}
              <div className="flex items-center gap-2 mt-1">
                {src.source && <Badge variant="outline" className="text-xs">{src.source}</Badge>}
                {src.relevance && <Badge variant={src.relevance === "strong" ? "default" : "secondary"} className="text-xs">{src.relevance}</Badge>}
              </div>
            </div>
          ))}
        </div>
      );
    }

    if (type === "fact_claims" && parsed && typeof parsed === "object") {
      const data = parsed as { claims?: Array<{ claim?: string; source_url?: string; source?: string; verified?: boolean; relevance?: string }>; count?: number };
      return (
        <div className="space-y-1">
          {data.claims?.map((claim, i) => (
            <div key={i} className="rounded border p-2 text-xs">
              <div className="flex items-center gap-2">
                {claim.verified ? <CheckCircle2 className="h-3 w-3 text-green-500 shrink-0" />
                 : <AlertTriangle className="h-3 w-3 text-amber-500 shrink-0" />}
                <span className="font-medium">{claim.claim}</span>
              </div>
              {claim.source && <p className="text-muted-foreground mt-1 ml-5">来源: {claim.source}</p>}
            </div>
          ))}
        </div>
      );
    }

    return (
      <pre className="text-xs overflow-x-auto whitespace-pre-wrap break-all max-h-96">
        {JSON.stringify(parsed, null, 2)}
      </pre>
    );
  }

  return (
    <div className="text-sm whitespace-pre-wrap break-words max-h-96 overflow-y-auto">{content}</div>
  );
}

// ─── 决策卡片 ─────────────────────────────────────────────

function DecisionCard({ decision }: { decision: Decision }) {
  const isApproved = decision.status === "approved";
  const isRejected = decision.status === "rejected";
  const isEscalated = decision.status === "escalated";
  const isPending = decision.status === "pending";

  return (
    <div className="rounded-lg border p-2 text-sm">
      <div className="flex items-center justify-between">
        <span className="font-medium">{DECISION_LABELS[decision.type] || decision.type}</span>
        <Badge
          variant={isApproved ? "default" : isRejected || isEscalated ? "destructive" : "outline"}
          className="text-xs"
        >
          {isApproved && <CheckCircle2 className="h-3 w-3 mr-1" />}
          {isRejected && <XCircle className="h-3 w-3 mr-1" />}
          {isEscalated && <AlertTriangle className="h-3 w-3 mr-1" />}
          {isPending && <Clock className="h-3 w-3 mr-1" />}
          {decision.status}
        </Badge>
      </div>
      {decision.rationale && <p className="mt-1 text-xs text-muted-foreground">{decision.rationale}</p>}
      <div className="mt-1 flex items-center gap-2 text-xs text-muted-foreground">
        <span className="flex items-center gap-0.5">
          {decision.decided_by_type === "human" ? <User className="h-3 w-3" /> : <Bot className="h-3 w-3" />}
          {decision.decided_by_type}
        </span>
        <span>·</span>
        <span>{new Date(decision.created_at).toLocaleString("zh-CN")}</span>
      </div>
      {decision.evidence && (
        <div className="mt-1 rounded bg-muted/50 p-1.5 text-xs">
          <span className="font-medium">证据: </span>{decision.evidence}
        </div>
      )}
    </div>
  );
}

// ─── 创建任务对话框 ───────────────────────────────────────

function CreateTaskDialog({
  onCreate,
}: {
  onCreate: (input: {
    title: string;
    description: string;
    accept_criteria?: string;
    priority?: number;
    tags?: string[];
    style_slug?: string;
  }) => Promise<void>;
}) {
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [criteria, setCriteria] = useState("");
  const [style, setStyle] = useState("yinyue");
  const [loading, setLoading] = useState(false);

  return (
    <DialogContent className="sm:max-w-[480px]">
      <DialogHeader>
        <DialogTitle>新建选题</DialogTitle>
      </DialogHeader>
      <div className="space-y-4 py-2">
        <div className="space-y-2">
          <Label>选题标题 *</Label>
          <Input value={title} onChange={(e) => setTitle(e.target.value)} placeholder="如：AI 对音乐产业的影响" />
        </div>
        <div className="space-y-2">
          <Label>选题描述</Label>
          <Textarea value={description} onChange={(e) => setDescription(e.target.value)} placeholder="选题目标、角度、要求等..." rows={3} />
        </div>
        <div className="space-y-2">
          <Label>验收标准</Label>
          <Input value={criteria} onChange={(e) => setCriteria(e.target.value)} placeholder="如：800-1200字，有数据支撑，无事实错误" />
        </div>
        <div className="space-y-2">
          <Label>写作风格</Label>
          <Input value={style} onChange={(e) => setStyle(e.target.value)} placeholder="yinyue / shenlun / xiaohongshu" />
        </div>
      </div>
      <div className="flex justify-end gap-2">
        <Button
          onClick={async () => {
            if (!title.trim()) return;
            setLoading(true);
            await onCreate({ title: title.trim(), description: description.trim(), accept_criteria: criteria.trim(), style_slug: style });
            setLoading(false);
          }}
          disabled={!title.trim() || loading}
        >
          {loading ? <Loader2 className="h-4 w-4 animate-spin" /> : "创建"}
        </Button>
      </div>
    </DialogContent>
  );
}

// ─── 辅助函数 ─────────────────────────────────────────────

function formatEventMessage(type: string, payload: Record<string, unknown>): string | null {
  switch (type) {
    case "task.status_changed": {
      const from = payload.from as string;
      const to = payload.to as string;
      const reason = payload.reason as string | undefined;
      if (reason === "agent_failure") {
        return `⚠️ 任务从 ${STATUS_LABELS[from] || from} 回退到 ${STATUS_LABELS[to] || to}（Agent 失败）`;
      }
      return `任务状态变更: ${STATUS_LABELS[from] || from} → ${STATUS_LABELS[to] || to}`;
    }
    case "agent.started": {
      const role = payload.role as string;
      return `${ASSIGNEE_LABELS[role] || role} 开始执行...`;
    }
    case "agent.completed": {
      const role = payload.role as string;
      return `${ASSIGNEE_LABELS[role] || role} 执行完成`;
    }
    case "agent.failed": {
      const role = payload.role as string;
      const error = payload.error as string;
      return `❌ ${ASSIGNEE_LABELS[role] || role} 执行失败: ${error}`;
    }
    case "artifact.produced": {
      const role = payload.role as string;
      const artType = payload.artifact_type as string;
      return `${ASSIGNEE_LABELS[role] || role} 产出 ${ARTIFACT_LABELS[artType] || artType}`;
    }
    case "decision.created": {
      const decType = payload.type as string;
      const by = payload.by as string;
      return `决策记录: ${DECISION_LABELS[decType] || decType}（由 ${ASSIGNEE_LABELS[by] || by} 创建）`;
    }
    case "decision.required": {
      const msg = payload.message as string;
      return `🔔 需要人工决策: ${msg}`;
    }
    default:
      return null;
  }
}

function getEventIcon(type: string) {
  switch (type) {
    case "agent.started": return <Bot className="h-3 w-3 text-blue-500" />;
    case "agent.completed": return <CheckCircle2 className="h-3 w-3 text-green-500" />;
    case "agent.failed": return <XCircle className="h-3 w-3 text-red-500" />;
    case "artifact.produced": return <FileText className="h-3 w-3 text-indigo-500" />;
    case "decision.required": return <AlertTriangle className="h-3 w-3 text-amber-500" />;
    case "decision.created": return <CheckCircle2 className="h-3 w-3 text-purple-500" />;
    case "task.status_changed": return <ChevronRight className="h-3 w-3 text-muted-foreground" />;
    default: return <Activity className="h-3 w-3 text-muted-foreground" />;
  }
}

// ─── 常量 ─────────────────────────────────────────────────

const ARTIFACT_LABELS: Record<string, string> = {
  topic_card: "选题卡",
  research_brief: "研究简报",
  source_pack: "信源包",
  fact_claims: "事实声明表",
  outline: "提纲",
  draft: "初稿",
  review_report: "审查报告",
  revised_draft: "修改稿",
};

const ARTIFACT_STATUS_LABELS: Record<string, string> = {
  draft: "草稿",
  submitted: "待审批",
  approved: "已通过",
  rejected: "已驳回",
  superseded: "已取代",
};

const DECISION_LABELS: Record<string, string> = {
  approve_topic: "立项审批",
  select_angle: "角度选择",
  trust_source: "信源确认",
  accept_review: "审校意见",
  allow_rewrite: "允许重写",
  publish: "发布审批",
  escalate: "升级裁决",
};

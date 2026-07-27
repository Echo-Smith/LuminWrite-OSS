/**
 * 编辑部任务看板 — 看板式协作界面
 *
 * 左侧：任务列表（按状态分列）
 * 右侧：当前任务详情（交付物时间线 + 决策记录）
 */
import { useEffect, useState } from "react";
import { useEditorialStore, type EditorialTask, type TaskStatus } from "@/stores/editorial-store";
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
  Clock,
  Coins,
  Loader2,
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
  const { tasks, loading, fetchTasks, createTask, advanceTask } = useEditorialStore();
  const [selectedTaskId, setSelectedTaskId] = useState<string | null>(null);
  const [showCreate, setShowCreate] = useState(false);

  useEffect(() => {
    fetchTasks();
  }, [fetchTasks]);

  return (
    <div className="flex h-[calc(100vh-3.5rem)]">
      {/* ─── 左侧：看板 ─── */}
      <div className="flex-1 overflow-x-auto">
        {/* 顶部栏 */}
        <div className="flex items-center justify-between border-b px-6 py-3">
          <div className="flex items-center gap-2">
            <FileText className="h-5 w-5 text-primary" />
            <h1 className="text-lg font-semibold">编辑部</h1>
            <Badge variant="secondary" className="ml-2">{tasks.length} 个任务</Badge>
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

        {/* 看板列 */}
        <div className="flex gap-4 p-4 min-w-max">
          {STATUS_COLUMNS.map((col) => {
            const colTasks = tasks.filter((t) => t.status === col.status);
            return (
              <div
                key={col.status}
                className="w-72 shrink-0 rounded-lg bg-muted/30"
              >
                {/* 列头 */}
                <div className="flex items-center gap-2 px-3 py-2 border-b">
                  <div className={cn("h-2 w-2 rounded-full", col.color)} />
                  <span className="text-sm font-medium">{col.label}</span>
                  <span className="text-xs text-muted-foreground ml-auto">
                    {colTasks.length}
                  </span>
                </div>
                {/* 任务卡片 */}
                <div className="p-2 space-y-2 max-h-[calc(100vh-10rem)] overflow-y-auto">
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
      </div>

      {/* ─── 右侧：任务详情 ─── */}
      {selectedTaskId && (
        <div className="w-96 border-l overflow-y-auto">
          <TaskDetailPanel taskId={selectedTaskId} />
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

  return (
    <div
      onClick={onClick}
      className={cn(
        "rounded-lg border bg-card p-3 cursor-pointer transition-all hover:shadow-md",
        selected && "ring-2 ring-primary"
      )}
    >
      <div className="flex items-start justify-between gap-2">
        <h3 className="text-sm font-medium line-clamp-2">{task.title}</h3>
        <ChevronRight className="h-4 w-4 shrink-0 text-muted-foreground" />
      </div>

      {task.description && (
        <p className="mt-1 text-xs text-muted-foreground line-clamp-2">
          {task.description}
        </p>
      )}

      {/* 元数据 */}
      <div className="mt-2 flex items-center gap-3 text-xs text-muted-foreground">
        <span className="flex items-center gap-1">
          <div className="h-1.5 w-1.5 rounded-full bg-blue-500" />
          {ASSIGNEE_LABELS[task.assignee_type] || task.assignee_type}
        </span>
        {task.deadline && (
          <span className="flex items-center gap-1">
            <Clock className="h-3 w-3" />
            {new Date(task.deadline).toLocaleDateString("zh-CN", { month: "short", day: "numeric" })}
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
            <Badge key={tag} variant="outline" className="text-xs py-0 px-1.5">
              {tag}
            </Badge>
          ))}
        </div>
      )}

      {/* 推进按钮 */}
      <AdvanceButton task={task} onAdvance={onAdvance} />
    </div>
  );
}

// ─── 推进按钮 ─────────────────────────────────────────────

function AdvanceButton({
  task,
  onAdvance,
}: {
  task: EditorialTask;
  onAdvance: (target: TaskStatus) => void;
}) {
  const nextStatuses: Record<string, TaskStatus | null> = {
    draft: "pending_approval",
    pending_approval: "research",
    research: null, // 自动推进到 writing
    writing: null, // 自动推进到 review
    review: null, // 由审校结果决定
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
      onClick={(e) => {
        e.stopPropagation();
        onAdvance(next);
      }}
      className="mt-2 w-full rounded-md bg-primary/10 hover:bg-primary/20 text-primary text-xs font-medium py-1.5 transition-colors"
    >
      {labels[next] || "推进"}
    </button>
  );
}

// ─── 任务详情面板 ─────────────────────────────────────────

function TaskDetailPanel({ taskId }: { taskId: string }) {
  const { currentTask, artifacts, decisions, fetchTask, fetchArtifacts, fetchDecisions } =
    useEditorialStore();

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

  return (
    <div className="p-4 space-y-4">
      {/* 任务信息 */}
      <div>
        <h2 className="text-base font-semibold">{currentTask.title}</h2>
        {currentTask.description && (
          <p className="mt-1 text-sm text-muted-foreground">{currentTask.description}</p>
        )}
        <div className="mt-2 flex items-center gap-2">
          <Badge variant="secondary">{STATUS_LABELS[currentTask.status]}</Badge>
          <Badge variant="outline">
            {ASSIGNEE_LABELS[currentTask.assignee_type] || currentTask.assignee_type}
          </Badge>
        </div>
      </div>

      {/* 验收标准 */}
      {currentTask.accept_criteria && (
        <div>
          <h3 className="text-xs font-medium text-muted-foreground uppercase tracking-wide">
            验收标准
          </h3>
          <p className="mt-1 text-sm">{currentTask.accept_criteria}</p>
        </div>
      )}

      {/* 交付物时间线 */}
      <div>
        <h3 className="text-xs font-medium text-muted-foreground uppercase tracking-wide">
          交付物 ({artifacts.length})
        </h3>
        <div className="mt-2 space-y-2">
          {artifacts.map((art) => (
            <div
              key={art.id}
              className="rounded-lg border p-2 text-sm"
            >
              <div className="flex items-center justify-between">
                <span className="font-medium">{ARTIFACT_LABELS[art.type] || art.type}</span>
                <Badge
                  variant={
                    art.status === "approved" ? "default" :
                    art.status === "rejected" ? "destructive" :
                    art.status === "superseded" ? "secondary" : "outline"
                  }
                  className="text-xs"
                >
                  {ARTIFACT_STATUS_LABELS[art.status] || art.status}
                </Badge>
              </div>
              <div className="mt-1 flex items-center gap-2 text-xs text-muted-foreground">
                <span>v{art.version}</span>
                <span>·</span>
                <span>{art.produced_by}</span>
                {art.token_cost > 0 && (
                  <>
                    <span>·</span>
                    <span className="flex items-center gap-0.5">
                      <Coins className="h-3 w-3" />
                      {art.token_cost}
                    </span>
                  </>
                )}
              </div>
            </div>
          ))}
          {artifacts.length === 0 && (
            <p className="text-xs text-muted-foreground">暂无交付物</p>
          )}
        </div>
      </div>

      {/* 决策记录 */}
      <div>
        <h3 className="text-xs font-medium text-muted-foreground uppercase tracking-wide">
          决策记录 ({decisions.length})
        </h3>
        <div className="mt-2 space-y-2">
          {decisions.map((d) => (
            <div key={d.id} className="rounded-lg border p-2 text-sm">
              <div className="flex items-center justify-between">
                <span className="font-medium">{DECISION_LABELS[d.type] || d.type}</span>
                <Badge
                  variant={
                    d.status === "approved" ? "default" :
                    d.status === "rejected" ? "destructive" :
                    d.status === "escalated" ? "destructive" : "outline"
                  }
                  className="text-xs"
                >
                  {d.status}
                </Badge>
              </div>
              {d.rationale && (
                <p className="mt-1 text-xs text-muted-foreground">{d.rationale}</p>
              )}
              <div className="mt-1 text-xs text-muted-foreground">
                {d.decided_by_type} · {new Date(d.created_at).toLocaleString("zh-CN")}
              </div>
            </div>
          ))}
          {decisions.length === 0 && (
            <p className="text-xs text-muted-foreground">暂无决策记录</p>
          )}
        </div>
      </div>
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
          <Input
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder="如：AI 对音乐产业的影响"
          />
        </div>
        <div className="space-y-2">
          <Label>选题描述</Label>
          <Textarea
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            placeholder="选题目标、角度、要求等..."
            rows={3}
          />
        </div>
        <div className="space-y-2">
          <Label>验收标准</Label>
          <Input
            value={criteria}
            onChange={(e) => setCriteria(e.target.value)}
            placeholder="如：800-1200字，有数据支撑，无事实错误"
          />
        </div>
        <div className="space-y-2">
          <Label>写作风格</Label>
          <Input
            value={style}
            onChange={(e) => setStyle(e.target.value)}
            placeholder="yinyue / shenlun / xiaohongshu"
          />
        </div>
      </div>
      <div className="flex justify-end gap-2">
        <Button
          onClick={async () => {
            if (!title.trim()) return;
            setLoading(true);
            await onCreate({
              title: title.trim(),
              description: description.trim(),
              accept_criteria: criteria.trim(),
              style_slug: style,
            });
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

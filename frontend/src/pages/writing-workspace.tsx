/**
 * 写作工作台 — 三栏布局：左侧栏 | 中央 Thread | 右侧详情面板
 *
 * 左侧栏可折叠/展开
 * 右侧面板可折叠/展开
 * 移动端：侧栏变为抽屉模式，详情面板变为全屏覆盖
 * 使用 useAgentWebSocket Hook 自动管理 WS 连接
 * 集成全局键盘快捷键
 */
import { useState, useEffect, useCallback, useRef } from "react";
import { PanelRightOpen, Menu, RefreshCw, Play, XCircle, FileText, Network, Pencil } from "lucide-react";
import { Sidebar } from "@/components/sidebar/sidebar";
import { DetailPanel } from "@/components/sidebar/detail-panel";
import { Thread } from "@/components/assistant-ui/thread";
import { WritingComposer, type WritingComposerHandle } from "@/components/composer/writing-composer";
import { Button } from "@/components/ui/button";
import { useAgentStore } from "@/stores/agent-store";
import { useSettingsStore } from "@/stores/settings-store";
import { useWorkflowStore } from "@/stores/workflow-store";
import { useBillingStore } from "@/stores/billing-store";
import { useAgentWebSocket } from "@/hooks/use-agent-websocket";
import { useKeyboardShortcuts } from "@/hooks/use-keyboard-shortcuts";
import { toast } from "@/stores/toast-store";
import { PulseIndicator } from "@/components/animation";
import { WorkflowCanvas } from "@/components/workflow/canvas";
import { getRoleTheme, getRoleIllustration } from "@/components/workflow/agent-illustration";
import { cn } from "@/lib/utils";

export function WritingWorkspace() {
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);
  const [showDetail, setShowDetail] = useState(false);
  const [mobileSidebarOpen, setMobileSidebarOpen] = useState(false);

  // 积分余额
  const billingBalance = useBillingStore((s) => s.balance);
  const loadBalance = useBillingStore((s) => s.loadBalance);

  // 通过 Hook 管理 WS 连接
  const { connected } = useAgentWebSocket();

  const sessions = useAgentStore((s) => s.sessions);
  const activeSessionId = useAgentStore((s) => s.activeSessionId);
  const loadSessions = useAgentStore((s) => s.loadSessions);
  const connectWS = useAgentStore((s) => s.connectWS);
  const composerRef = useRef<WritingComposerHandle | null>(null);

  // 工作台模式状态
  const agentMode = useSettingsStore((s) => s.agentMode);
  const isEditorial = agentMode === "editorial";
  const workflowPlan = useWorkflowStore((s) => s.plan);
  const workflowStatus = useWorkflowStore((s) => s.runStatus);
  const workflowReset = useWorkflowStore((s) => s.reset);
  const agents = useWorkflowStore((s) => s.agents);
  const workflowUserInput = useWorkflowStore((s) => s.userInput);

  // 有活跃会话且开始写作时自动显示右侧面板
  const session = sessions.find((s) => s.id === activeSessionId);
  const isWriting = session?.status === "running" || session?.status === "paused";

  // 工作台模式激活条件：
  // 1. 全局 agentMode 为 editorial 且有活跃 workflow（plan 存在或状态非 idle）
  // 2. 当前活跃会话的 mode 为 editorial（从左侧历史切换到工作台会话时）
  const isSessionEditorial = session?.mode === "editorial";
  const isEditorialActive = isSessionEditorial || (isEditorial && (workflowPlan != null || workflowStatus !== "idle"));

  // 从数据库加载历史会话列表
  useEffect(() => {
    loadSessions();
  }, [loadSessions]);

  // 加载积分余额
  useEffect(() => {
    loadBalance();
  }, [loadBalance]);

  // 工作台模式：有 plan 或正在 planning 时自动展开右侧面板
  useEffect(() => {
    if (isEditorialActive && (workflowPlan || workflowStatus === "planning" || workflowStatus === "running")) {
      setShowDetail(true);
    }
  }, [isEditorialActive, workflowPlan, workflowStatus]);

  // 非活跃 editorial 模式时原有逻辑
  useEffect(() => {
    if (!isEditorialActive && isWriting) setShowDetail(true);
  }, [isWriting, isEditorialActive]);

  // 离开工作台模式时重置 workflow 状态
  useEffect(() => {
    if (!isEditorial) {
      workflowReset();
    }
  }, [isEditorial, workflowReset]);

  // 切换会话时重置已完成的 workflow 状态（避免之前完成的工作台工作流覆盖新会话的 Thread）
  // 仅当 workflow 不是正在规划/运行时才重置，避免中断活跃工作流
  // 注意：切换到工作台历史会话时不 reset，因为需要保留恢复的 workflow 状态
  useEffect(() => {
    const currentSession = sessions.find((s) => s.id === activeSessionId);
    // 如果切到的是工作台历史会话，不 reset（需要恢复状态）
    if (currentSession?.mode === "editorial") return;
    if (workflowStatus === "completed" || workflowStatus === "failed" || workflowStatus === "idle") {
      // 如果当前会话有消息（普通写作会话），重置 workflow 以显示 Thread
      if (currentSession && currentSession.messages.length > 0) {
        workflowReset();
      }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeSessionId]);

  // WS 断线通知 — 降级处理：只在连续重连失败 3 次后才弹 toast，
  // 避免瞬断（Nginx proxy_read_timeout 等）造成的频繁打扰。
  const wasConnectedRef = useRef(false);
  const disconnectCountRef = useRef(0);
  const reconnectToastShownRef = useRef(false);
  useEffect(() => {
    if (connected) {
      wasConnectedRef.current = true;
      disconnectCountRef.current = 0;
      reconnectToastShownRef.current = false;
    } else if (wasConnectedRef.current) {
      disconnectCountRef.current += 1;
      // 只在连续断线 3 次后才弹一次 toast（后续不再重复弹）
      if (disconnectCountRef.current >= 3 && !reconnectToastShownRef.current) {
        toast.warning("连接已断开", "正在自动重连…", 3000);
        reconnectToastShownRef.current = true;
      }
    }
  }, [connected]);

  // 手动重连
  const handleManualReconnect = useCallback(() => {
    connectWS();
    toast.info("正在重连…");
  }, [connectWS]);

  // 键盘快捷键
  useKeyboardShortcuts({
    onToggleSidebar: () => {
      // 移动端切换抽屉，桌面端切换折叠
      if (window.innerWidth < 768) {
        setMobileSidebarOpen((v) => !v);
      } else {
        setSidebarCollapsed((v) => !v);
      }
    },
    onToggleDetail: () => setShowDetail((v) => !v),
    onFocusInput: () => composerRef.current?.focusTextarea(),
    onEscape: () => {
      if (mobileSidebarOpen) setMobileSidebarOpen(false);
      else if (showDetail) setShowDetail(false);
    },
  });

  return (
    <div className="flex h-screen overflow-hidden">
      {/* 移动端遮罩 */}
      {mobileSidebarOpen && (
        <div
          className="fixed inset-0 z-30 bg-black/40 backdrop-blur-sm md:hidden anim-fade-in"
          onClick={() => setMobileSidebarOpen(false)}
        />
      )}

      {/* 左侧栏 — 桌面端固定，移动端抽屉 */}
      <div className={cn(
        "z-40 transition-transform duration-300 md:relative md:transition-none",
        "max-md:fixed max-md:inset-y-0 max-md:left-0",
        mobileSidebarOpen ? "max-md:translate-x-0" : "max-md:-translate-x-full"
      )}>
        <Sidebar collapsed={sidebarCollapsed} onToggle={() => setSidebarCollapsed(!sidebarCollapsed)} />
      </div>

      {/* 中央区域 */}
      <div className="flex flex-1 flex-col overflow-hidden">
        {/* 顶部工具栏 */}
        <header className="flex h-12 shrink-0 items-center justify-between border-b bg-surface/50 backdrop-blur-sm px-4">
          <div className="flex items-center gap-2 min-w-0">
            {/* 移动端菜单按钮 */}
            <button
              onClick={() => setMobileSidebarOpen(true)}
              className="md:hidden flex h-8 w-8 items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-foreground transition-ui"
              title="打开菜单"
            >
              <Menu className="h-4 w-4" />
            </button>

            <h2 className="text-sm font-medium truncate">
              {isEditorialActive
                ? (workflowUserInput
                  ? workflowUserInput.length > 30
                    ? workflowUserInput.slice(0, 30) + "…"
                    : workflowUserInput
                  : workflowStatus === "planning" ? "规划中…" :
                   workflowStatus === "running" ? "工作台工作流执行中" :
                   workflowStatus === "completed" ? "写作完成" :
                   workflowStatus === "failed" ? "执行失败" :
                   workflowPlan ? "工作台模式" : "写作工作台")
                : (session?.title ?? "写作工作台")}
            </h2>
            {!isEditorialActive && session && (
              <span className="hidden sm:inline text-xs text-muted-foreground font-mono-sm">
                · {session.style} · {session.mode === "auto" ? "自动" : session.mode}
                · {new Date(session.createdAt).toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" })}
              </span>
            )}
            {isEditorialActive && workflowPlan && (
              <span className="hidden sm:inline text-xs text-muted-foreground font-mono-sm">
                · {agents.length} Agents · {workflowPlan.workflow.nodes.length} nodes
              </span>
            )}
          </div>

          <div className="flex items-center gap-2">
            {/* 积分余额 */}
            {billingBalance && billingBalance.point_balance > 0 && (
              <div className="group relative flex items-center gap-1 rounded-full border border-border/60 px-2.5 py-1 text-xs text-muted-foreground">
                <span className="font-mono-sm">余额 {Math.floor(billingBalance.point_balance)} 积分</span>
                {/* hover 显示详细 — 向下弹出避免被 overflow-hidden 截断 */}
                <div className="pointer-events-none absolute top-full right-0 mt-1.5 whitespace-nowrap rounded-md border border-border bg-popover px-2.5 py-1.5 text-[10px] text-muted-foreground opacity-0 shadow-lg z-50 transition-opacity group-hover:opacity-100">
                  余额 {Math.floor(billingBalance.point_balance)} 积分 · {billingBalance.plan_display_name}
                </div>
              </div>
            )}
            {/* WS 连接状态指示 */}
            {!connected && (
              <button
                onClick={handleManualReconnect}
                className="flex items-center gap-1.5 text-xs text-amber-600 anim-fade-in hover:text-amber-700 transition-ui"
                title="点击手动重连"
              >
                <PulseIndicator status="paused" size="sm" ring={false} />
                <span className="hidden sm:inline">重连中…</span>
                <RefreshCw className="h-3 w-3 sm:hidden" />
              </button>
            )}
            {connected && isEditorialActive && (workflowStatus === "planning" || workflowStatus === "created" || workflowStatus === "running") && (
              <span className="flex items-center gap-1.5 text-xs text-muted-foreground anim-fade-in">
                <PulseIndicator status="running" size="sm" />
                <span className="font-mono-sm hidden sm:inline">{workflowStatus === "planning" ? "planning" : workflowStatus === "created" ? "待确认" : "live"}</span>
              </span>
            )}
            {connected && !isEditorialActive && session && isWriting && (
              <span className="flex items-center gap-1.5 text-xs text-muted-foreground anim-fade-in">
                <PulseIndicator status="running" size="sm" />
                <span className="font-mono-sm hidden sm:inline">live</span>
              </span>
            )}

            {!showDetail && (
              <Button
                variant="ghost"
                size="sm"
                onClick={() => setShowDetail(true)}
                className="gap-1.5"
              >
                <PanelRightOpen className="h-4 w-4" />
                <span className="text-xs hidden sm:inline">详情</span>
              </Button>
            )}
          </div>
        </header>

        {/* 消息流 / 工作台工作流区域 */}
        {/* 工作台模式仅在有活跃 workflow（plan 存在或状态非 idle）时接管中央区域， */}
        {/* 否则回退到普通 Thread，这样切换到其他写作会话时不会被工作台页面覆盖。 */}
        <div className="flex-1 overflow-hidden">
          {isEditorialActive ? (
            <EditorialContent
              plan={workflowPlan}
              runStatus={workflowStatus}
              wsConnected={connected}
            />
          ) : (
            <Thread />
          )}
        </div>

        {/* 输入区 — 浮在消息流上方，顶部渐变遮罩实现过渡 */}
        <div className="shrink-0">
          <WritingComposer ref={composerRef} />
        </div>
      </div>

      {/* 右侧详情面板 — 桌面/平板端侧栏，手机端全屏覆盖 */}
      {showDetail && (
        <div className="z-40 max-sm:fixed max-sm:inset-0 sm:relative">
          {isEditorialActive ? (
            <WorkflowDetailPanel onClose={() => setShowDetail(false)} />
          ) : (
            <DetailPanel onClose={() => setShowDetail(false)} />
          )}
        </div>
      )}
    </div>
  );
}

// ─── 工作台模式中央内容 ───────────────────────────────

function EditorialContent({
  plan,
  runStatus,
  wsConnected,
}: {
  plan: import("@/stores/workflow-store").PlanResult | null;
  runStatus: import("@/stores/workflow-store").WorkflowRunStatus;
  wsConnected: boolean;
}) {
  const finalArticle = useWorkflowStore((s) => s.finalArticle);
  const finalArticleLoading = useWorkflowStore((s) => s.finalArticleLoading);
  const isEditMode = useWorkflowStore((s) => s.isEditMode);
  const setEditMode = useWorkflowStore((s) => s.setEditMode);

  // 视图切换：completed 状态下在「文章」和「DAG 图」之间切换
  const [completedView, setCompletedView] = useState<"article" | "dag">("article");

  // 进入编辑模式时自动切换到 DAG 视图
  useEffect(() => {
    if (isEditMode && completedView !== "dag") {
      setCompletedView("dag");
    }
  }, [isEditMode, completedView]);

  // DAG 完成但文章还在加载
  if (runStatus === "completed" && finalArticleLoading) {
    return (
      <div className="flex h-full items-center justify-center text-muted-foreground">
        <div className="text-center">
          <div className="mb-3 text-4xl animate-pulse">📝</div>
          <p className="text-sm">正在加载最终文章...</p>
        </div>
      </div>
    );
  }

  // DAG 完成但未找到文章 → 直接显示 DAG 图
  if (runStatus === "completed" && !finalArticle && !finalArticleLoading) {
    if (plan) {
      return <WorkflowCanvas />;
    }
    return (
      <div className="flex h-full items-center justify-center text-muted-foreground">
        <div className="text-center max-w-md px-6">
          <div className="mb-3 text-5xl">✅</div>
          <p className="text-sm font-medium">写作完成</p>
          <p className="mt-2 text-xs text-muted-foreground/60">
            文章已生成，但未找到可展示的稿件。请在右侧面板查看详情。
          </p>
        </div>
      </div>
    );
  }

  // DAG 完成 — 文章/DAG 图切换视图
  if (runStatus === "completed" && finalArticle) {
    return (
      <div className="flex h-full flex-col">
        {/* 视图切换工具栏 */}
        <div className="flex shrink-0 items-center justify-between border-b px-3 py-1.5">
          <div className="flex items-center gap-1">
            <button
              onClick={() => !isEditMode && setCompletedView("article")}
              disabled={isEditMode}
              className={cn(
                "flex items-center gap-1.5 rounded-md px-3 py-1 text-xs font-medium transition-ui",
                completedView === "article"
                  ? "bg-primary/10 text-primary"
                  : "text-muted-foreground hover:bg-accent hover:text-foreground",
                isEditMode && "opacity-40 cursor-not-allowed",
              )}
            >
              <FileText className="h-3.5 w-3.5" />
              文章
            </button>
            <button
              onClick={() => setCompletedView("dag")}
              className={cn(
                "flex items-center gap-1.5 rounded-md px-3 py-1 text-xs font-medium transition-ui",
                completedView === "dag"
                  ? "bg-primary/10 text-primary"
                  : "text-muted-foreground hover:bg-accent hover:text-foreground"
              )}
            >
              <Network className="h-3.5 w-3.5" />
              DAG 图
            </button>
          </div>
          {/* 编辑 DAG 按钮 — 仅在 DAG 视图显示 */}
          {completedView === "dag" && plan && !isEditMode && (
            <button
              onClick={() => setEditMode(true)}
              className="flex items-center gap-1.5 rounded-md bg-blue-600 px-3 py-1 text-xs font-medium text-white transition hover:bg-blue-700"
            >
              <Pencil className="h-3 w-3" />
              编辑 DAG
            </button>
          )}
          {isEditMode && (
            <span className="text-xs font-medium text-blue-600">
              编辑模式中 — 完成后点击保存
            </span>
          )}
        </div>

        {/* 视图内容 */}
        {completedView === "article" ? (
          <div className="flex-1 overflow-y-auto px-6 py-8">
            <article className="prose prose-sm prose-zh max-w-2xl mx-auto">
              {finalArticle.title && (
                <h1 className="text-xl font-bold mb-4">{finalArticle.title}</h1>
              )}
              <div className="whitespace-pre-wrap text-sm leading-relaxed text-foreground/90">
                {finalArticle.content}
              </div>
              {finalArticle.word_count > 0 && (
                <p className="mt-6 text-xs text-muted-foreground/60">
                  {finalArticle.word_count} 字
                </p>
              )}
            </article>
          </div>
        ) : (
          <div className="flex-1 min-h-0">
            <WorkflowCanvas />
          </div>
        )}
      </div>
    );
  }

  if (plan) {
    return (
      <div className="relative h-full">
        {/* 编辑模式切换按钮 */}
        {!isEditMode && (
          <button
            onClick={() => setEditMode(true)}
            className="absolute right-3 top-3 z-10 flex items-center gap-1.5 rounded-lg bg-blue-600 px-3 py-1.5 text-xs font-medium text-white shadow-lg transition hover:bg-blue-700"
          >
            <Pencil className="h-3.5 w-3.5" />
            编辑 DAG
          </button>
        )}
        <WorkflowCanvas />
      </div>
    );
  }

  return (
    <div className="flex h-full items-center justify-center text-muted-foreground">
      <div className="text-center max-w-md px-6">
        <div className="mb-3 text-5xl">🎨</div>
        <p className="text-sm font-medium">
          {runStatus === "planning"
            ? "正在分析写作意图，生成 Agent 集群..."
            : !wsConnected
              ? "正在连接服务器..."
              : "工作台模式 (Beta)"}
        </p>
        <p className="mt-2 text-xs text-muted-foreground/60">
          {runStatus === "planning"
            ? "Planner 正在为你定制专属 Agent 集群"
            : "在下方输入写作要求，AI 将自动规划多 Agent 协作流程"}
        </p>
      </div>
    </div>
  );
}

// ─── 工作台模式右侧面板 ─────────────────────────────────

function WorkflowDetailPanel({ onClose }: { onClose: () => void }) {
  const plan = useWorkflowStore((s) => s.plan);
  const runStatus = useWorkflowStore((s) => s.runStatus);
  const totalTokensUsed = useWorkflowStore((s) => s.totalTokensUsed);
  const nodeStates = useWorkflowStore((s) => s.nodeStates);
  const agents = useWorkflowStore((s) => s.agents);
  const userInput = useWorkflowStore((s) => s.userInput);
  const taskId = useWorkflowStore((s) => s.taskId);
  const errorMessage = useWorkflowStore((s) => s.errorMessage);
  const sendWS = useAgentStore((s) => s.sendWS);

  const isPlanning = runStatus === "planning";
  const isCreated = runStatus === "created";
  const isRunning = runStatus === "running";
  const isCompleted = runStatus === "completed";
  const hasPlan = plan != null;

  const handleStartExecution = useCallback(() => {
    if (taskId) {
      sendWS("workflow.start", { task_id: taskId });
    }
  }, [taskId, sendWS]);

  const handleAbort = useCallback(() => {
    useWorkflowStore.getState().reset();
  }, []);

  const completedNodes = Array.from(nodeStates.values()).filter((n) => n.status === "completed").length;
  const runningNodes = Array.from(nodeStates.values()).filter((n) => n.status === "running").length;
  const failedNodes = Array.from(nodeStates.values()).filter((n) => n.status === "failed").length;
  const totalNodes = plan?.workflow.nodes.length ?? 0;

  return (
    <div className="flex h-full w-full sm:w-80 flex-col sm:border-l bg-surface anim-slide-left">
      {/* 头部 */}
      <div className="flex h-12 shrink-0 items-center justify-between px-4">
        <h3 className="text-sm font-medium">工作台工作流</h3>
        <button
          onClick={onClose}
          className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground transition-ui"
        >
          <PanelRightOpen className="h-4 w-4" />
          收起
        </button>
      </div>

      <div className="flex-1 overflow-y-auto p-4 space-y-4">
        {/* 用户输入主题 */}
        {userInput && (
          <div className="rounded-lg border bg-card/50 p-3 space-y-1">
            <p className="text-xs font-medium text-muted-foreground">写作主题</p>
            <p className="text-sm font-medium leading-snug">{userInput}</p>
          </div>
        )}

        {/* 状态总览 */}
        <div className="rounded-lg border bg-card/50 p-3 space-y-2">
          <div className="flex items-center justify-between text-xs">
            <span className="text-muted-foreground">状态</span>
            <span className={cn(
              "font-medium",
              isPlanning && "text-blue-600",
              isCreated && "text-purple-600",
              isRunning && "text-amber-600",
              isCompleted && "text-green-600",
              runStatus === "failed" && "text-red-600",
              !hasPlan && "text-muted-foreground"
            )}>
              {isPlanning ? "规划中" : isCreated ? "待确认" : isRunning ? "执行中" : isCompleted ? "已完成" : runStatus === "failed" ? "失败" : "待启动"}
            </span>
          </div>
          {runStatus === "failed" && errorMessage && (
            <div className="rounded-md bg-red-50 border border-red-200 px-2.5 py-2">
              <p className="text-xs text-red-700 leading-relaxed">{errorMessage}</p>
            </div>
          )}
          {hasPlan && (
            <>
              <div className="flex items-center justify-between text-xs">
                <span className="text-muted-foreground">节点进度</span>
                <span className="font-medium tabular-nums">
                  {completedNodes}/{totalNodes}
                  {runningNodes > 0 && <span className="text-blue-500 ml-1">（{runningNodes} 执行中）</span>}
                  {failedNodes > 0 && <span className="text-red-500 ml-1">（{failedNodes} 失败）</span>}
                </span>
              </div>
              <div className="flex items-center justify-between text-xs">
                <span className="text-muted-foreground">Agent 数</span>
                <span className="font-medium tabular-nums">{agents.length}</span>
              </div>
              {totalTokensUsed > 0 && (
                <div className="flex items-center justify-between text-xs">
                  <span className="text-muted-foreground">Token 用量</span>
                  <span className="font-medium tabular-nums">{totalTokensUsed}</span>
                </div>
              )}
            </>
          )}
        </div>

        {/* Agent 列表 */}
        {hasPlan && agents.length > 0 && (
          <div className="space-y-2">
            <p className="text-xs font-medium text-muted-foreground">Agent 集群</p>
            {agents.map((agent) => {
              const agentNodes = plan!.workflow.nodes.filter((n) => n.agent_id === agent.id);
              const nodeState = agentNodes.length > 0 ? nodeStates.get(agentNodes[0].id) : undefined;
              const status = nodeState?.status || "pending";
              const theme = getRoleTheme(agent.role);
              const RoleIllustration = getRoleIllustration(agent.role);
              return (
                <div
                  key={agent.id}
                  className="overflow-hidden rounded-lg border"
                  style={{ borderColor: theme.morandiBorder, background: "var(--card)" }}
                >
                  {/* 莫兰迪色卡头部 */}
                  <div
                    className="flex items-center gap-2 px-2.5 py-2"
                    style={{ background: theme.morandiBg }}
                  >
                    <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-md bg-white/60 dark:bg-black/20">
                      <RoleIllustration size={24} />
                    </span>
                    <span className="text-xs font-medium truncate flex-1" style={{ color: theme.morandiText }}>
                      {agent.role}
                    </span>
                    <span className={cn("h-2 w-2 rounded-full shrink-0",
                      status === "running" ? "bg-blue-500 animate-pulse" :
                      status === "completed" ? "bg-green-500" :
                      status === "failed" ? "bg-red-500" :
                      "bg-muted-foreground/30"
                    )} />
                  </div>
                  {/* Agent 信息 */}
                  <div className="p-2.5">
                    <span className="text-xs font-medium text-foreground">{agent.name}</span>
                    {agent.persona && (
                      <p className="mt-1 text-[10px] text-muted-foreground line-clamp-2">
                        {agent.persona}
                      </p>
                    )}
                  </div>
                </div>
              );
            })}
          </div>
        )}

        {/* 规划说明 */}
        {hasPlan && plan?.rationale && (
          <div className="space-y-2">
            <p className="text-xs font-medium text-muted-foreground">规划说明</p>
            <p className="text-xs text-muted-foreground/80 leading-relaxed">
              {plan.rationale}
            </p>
          </div>
        )}

        {/* 确认/取消按钮 — 规划完成后等待用户确认 */}
        {isCreated && (
          <div className="space-y-2">
            {/* 预估积分消耗 */}
            <div className="flex items-center justify-center gap-1.5 text-[10px] text-muted-foreground/70">
              <span>预计消耗约 {Math.max(50, (plan?.workflow?.nodes?.length || 1) * 30)}-{Math.max(150, (plan?.workflow?.nodes?.length || 1) * 80)} 积分（取决于实际输出）</span>
            </div>
            <button
              onClick={handleStartExecution}
              className="w-full flex items-center justify-center gap-2 rounded-lg bg-foreground text-background py-2.5 text-sm font-medium transition-ui hover:opacity-90 active:scale-[0.98]"
            >
              <Play className="h-4 w-4" />
              确认执行
            </button>
            <button
              onClick={handleAbort}
              className="w-full flex items-center justify-center gap-2 rounded-lg border border-border/60 py-2.5 text-sm text-muted-foreground transition-ui hover:bg-accent hover:text-foreground"
            >
              <XCircle className="h-4 w-4" />
              取消
            </button>
            <p className="text-[10px] text-muted-foreground/60 text-center">
              审查上方的 Agent 集群配置，确认后点击执行
            </p>
          </div>
        )}

        {/* 流式输出预览 */}
        {hasPlan && runningNodes > 0 && (
          <div className="space-y-2">
            <p className="text-xs font-medium text-muted-foreground">实时输出</p>
            {Array.from(nodeStates.entries())
              .filter(([_, state]) => state.status === "running" && state.stream_text)
              .map(([nodeId, state]) => {
                const node = plan?.workflow.nodes.find((n) => n.id === nodeId);
                const agent = agents.find((a) => a.id === node?.agent_id);
                return (
                  <div key={nodeId} className="rounded-lg border border-blue-200/50 bg-blue-50/30 p-2.5">
                    <p className="text-[10px] font-medium text-blue-600 mb-1">
                      {agent?.name ?? node?.label}
                    </p>
                    <p className="text-[10px] text-muted-foreground line-clamp-6 font-mono">
                      {state.stream_text?.slice(-300)}
                      <span className="inline-block h-3 w-0.5 animate-pulse bg-blue-500 ml-0.5" />
                    </p>
                  </div>
                );
              })
            }
          </div>
        )}
      </div>
    </div>
  );
}

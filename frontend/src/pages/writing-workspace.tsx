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
import { PanelRightOpen, Menu, X, RefreshCw, Play, XCircle } from "lucide-react";
import { Sidebar } from "@/components/sidebar/sidebar";
import { DetailPanel } from "@/components/sidebar/detail-panel";
import { Thread } from "@/components/assistant-ui/thread";
import { WritingComposer } from "@/components/composer/writing-composer";
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
  const composerRef = useRef<{ focusTextarea: () => void } | null>(null);

  // 编辑部模式状态
  const agentMode = useSettingsStore((s) => s.agentMode);
  const isEditorial = agentMode === "editorial";
  const workflowPlan = useWorkflowStore((s) => s.plan);
  const workflowStatus = useWorkflowStore((s) => s.runStatus);
  const workflowReset = useWorkflowStore((s) => s.reset);
  const agents = useWorkflowStore((s) => s.agents);
  const workflowUserInput = useWorkflowStore((s) => s.userInput);

  // 从数据库加载历史会话列表
  useEffect(() => {
    loadSessions();
  }, [loadSessions]);

  // 加载积分余额
  useEffect(() => {
    loadBalance();
  }, [loadBalance]);

  // 有活跃会话且开始写作时自动显示右侧面板
  const session = sessions.find((s) => s.id === activeSessionId);
  const isWriting = session?.status === "running" || session?.status === "paused";

  // 编辑部模式：有 plan 或正在 planning 时自动展开右侧面板
  useEffect(() => {
    if (isEditorial && (workflowPlan || workflowStatus === "planning" || workflowStatus === "running")) {
      setShowDetail(true);
    }
  }, [isEditorial, workflowPlan, workflowStatus]);

  // 非 editorial 模式时原有逻辑
  useEffect(() => {
    if (!isEditorial && isWriting) setShowDetail(true);
  }, [isWriting, isEditorial]);

  // 离开 editorial 模式时重置 workflow 状态
  useEffect(() => {
    if (!isEditorial) {
      workflowReset();
    }
  }, [isEditorial, workflowReset]);

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
              {isEditorial
                ? (workflowUserInput
                  ? workflowUserInput.length > 30
                    ? workflowUserInput.slice(0, 30) + "…"
                    : workflowUserInput
                  : workflowStatus === "planning" ? "规划中…" :
                   workflowStatus === "running" ? "编辑部工作流执行中" :
                   workflowStatus === "completed" ? "写作完成" :
                   workflowStatus === "failed" ? "执行失败" :
                   workflowPlan ? "编辑部模式" : "写作工作台")
                : (session?.title ?? "写作工作台")}
            </h2>
            {!isEditorial && session && (
              <span className="hidden sm:inline text-xs text-muted-foreground font-mono-sm">
                · {session.style} · {session.mode}
                · {new Date(session.createdAt).toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" })}
              </span>
            )}
            {isEditorial && workflowPlan && (
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
            {connected && isEditorial && (workflowStatus === "planning" || workflowStatus === "created" || workflowStatus === "running") && (
              <span className="flex items-center gap-1.5 text-xs text-muted-foreground anim-fade-in">
                <PulseIndicator status="running" size="sm" />
                <span className="font-mono-sm hidden sm:inline">{workflowStatus === "planning" ? "planning" : workflowStatus === "created" ? "待确认" : "live"}</span>
              </span>
            )}
            {connected && !isEditorial && session && isWriting && (
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

        {/* 消息流 / 编辑部工作流区域 */}
        <div className="flex-1 overflow-hidden">
          {isEditorial ? (
            <EditorialContent
              plan={workflowPlan}
              runStatus={workflowStatus}
              wsConnected={connected}
            />
          ) : (
            <Thread />
          )}
        </div>

        {/* 输入区 */}
        <WritingComposer ref={composerRef} />
      </div>

      {/* 右侧详情面板 — 桌面端固定，移动端全屏覆盖 */}
      {showDetail && (
        <div className={cn(
          "z-40 md:relative",
          "max-md:fixed max-md:inset-0 max-md:bg-background"
        )}>
          {/* 移动端关闭按钮 */}
          <button
            onClick={() => setShowDetail(false)}
            className="md:hidden absolute top-3 right-3 z-10 flex h-8 w-8 items-center justify-center rounded-md text-muted-foreground hover:bg-accent transition-ui"
          >
            <X className="h-4 w-4" />
          </button>
          {isEditorial ? (
            <WorkflowDetailPanel onClose={() => setShowDetail(false)} />
          ) : (
            <DetailPanel onClose={() => setShowDetail(false)} />
          )}
        </div>
      )}
    </div>
  );
}

// ─── 编辑部模式中央内容 ───────────────────────────────

function EditorialContent({
  plan,
  runStatus,
  wsConnected,
}: {
  plan: import("@/stores/workflow-store").PlanResult | null;
  runStatus: import("@/stores/workflow-store").WorkflowRunStatus;
  wsConnected: boolean;
}) {
  if (plan) {
    return <WorkflowCanvas />;
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
              : "编辑部模式 (Beta)"}
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

// ─── 编辑部模式右侧面板 ─────────────────────────────────

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
    <div className="flex h-full w-80 flex-col border-l bg-surface anim-slide-left">
      {/* 头部 */}
      <div className="flex h-12 shrink-0 items-center justify-between px-4">
        <h3 className="text-sm font-medium">编辑部工作流</h3>
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
              const roleColor =
                agent.role === "researcher" ? "text-blue-600" :
                agent.role === "writer" ? "text-green-600" :
                agent.role === "reviewer" ? "text-amber-600" :
                "text-purple-600";
              return (
                <div key={agent.id} className="rounded-lg border bg-card/50 p-2.5 space-y-1">
                  <div className="flex items-center gap-2">
                    <span className={cn("h-2 w-2 rounded-full shrink-0",
                      status === "running" ? "bg-blue-500 animate-pulse" :
                      status === "completed" ? "bg-green-500" :
                      status === "failed" ? "bg-red-500" :
                      "bg-muted-foreground/30"
                    )} />
                    <span className="text-xs font-medium truncate flex-1">{agent.name}</span>
                    <span className={cn("text-[10px] font-medium shrink-0", roleColor)}>
                      {agent.role}
                    </span>
                  </div>
                  {agent.persona && (
                    <p className="text-[10px] text-muted-foreground line-clamp-2 pl-4">
                      {agent.persona}
                    </p>
                  )}
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

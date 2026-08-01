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
import { PanelRightOpen, Menu, X, RefreshCw } from "lucide-react";
import { Sidebar } from "@/components/sidebar/sidebar";
import { DetailPanel } from "@/components/sidebar/detail-panel";
import { Thread } from "@/components/assistant-ui/thread";
import { WritingComposer } from "@/components/composer/writing-composer";
import { Button } from "@/components/ui/button";
import { useAgentStore } from "@/stores/agent-store";
import { useAgentWebSocket } from "@/hooks/use-agent-websocket";
import { useKeyboardShortcuts } from "@/hooks/use-keyboard-shortcuts";
import { toast } from "@/stores/toast-store";
import { PulseIndicator } from "@/components/animation";
import { cn } from "@/lib/utils";

export function WritingWorkspace() {
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);
  const [showDetail, setShowDetail] = useState(false);
  const [mobileSidebarOpen, setMobileSidebarOpen] = useState(false);

  // 通过 Hook 管理 WS 连接
  const { connected } = useAgentWebSocket();

  const sessions = useAgentStore((s) => s.sessions);
  const activeSessionId = useAgentStore((s) => s.activeSessionId);
  const loadSessions = useAgentStore((s) => s.loadSessions);
  const connectWS = useAgentStore((s) => s.connectWS);
  const composerRef = useRef<{ focusTextarea: () => void } | null>(null);

  // 从数据库加载历史会话列表
  useEffect(() => {
    loadSessions();
  }, [loadSessions]);

  // 有活跃会话且开始写作时自动显示右侧面板
  const session = sessions.find((s) => s.id === activeSessionId);
  const isWriting = session?.status === "running" || session?.status === "paused";

  useEffect(() => {
    if (isWriting) setShowDetail(true);
  }, [isWriting]);

  // WS 断线通知
  const wasConnectedRef = useRef(false);
  useEffect(() => {
    if (connected) {
      wasConnectedRef.current = true;
    } else if (wasConnectedRef.current) {
      toast.warning("连接已断开", "正在自动重连…", 3000);
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
              {session?.title ?? "写作工作台"}
            </h2>
            {session && (
              <span className="hidden sm:inline text-xs text-muted-foreground font-mono-sm">
                · {session.style} · {session.mode}
                · {new Date(session.createdAt).toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" })}
              </span>
            )}
          </div>

          <div className="flex items-center gap-2">
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
            {connected && session && isWriting && (
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

        {/* 消息流 */}
        <div className="flex-1 overflow-hidden">
          <Thread />
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
          <DetailPanel onClose={() => setShowDetail(false)} />
        </div>
      )}
    </div>
  );
}

/**
 * 写作工作台 — 三栏布局：左侧栏 | 中央 Thread | 右侧详情面板
 *
 * 使用 useAgentWebSocket Hook 自动管理 WS 连接，
 * 认证就绪后自动连接触发写作流程。
 */
import { useState, useEffect } from "react";
import { PanelRightOpen, WifiOff } from "lucide-react";
import { Sidebar } from "@/components/sidebar/sidebar";
import { DetailPanel } from "@/components/sidebar/detail-panel";
import { Thread } from "@/components/assistant-ui/thread";
import { WritingComposer } from "@/components/composer/writing-composer";
import { Button } from "@/components/ui/button";
import { useAgentStore } from "@/stores/agent-store";
import { useAgentWebSocket } from "@/hooks/use-agent-websocket";

export function WritingWorkspace() {
  const [showDetail, setShowDetail] = useState(false);

  // 通过 Hook 管理 WS 连接
  const { connected } = useAgentWebSocket();

  const sessions = useAgentStore((s) => s.sessions);
  const activeSessionId = useAgentStore((s) => s.activeSessionId);
  const createSession = useAgentStore((s) => s.createSession);

  // 有活跃会话且开始写作时自动显示右侧面板
  const session = sessions.find((s) => s.id === activeSessionId);
  const isWriting = session?.status === "running" || session?.status === "paused";

  useEffect(() => {
    if (isWriting) setShowDetail(true);
  }, [isWriting]);

  // 确保有活跃会话
  useEffect(() => {
    if (!activeSessionId && sessions.length === 0) {
      // 不自动创建，等用户交互
    } else if (!activeSessionId && sessions.length > 0) {
      // 自动切换到第一个
    }
  }, [activeSessionId, sessions.length]);

  return (
    <div className="flex h-screen overflow-hidden">
      {/* 左侧栏 */}
      <Sidebar />

      {/* 中央区域 */}
      <div className="flex flex-1 flex-col overflow-hidden">
        {/* 顶部工具栏 */}
        <header className="flex items-center justify-between border-b px-4 py-2.5">
          <div className="flex items-center gap-2">
            <h2 className="text-sm font-medium">
              {session?.title ?? "写作工作台"}
            </h2>
            {session && (
              <span className="text-xs text-muted-foreground">
                · {session.style} · {session.mode}
              </span>
            )}
          </div>

          <div className="flex items-center gap-2">
            {/* WS 连接状态指示 */}
            {!connected && (
              <span className="flex items-center gap-1 text-xs text-amber-600">
                <WifiOff className="h-3.5 w-3.5" />
                重连中...
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
                <span className="text-xs">详情</span>
              </Button>
            )}
          </div>
        </header>

        {/* 消息流 */}
        <div className="flex-1 overflow-hidden">
          <Thread />
        </div>

        {/* 输入区 */}
        <WritingComposer />
      </div>

      {/* 右侧详情面板 */}
      {showDetail && <DetailPanel onClose={() => setShowDetail(false)} />}
    </div>
  );
}

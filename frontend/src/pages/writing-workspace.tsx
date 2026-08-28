/**
 * 未来写作工作台：全局导航 | 文档主舞台 | 运行摘要 | 详情分页 | 对话停靠。
 * 运行资源与布局偏好是两套独立状态，任何事件都不能替用户展开或切换面板。
 */
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Menu, PanelRightOpen, RefreshCw } from "lucide-react";
import { Sidebar } from "@/components/sidebar/sidebar";
import { DetailPanel } from "@/components/sidebar/detail-panel";
import { Thread } from "@/components/assistant-ui/thread";
import { ConversationDock } from "@/components/assistant-ui/conversation-dock";
import { WritingComposer, type WritingComposerHandle } from "@/components/composer/writing-composer";
import { DocumentSurface } from "@/components/document/document-surface";
import { RevisionDiff } from "@/components/document/revision-diff";
import { RunSummaryStrip } from "@/components/runtime/run-summary-strip";
import { Button } from "@/components/ui/button";
import { PulseIndicator } from "@/components/animation";
import { useAgentStore } from "@/stores/agent-store";
import { useAuthStore } from "@/stores/auth-store";
import { useBillingStore } from "@/stores/billing-store";
import { useWorkflowStore } from "@/stores/workflow-store";
import { useWritingRuntimeStore } from "@/stores/writing-runtime-store";
import { useWorkspaceLayoutStore } from "@/stores/workspace-layout-store";
import { useAgentWebSocket } from "@/hooks/use-agent-websocket";
import { useKeyboardShortcuts } from "@/hooks/use-keyboard-shortcuts";
import type { RevisionSet } from "@/lib/writing-runtime-types";
import { cn } from "@/lib/utils";

function currentDeviceId(): string {
  const key = "lumin-writing-device-id";
  const existing = localStorage.getItem(key);
  if (existing) return existing;
  const created = crypto.randomUUID?.() ?? `browser-${Date.now()}`;
  localStorage.setItem(key, created);
  return created;
}

export function WritingWorkspace() {
  const [mobileSidebarOpen, setMobileSidebarOpen] = useState(false);
  const [pendingRevision, setPendingRevision] = useState<RevisionSet | null>(null);
  const composerRef = useRef<WritingComposerHandle | null>(null);
  const { connected } = useAgentWebSocket();

  const sessions = useAgentStore((state) => state.sessions);
  const activeSessionId = useAgentStore((state) => state.activeSessionId);
  const loadSessions = useAgentStore((state) => state.loadSessions);
  const connectWS = useAgentStore((state) => state.connectWS);
  const session = sessions.find((item) => item.id === activeSessionId);
  const token = useAuthStore((state) => state.token);
  const user = useAuthStore((state) => state.user);

  const billingBalance = useBillingStore((state) => state.balance);
  const loadBalance = useBillingStore((state) => state.loadBalance);
  const finalArticle = useWorkflowStore((state) => state.finalArticle);
  const workflowStatus = useWorkflowStore((state) => state.runStatus);

  const runtimeDocument = useWritingRuntimeStore((state) => state.document);
  const versions = useWritingRuntimeStore((state) => state.versions);
  const run = useWritingRuntimeStore((state) => state.run);
  const nodeStatuses = useWritingRuntimeStore((state) => state.nodeStatuses);
  const provisionalDeltas = useWritingRuntimeStore((state) => state.provisionalDeltas);
  const quality = useWritingRuntimeStore((state) => state.quality);
  const runtimeError = useWritingRuntimeStore((state) => state.error);
  const loadDocument = useWritingRuntimeStore((state) => state.loadDocument);
  const loadRun = useWritingRuntimeStore((state) => state.loadRun);
  const refreshRunEvents = useWritingRuntimeStore((state) => state.refreshRunEvents);
  const controlRun = useWritingRuntimeStore((state) => state.controlRun);

  const globalSidebar = useWorkspaceLayoutStore((state) => state.globalSidebar);
  const detailPanel = useWorkspaceLayoutStore((state) => state.detailPanel);
  const conversationPanel = useWorkspaceLayoutStore((state) => state.conversationPanel);
  const setGlobalSidebar = useWorkspaceLayoutStore((state) => state.setGlobalSidebar);
  const setDetailPanel = useWorkspaceLayoutStore((state) => state.setDetailPanel);
  const setConversationPanel = useWorkspaceLayoutStore((state) => state.setConversationPanel);
  const setLayoutScope = useWorkspaceLayoutStore((state) => state.setScope);

  const governedVersion = useMemo(() => {
    return versions.find((item) => item.document.version_id === runtimeDocument?.current_version_id)?.document
      ?? versions[versions.length - 1]?.document
      ?? null;
  }, [runtimeDocument?.current_version_id, versions]);

  const legacyDraft = useMemo(() => {
    if (finalArticle?.content) return finalArticle.content;
    const assistant = session?.messages.slice().reverse().find((message) => message.role === "assistant");
    return assistant?.parts
      .filter((part): part is Extract<typeof part, { type: "text" }> => part.type === "text")
      .map((part) => part.text)
      .join("\n") ?? "";
  }, [finalArticle?.content, session?.messages]);

  const documentId = runtimeDocument?.document_id ?? session?.id ?? "new";
  const title = runtimeDocument?.title ?? finalArticle?.title ?? session?.title ?? "未命名文档";

  useEffect(() => { void loadSessions(); void loadBalance(); }, [loadBalance, loadSessions]);

  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const requestedDocument = params.get("document");
    const requestedRun = params.get("run");
    if (requestedDocument) void loadDocument(requestedDocument, token ?? undefined);
    if (requestedRun) void loadRun(requestedRun, token ?? undefined);
  }, [loadDocument, loadRun, token]);

  useEffect(() => {
    if (!run?.run_id) return;
    const sync = () => void refreshRunEvents(run.run_id, token ?? undefined);
    sync();
    const timer = window.setInterval(sync, 2000);
    return () => window.clearInterval(timer);
  }, [refreshRunEvents, run?.run_id, token]);

  useEffect(() => {
    setLayoutScope({
      userId: user?.userId ?? "guest",
      deviceId: currentDeviceId(),
      workspaceId: "writing-desk",
      documentId,
    });
  }, [documentId, setLayoutScope, user?.userId]);

  useKeyboardShortcuts({
    onToggleSidebar: () => window.innerWidth < 768
      ? setMobileSidebarOpen((value) => !value)
      : setGlobalSidebar(globalSidebar === "expanded" ? "collapsed" : "expanded"),
    onToggleDetail: () => setDetailPanel(detailPanel === "collapsed" ? (window.innerWidth < 1280 ? "drawer" : "expanded") : "collapsed"),
    onFocusInput: () => composerRef.current?.focusTextarea(),
    onEscape: () => {
      if (mobileSidebarOpen) setMobileSidebarOpen(false);
      else if (detailPanel !== "collapsed") setDetailPanel("collapsed");
    },
  });

  const handleReconnect = useCallback(() => connectWS(), [connectWS]);
  const handleRunControl = useCallback((action: "pause" | "resume" | "cancel") => {
    if (run) void controlRun(run.run_id, action, token ?? undefined);
  }, [controlRun, run, token]);

  return (
    <div className="governed-workspace">
      {mobileSidebarOpen && <button className="workspace-scrim md:hidden" onClick={() => setMobileSidebarOpen(false)} aria-label="关闭导航" />}
      <div className={cn("workspace-global-sidebar", mobileSidebarOpen && "workspace-global-sidebar-open")}>
        <Sidebar collapsed={globalSidebar === "collapsed"} onToggle={() => setGlobalSidebar(globalSidebar === "expanded" ? "collapsed" : "expanded")} />
      </div>

      <section className="workspace-center">
        <header className="workspace-toolbar">
          <div className="flex min-w-0 items-center gap-2">
            <button onClick={() => setMobileSidebarOpen(true)} className="workspace-icon-button md:hidden" aria-label="打开全局导航"><Menu className="h-4 w-4" /></button>
            <div className="min-w-0"><p className="workspace-eyebrow">WRITING WORKSPACE</p><h2>{title}</h2></div>
          </div>
          <div className="flex items-center gap-2">
            {billingBalance && billingBalance.point_balance > 0 && <span className="workspace-balance">{Math.floor(billingBalance.point_balance)} 积分</span>}
            {!connected && <button className="workspace-connection" onClick={handleReconnect}><PulseIndicator status="paused" size="sm" ring={false} /><span>重新连接</span><RefreshCw className="h-3 w-3" /></button>}
            {detailPanel === "collapsed" && <Button variant="ghost" size="sm" aria-label="打开详情面板" onClick={() => setDetailPanel(window.innerWidth < 1280 ? "drawer" : "expanded")} className="gap-1.5"><PanelRightOpen className="h-4 w-4" /><span className="hidden sm:inline">详情</span></Button>}
          </div>
        </header>

        <RunSummaryStrip run={run} nodeStatuses={nodeStatuses} quality={quality} legacyStatus={workflowStatus === "idle" ? session?.status : workflowStatus} onControl={handleRunControl} />
        {runtimeError && <div className="runtime-error" role="alert">{runtimeError}</div>}

        <div className="workspace-document-region">
          <DocumentSurface
            title={title}
            version={governedVersion}
            legacyDraft={legacyDraft}
            provisionalDeltas={provisionalDeltas}
            qualityState={quality?.quality_state ?? versions[versions.length - 1]?.quality_state}
            onRevisionSet={setPendingRevision}
          />
          <RevisionDiff revisionSet={pendingRevision} />
        </div>

        <ConversationDock state={conversationPanel} onStateChange={setConversationPanel} statusText={run?.status === "running" ? "运行中，可继续补充要求" : "修改合约、解释决策与控制执行"}>
          <div className="conversation-thread"><Thread variant="dock" /></div>
          <WritingComposer ref={composerRef} compact />
        </ConversationDock>
      </section>

      {detailPanel !== "collapsed" && (
        <div className={cn("workspace-detail", detailPanel === "drawer" && "workspace-detail-drawer")} data-panel-state={detailPanel}>
          <button className="workspace-detail-scrim" onClick={() => setDetailPanel("collapsed")} aria-label="关闭详情" />
          <DetailPanel governed onClose={() => setDetailPanel("collapsed")} />
        </div>
      )}
    </div>
  );
}

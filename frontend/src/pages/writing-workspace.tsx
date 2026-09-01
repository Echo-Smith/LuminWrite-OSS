/**
 * 未来写作工作台：全局导航 | Codex 式内联对话 | 连续文档纸面 | 详情分页。
 * 运行资源与布局偏好是两套独立状态，任何事件都不能替用户展开或切换面板。
 */
import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent as ReactKeyboardEvent, type PointerEvent as ReactPointerEvent } from "react";
import { Menu, PanelRightOpen, RefreshCw } from "lucide-react";
import { Sidebar } from "@/components/sidebar/sidebar";
import { DetailPanel } from "@/components/sidebar/detail-panel";
import { Thread } from "@/components/assistant-ui/thread";
import { WritingComposer, type WritingComposerHandle } from "@/components/composer/writing-composer";
import { DocumentSurface } from "@/components/document/document-surface";
import { RevisionDiff } from "@/components/document/revision-diff";
import { FeedbackBar } from "@/components/feedback/feedback-bar";
import { Button } from "@/components/ui/button";
import { PulseIndicator } from "@/components/animation";
import { useAgentStore } from "@/stores/agent-store";
import { useAuthStore } from "@/stores/auth-store";
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

const DETAIL_WIDTH_MIN = 320;
const DETAIL_WIDTH_MAX = 520;
const DETAIL_RESIZE_VIEWPORT_MIN = 1180;
const DOCUMENT_SAFE_WIDTH = 560;
type WorkspaceStyle = CSSProperties & { "--workspace-detail-width": string };

function clampDetailWidth(width: number, sidebarOpen: boolean): number {
  if (typeof window === "undefined" || window.innerWidth < DETAIL_RESIZE_VIEWPORT_MIN) return width;
  const navigationWidth = sidebarOpen ? 256 : 0;
  const safeMaximum = Math.max(DETAIL_WIDTH_MIN, Math.min(DETAIL_WIDTH_MAX, window.innerWidth - navigationWidth - DOCUMENT_SAFE_WIDTH));
  return Math.min(Math.max(width, DETAIL_WIDTH_MIN), safeMaximum);
}

export function WritingWorkspace() {
  const [sidebarOpen, setSidebarOpen] = useState(() => typeof window === "undefined" || window.innerWidth >= 1024);
  const [detailWidth, setDetailWidth] = useState(360);
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

  const finalArticle = useWorkflowStore((state) => state.finalArticle);

  const runtimeDocument = useWritingRuntimeStore((state) => state.document);
  const versions = useWritingRuntimeStore((state) => state.versions);
  const run = useWritingRuntimeStore((state) => state.run);
  const provisionalDeltas = useWritingRuntimeStore((state) => state.provisionalDeltas);
  const quality = useWritingRuntimeStore((state) => state.quality);
  const runtimeError = useWritingRuntimeStore((state) => state.error);
  const loadDocument = useWritingRuntimeStore((state) => state.loadDocument);
  const loadRun = useWritingRuntimeStore((state) => state.loadRun);
  const refreshRunEvents = useWritingRuntimeStore((state) => state.refreshRunEvents);

  const detailPanel = useWorkspaceLayoutStore((state) => state.detailPanel);
  const composerWidth = useWorkspaceLayoutStore((state) => state.composerWidth);
  const setDetailPanel = useWorkspaceLayoutStore((state) => state.setDetailPanel);
  const setComposerWidth = useWorkspaceLayoutStore((state) => state.setComposerWidth);
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

  const feedbackContext = useMemo(() => {
    if (!session?.traceId) return null;
    for (const message of session.messages.slice().reverse()) {
      if (message.role !== "assistant") continue;
      const feedbackPart = message.parts.slice().reverse().find((part) => part.type === "data" && part.dataType === "feedback");
      if (!feedbackPart || feedbackPart.type !== "data") continue;
      const data = feedbackPart.data as { article?: string; has_feedback?: boolean };
      if (data.article?.trim()) return { traceId: session.traceId, article: data.article, hasFeedback: data.has_feedback };
    }
    return null;
  }, [session?.messages, session?.traceId]);

  useEffect(() => { void loadSessions(); }, [loadSessions]);

  useEffect(() => {
    const desktopQuery = window.matchMedia("(min-width: 1024px)");
    const syncSidebarDefault = (event: MediaQueryListEvent) => setSidebarOpen(event.matches);
    desktopQuery.addEventListener("change", syncSidebarDefault);
    return () => desktopQuery.removeEventListener("change", syncSidebarDefault);
  }, []);

  useEffect(() => {
    const keepDetailWidthSafe = () => setDetailWidth((width) => clampDetailWidth(width, sidebarOpen));
    keepDetailWidthSafe();
    window.addEventListener("resize", keepDetailWidthSafe);
    return () => window.removeEventListener("resize", keepDetailWidthSafe);
  }, [sidebarOpen]);

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
    onToggleSidebar: () => setSidebarOpen((value) => !value),
    onToggleDetail: () => setDetailPanel(detailPanel === "collapsed" ? (window.innerWidth < 768 ? "drawer" : "expanded") : "collapsed"),
    onFocusInput: () => composerRef.current?.focusTextarea(),
    onEscape: () => {
      if (sidebarOpen) setSidebarOpen(false);
      else if (detailPanel !== "collapsed") setDetailPanel("collapsed");
    },
  });

  const handleReconnect = useCallback(() => connectWS(), [connectWS]);
  const resizeDetailFromPointer = (event: ReactPointerEvent<HTMLButtonElement>) => {
    if (!event.currentTarget.hasPointerCapture(event.pointerId)) return;
    setDetailWidth(clampDetailWidth(window.innerWidth - event.clientX, sidebarOpen));
  };
  const resizeDetailFromKeyboard = (event: ReactKeyboardEvent<HTMLButtonElement>) => {
    const increments: Record<string, number> = { ArrowLeft: 16, ArrowRight: -16 };
    if (event.key === "Home") { event.preventDefault(); setDetailWidth(DETAIL_WIDTH_MIN); return; }
    if (event.key === "End") { event.preventDefault(); setDetailWidth(clampDetailWidth(DETAIL_WIDTH_MAX, sidebarOpen)); return; }
    if (!(event.key in increments)) return;
    event.preventDefault();
    setDetailWidth((width) => clampDetailWidth(width + increments[event.key], sidebarOpen));
  };
  const workspaceStyle: WorkspaceStyle = { "--workspace-detail-width": `${detailWidth}px` };
  return (
    <div className="governed-workspace" style={workspaceStyle} data-sidebar-open={sidebarOpen} data-detail-state={detailPanel} data-composer-width={composerWidth}>
      {sidebarOpen && <button className="workspace-scrim lg:hidden" onClick={() => setSidebarOpen(false)} aria-label="关闭导航" />}
      <div className={cn("workspace-global-sidebar", sidebarOpen && "workspace-global-sidebar-open")}>
        <Sidebar
          onClose={() => setSidebarOpen(false)}
          onNavigate={() => { if (window.innerWidth < 1024) setSidebarOpen(false); }}
        />
      </div>

      <section className="workspace-center">
        <header className="workspace-toolbar">
          <div className="flex min-w-0 items-center gap-2">
            {!sidebarOpen && <button onClick={() => setSidebarOpen(true)} className="workspace-icon-button" aria-label="打开全局导航"><Menu className="h-4 w-4" /></button>}
            <div className="min-w-0"><h2>{title}</h2></div>
          </div>
          <div className="flex items-center gap-2">
            {!connected && <button className="workspace-connection" onClick={handleReconnect}><PulseIndicator status="paused" size="sm" ring={false} /><span>重新连接</span><RefreshCw className="h-3 w-3" /></button>}
            {detailPanel === "collapsed" && <Button variant="ghost" size="icon" aria-label="打开详情面板" title="打开详情" onClick={() => setDetailPanel(window.innerWidth < 768 ? "drawer" : "expanded")} className="workspace-icon-button"><PanelRightOpen className="h-4 w-4" /></Button>}
          </div>
        </header>

        {runtimeError && <div className="runtime-error" role="alert">{runtimeError}</div>}

        <div className="workspace-document-region">
          <DocumentSurface
            title={title}
            version={governedVersion}
            legacyDraft={legacyDraft}
            provisionalDeltas={provisionalDeltas}
            qualityState={quality?.quality_state ?? versions[versions.length - 1]?.quality_state}
            onRevisionSet={setPendingRevision}
            beforePaper={session?.messages.length ? <Thread variant="flow" /> : undefined}
            afterPaper={feedbackContext ? (
              <FeedbackBar traceId={feedbackContext.traceId} article={feedbackContext.article} hasFeedback={feedbackContext.hasFeedback} />
            ) : undefined}
          />
          <RevisionDiff revisionSet={pendingRevision} />
        </div>

      </section>

      <aside className="workspace-sidecar" data-panel-state={detailPanel} aria-label="写作控制栏">
        {detailPanel === "expanded" && (
          <button
            className="workspace-detail-resizer"
            role="separator"
            aria-orientation="vertical"
            aria-label="调整详情栏宽度"
            aria-valuemin={DETAIL_WIDTH_MIN}
            aria-valuemax={DETAIL_WIDTH_MAX}
            aria-valuenow={detailWidth}
            title="拖动调整详情栏宽度"
            onPointerDown={(event) => event.currentTarget.setPointerCapture(event.pointerId)}
            onPointerMove={resizeDetailFromPointer}
            onPointerUp={(event) => event.currentTarget.releasePointerCapture(event.pointerId)}
            onKeyDown={resizeDetailFromKeyboard}
          />
        )}
        <div className="workspace-sidecar-main">
          <div
            className={cn("workspace-detail", detailPanel === "drawer" && "workspace-detail-drawer")}
            data-panel-state={detailPanel}
            aria-hidden={detailPanel === "collapsed"}
          >
            <button className="workspace-detail-scrim" onClick={() => setDetailPanel("collapsed")} aria-label="关闭详情" />
            <DetailPanel governed onClose={() => setDetailPanel("collapsed")} />
          </div>
        </div>
      </aside>

      <div className={cn("workspace-composer-layer", composerWidth === "compact" && "workspace-composer-layer-compact")} aria-label="写作输入">
        <WritingComposer
          ref={composerRef}
          compact={composerWidth === "compact"}
          floating
          onToggleWidth={() => setComposerWidth(composerWidth === "wide" ? "compact" : "wide")}
        />
      </div>
    </div>
  );
}

/**
 * 未来写作工作台：全局导航 | Codex 式内联对话 | 连续文档纸面 | 详情分页。
 * 运行资源与布局偏好是两套独立状态，任何事件都不能替用户展开或切换面板。
 * 研究综述（research_review）的面板在运行激活时出现在文档区域上方。
 */
import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent as ReactKeyboardEvent, type PointerEvent as ReactPointerEvent } from "react";
import { BookOpenText, ChevronDown, Menu, PanelRightOpen, RefreshCw } from "lucide-react";
import { Sidebar } from "@/components/sidebar/sidebar";
import { DetailPanel } from "@/components/sidebar/detail-panel";
import { Thread } from "@/components/assistant-ui/thread";
import { WritingComposer, type WritingComposerHandle } from "@/components/composer/writing-composer";
import { DocumentSurface } from "@/components/document/document-surface";
import { RevisionDiff } from "@/components/document/revision-diff";
import { FeedbackBar } from "@/components/feedback/feedback-bar";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { PulseIndicator } from "@/components/animation";
import { useAgentStore } from "@/stores/agent-store";
import { useAuthStore } from "@/stores/auth-store";
import { useWorkflowStore } from "@/stores/workflow-store";
import { useWritingRuntimeStore } from "@/stores/writing-runtime-store";
import { pendingGate, researchSliceActive, type ResearchSlice } from "@/stores/research-slice";
import { useWorkspaceLayoutStore } from "@/stores/workspace-layout-store";
import { useAgentWebSocket } from "@/hooks/use-agent-websocket";
import { useKeyboardShortcuts } from "@/hooks/use-keyboard-shortcuts";
import { ResearchProgress } from "@/components/writing/research-progress";
import { ResearchGatePanel } from "@/components/writing/research-gate-panel";
import { ResearchEvidencePanel } from "@/components/writing/research-evidence-panel";
import { ResearchQualityGateCard } from "@/components/writing/research-quality-gate-card";
import { CitationMarker, findCitationMarkers } from "@/components/writing/research-citation-popover";
import type { CitationRenderContextValue } from "@/components/writing/research-citation-popover";
import {
  EVIDENCE_REPORT_ARTIFACT_ID,
  VALIDATION_DETAILS_ARTIFACT_ID,
  buildBibliographyNumbers,
  fetchEvidencePack,
  fetchRunArtifactContent,
  getLastResearchSpec,
  qualityGatePauseGuidance,
  type EvidenceReportView,
  type ResearchCitationIndex,
  type ResearchEvidencePack,
  type ResearchValidationDetailsView,
} from "@/lib/research-api";
import type { DocumentNode } from "@/lib/writing-runtime-types";
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

/** 从正式文档版本树收集纯文本，用于扫描 [@ev_xxx] 引用标记。 */
function collectDocumentText(node: DocumentNode | null | undefined): string {
  if (!node) return "";
  let text = typeof node.text === "string" ? node.text : "";
  for (const child of node.children ?? []) text += collectDocumentText(child);
  return text;
}

/**
 * 研究综述工作台：进度投影 + 待确认 gate + 证据包 + 草稿引用核对。
 * 只在研究切片活跃时渲染；数据以 GET 兜底（refreshResearch）+ 事件流合并。
 */
function ResearchWorkbench({ runId }: { runId: string }) {
  const research: ResearchSlice = useWritingRuntimeStore((state) => state.research);
  const applyGateView = useWritingRuntimeStore((state) => state.applyGateView);
  const artifacts = useWritingRuntimeStore((state) => state.artifacts);
  const nodeStatuses = useWritingRuntimeStore((state) => state.nodeStatuses);
  const runStatus = useWritingRuntimeStore((state) => state.run?.status ?? null);
  const provisionalDeltas = useWritingRuntimeStore((state) => state.provisionalDeltas);
  const versions = useWritingRuntimeStore((state) => state.versions);

  const [evidenceOpen, setEvidenceOpen] = useState(true);
  const [citations, setCitations] = useState<ResearchCitationIndex | null>(null);
  const [pack, setPack] = useState<ResearchEvidencePack | null>(null);
  const [evidenceReport, setEvidenceReport] = useState<EvidenceReportView | null>(null);
  const [validationDetails, setValidationDetails] = useState<ResearchValidationDetailsView | null>(null);

  const packRef = research.progress?.pack_ref ?? null;
  const gate = pendingGate(research);
  const decidedGates = Object.values(research.gates).filter((item) => item.status !== "pending");
  // 阅读上限：运行合同投影（GET research 的 spec.max_papers）优先，回退启动表单值。
  const maxPapers = research.spec?.max_papers ?? getLastResearchSpec()?.max_papers ?? null;
  const packArtifactId = packRef?.artifact_id ?? null;

  useEffect(() => {
    if (!packRef || !packArtifactId) { setPack(null); return; }
    let cancelled = false;
    fetchEvidencePack(runId, packRef)
      .then((content) => { if (!cancelled) setPack(content); })
      .catch(() => { if (!cancelled) setPack(null); });
    return () => { cancelled = true; };
  }, [runId, packArtifactId]);

  const citationArtifactId = useMemo(
    () => artifacts.find((artifact) => artifact.artifact_type === "research_citation_index")?.artifact_id ?? "art_citation_index_demo",
    [artifacts],
  );

  useEffect(() => {
    let cancelled = false;
    fetchRunArtifactContent(runId, citationArtifactId)
      .then((content) => {
        if (!cancelled && (content as ResearchCitationIndex)?.schema_version === "research-citation-index/1") setCitations(content as ResearchCitationIndex);
      })
      .catch(() => { /* 引用索引不可用时不阻塞工作台 */ });
    return () => { cancelled = true; };
  }, [runId, citationArtifactId]);

  // 质量门校验产物：evidence_report（blocker issues）与 research_validation_details
  // （可检查发现 + bibliography 编号投影）。加载失败不阻塞工作台。
  useEffect(() => {
    let cancelled = false;
    fetchRunArtifactContent(runId, EVIDENCE_REPORT_ARTIFACT_ID)
      .then((content) => { if (!cancelled) setEvidenceReport(content as EvidenceReportView); })
      .catch(() => { /* 无校验产物（尚未跑到质量门） */ });
    fetchRunArtifactContent(runId, VALIDATION_DETAILS_ARTIFACT_ID)
      .then((content) => { if (!cancelled) setValidationDetails(content as ResearchValidationDetailsView); })
      .catch(() => { /* 同上 */ });
    return () => { cancelled = true; };
  }, [runId]);

  // 质量门暂停引导：paused + node_quality failed（EVIDENCE_INVALID 语义）才出现。
  const qualityGuidance = qualityGatePauseGuidance({
    runStatus,
    nodeStatuses,
    reportIssues: evidenceReport?.issues ?? null,
    validationFindings: validationDetails?.findings ?? null,
    serverErrorCode: evidenceReport?.passed === false ? "EVIDENCE_INVALID" : null,
  });

  // 纸面内联引用上下文：编号来自 T07 bibliography 顺序投影，缺省按出现顺序本地编号。
  const citationContext: CitationRenderContextValue | null = useMemo(() => {
    if (!citations) return null;
    return { index: citations, numbers: buildBibliographyNumbers(validationDetails) };
  }, [citations, validationDetails]);

  const provisionalText = Object.values(provisionalDeltas).join("");
  const draftText = provisionalText || collectDocumentText(versions[versions.length - 1]?.document.root ?? null);
  const markers = findCitationMarkers(draftText);

  return (
    <section className="research-workbench" aria-label="研究综述工作台">
      <div className="research-workbench-grid">
        <div className="research-workbench-main">
          <ResearchProgress runId={runId} slice={research} maxPapers={maxPapers} />
          {qualityGuidance && <ResearchQualityGateCard guidance={qualityGuidance} />}
          {gate && (
            <ResearchGatePanel runId={runId} gate={gate} pack={pack} onGateUpdated={applyGateView} />
          )}
          {!gate && !qualityGuidance && decidedGates.length > 0 && (
            <p className="research-workbench-decided">已确认 {decidedGates.length} 个确认点（运行按计划继续）。</p>
          )}
          <Collapsible open={evidenceOpen} onOpenChange={setEvidenceOpen}>
            <CollapsibleTrigger asChild>
              <button className="research-workbench-toggle" aria-expanded={evidenceOpen}>
                <BookOpenText className="h-3.5 w-3.5" />
                <span>来源与证据包</span>
                <ChevronDown className={cn("h-4 w-4 opacity-50 transition-transform", evidenceOpen && "rotate-180")} />
              </button>
            </CollapsibleTrigger>
            <CollapsibleContent>
              <ResearchEvidencePanel runId={runId} packRef={packRef} />
            </CollapsibleContent>
          </Collapsible>
        </div>

        <aside className="research-workbench-citations" aria-label="草稿引用核对">
          <h4 className="text-xs font-semibold">草稿引用核对（[@ev_xxx]）</h4>
          {citations && markers.length > 0 ? (
            <ul className="research-citation-list">
              {markers.map((marker, index) => {
                const citation = citations.citations.find((item) => item.evidence_id === marker);
                const displayNumber = citationContext?.numbers?.[marker] ?? index + 1;
                return (
                  <li key={marker}>
                    <CitationMarker index={citations} evidenceId={marker} ordinal={displayNumber} />
                    {citation ? (
                      <span className="min-w-0">
                        <strong className="block truncate text-xs">{citation.paper_title}</strong>
                        <small className="block text-[10px] text-muted-foreground">
                          {citation.evidence_scope === "abstract" ? "摘要证据 · " : "全文证据 · "}页码 {citation.page}
                          {citation.partial ? " · 部分覆盖" : ""}
                        </small>
                      </span>
                    ) : (
                      <span className="text-xs text-destructive">引用索引中不存在 {marker}</span>
                    )}
                  </li>
                );
              })}
            </ul>
          ) : (
            <p className="text-xs text-muted-foreground">草稿中出现 [@ev_xxx] 标记后，可在此核对原文摘录、页码与覆盖范围。</p>
          )}
        </aside>
      </div>
    </section>
  );
}

/**
 * 纸面内联引用上下文（T09）：引用索引加载后，正文中的 [@ev_xxx] 在渲染时
 * 转换为上标编号链接（编号来自 T07 bibliography 顺序，缺省本地编号）；
 * 模型层数据不改写。非研究上下文（无索引）时为 null，正文原样渲染。
 */
function useCitationSurfaceContext(): CitationRenderContextValue | null {
  const artifacts = useWritingRuntimeStore((state) => state.artifacts);
  const [citations, setCitations] = useState<ResearchCitationIndex | null>(null);
  const [validationDetails, setValidationDetails] = useState<ResearchValidationDetailsView | null>(null);

  const runId = useWritingRuntimeStore((state) => state.run?.run_id ?? null);
  const citationArtifactId = useMemo(
    () => artifacts.find((artifact) => artifact.artifact_type === "research_citation_index")?.artifact_id ?? "art_citation_index_demo",
    [artifacts],
  );

  useEffect(() => {
    if (!runId) { setCitations(null); setValidationDetails(null); return; }
    let cancelled = false;
    fetchRunArtifactContent(runId, citationArtifactId)
      .then((content) => {
        if (!cancelled && (content as ResearchCitationIndex)?.schema_version === "research-citation-index/1") setCitations(content as ResearchCitationIndex);
        else if (!cancelled) setCitations(null);
      })
      .catch(() => { if (!cancelled) setCitations(null); });
    fetchRunArtifactContent(runId, VALIDATION_DETAILS_ARTIFACT_ID)
      .then((content) => {
        if (!cancelled && (content as ResearchValidationDetailsView)?.schema_version === "research-validation-details/1") setValidationDetails(content as ResearchValidationDetailsView);
        else if (!cancelled) setValidationDetails(null);
      })
      .catch(() => { if (!cancelled) setValidationDetails(null); });
    return () => { cancelled = true; };
  }, [runId, citationArtifactId]);

  return useMemo(() => {
    if (!citations) return null;
    return { index: citations, numbers: buildBibliographyNumbers(validationDetails) };
  }, [citations, validationDetails]);
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
  const research = useWritingRuntimeStore((state) => state.research);
  const loadDocument = useWritingRuntimeStore((state) => state.loadDocument);
  const loadRun = useWritingRuntimeStore((state) => state.loadRun);
  const refreshRunEvents = useWritingRuntimeStore((state) => state.refreshRunEvents);
  const refreshResearch = useWritingRuntimeStore((state) => state.refreshResearch);

  const detailPanel = useWorkspaceLayoutStore((state) => state.detailPanel);
  const composerWidth = useWorkspaceLayoutStore((state) => state.composerWidth);
  const setDetailPanel = useWorkspaceLayoutStore((state) => state.setDetailPanel);
  const setComposerWidth = useWorkspaceLayoutStore((state) => state.setComposerWidth);
  const setLayoutScope = useWorkspaceLayoutStore((state) => state.setScope);

  const citationSurfaceContext = useCitationSurfaceContext();

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

  // 研究综述：以 GET 为准的兜底轮询（断线重连/刷新恢复后由 GET 重建待确认页面）。
  // 每次载入运行先做一次 GET 探测；确认切片活跃后才持续轮询。
  const researchActive = researchSliceActive(research);
  const researchRunId = research.runId;
  useEffect(() => {
    if (!run?.run_id) return;
    if (!researchActive && researchRunId === run.run_id && researchRunId !== null) return;
    const sync = () => void refreshResearch(run.run_id, token ?? undefined);
    sync();
    if (!researchActive) return;
    const timer = window.setInterval(sync, 4000);
    return () => window.clearInterval(timer);
  }, [refreshResearch, researchActive, researchRunId, run?.run_id, token]);

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

        {researchActive && run?.run_id && <ResearchWorkbench runId={run.run_id} />}

        <div className="workspace-document-region">
          <DocumentSurface
            title={title}
            version={governedVersion}
            legacyDraft={legacyDraft}
            provisionalDeltas={provisionalDeltas}
            qualityState={quality?.quality_state ?? versions[versions.length - 1]?.quality_state}
            onRevisionSet={setPendingRevision}
            citationContext={citationSurfaceContext}
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

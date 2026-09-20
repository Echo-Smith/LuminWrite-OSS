/**
 * 未来写作工作台：全局导航 | Codex 式内联对话 | 连续文档纸面 | 详情分页。
 * 运行资源与布局偏好是两套独立状态，任何事件都不能替用户展开或切换面板。
 * 研究综述（research_review）的面板在运行激活时出现在文档区域上方。
 */
import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent as ReactKeyboardEvent, type PointerEvent as ReactPointerEvent } from "react";
import { useLocation, useNavigate, useParams } from "react-router-dom";
import { BookOpenText, ChevronDown, Menu, PanelLeftClose, PanelRightClose, PanelRightOpen, RefreshCw } from "lucide-react";
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
import { useWritingRuntimeStore } from "@/stores/writing-runtime-store";
import { useAuthStore } from "@/stores/auth-store";
import { useSettingsStore } from "@/stores/settings-store";
import { useWritingBg } from "@/hooks/use-writing-bg";
import { useWorkflowStore } from "@/stores/workflow-store";
import { pendingGate, researchSliceActive, type ResearchSlice } from "@/stores/research-slice";
import { useWorkspaceLayoutStore, COMPOSER_WIDTH_MIN, COMPOSER_WIDTH_MAX } from "@/stores/workspace-layout-store";
import { Lumi, type LumiState } from "@/components/lumi/lumi";
import { useRunEventsSSE } from "@/hooks/use-run-events-sse";
import { useKeyboardShortcuts } from "@/hooks/use-keyboard-shortcuts";
import { ResearchProgress, researchPhaseLabel } from "@/components/writing/research-progress";
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
type ComposerLayerStyle = CSSProperties & { left?: string };

function clampDetailWidth(width: number, sidebarOpen: boolean): number {
  if (typeof window === "undefined" || window.innerWidth < DETAIL_RESIZE_VIEWPORT_MIN) return width;
  const navigationWidth = sidebarOpen ? 224 : 0;
  const safeMaximum = Math.max(DETAIL_WIDTH_MIN, Math.min(DETAIL_WIDTH_MAX, window.innerWidth - navigationWidth - DOCUMENT_SAFE_WIDTH));
  return Math.min(Math.max(width, DETAIL_WIDTH_MIN), safeMaximum);
}

/** 输入区拖拽宽度收敛：不越过全局侧栏，左右各留 16px 边距 */
function clampComposerWidth(width: number, sidebarOpen: boolean): number {
  if (typeof window === "undefined") return width;
  const navigationWidth = sidebarOpen ? 224 : 0;
  const safeMaximum = Math.max(COMPOSER_WIDTH_MIN, Math.min(COMPOSER_WIDTH_MAX, window.innerWidth - navigationWidth - 32));
  return Math.min(Math.max(width, COMPOSER_WIDTH_MIN), safeMaximum);
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
      {/* Lumi 研究指示：检索阅读=思考圆点，撰写=摆笔，失败=断墨 */}
      {(() => {
        const phase = research.progress?.phase ?? null;
        const state: LumiState =
          phase === "failed" ? "error"
          : phase === "writing" ? "writing"
          : phase && phase !== "pending" ? "thinking"
          : "idle";
        const show = state !== "idle";
        return show ? (
          <div className="flex items-center gap-2 px-1 pb-1 text-xs text-muted-foreground">
            <Lumi state={state} size={16} />
            <span>{phase === "failed" ? "研究运行失败" : `Lumi 正在${researchPhaseLabel(phase ?? "")}`}</span>
          </div>
        ) : null;
      })()}
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
  const [pendingRevision, setPendingRevision] = useState<RevisionSet | null>(null);
  const composerRef = useRef<WritingComposerHandle | null>(null);
  const composerLayerRef = useRef<HTMLDivElement | null>(null);
  // SSE connection for governed run events
  const runId = useWritingRuntimeStore((state) => state.run?.run_id ?? null);
  useRunEventsSSE(runId);
  const connected = useWritingRuntimeStore((state) => state.wsConnected);

  const sessions = useWritingRuntimeStore((state) => state.sessions);
  const activeSessionId = useWritingRuntimeStore((state) => state.activeSessionId);
  const loadSessions = useWritingRuntimeStore((state) => state.loadSessions);
  const connectWS = useWritingRuntimeStore((state) => state.connectWS);
  const switchSession = useWritingRuntimeStore((state) => state.switchSession);
  const createSession = useWritingRuntimeStore((state) => state.createSession);
  const sessionsLoaded = useWritingRuntimeStore((state) => state.sessionsLoaded);

  // ── 会话路由：/write/:sessionId（仅登录用户生效，ProtectedRoute 包裹）──
  const { sessionId: sessionIdParam } = useParams<{ sessionId?: string }>();
  const location = useLocation();
  const navigate = useNavigate();
  // 未知/已删除的 URL 会话只自动新建一次，避免抖动循环
  const autoCreatedForRef = useRef<string | null>(null);
  // 已处理过的 URL 参数（见 URL → 会话 effect 的防打架说明）
  const lastUrlSessionRef = useRef<string | null>(null);

  // URL → 会话：深链打开与刷新恢复（DB 会话 id=trace_id，与 conversationId 一致）。
  // 只响应"新的" URL 参数（深链/刷新/浏览器前进后退）：记录已处理过的参数，
  // 避免与下面的"会话 → URL"effect 打架——否则点击左侧另一会话时，地址栏
  // 还停在旧 trace，本 effect 会立刻把活跃会话切回去（表现为切换无效、
  // URL 永远不变、侧栏状态点闪烁）。
  useEffect(() => {
    if (!sessionIdParam || !sessionsLoaded) return;
    const active = sessions.find((s) => s.id === activeSessionId);
    const activeTarget = active ? active.conversationId ?? active.traceId ?? active.id : null;
    if (sessionIdParam === activeTarget) {
      lastUrlSessionRef.current = sessionIdParam;
      return;
    }
    // 同一个 URL 参数已经处理过（会话切换由"会话 → URL"负责回写地址栏）
    if (lastUrlSessionRef.current === sessionIdParam) return;
    const match = sessions.find(
      (s) => s.id === sessionIdParam || s.conversationId === sessionIdParam || s.traceId === sessionIdParam,
    );
    if (match) {
      lastUrlSessionRef.current = sessionIdParam;
      if (match.id !== activeSessionId) switchSession(match.id);
      return;
    }
    if (autoCreatedForRef.current !== sessionIdParam) {
      autoCreatedForRef.current = sessionIdParam;
      createSession();
    }
  }, [sessionIdParam, sessionsLoaded, sessions, activeSessionId, switchSession, createSession]);

  // 会话 → URL：稳定键优先 conversationId（首轮 trace），新会话落 trace 后自动替换。
  // 只在 /write 路由内同步——个人中心（/profile）等工作台承载页也渲染本组件，
  // 不能把用户弹走（否则个人中心一闪而过）。
  // 必须等会话列表就绪（sessionsLoaded）：否则首帧 activeSessionId=null 会把
  // 深链 /write/trace_xxx 立刻弹回 /write，刷新恢复与深链自动建会话全部失效。
  useEffect(() => {
    if (!location.pathname.startsWith("/write")) return;
    if (!sessionsLoaded) return;
    const active = sessions.find((s) => s.id === activeSessionId);
    const target = active ? active.conversationId ?? active.traceId ?? active.id : null;
    const expected = target ? `/write/${target}` : "/write";
    if (location.pathname !== expected) navigate(expected, { replace: true });
  }, [activeSessionId, sessions, sessionsLoaded, location.pathname, navigate]);
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
  const composerCustomWidth = useWorkspaceLayoutStore((state) => state.composerCustomWidth);
  const setDetailPanel = useWorkspaceLayoutStore((state) => state.setDetailPanel);
  const setComposerWidth = useWorkspaceLayoutStore((state) => state.setComposerWidth);
  const setComposerCustomWidth = useWorkspaceLayoutStore((state) => state.setComposerCustomWidth);
  const setLayoutScope = useWorkspaceLayoutStore((state) => state.setScope);
  // 面板宽度持久化在工作区布局偏好中；保留函数式更新签名以兼容拖拽/键盘调整逻辑
  const detailWidth = useWorkspaceLayoutStore((state) => state.detailWidth);
  const setDetailWidth = useCallback((value: number | ((prev: number) => number)) => {
    const current = useWorkspaceLayoutStore.getState().detailWidth;
    const next = typeof value === "function" ? value(current) : value;
    useWorkspaceLayoutStore.getState().setDetailWidth(next);
  }, []);

  // ── 输入区拖拽调宽（与详情面板同款交互）──
  const [composerResizing, setComposerResizing] = useState(false);
  // 收窄判定：compact 预设，或用户拖出了自定义宽度（放大按钮负责恢复最大）
  const composerNarrow = composerWidth === "compact" || composerCustomWidth != null;
  const resizeComposerFromPointer = (event: ReactPointerEvent<HTMLButtonElement>) => {
    if (!event.currentTarget.hasPointerCapture(event.pointerId)) return;
    setComposerCustomWidth(clampComposerWidth(window.innerWidth - event.clientX - 16, sidebarOpen));
  };
  const resizeComposerFromKeyboard = (event: ReactKeyboardEvent<HTMLButtonElement>) => {
    // 把手在左缘：向左拖/按 = 变宽，向右 = 变窄
    const increments: Record<string, number> = { ArrowLeft: 16, ArrowRight: -16 };
    if (event.key === "Home") { event.preventDefault(); setComposerCustomWidth(clampComposerWidth(COMPOSER_WIDTH_MIN, sidebarOpen)); return; }
    if (event.key === "End") { event.preventDefault(); setComposerCustomWidth(clampComposerWidth(COMPOSER_WIDTH_MAX, sidebarOpen)); return; }
    if (!(event.key in increments)) return;
    event.preventDefault();
    const current = composerCustomWidth
      ?? (composerWidth === "compact" ? COMPOSER_WIDTH_MIN : clampComposerWidth(COMPOSER_WIDTH_MAX, sidebarOpen));
    setComposerCustomWidth(clampComposerWidth(current + increments[event.key], sidebarOpen));
  };
  // 放大/收窄按钮：从收窄或自定义宽度一键恢复最大宽度；已是最大则收窄到 compact 预设
  const handleToggleComposerWidth = useCallback(() => {
    if (composerWidth === "wide" && composerCustomWidth == null) {
      setComposerWidth("compact");
    } else {
      setComposerWidth("wide");
      setComposerCustomWidth(null);
    }
  }, [composerWidth, composerCustomWidth, setComposerWidth, setComposerCustomWidth]);
  // 自定义宽度时覆盖预设 left（right 恒为 16px）：left = 视口宽 - 手动宽度 - 右边距
  const composerLayerStyle: ComposerLayerStyle | undefined = composerCustomWidth != null
    ? { left: `max(calc(var(--workspace-sidebar-width) + 16px), calc(100% - ${composerCustomWidth + 16}px))` }
    : undefined;

  const citationSurfaceContext = useCitationSurfaceContext();

  // 写作区底色（个人中心-自定义，localStorage 持久化）
  const [writingBg] = useWritingBg();

  // 工具栏 Lumi 指示：合并 agent 会话状态与编辑部工作流状态
  const agentMode = useSettingsStore((state) => state.agentMode);
  const wfRunStatus = useWorkflowStore((state) => state.runStatus);
  const sessionStatus = session?.status ?? "idle";
  const awaitingInput = session?.awaitInputAt != null;
  const lumiToolbarState: LumiState = useMemo(() => {
    if (agentMode === "editorial") {
      if (wfRunStatus === "failed") return "error";
      if (wfRunStatus === "paused") return "paused";
      if (wfRunStatus === "planning" || wfRunStatus === "created") return "thinking";
      if (wfRunStatus === "running") return "writing";
      return "idle";
    }
    if (sessionStatus === "error") return "error";
    if (sessionStatus === "paused") return "paused";
    if (awaitingInput) return "thinking";
    if (sessionStatus === "running") return "writing";
    return "idle";
  }, [agentMode, wfRunStatus, sessionStatus, awaitingInput]);
  const lumiToolbarTitle =
    lumiToolbarState === "writing" ? "写作进行中"
    : lumiToolbarState === "thinking" ? "等待确认"
    : lumiToolbarState === "paused" ? "已暂停"
    : lumiToolbarState === "error" ? "运行失败"
    : "";

  const governedVersion = useMemo(() => {
    return versions.find((item) => item.document.version_id === runtimeDocument?.current_version_id)?.document
      ?? versions[versions.length - 1]?.document
      ?? null;
  }, [runtimeDocument?.current_version_id, versions]);

  const legacyDraft = useMemo(() => {
    if (finalArticle?.content) return finalArticle.content;
    // chat 模式的回复会进 article/消息流，但它是对话不是草稿——
    // 不再渲染为"兼容预览"，避免开场白冒充文档
    if (session?.intent === "chat") return "";
    const assistant = session?.messages.slice().reverse().find((message) => message.role === "assistant");
    return assistant?.parts
      .filter((part): part is Extract<typeof part, { type: "text" }> => part.type === "text")
      .map((part) => part.text)
      .join("\n") ?? "";
  }, [finalArticle?.content, session?.intent, session?.messages]);

  // 标题优先级：用户命名（custom_title）> 运行时文档标题 > 文章标题 > 会话标题
  const title = session?.customTitle ?? runtimeDocument?.title ?? finalArticle?.title ?? session?.title ?? "未命名文档";

  // title-bar 点击重命名（仅当前会话已有 trace 时可改）
  const [editingTitle, setEditingTitle] = useState(false);
  const [titleDraft, setTitleDraft] = useState("");
  const renameSession = useWritingRuntimeStore((s) => s.renameSession);
  const canRename = Boolean(session?.traceId);
  const startRename = () => {
    if (!canRename) return;
    setTitleDraft(title);
    setEditingTitle(true);
  };
  const commitRename = async () => {
    const next = titleDraft.trim();
    setEditingTitle(false);
    if (!next || !session?.traceId || next === title) return;
    await renameSession(session.traceId, next);
  };

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

  // 悬浮详情卡片底部让位：实测 composer 高度写入 CSS 变量，
  // 卡片 bottom 随输入区宽度模式（compact/wide）自动抬降。
  useEffect(() => {
    const layer = composerLayerRef.current;
    const root = layer?.closest<HTMLElement>(".governed-workspace") ?? null;
    if (!layer || !root) return;
    const apply = () => {
      root.style.setProperty("--workspace-composer-clearance", `${Math.round(layer.offsetHeight)}px`);
    };
    apply();
    const observer = new ResizeObserver(apply);
    observer.observe(layer);
    return () => observer.disconnect();
  }, []);

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
    });
  }, [setLayoutScope, user?.userId]);

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
    <div className="governed-workspace" style={workspaceStyle} data-sidebar-open={sidebarOpen} data-detail-state={detailPanel} data-composer-width={composerNarrow ? "compact" : "wide"} data-writing-bg={writingBg}>
      {sidebarOpen && <button className="workspace-scrim lg:hidden" onClick={() => setSidebarOpen(false)} aria-label="关闭导航" />}
      <div className={cn("workspace-global-sidebar", sidebarOpen && "workspace-global-sidebar-open")}>
        <Sidebar
          onNavigate={() => { if (window.innerWidth < 1024) setSidebarOpen(false); }}
        />
      </div>

      <section className="workspace-center">
        <header className="workspace-toolbar">
          <div className="flex min-w-0 items-center gap-2">
            {sidebarOpen && (
              <button
                onClick={() => setSidebarOpen(false)}
                className="flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-ui hover:bg-accent hover:text-foreground"
                title="关闭侧栏"
                aria-label="关闭侧栏"
              >
                <PanelLeftClose className="h-4 w-4" />
              </button>
            )}
            {!sidebarOpen && <button onClick={() => setSidebarOpen(true)} className="workspace-icon-button" aria-label="打开全局导航"><Menu className="h-4 w-4" /></button>}
            <div className="min-w-0">
              {editingTitle ? (
                <input
                  autoFocus
                  value={titleDraft}
                  maxLength={128}
                  onChange={(e) => setTitleDraft(e.target.value)}
                  onBlur={() => void commitRename()}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") { e.preventDefault(); e.currentTarget.blur(); }
                    if (e.key === "Escape") { setEditingTitle(false); }
                  }}
                  aria-label="重命名会话"
                  className="h-7 w-full min-w-0 rounded-md border border-border bg-background px-2 text-sm font-semibold outline-none focus:ring-2 focus:ring-ring/40"
                />
              ) : (
                <h2
                  onClick={startRename}
                  title={canRename ? "点击修改标题" : undefined}
                  className={cn(canRename && "cursor-text hover:bg-accent/60 rounded-md px-1 -mx-1 transition-ui")}
                >
                  {title}
                </h2>
              )}
            </div>
            {/* Lumi 运行指示：思考（含等提纲确认）/书写/出错；完成后闪一次星星 */}
            {lumiToolbarState !== "idle" && (
              <span key={`lumi-${lumiToolbarState}`} className="anim-fade-scale flex items-center" title={lumiToolbarTitle}>
                <Lumi state={lumiToolbarState} size={18} />
              </span>
            )}
          </div>
          <div className="flex items-center gap-2">
            {!connected && <button className="workspace-connection" onClick={handleReconnect}><PulseIndicator status="paused" size="sm" ring={false} /><span>重新连接</span><RefreshCw className="h-3 w-3" /></button>}
            {detailPanel !== "drawer" && (
              <Button
                variant="ghost"
                size="icon"
                aria-label={detailPanel === "expanded" ? "收起详情面板" : "固定悬浮详情面板"}
                title={detailPanel === "expanded" ? "收起" : "固定悬浮详情"}
                onClick={() => setDetailPanel(detailPanel === "expanded" ? "collapsed" : (window.innerWidth < 768 ? "drawer" : "expanded"))}
                className="workspace-icon-button"
              >
                {detailPanel === "expanded" ? <PanelRightClose className="h-4 w-4" /> : <PanelRightOpen className="h-4 w-4" />}
              </Button>
            )}
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
            onPolishSelection={(text) => composerRef.current?.beginPolish(text)}
            citationContext={citationSurfaceContext}
            beforePaper={session?.messages.length ? <Thread variant="flow" /> : undefined}
            conversationStarted={Boolean(session?.messages.some((message) => message.role === "user"))}
            afterPaper={feedbackContext ? (
              <FeedbackBar traceId={feedbackContext.traceId} article={feedbackContext.article} hasFeedback={feedbackContext.hasFeedback} />
            ) : undefined}
          />
          <RevisionDiff revisionSet={pendingRevision} />
          {/* 底部过渡遮罩：配合悬浮输入区遮住缝隙文字，随写作区底色联动 */}
          <div className="workspace-bottom-fade" aria-hidden="true" />
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

      <div
        ref={composerLayerRef}
        className={cn(
          "workspace-composer-layer",
          composerWidth === "compact" && composerCustomWidth == null && "workspace-composer-layer-compact",
        )}
        style={composerLayerStyle}
        data-resizing={composerResizing || undefined}
        aria-label="写作输入"
      >
        {/* 左缘拖拽把手：与详情面板同款的宽度调整交互（桌面端显示） */}
        <button
          className="workspace-composer-resizer"
          role="separator"
          aria-orientation="vertical"
          aria-label="调整输入区宽度"
          aria-valuemin={COMPOSER_WIDTH_MIN}
          aria-valuemax={COMPOSER_WIDTH_MAX}
          aria-valuenow={composerCustomWidth ?? (composerNarrow ? COMPOSER_WIDTH_MIN : COMPOSER_WIDTH_MAX)}
          title="拖动调整输入区宽度"
          onPointerDown={(event) => {
            event.currentTarget.setPointerCapture(event.pointerId);
            setComposerResizing(true);
          }}
          onPointerMove={resizeComposerFromPointer}
          onPointerUp={(event) => {
            event.currentTarget.releasePointerCapture(event.pointerId);
            setComposerResizing(false);
          }}
          onKeyDown={resizeComposerFromKeyboard}
        />
        <WritingComposer
          ref={composerRef}
          compact={composerNarrow}
          floating
          onToggleWidth={handleToggleComposerWidth}
        />
      </div>
    </div>
  );
}

import { useEffect, useState } from "react";
import { BookOpenText, Boxes, Clock3, FileClock, Files, ListTree, ShieldCheck } from "lucide-react";
import type { DetailTab } from "@/stores/workspace-layout-store";
import type { DocumentVersion, RuntimeRun, StoredDocumentVersion, UserQualitySummary, WritingArtifactEventPayload, WritingEvent } from "@/lib/writing-runtime-types";
import type { ToolCallPart } from "@/stores/agent-store";
import { useAgentStore } from "@/stores/agent-store";
import { QualityStatus } from "@/components/quality/quality-status";
import { QualityFinding } from "@/components/quality/quality-finding";
import { CompactStepTimeline } from "@/components/tools/compact-step-timeline";
import { toast } from "@/stores/toast-store";
import { cn } from "@/lib/utils";

/**
 * 详情面板三个合并 tab：
 * - 文档（outline）：当前版本大纲 + 版本历史（governed 版本账本；WS 会话回退到
 *   /sessions/{traceId}/versions）。
 * - 材料（materials）：governed 产物登记；WS 会话回退显示检索素材统计。
 * - 运行（run）：governed 运行记录 + 事件账本 + 质量验收（原质量 tab 并入）；
 *   WS 会话回退显示步骤时间线（原流程信息的运行侧投影）。
 *
 * 写作路径接入（审计 2026-09-08）：
 * - research_review / governed fast·sourced·strict_research：writing-runtime-store
 *   全量数据（run/events/artifacts/quality/versions）。
 * - WS fast·guided（agent-store 会话）：governed 数据为空时按上述回退渲染，
 *   保证普通快写在合并后的面板上不再全空。
 */
const TABS: Array<{ value: DetailTab; label: string; icon: typeof ListTree }> = [
  { value: "outline", label: "文档", icon: ListTree },
  { value: "materials", label: "材料", icon: Files },
  { value: "run", label: "运行", icon: Clock3 },
];

function collectSections(node: DocumentVersion["root"], result: Array<{ id: string; title: string }> = []) {
  if (node.type === "section") result.push({ id: node.block_id ?? `section-${result.length + 1}`, title: String(node.attrs.title ?? `第 ${result.length + 1} 节`) });
  node.children.forEach((child) => collectSections(child, result));
  return result;
}

export interface LegacySessionDetail {
  /** WS 会话 traceId（/sessions/{id}/versions 的版本历史来源）；governed-only 时为 null。 */
  traceId: string | null;
  /** 最后一条 assistant 消息的 tool-call 步骤（运行 tab 的 WS 回退投影）。 */
  steps: ToolCallPart[];
  running: boolean;
}

interface RunDetailTabsProps {
  activeTab: DetailTab;
  onTabChange: (tab: DetailTab) => void;
  version: DocumentVersion | null;
  versions: StoredDocumentVersion[];
  run: RuntimeRun | null;
  events: WritingEvent[];
  artifacts: WritingArtifactEventPayload[];
  quality: UserQualitySummary | null;
  legacy?: LegacySessionDetail;
}

export function RunDetailTabs({ activeTab, onTabChange, version, versions, run, events, artifacts, quality, legacy }: RunDetailTabsProps) {
  const sections = version ? collectSections(version.root) : [];
  const governedRunData = Boolean(run || events.length || versions.length || artifacts.length || quality);
  return (
    <div className="run-detail-tabs">
      <div className="run-detail-tablist" role="tablist" aria-label="文档详情">
        {TABS.map((tab) => (
          <button key={tab.value} role="tab" aria-selected={activeTab === tab.value} title={tab.label} onClick={() => onTabChange(tab.value)} className={cn("run-detail-tab", activeTab === tab.value && "run-detail-tab-active")}>
            <tab.icon className="h-3.5 w-3.5" /><span>{tab.label}</span>
          </button>
        ))}
      </div>
      <div className="run-detail-content" role="tabpanel">
        {activeTab === "outline" && (
          <section>
            <PanelHeading icon={BookOpenText} title="文档大纲" meta={`${sections.length} 节`} />
            {sections.length ? <ol className="detail-list">{sections.map((section, index) => <li key={section.id}><span>{String(index + 1).padStart(2, "0")}</span><p>{section.title}</p></li>)}</ol> : <EmptyDetail text="正式文档形成后，大纲会从文档结构中读取。" />}
            {/* 版本历史（原版本 tab 并入）：governed 版本账本优先，WS 会话回退 */}
            {versions.length ? (
              <>
                <PanelHeading icon={FileClock} title="版本历史" meta={`${versions.length} 版`} />
                <div className="version-ledger">{versions.slice().reverse().map((item) => <div key={item.document.version_id}><span>V{item.sequence}</span><div><p>{item.document.version_id}</p><small>{new Date(item.created_at).toLocaleString("zh-CN")} · {item.quality_state}</small></div></div>)}</div>
              </>
            ) : legacy?.traceId ? (
              <>
                <PanelHeading icon={FileClock} title="版本历史" meta="会话" />
                <SessionVersionHistory traceId={legacy.traceId} />
              </>
            ) : (
              <EmptyDetail text="正式提交后，版本、基础版本和质量状态会一并记录。" />
            )}
          </section>
        )}
        {activeTab === "materials" && (
          <section>
            <PanelHeading icon={Files} title="材料与产物" meta={`${artifacts.length} 项`} />
            {artifacts.length ? <div className="space-y-2">{artifacts.map((artifact) => <div className="detail-record" key={artifact.artifact_id}><Boxes className="h-4 w-4" /><div><p>{artifact.artifact_type}</p><span>{artifact.lifecycle} · {artifact.artifact_id}</span></div></div>)}</div>
              : legacy?.steps.length ? <LegacyMaterialsHint steps={legacy.steps} />
              : <EmptyDetail text="运行开始后，来源与产物会在这里按类型显示。" />}
          </section>
        )}
        {activeTab === "run" && (
          <section>
            <PanelHeading icon={Clock3} title="运行记录" meta={run ? run.status : legacy?.steps.length ? "会话进行中" : "未启动"} />
            {run && <div className="detail-metadata"><Metadata label="运行" value={run.run_id} /><Metadata label="计划" value={run.active_plan_id || "待激活"} /><Metadata label="审批" value={run.approval_status || run.approval_mode} /><Metadata label="事件序号" value={String(run.last_event_sequence)} /></div>}
            {events.length ? <div className="event-ledger">{events.slice().reverse().map((event) => <div key={event.sequence}><span>{event.sequence}</span><p>{eventLabel(event.type)}</p><time>{new Date(event.timestamp).toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit", second: "2-digit" })}</time></div>)}</div>
              : !run && legacy?.steps.length ? <CompactStepTimeline parts={legacy.steps} isRunning={legacy.running} />
              : <EmptyDetail text="运行开始后，这里显示可恢复的正式事件账本。" />}
            {/* 质量验收（原质量 tab 并入）：governed 路径有摘要与发现；WS 路径暂无质量报告 */}
            <PanelHeading icon={ShieldCheck} title="质量验收" meta={quality?.assurance_satisfied ? "已满足" : governedRunData ? "待验证" : "会话"} />
            {quality || governedRunData ? (
              <>
                <QualityStatus summary={quality} />
                <div className="mt-3 space-y-2">{quality?.key_findings.map((finding) => <QualityFinding key={finding.finding_id} finding={finding} />)}</div>
                {!quality?.key_findings.length && <EmptyDetail text="当前没有需要普通用户处理的质量问题。完整报告保留在审计视图。" />}
              </>
            ) : (
              <EmptyDetail text="当前写作方式未产生质量报告。" />
            )}
          </section>
        )}
      </div>
    </div>
  );
}

/** WS 会话的材料回退：从检索类步骤统计素材条数，引导到对话区查看详情。 */
function LegacyMaterialsHint({ steps }: { steps: ToolCallPart[] }) {
  const count = (names: string[]) => steps.filter((step) => names.includes(step.toolName)).reduce((sum, step) => {
    const result = step.result as { results?: unknown[]; items?: unknown[] } | undefined;
    const list = Array.isArray(result?.items) ? result!.items : Array.isArray(result?.results) ? result!.results : null;
    return sum + (list?.length ?? 0);
  }, 0);
  const searched = count(["search", "search_web"]);
  const kb = count(["search_knowledge"]);
  if (!searched && !kb) return <EmptyDetail text="本次会话没有检索素材；粘贴素材在对话区查看。" />;
  return (
    <div className="detail-metadata">
      {searched > 0 && <Metadata label="联网检索" value={`${searched} 条`} />}
      {kb > 0 && <Metadata label="素材库检索" value={`${kb} 条`} />}
    </div>
  );
}

/** WS 会话版本历史（原 detail-panel VersionHistory，接口为 /sessions/{traceId}/versions）。 */
interface SessionArticleVersion {
  version_id: string;
  article_title?: string;
  version_note?: string;
  created_at: string;
}

export function SessionVersionHistory({ traceId }: { traceId: string }) {
  const [versions, setVersions] = useState<SessionArticleVersion[]>([]);
  const [loadingVersion, setLoadingVersion] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    fetch(`/api/v2/sessions/${traceId}/versions`)
      .then((res) => res.json())
      .then((json) => { if (!cancelled && json.success) setVersions(json.data?.versions ?? []); })
      .catch(() => { /* silent fail */ });
    return () => { cancelled = true; };
  }, [traceId]);

  if (!versions.length) return <EmptyDetail text="本次会话暂无历史版本。" />;

  const handleRestore = async (versionId: string) => {
    setLoadingVersion(versionId);
    const ok = await useAgentStore.getState().loadArticleVersion(traceId, versionId);
    setLoadingVersion(null);
    if (ok) toast.info("已切换版本", "文章内容已更新为该版本");
    else toast.error("加载失败", "无法获取该版本内容");
  };

  return (
    <div className="version-ledger">
      {versions.map((version, index) => (
        <button key={version.version_id} onClick={() => handleRestore(version.version_id)} disabled={loadingVersion === version.version_id}
          className={cn("run-detail-session-version", loadingVersion === version.version_id && "run-detail-session-version-loading")}>
          <span>V{versions.length - index}</span>
          <div>
            <p>{version.article_title || `版本 ${versions.length - index}`}</p>
            <small>{version.version_note || "自动保存"} · {new Date(version.created_at).toLocaleString("zh-CN")}</small>
          </div>
        </button>
      ))}
    </div>
  );
}

function PanelHeading({ icon: Icon, title, meta }: { icon: typeof ListTree; title: string; meta: string }) {
  return <div className="detail-heading"><span><Icon className="h-4 w-4" />{title}</span><small>{meta}</small></div>;
}
function EmptyDetail({ text }: { text: string }) { return <p className="detail-empty">{text}</p>; }
function Metadata({ label, value }: { label: string; value: string }) { return <div><span>{label}</span><code>{value}</code></div>; }
function eventLabel(type: WritingEvent["type"]) {
  const labels: Record<WritingEvent["type"], string> = {
    "writing.run.status": "运行状态变化", "writing.document.delta": "正文正在形成", "writing.document.committed": "文档版本已提交",
    "writing.node.status": "写作步骤变化", "writing.artifact.created": "产物已登记", "writing.quality.updated": "质量状态更新", "writing.ledger.event": "治理事件记录",
  };
  return labels[type];
}

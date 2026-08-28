import { BookOpenText, Boxes, Clock3, FileClock, Files, ListTree, ShieldCheck } from "lucide-react";
import type { DetailTab } from "@/stores/workspace-layout-store";
import type { DocumentVersion, RuntimeRun, StoredDocumentVersion, UserQualitySummary, WritingArtifactEventPayload, WritingEvent } from "@/lib/writing-runtime-types";
import { QualityStatus } from "@/components/quality/quality-status";
import { QualityFinding } from "@/components/quality/quality-finding";
import { cn } from "@/lib/utils";

const TABS: Array<{ value: DetailTab; label: string; icon: typeof ListTree }> = [
  { value: "outline", label: "大纲", icon: ListTree },
  { value: "materials", label: "材料", icon: Files },
  { value: "run", label: "运行", icon: Clock3 },
  { value: "quality", label: "质量", icon: ShieldCheck },
  { value: "versions", label: "版本", icon: FileClock },
];

function collectSections(node: DocumentVersion["root"], result: Array<{ id: string; title: string }> = []) {
  if (node.type === "section") result.push({ id: node.block_id ?? `section-${result.length + 1}`, title: String(node.attrs.title ?? `第 ${result.length + 1} 节`) });
  node.children.forEach((child) => collectSections(child, result));
  return result;
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
}

export function RunDetailTabs({ activeTab, onTabChange, version, versions, run, events, artifacts, quality }: RunDetailTabsProps) {
  const sections = version ? collectSections(version.root) : [];
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
          </section>
        )}
        {activeTab === "materials" && (
          <section>
            <PanelHeading icon={Files} title="材料与产物" meta={`${artifacts.length} 项`} />
            {artifacts.length ? <div className="space-y-2">{artifacts.map((artifact) => <div className="detail-record" key={artifact.artifact_id}><Boxes className="h-4 w-4" /><div><p>{artifact.artifact_type}</p><span>{artifact.lifecycle} · {artifact.artifact_id}</span></div></div>)}</div> : <EmptyDetail text="Task11 接入材料产物后，来源与快照会在这里按类型显示。" />}
          </section>
        )}
        {activeTab === "run" && (
          <section>
            <PanelHeading icon={Clock3} title="运行记录" meta={run ? run.status : "未启动"} />
            {run && <div className="detail-metadata"><Metadata label="运行" value={run.run_id} /><Metadata label="计划" value={run.active_plan_id || "待激活"} /><Metadata label="审批" value={run.approval_status || run.approval_mode} /><Metadata label="事件序号" value={String(run.last_event_sequence)} /></div>}
            {events.length ? <div className="event-ledger">{events.slice().reverse().map((event) => <div key={event.sequence}><span>{event.sequence}</span><p>{eventLabel(event.type)}</p><time>{new Date(event.timestamp).toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit", second: "2-digit" })}</time></div>)}</div> : <EmptyDetail text="运行开始后，这里显示可恢复的正式事件账本。" />}
          </section>
        )}
        {activeTab === "quality" && (
          <section>
            <PanelHeading icon={ShieldCheck} title="质量验收" meta={quality?.assurance_satisfied ? "已满足" : "待验证"} />
            <QualityStatus summary={quality} />
            <div className="mt-3 space-y-2">{quality?.key_findings.map((finding) => <QualityFinding key={finding.finding_id} finding={finding} />)}</div>
            {!quality?.key_findings.length && <EmptyDetail text="当前没有需要普通用户处理的质量问题。完整报告保留在审计视图。" />}
          </section>
        )}
        {activeTab === "versions" && (
          <section>
            <PanelHeading icon={FileClock} title="版本历史" meta={`${versions.length} 版`} />
            {versions.length ? <div className="version-ledger">{versions.slice().reverse().map((item) => <div key={item.document.version_id}><span>V{item.sequence}</span><div><p>{item.document.version_id}</p><small>{new Date(item.created_at).toLocaleString("zh-CN")} · {item.quality_state}</small></div></div>)}</div> : <EmptyDetail text="正式提交后，版本、基础版本和质量状态会一并记录。" />}
          </section>
        )}
      </div>
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

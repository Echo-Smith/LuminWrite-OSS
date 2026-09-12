/**
 * 引用气泡与纸面内联引用渲染 — 草稿中 [@ev_xxx] 标记只在渲染时转换为上标
 * 编号链接（模型层数据不改写，原文标记保留在数据里）；点击/hover 打开既有
 * 引用气泡显示原文摘录、来源论文、页码/范围。编号优先取 T07 bibliography
 * 顺序（research_validation_details 投影），没有编号数据时按出现顺序本地编号。
 * 引用索引中不存在的标记渲染为警示样式（不静默消失）。
 */
import { createContext, useContext, useMemo, useState, type MouseEvent as ReactMouseEvent } from "react";
import { BookMarked, TriangleAlert } from "lucide-react";
import { CITATION_MARKER_PATTERN, findCitationMarkers, splitCitationSegments, type CitationSegment, type ResearchCitationIndex } from "@/lib/research-api";
import { cn } from "@/lib/utils";

/** 渲染层引用上下文：DocumentSurface 提供，document-block 的 text 节点消费。 */
export interface CitationRenderContextValue {
  index: ResearchCitationIndex | null;
  /** evidence_id → 显示编号（T07 bibliography 顺序）；null 表示本地编号。 */
  numbers: Record<string, number> | null;
}

export const CitationRenderContext = createContext<CitationRenderContextValue | null>(null);

/**
 * 渲染含 [@ev_xxx] 标记的文本：
 * - 命中引用索引 → 可交互上标编号链接（点击/悬停打开引用气泡）；
 * - 索引存在但证据缺失 → 警示样式，原标记可见（不静默消失）；
 * - 索引尚未加载 → 只做编号上标，不臆断缺失；
 * - 无索引上下文（citationContext 为 null）→ 原样渲染文本。
 */
export function InlineCitationText({
  text,
  index,
  numbers,
  className,
}: {
  text: string;
  index: ResearchCitationIndex | null;
  numbers: Record<string, number> | null;
  className?: string;
}) {
  const segments = useMemo(
    () => splitCitationSegments(text, {
      numbers,
      knownEvidenceIds: index ? new Set(index.citations.map((item) => item.evidence_id)) : null,
    }),
    [text, index, numbers],
  );
  return (
    <span className={className}>
      {segments.map((segment, position) =>
        segment.kind === "text"
          ? <span key={`t${position}`}>{segment.text}</span>
          : <InlineCitationNode key={`c${position}`} segment={segment} index={index} />,
      )}
    </span>
  );
}

function InlineCitationNode({ segment, index }: { segment: Extract<CitationSegment, { kind: "citation" }>; index: ResearchCitationIndex | null }) {
  const citation = index?.citations.find((item) => item.evidence_id === segment.evidenceId);
  if (index && citation) {
    return <sup className="research-citation-inline"><CitationMarker index={index} evidenceId={segment.evidenceId} ordinal={segment.number} /></sup>;
  }
  if (index) {
    // 索引已加载而证据缺失：警示样式，原标记保留可见。
    return (
      <sup className="research-citation-inline">
        <span
          className="research-citation research-citation-missing"
          role="note"
          aria-label={`警示：引用标记 [@${segment.evidenceId}] 没有对应证据`}
          title={`[@${segment.evidenceId}] 在引用索引中不存在——请核对草稿引用或补充证据`}
        >
          <TriangleAlert className="h-3 w-3" aria-hidden="true" />[@{segment.evidenceId}]
        </span>
      </sup>
    );
  }
  // 引用索引尚未加载：只渲染编号上标（本地/已有编号），不臆断缺失。
  return (
    <sup className="research-citation-plain" title={`引用标记 [@${segment.evidenceId}]（引用索引尚未加载）`}>
      [{segment.number}]
    </sup>
  );
}

export function CitationMarker({ index, evidenceId, ordinal }: { index: ResearchCitationIndex; evidenceId: string; ordinal: number }) {
  const [open, setOpen] = useState(false);
  const citation = index.citations.find((item) => item.evidence_id === evidenceId);

  if (!citation) {
    return (
      <span
        className="research-citation research-citation-missing"
        role="note"
        aria-label={`警示：引用标记 [@${evidenceId}] 没有对应证据`}
        title={`[@${evidenceId}] 在引用索引中不存在——请核对草稿引用或补充证据`}
      >
        <TriangleAlert className="h-3 w-3" aria-hidden="true" />[@{evidenceId}]
      </span>
    );
  }

  const toggle = (event: ReactMouseEvent<HTMLButtonElement>) => {
    event.preventDefault();
    event.stopPropagation();
    setOpen((value) => !value);
  };

  return (
    <span className="research-citation-anchor">
      <button
        type="button"
        className={cn("research-citation", citation.partial && "research-citation-partial", open && "research-citation-open")}
        onClick={toggle}
        onMouseEnter={() => setOpen(true)}
        onMouseLeave={() => setOpen(false)}
        aria-label={`引用 ${ordinal}：${citation.paper_title}${citation.partial ? "（部分覆盖）" : ""}`}
        aria-expanded={open}
      >
        [{ordinal}]
      </button>
      {open && (
        <span className="research-citation-popover" role="dialog" aria-label={`引用 ${ordinal} 详情`}>
          <header>
            <BookMarked className="h-3.5 w-3.5 shrink-0" />
            <strong>{citation.paper_title}</strong>
          </header>
          <blockquote>「{citation.quote}」</blockquote>
          <footer>
            <span>{citation.evidence_scope === "abstract" ? "摘要证据" : "全文证据"}</span>
            <span>页码/范围：{citation.page}</span>
            <code>{citation.block_id}</code>
          </footer>
          {citation.evidence_scope === "abstract" && (
            <p className="research-citation-warning">该引用来自摘要：不能支撑定量结论，仅可支撑方向性表述。</p>
          )}
          {citation.partial && (
            <p className="research-citation-warning">部分覆盖：该来源为截断阅读（未穷尽全文），相关表述建议保守。</p>
          )}
        </span>
      )}
    </span>
  );
}

/** 文档渲染层（document-block 的 text 节点）消费上下文做渲染时转换。 */
export function CitationAwareText({ text, className }: { text: string; className?: string }) {
  const context = useContext(CitationRenderContext);
  if (!context?.index || !text.includes("[@ev_")) return <>{text}</>;
  return <InlineCitationText text={text} index={context.index} numbers={context.numbers} className={className} />;
}

/** 便捷导出：扫描文本中的引用标记（供测试与草稿侧栏使用）。 */
export { CITATION_MARKER_PATTERN, findCitationMarkers };

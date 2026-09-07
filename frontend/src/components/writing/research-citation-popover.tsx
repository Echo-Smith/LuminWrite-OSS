/**
 * 引用气泡 — 草稿中 [@ev_xxx] 标记 hover/点击显示原文摘录、来源论文、
 * 页码/范围；摘要与部分覆盖明确标注（mock 数据演示）。
 */
import { useState, type MouseEvent as ReactMouseEvent } from "react";
import { BookMarked } from "lucide-react";
import { CITATION_MARKER_PATTERN, findCitationMarkers, type ResearchCitationIndex } from "@/lib/research-api";
import { cn } from "@/lib/utils";

/**
 * 渲染包含 [@ev_xxx] 标记的草稿文本；命中引用索引的标记渲染为可交互的
 * 引用气泡，未命中的标记原样保留（提示草稿与索引不一致）。
 */
export function CitationText({ text, index, className }: { text: string; index: ResearchCitationIndex | null; className?: string }) {
  if (!index) return <span className={className}>{text}</span>;
  const parts: Array<{ key: string; node: React.ReactNode }> = [];
  let cursor = 0;
  let position = 0;
  for (const match of text.matchAll(CITATION_MARKER_PATTERN)) {
    const markerStart = match.index ?? 0;
    if (markerStart > cursor) parts.push({ key: `t${cursor}`, node: text.slice(cursor, markerStart) });
    const evidenceId = match[1];
    position += 1;
    parts.push({ key: `c${evidenceId}-${position}`, node: <CitationMarker key={`m${position}`} index={index} evidenceId={evidenceId} ordinal={position} /> });
    cursor = markerStart + match[0].length;
  }
  if (cursor < text.length) parts.push({ key: `t${cursor}`, node: text.slice(cursor) });
  return <span className={className}>{parts.map((part) => <span key={part.key}>{part.node}</span>)}</span>;
}

export function CitationMarker({ index, evidenceId, ordinal }: { index: ResearchCitationIndex; evidenceId: string; ordinal: number }) {
  const [open, setOpen] = useState(false);
  const citation = index.citations.find((item) => item.evidence_id === evidenceId);

  if (!citation) {
    return <span className="research-citation research-citation-missing" title={`引用索引中不存在 ${evidenceId}`}>[{ordinal}]</span>;
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

/** 便捷导出：扫描文本中的引用标记（供测试与草稿侧栏使用）。 */
export { findCitationMarkers };

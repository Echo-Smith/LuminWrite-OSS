import type { ReactNode } from "react";
import { useRef } from "react";
import { FilePenLine, FileText, Radio } from "lucide-react";
import { DocumentBlock } from "./document-block";
import type { DocumentVersion, QualityState, Revision, RevisionSet } from "@/lib/writing-runtime-types";
import { QUALITY_STATE_COPY } from "@/lib/writing-runtime-types";
import { CitationAwareText, CitationRenderContext, InlineCitationText, type CitationRenderContextValue } from "@/components/writing/research-citation-popover";
import { SelectionLumi } from "@/components/lumi/selection-lumi";

interface DocumentSurfaceProps {
  title: string;
  version: DocumentVersion | null;
  legacyDraft?: string;
  provisionalDeltas?: Record<string, string>;
  qualityState?: QualityState;
  onRevisionSet?: (set: RevisionSet) => void;
  beforePaper?: ReactNode;
  afterPaper?: ReactNode;
  /** 选中正文后点「让 Lumi 润色」回调选中文本 */
  onPolishSelection?: (text: string) => void;
  /**
   * 纸面内联引用上下文（T09）：提供后，正文中的 [@ev_xxx] 标记在渲染时转换为
   * 上标编号链接并打开引用气泡；数据不改写。缺省时正文原样渲染。
   */
  citationContext?: CitationRenderContextValue | null;
}

export function DocumentSurface({
  title,
  version,
  legacyDraft,
  provisionalDeltas = {},
  qualityState = "candidate_draft",
  onRevisionSet,
  beforePaper,
  afterPaper,
  onPolishSelection,
  citationContext = null,
}: DocumentSurfaceProps) {
  const deltas = Object.entries(provisionalDeltas).filter(([, value]) => value.trim());
  const hasDraft = Boolean(version || legacyDraft?.trim());
  const documentBodyRef = useRef<HTMLDivElement | null>(null);
  const handleRevision = (revision: Revision) => {
    if (!version) return;
    onRevisionSet?.({ base_version: version.version_id, revisions: [revision] });
  };

  return (
    <main className="document-stage" aria-label="文档正文">
      {beforePaper && <div className="document-conversation-flow">{beforePaper}</div>}
      {hasDraft ? <article className="document-paper">
        <div className="document-paper-content">
          <header className="document-masthead">
            <div className="flex items-center gap-2 text-[11px] uppercase tracking-[0.18em] text-muted-foreground">
              <FileText className="h-3.5 w-3.5" />
              <span>{version ? `版本 ${version.version_id.replace("ver_", "")}` : "新文档"}</span>
              <span aria-hidden="true">/</span>
              <span>{QUALITY_STATE_COPY[qualityState].label}</span>
            </div>
            <h1>{title || "未命名文档"}</h1>
          </header>

          <div className="document-body" ref={documentBodyRef}>
            {onPolishSelection && <SelectionLumi containerRef={documentBodyRef} onPolish={onPolishSelection} />}
            <CitationRenderContext.Provider value={citationContext}>
              {version ? (
                <DocumentBlock node={version.root} onRevision={onRevisionSet ? handleRevision : undefined} />
              ) : legacyDraft ? (
                <div className="legacy-draft" data-lifecycle="provisional">
                  <div className="legacy-draft-label"><FilePenLine className="h-3.5 w-3.5" />兼容预览 · 尚未提交为文档版本</div>
                  <div className="whitespace-pre-wrap">
                    {citationContext?.index
                      ? <InlineCitationText text={legacyDraft} index={citationContext.index} numbers={citationContext.numbers} />
                      : <CitationAwareText text={legacyDraft} />}
                  </div>
                </div>
              ) : null}
            </CitationRenderContext.Provider>
          </div>

          {deltas.length > 0 && (
            <aside className="provisional-layer" aria-label="正在生成的临时内容" data-lifecycle="provisional">
              <div className="flex items-center gap-2 text-xs font-medium"><Radio className="h-3.5 w-3.5" />正在形成，尚未写入正式版本</div>
              {deltas.map(([blockId, delta]) => <p key={blockId} className="whitespace-pre-wrap">{delta}</p>)}
            </aside>
          )}

        </div>
      </article> : (
        <section className="document-welcome" aria-label="欢迎写作">
          <p className="document-kicker">THE WRITING DESK</p>
          <h2>先描述你要完成的文档</h2>
          <p>从一句要求开始。对话会整理目标、材料和写作步骤，第一版完成后再进入稿件预览。</p>
        </section>
      )}
      {hasDraft && afterPaper && <footer className="document-feedback">{afterPaper}</footer>}
    </main>
  );
}

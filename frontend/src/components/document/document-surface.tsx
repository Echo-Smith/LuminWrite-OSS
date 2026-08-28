import { FilePenLine, FileText, Radio } from "lucide-react";
import { DocumentBlock } from "./document-block";
import type { DocumentVersion, QualityState, Revision, RevisionSet } from "@/lib/writing-runtime-types";
import { QUALITY_STATE_COPY } from "@/lib/writing-runtime-types";

interface DocumentSurfaceProps {
  title: string;
  version: DocumentVersion | null;
  legacyDraft?: string;
  provisionalDeltas?: Record<string, string>;
  qualityState?: QualityState;
  onRevisionSet?: (set: RevisionSet) => void;
}

export function DocumentSurface({
  title,
  version,
  legacyDraft,
  provisionalDeltas = {},
  qualityState = "candidate_draft",
  onRevisionSet,
}: DocumentSurfaceProps) {
  const deltas = Object.entries(provisionalDeltas).filter(([, value]) => value.trim());
  const handleRevision = (revision: Revision) => {
    if (!version) return;
    onRevisionSet?.({ base_version: version.version_id, revisions: [revision] });
  };

  return (
    <main className="document-stage" aria-label="文档正文">
      <article className="document-paper">
        <header className="document-masthead">
          <div className="flex items-center gap-2 text-[11px] uppercase tracking-[0.18em] text-muted-foreground">
            <FileText className="h-3.5 w-3.5" />
            <span>{version ? `版本 ${version.version_id.replace("ver_", "")}` : "新文档"}</span>
            <span aria-hidden="true">/</span>
            <span>{QUALITY_STATE_COPY[qualityState].label}</span>
          </div>
          <h1>{title || "未命名文档"}</h1>
        </header>

        <div className="document-body">
          {version ? (
            <DocumentBlock node={version.root} onRevision={onRevisionSet ? handleRevision : undefined} />
          ) : legacyDraft ? (
            <div className="legacy-draft" data-lifecycle="provisional">
              <div className="legacy-draft-label"><FilePenLine className="h-3.5 w-3.5" />兼容预览 · 尚未提交为文档版本</div>
              <div className="whitespace-pre-wrap">{legacyDraft}</div>
            </div>
          ) : (
            <div className="document-empty-state">
              <p className="document-kicker">THE WRITING DESK</p>
              <h2>先描述你要完成的文档</h2>
              <p>对话将需求整理为任务合约；正文、版本和质量报告会在这里持续形成。</p>
            </div>
          )}
        </div>

        {deltas.length > 0 && (
          <aside className="provisional-layer" aria-label="正在生成的临时内容" data-lifecycle="provisional">
            <div className="flex items-center gap-2 text-xs font-medium"><Radio className="h-3.5 w-3.5" />正在形成，尚未写入正式版本</div>
            {deltas.map(([blockId, delta]) => <p key={blockId} className="whitespace-pre-wrap">{delta}</p>)}
          </aside>
        )}
      </article>
    </main>
  );
}

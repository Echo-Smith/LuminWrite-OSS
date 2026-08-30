import { ArrowRight, CornerDownRight } from "lucide-react";
import type { RevisionSet } from "@/lib/writing-runtime-types";

export function RevisionDiff({ revisionSet }: { revisionSet: RevisionSet | null }) {
  if (!revisionSet) return null;
  return (
    <section className="revision-diff" aria-label="待提交修改">
      <div className="flex items-center justify-between gap-3">
        <span className="flex items-center gap-2 font-medium"><CornerDownRight className="h-3.5 w-3.5" />待验证修改</span>
        <span className="font-mono-sm text-[10px] text-muted-foreground">base {revisionSet.base_version}</span>
      </div>
      {revisionSet.revisions.map((revision, index) => (
        <div key={`${revision.target_block_id ?? revision.parent_block_id}-${index}`} className="mt-2 flex items-start gap-2 text-xs text-muted-foreground">
          <ArrowRight className="mt-0.5 h-3 w-3 shrink-0" />
          <span>{revision.reason} · {revision.semantic_impact}</span>
        </div>
      ))}
    </section>
  );
}

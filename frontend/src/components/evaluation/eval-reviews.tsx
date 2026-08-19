import { ClipboardCheck, EyeOff, Gavel } from "lucide-react";
import type { WABenchReviewItem } from "@/lib/admin-types";
import { ACCEPTANCE_LABELS, ROOT_CAUSE_LABELS, reviewsDisagree } from "@/lib/wabench-eval";
import { EvalEmptyState, EvalSectionHeader, EvalStatus } from "./eval-states";
import { ReviewImportPanel } from "./review-import-panel";
import { ReviewProvenance } from "./review-provenance";

function reviewScoreCells(review: WABenchReviewItem) {
  return [review.taskCompliance, review.sourceFidelity, review.structureReasoning, review.styleConsistency, review.directUsability];
}

export function EvalReviews({ items, onImported, canMutate = false }: { items: WABenchReviewItem[]; onImported: () => Promise<void>; canMutate?: boolean }) {
  const groups = items.reduce<Record<string, WABenchReviewItem[]>>((result, review) => {
    (result[review.outputId] ??= []).push(review); return result;
  }, {});
  return (
    <div className="space-y-7">
      <EvalSectionHeader eyebrow="Human evidence / 05" title="评审结果可以不一致，但不能不可追溯" description="机评、人评和规则检查保持独立。两位人工评审的分歧不会被平均掉，仲裁作为另一条决策记录保留。" />
      <ReviewImportPanel onImported={onImported} disabled={!canMutate} />
      {items.length === 0 ? <EvalEmptyState icon={ClipboardCheck} title="还没有评审记录" description="运行成功后会产生机评；人工评审可以通过上方中文 Excel 表导入。" /> : (
        <div className="space-y-8">{Object.entries(groups).map(([outputId, reviews]) => {
          const disagreement = reviewsDisagree(reviews);
          const arbitration = reviews[0]?.arbitrationStatus;
          return <section key={outputId} className="border-y border-[#161917]/20 dark:border-border">
            <header className="flex flex-col gap-3 bg-[#ebe9e1]/60 px-4 py-3 dark:bg-muted/20 sm:flex-row sm:items-center sm:justify-between"><div><p className="font-mono text-xs">{outputId}</p><p className="mt-1 text-xs text-muted-foreground">case {reviews[0]?.caseId} · run {reviews[0]?.runId}</p></div><div className="flex flex-wrap items-center gap-2"><EvalStatus value={reviews[0]?.outputStatus ?? "unknown"} />{!reviews[0]?.contentAvailable && <span className="inline-flex min-h-7 items-center gap-1.5 rounded-full border border-border bg-background px-2.5 text-xs text-muted-foreground"><EyeOff className="h-3.5 w-3.5" />私有正文未下发</span>}{disagreement && <span className="inline-flex min-h-7 items-center gap-1.5 rounded-full bg-amber-700/10 px-2.5 text-xs font-semibold text-amber-800 dark:text-amber-300"><Gavel className="h-3.5 w-3.5" />{arbitration === "resolved" ? "分歧已仲裁" : "存在分歧·待仲裁"}</span>}</div></header>
            <div className="divide-y divide-border/70">{reviews.map((review) => <article key={review.reviewId} className={`grid gap-5 px-4 py-5 xl:grid-cols-[1.1fr_0.85fr_0.75fr] ${review.isArbitration ? "bg-emerald-700/[0.035]" : ""}`}><ReviewProvenance review={review} /><div><div className="grid grid-cols-5 gap-2">{reviewScoreCells(review).map((score, index) => <div key={index} className="text-center"><p className="font-mono text-lg font-semibold">{score}</p><p className="mt-1 text-[10px] text-muted-foreground">{["任务", "来源", "结构", "风格", "可用"][index]}</p></div>)}</div></div><div className="text-xs"><p><span className="text-muted-foreground">接受：</span>{ACCEPTANCE_LABELS[review.acceptanceLabel]}</p><p className="mt-2"><span className="text-muted-foreground">修改负担：</span>{review.modificationBurden ?? "未知"}</p><p className="mt-2"><span className="text-muted-foreground">根因：</span>{review.primaryRootCause ? ROOT_CAUSE_LABELS[review.primaryRootCause] : "无"}</p>{review.hardFailureIds.length > 0 && <p className="mt-2 text-red-800 dark:text-red-300">硬失败 {review.hardFailureIds.join("、")}</p>}</div></article>)}</div>
          </section>;
        })}</div>
      )}
    </div>
  );
}

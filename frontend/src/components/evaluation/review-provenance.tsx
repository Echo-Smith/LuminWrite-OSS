import { Bot, Gavel, UserRound } from "lucide-react";
import type { WABenchReviewItem } from "@/lib/admin-types";
import { ARBITRATION_LABELS } from "@/lib/wabench-eval";

export function ReviewProvenance({ review }: { review: WABenchReviewItem }) {
  const Icon = review.isArbitration ? Gavel : review.reviewerType === "model" ? Bot : UserRound;
  return (
    <div className="flex min-w-0 items-start gap-3">
      <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full border border-border bg-background"><Icon className="h-4 w-4" aria-hidden="true" /></span>
      <div className="min-w-0"><p className="truncate text-sm font-semibold">{review.reviewerId}</p><p className="mt-0.5 text-xs text-muted-foreground">{review.reviewerRole} · {review.reviewerType} · {review.reviewMethod}</p><p className="mt-1 text-[11px] text-muted-foreground">{new Date(review.reviewedAt).toLocaleString("zh-CN")} · {review.labelSource} · {review.isBlind ? "盲评" : "非盲评"} · {ARBITRATION_LABELS[review.arbitrationStatus]}</p></div>
    </div>
  );
}

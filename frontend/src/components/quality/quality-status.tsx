import { CheckCircle2, CircleDotDashed, ShieldCheck } from "lucide-react";
import type { QualityState, UserQualitySummary } from "@/lib/writing-runtime-types";
import { QUALITY_STATE_COPY } from "@/lib/writing-runtime-types";
import { cn } from "@/lib/utils";

const STATE_STYLES: Record<QualityState, string> = {
  candidate_draft: "quality-candidate",
  accepted_draft: "quality-accepted",
  verified_deliverable: "quality-verified",
};

export function QualityStatus({ summary, compact = false }: { summary: UserQualitySummary | null; compact?: boolean }) {
  const state = summary?.quality_state ?? "candidate_draft";
  const copy = QUALITY_STATE_COPY[state];
  const Icon = state === "verified_deliverable" ? ShieldCheck : state === "accepted_draft" ? CheckCircle2 : CircleDotDashed;
  return (
    <div className={cn("quality-status", STATE_STYLES[state], compact && "quality-status-compact")}>
      <Icon className="h-4 w-4 shrink-0" />
      <div className="min-w-0">
        <div className="font-medium">{copy.label}</div>
        {!compact && <p>{copy.description}</p>}
      </div>
      {summary && !compact && (
        <span className="ml-auto shrink-0 font-mono-sm text-[10px]">
          {summary.achieved_assurance} / {summary.requested_assurance}
        </span>
      )}
    </div>
  );
}

import { AlertCircle, AlertTriangle, Ban, MapPin } from "lucide-react";
import type { QualityFinding as QualityFindingType } from "@/lib/writing-runtime-types";
import { cn } from "@/lib/utils";

export function QualityFinding({ finding }: { finding: QualityFindingType }) {
  const Icon = finding.severity === "BLOCKER" ? Ban : finding.severity === "ERROR" ? AlertCircle : AlertTriangle;
  return (
    <article className={cn("quality-finding", `quality-finding-${finding.severity.toLowerCase()}`)}>
      <div className="flex items-start gap-2">
        <Icon className="mt-0.5 h-4 w-4 shrink-0" />
        <div className="min-w-0 flex-1">
          <div className="flex items-center justify-between gap-2">
            <span className="text-xs font-semibold">{finding.code}</span>
            <span className="text-[10px] uppercase tracking-wide">{finding.severity}</span>
          </div>
          <p className="mt-1 text-xs leading-relaxed">{finding.message}</p>
          {finding.explanation && <p className="mt-1 text-[11px] text-muted-foreground">{finding.explanation}</p>}
          {finding.location?.block_id && (
            <span className="mt-2 inline-flex items-center gap-1 text-[10px] text-muted-foreground">
              <MapPin className="h-3 w-3" />{finding.location.block_id}
            </span>
          )}
        </div>
      </div>
    </article>
  );
}

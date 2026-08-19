import { Boxes, Check, X } from "lucide-react";
import type { WABenchCandidateItem } from "@/lib/admin-types";
import { shortWABenchHash } from "@/lib/wabench-eval";
import { EvalEmptyState, EvalSectionHeader } from "./eval-states";

function manifestValue(manifest: Record<string, unknown>, ...keys: string[]): string {
  for (const key of keys) {
    const value = manifest[key];
    if (typeof value === "string" && value) return value;
  }
  return "未记录";
}

function BooleanState({ value, label }: { value: boolean; label: string }) {
  const Icon = value ? Check : X;
  return <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground"><Icon className={`h-3.5 w-3.5 ${value ? "text-emerald-700" : "text-muted-foreground"}`} />{label} {value ? "开" : "关"}</span>;
}

export function EvalCandidates({ items }: { items: WABenchCandidateItem[] }) {
  return (
    <div className="space-y-7">
      <EvalSectionHeader eyebrow="Immutable manifests / 03" title="比较的不是“某个模型”，而是整套候选版本" description="Prompt、Memory、模型、代码、工具与 feature flag 全部冻结。同一 candidateId 不能覆写为另一套配置。" />
      {items.length === 0 ? <EvalEmptyState icon={Boxes} title="还没有冻结候选" description="运行前必须通过 Admin API 写入完整 manifest；Memory 开启时必须包含 memoryHash。" /> : (
        <div className="space-y-0 border-y border-[#161917]/20 dark:border-border">
          {items.map((item) => {
            const memoryEnabled = item.featureFlags.memoryEnabled === true;
            const sourceGate = item.featureFlags.sourceEvidenceGateEnabled !== false;
            return (
              <article key={item.candidateId} className="grid gap-5 border-b border-border/70 py-5 last:border-b-0 xl:grid-cols-[1.25fr_0.75fr_1fr]">
                <div className="min-w-0"><p className="text-base font-semibold">{item.name}</p><p className="mt-1 truncate font-mono text-xs text-muted-foreground">{item.candidateId}</p><div className="mt-4 flex flex-wrap gap-4"><BooleanState value={sourceGate} label="来源闸门" /><BooleanState value={memoryEnabled} label="风格 Memory" /></div></div>
                <dl className="grid grid-cols-[78px_1fr] gap-x-3 gap-y-2 text-xs"><dt className="text-muted-foreground">Model</dt><dd className="truncate font-mono">{manifestValue(item.modelManifest, "model", "modelName")}</dd><dt className="text-muted-foreground">Provider</dt><dd className="truncate font-mono">{manifestValue(item.modelManifest, "provider")}</dd><dt className="text-muted-foreground">Created</dt><dd>{new Date(item.createdAt).toLocaleString("zh-CN")}</dd></dl>
                <dl className="grid grid-cols-[72px_1fr] gap-x-3 gap-y-2 text-xs"><dt className="text-muted-foreground">Prompt</dt><dd className="font-mono" title={item.promptHash}>{shortWABenchHash(item.promptHash)}</dd><dt className="text-muted-foreground">Memory</dt><dd className="font-mono" title={item.memoryHash}>{shortWABenchHash(item.memoryHash)}</dd><dt className="text-muted-foreground">Code</dt><dd className="font-mono" title={item.codeHash}>{shortWABenchHash(item.codeHash)}</dd></dl>
              </article>
            );
          })}
        </div>
      )}
    </div>
  );
}

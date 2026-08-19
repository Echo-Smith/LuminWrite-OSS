import { Bug, CircleDotDashed } from "lucide-react";
import type { WABenchBadcaseItem } from "@/lib/admin-types";
import { ROOT_CAUSE_LABELS } from "@/lib/wabench-eval";
import { EvalEmptyState, EvalSectionHeader, EvalStatus, PrivacyBadge } from "./eval-states";

function symptomLabel(symptom: Record<string, unknown>): string {
  for (const key of ["symptom", "stage", "code", "type", "message"]) {
    const value = symptom[key];
    if (typeof value === "string" && value) return value;
  }
  return "未命名失败";
}

export function EvalBadcases({ items }: { items: WABenchBadcaseItem[] }) {
  return (
    <div className="space-y-7">
      <EvalSectionHeader eyebrow="Failure ownership / 06" title="先记录症状，再定位根因，最后关闭回归" description="生成、Judge、路由和工具失败不会伪装成普通低分。根因固定为输入、检索、Prompt、Memory、工具、模型和交互七类。" />
      {items.length === 0 ? <EvalEmptyState icon={Bug} title="当前没有 WABench Badcase" description="这只表示现有运行中没有显式失败或根因记录，不代表产品已通过发布门禁。" /> : (
        <div className="overflow-x-auto border-y border-[#161917]/20 dark:border-border"><table className="eval-data-table w-full min-w-[1120px] text-left text-sm"><thead><tr><th>Case / Output</th><th>症状</th><th>根因</th><th>硬失败</th><th>Owner / 修复</th><th>回归</th><th>隐私</th></tr></thead><tbody>{items.map((item) => <tr key={item.outputId}><td><p className="font-mono text-xs">{item.caseId}</p><p className="mt-1 max-w-[220px] truncate font-mono text-[11px] text-muted-foreground">{item.outputId}</p><div className="mt-2"><EvalStatus value={item.outputStatus} /></div></td><td><div className="flex max-w-[250px] flex-wrap gap-1.5">{item.symptoms.length > 0 ? item.symptoms.map((symptom, index) => <span key={index} className="rounded bg-red-700/8 px-2 py-1 text-xs text-red-800 dark:text-red-300">{symptomLabel(symptom)}</span>) : <span className="text-xs text-muted-foreground">评审根因标记</span>}</div></td><td>{item.primaryRootCause ? ROOT_CAUSE_LABELS[item.primaryRootCause] : "待定位"}</td><td className="max-w-[220px] text-xs text-red-800 dark:text-red-300">{item.hardFailureIds.join("、") || "无"}</td><td><p>{item.owner || "未分派"}</p><p className="mt-1 font-mono text-xs text-muted-foreground">{item.fixVersion || "未关联修复版本"}</p></td><td><span className="inline-flex items-center gap-1.5 text-xs"><CircleDotDashed className="h-3.5 w-3.5" />{item.regressionStatus}</span></td><td><PrivacyBadge value={item.privacyLevel === "synthetic" ? "public" : item.privacyLevel === "anonymized" ? "redacted" : "private"} /></td></tr>)}</tbody></table></div>
      )}
    </div>
  );
}

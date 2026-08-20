import { DatabaseZap, ShieldPlus } from "lucide-react";
import { Button } from "@/components/ui/button";
import type { WABenchSuiteItem } from "@/lib/admin-types";
import { PrivacyBadge, EvalEmptyState, EvalSectionHeader, EvalStatus } from "./eval-states";

const partitionLabels: Record<WABenchSuiteItem["partition"], string> = {
  development: "开发集",
  public_holdout: "公开 Holdout",
  private_holdout: "私有 Holdout",
  red_team: "红队",
  live_probe: "线上探针",
};

export function EvalDatasets({ items, onSeedRedTeam, seeding, canMutate = false }: {
  items: WABenchSuiteItem[];
  onSeedRedTeam: () => void;
  seeding: boolean;
  canMutate?: boolean;
}) {
  return (
    <div className="space-y-7">
      <EvalSectionHeader
        eyebrow="Dataset registry / 02"
        title="数据集不是一个名字，而是一份冻结契约"
        description="分区、版本、覆盖、隐私策略和内容哈希共同决定一次运行是否可复现。"
        action={<Button variant="outline" className="min-h-11 gap-2" onClick={onSeedRedTeam} disabled={seeding || !canMutate}><ShieldPlus className="h-4 w-4" />{seeding ? "正在校验红队套件…" : canMutate ? "校验红队套件" : "只读权限"}</Button>}
      />
      {items.length === 0 ? (
        <EvalEmptyState icon={DatabaseZap} title="还没有 WABench 数据集" description="可先创建开发集，或写入独立红队套件。私有 Holdout 正文不会出现在列表 API 中。" action={canMutate ? { label: "写入红队套件", onClick: onSeedRedTeam } : undefined} />
      ) : (
        <div className="overflow-x-auto border-y border-[#161917]/20 dark:border-border">
          <table className="eval-data-table w-full min-w-[980px] text-left text-sm">
            <thead><tr><th>数据集</th><th>分区</th><th>状态</th><th>样本</th><th>任务覆盖</th><th>隐私</th><th>内容哈希</th></tr></thead>
            <tbody>{items.map((item) => (
              <tr key={item.suiteId}>
                <td><p className="font-semibold">{item.name}</p><p className="mt-1 font-mono text-xs text-muted-foreground">{item.suiteId} · {item.version}</p></td>
                <td>{partitionLabels[item.partition]}</td>
                <td><EvalStatus value={item.status} /></td>
                <td className="font-mono tabular-nums">{item.caseCount}</td>
                <td><div className="flex max-w-[260px] flex-wrap gap-1.5">{Object.entries(item.taskCounts).map(([task, count]) => <span key={task} className="rounded bg-muted px-2 py-1 text-xs">{task} {count}</span>)}</div></td>
                <td><PrivacyBadge value={item.privacyLabel} /></td>
                <td className="max-w-[220px] truncate font-mono text-xs text-muted-foreground">{item.contentHash || "未冻结"}</td>
              </tr>
            ))}</tbody>
          </table>
        </div>
      )}
    </div>
  );
}

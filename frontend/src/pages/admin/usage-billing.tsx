/**
 * 用量与计费（合并入口）
 *
 * - 用量统计：Token 用量与成本分布（audit.view）
 * - 计费管理：收入 / 积分 / 订阅 / 套餐管理（billing.view，仅商业版；
 *   开源版后端为 stub，不渲染该标签）
 */
import { AdminTabbedPage } from "@/components/admin";
import { IS_COMMERCIAL } from "@/lib/edition";
import { TokenUsagePage } from "./token-usage";
import { BillingPage } from "./billing";

export function UsageBillingPage() {
  return (
    <AdminTabbedPage
      tabs={[
        { key: "usage", label: "用量统计", permission: "audit.view", content: <TokenUsagePage /> },
        ...(IS_COMMERCIAL
          ? [{ key: "billing", label: "计费管理", permission: "billing.view", content: <BillingPage /> }]
          : []),
      ]}
    />
  );
}

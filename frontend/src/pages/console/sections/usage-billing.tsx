/**
 * 全站用量 — 审计中心 sections
 *
 * Token 用量与成本分布（audit.view）。
 * 计费管理为商业版专属，不在 OSS 控制台。
 */
import { AdminTabbedPage } from "@/components/admin-ui";
import { TokenUsagePage } from "./token-usage";

export function UsageBillingPage() {
  return (
    <AdminTabbedPage
      tabs={[
        { key: "usage", label: "用量统计", permission: "audit.view", content: <TokenUsagePage /> },
      ]}
    />
  );
}

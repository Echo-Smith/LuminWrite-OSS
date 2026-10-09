/**
 * 审计与治理控制台（头像菜单入口，/console）
 *
 * FloatingShell 统一骨架，合并四类管理面：
 * - 审计：操作日志 / 安全审计
 * - 数据：Trace 历史 / 全站用量
 * - 质量：评测中心 / 反馈分析 / 风格管理（含社区审核）
 * - 治理：自演进 / 角色权限
 *
 * 仅 admin 可见（路由 requireAdmin；后端 admin 鉴权兜底）。
 */
import { useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  ScrollText, ShieldAlert, ClipboardCheck, GitBranch, Users,
  ListTree, MessageSquareText, TrendingUp, PenLine,
} from "lucide-react";
import { useAuthStore } from "@/stores/auth-store";
import { closeOverlayAndBack } from "@/lib/close-overlay";
import {
  FloatingShell, type FloatingShellEntry,
} from "@/components/shell/floating-shell";
import { AuditLogsPage } from "./sections/audit-logs";
import { SecurityAuditPage } from "./sections/security-audit";
import { EvaluationPage } from "./sections/evaluation-panel";
import { EvolutionPage } from "./sections/evolution";
import { RbacPage } from "./sections/rbac";
import { StyleCenterPage } from "./sections/style-center";
import { TraceHistoryPage } from "./sections/trace-history";
import { FeedbackAnalysisPage } from "./sections/feedback-analysis";
import { UsageBillingPage } from "./sections/usage-billing";

type ConsoleKey =
  | "audit-logs" | "security-audit" | "traces" | "usage"
  | "evaluation" | "feedback" | "styles" | "evolution" | "rbac";

const item = (key: ConsoleKey, label: string, icon: typeof ScrollText): FloatingShellEntry => ({ key, label, icon });

const CONSOLE_ITEMS: FloatingShellEntry[] = [
  { group: "审计" },
  item("audit-logs", "操作日志", ScrollText),
  item("security-audit", "安全审计", ShieldAlert),
  { group: "数据" },
  item("traces", "Trace 历史", ListTree),
  item("usage", "全站用量", TrendingUp),
  { group: "质量" },
  item("evaluation", "评测中心", ClipboardCheck),
  item("feedback", "反馈分析", MessageSquareText),
  item("styles", "风格管理", PenLine),
  { group: "治理" },
  item("evolution", "自演进", GitBranch),
  item("rbac", "角色权限", Users),
];

const CONSOLE_META: Record<ConsoleKey, { title: string; subtitle: string }> = {
  "audit-logs": { title: "操作日志", subtitle: "管理员写操作的追加式审计日志" },
  "security-audit": { title: "安全审计", subtitle: "Prompt Injection 拦截 · 沙箱违规 · 防御状态" },
  traces: { title: "Trace 历史", subtitle: "全站写作记录与评分回看" },
  usage: { title: "全站用量", subtitle: "全站 Token 与成本分布" },
  evaluation: { title: "评测中心", subtitle: "WABench 盲评流水线与红队评估" },
  feedback: { title: "反馈分析", subtitle: "风格级反馈聚合与改进建议" },
  styles: { title: "风格管理", subtitle: "官方风格 CRUD、发布与社区审核" },
  evolution: { title: "自演进", subtitle: "候选审批、灰度与回滚" },
  rbac: { title: "角色权限", subtitle: "角色与权限管理（家庭/小团队部署）" },
};

export function ConsoleDialog() {
  const navigate = useNavigate();
  const [active, setActive] = useState<ConsoleKey>("audit-logs");
  const [open, setOpen] = useState(true);
  const isGuest = useAuthStore((s) => s.user?.role === "guest");

  const handleClose = () => {
    setOpen(false);
    closeOverlayAndBack(navigate);
  };

  const header = (
    <div className="flex items-center gap-2.5 w-full">
      <div className="flex h-9 w-9 items-center justify-center rounded-full bg-primary/10">
        <ScrollText className="h-4 w-4 text-primary" />
      </div>
      <div>
        <p className="text-sm font-medium">审计中心</p>
        <p className="text-[11px] text-muted-foreground">governance console</p>
      </div>
    </div>
  );

  return (
    <FloatingShell
      header={header}
      items={CONSOLE_ITEMS}
      active={active}
      onItemChange={setActive}
      meta={CONSOLE_META}
      onClose={handleClose}
      isGuest={isGuest}
      guestNotice={{
        icon: ScrollText,
        title: "游客模式无法查看审计中心",
        description: "注册并登录后可查看审计、评测、自演进与权限管理。",
      }}
      contentClassName="p-6"
    >
      {active === "audit-logs" && <AuditLogsPage />}
      {active === "security-audit" && <SecurityAuditPage />}
      {active === "traces" && <TraceHistoryPage />}
      {active === "usage" && <UsageBillingPage />}
      {active === "evaluation" && <EvaluationPage />}
      {active === "feedback" && <FeedbackAnalysisPage />}
      {active === "styles" && <StyleCenterPage />}
      {active === "evolution" && <EvolutionPage />}
      {active === "rbac" && <RbacPage />}
    </FloatingShell>
  );
}

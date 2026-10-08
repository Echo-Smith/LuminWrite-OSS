/**
 * 审计与治理控制台（头像菜单入口，/console）
 *
 * 个人中心同款悬浮窗形态，合并四类管理面：
 * - 操作日志 / 安全审计：审计中心
 * - 评测中心：WABench 盲评流水线
 * - 自演进：候选审批 / 灰度 / 回滚
 * - 角色权限：RBAC 管理（家庭/小团队部署）
 *
 * 仅 admin 可见（路由 requireAdmin；后端 admin 鉴权兜底）。
 */
import { useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  ScrollText, ShieldAlert, ClipboardCheck, GitBranch, Users, X,
  ListTree, MessageSquareText, TrendingUp, PenLine,
} from "lucide-react";
import { DialogPortal, DialogOverlay } from "@/components/ui/dialog";
import * as DialogPrimitive from "@radix-ui/react-dialog";
import { cn } from "@/lib/utils";
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
  | "audit-logs" | "security-audit" | "evaluation" | "evolution" | "rbac"
  | "styles" | "traces" | "feedback" | "usage";

const CONSOLE_ITEMS: Array<{ key: ConsoleKey; label: string; icon: typeof ScrollText }> = [
  { key: "audit-logs", label: "操作日志", icon: ScrollText },
  { key: "security-audit", label: "安全审计", icon: ShieldAlert },
  { key: "evaluation", label: "评测中心", icon: ClipboardCheck },
  { key: "evolution", label: "自演进", icon: GitBranch },
  { key: "rbac", label: "角色权限", icon: Users },
  { key: "styles", label: "风格管理", icon: PenLine },
  { key: "traces", label: "Trace 历史", icon: ListTree },
  { key: "feedback", label: "反馈分析", icon: MessageSquareText },
  { key: "usage", label: "全站用量", icon: TrendingUp },
];

const CONSOLE_META: Record<ConsoleKey, { title: string; subtitle: string }> = {
  "audit-logs": { title: "操作日志", subtitle: "管理员写操作的追加式审计日志" },
  "security-audit": { title: "安全审计", subtitle: "Prompt Injection 拦截 · 沙箱违规 · 防御状态" },
  evaluation: { title: "评测中心", subtitle: "WABench 盲评流水线与红队评估" },
  evolution: { title: "自演进", subtitle: "候选审批、灰度与回滚" },
  rbac: { title: "角色权限", subtitle: "角色与权限管理（家庭/小团队部署）" },
  styles: { title: "风格管理", subtitle: "官方风格 CRUD、发布与社区审核" },
  traces: { title: "Trace 历史", subtitle: "全站写作记录与评分回看" },
  feedback: { title: "反馈分析", subtitle: "风格级反馈聚合与改进建议" },
  usage: { title: "全站用量", subtitle: "全站 Token 与成本分布" },
};

export function ConsoleDialog() {
  const navigate = useNavigate();
  const [active, setActive] = useState<ConsoleKey>("audit-logs");
  const [open, setOpen] = useState(true);

  const handleClose = () => {
    setOpen(false);
    navigate(-1);
  };

  return (
    <DialogPrimitive.Root open={open} onOpenChange={() => {}}>
      <DialogPortal>
        <DialogOverlay className="bg-black/20 backdrop-blur-[2px]" />
        <DialogPrimitive.Content
          onInteractOutside={(e) => e.preventDefault()}
          className={cn(
            "fixed left-[50%] top-[50%] z-50 flex h-[85vh] max-h-[90vh] w-[1100px] max-w-[94vw] translate-x-[-50%] translate-y-[-50%] flex-row overflow-hidden rounded-xl border bg-background shadow-md duration-200 data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95",
          )}
        >
          {/* ── 左侧菜单 ── */}
          <div className="w-48 shrink-0 border-r bg-muted/30 flex flex-col">
            <div className="flex h-[60px] items-center gap-2.5 px-4 border-b">
              <div className="flex h-9 w-9 items-center justify-center rounded-full bg-primary/10">
                <ScrollText className="h-4 w-4 text-primary" />
              </div>
              <div>
                <p className="text-sm font-medium">审计中心</p>
                <p className="text-[11px] text-muted-foreground">governance console</p>
              </div>
            </div>

            <div className="flex-1 p-2 space-y-0.5">
              {CONSOLE_ITEMS.map((item) => {
                const Icon = item.icon;
                const isActive = active === item.key;
                return (
                  <button
                    key={item.key}
                    onClick={() => setActive(item.key)}
                    className={cn(
                      "flex w-full items-center gap-2.5 rounded-lg px-3 py-2 text-sm transition-ui",
                      isActive
                        ? "bg-accent text-foreground font-medium"
                        : "text-muted-foreground hover:bg-accent/50 hover:text-foreground",
                    )}
                  >
                    <Icon className="h-4 w-4 shrink-0" />
                    <span className="flex-1 text-left">{item.label}</span>
                  </button>
                );
              })}
            </div>
          </div>

          {/* ── 右侧内容区 ── */}
          <div className="flex-1 flex flex-col min-h-0">
            <div className="flex h-[60px] shrink-0 items-center justify-between px-6 bg-background border-b">
              <div className="min-w-0">
                <h2 className="text-lg font-semibold leading-tight">{CONSOLE_META[active].title}</h2>
                <p className="text-sm text-muted-foreground leading-tight">{CONSOLE_META[active].subtitle}</p>
              </div>
              <button
                onClick={handleClose}
                className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-ui hover:bg-accent hover:text-foreground"
              >
                <X className="h-4 w-4" />
              </button>
            </div>

            <div className="flex-1 overflow-y-auto relative scrollbar-hide p-6">
              {active === "audit-logs" && <AuditLogsPage />}
              {active === "security-audit" && <SecurityAuditPage />}
              {active === "evaluation" && <EvaluationPage />}
              {active === "evolution" && <EvolutionPage />}
              {active === "rbac" && <RbacPage />}
              {active === "styles" && <StyleCenterPage />}
              {active === "traces" && <TraceHistoryPage />}
              {active === "feedback" && <FeedbackAnalysisPage />}
              {active === "usage" && <UsageBillingPage />}
            </div>
          </div>
        </DialogPrimitive.Content>
      </DialogPortal>
    </DialogPrimitive.Root>
  );
}

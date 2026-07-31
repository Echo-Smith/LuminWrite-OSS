/**
 * Admin Dashboard 布局
 */
import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { ArrowLeft, LogOut } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import { useAuthStore } from "@/stores/auth-store";
import { OverviewPage } from "./admin/overview";
import { StyleManagementPage } from "./admin/style-management";
import { TraceHistoryPage } from "./admin/trace-history";
import { SensitiveWordsPage } from "./admin/sensitive-words";
import { FeedbackAnalysisPage } from "./admin/feedback-analysis";
import { EvaluationPage } from "./admin/evaluation-panel";
import { ModelConfigsPage } from "./admin/model-configs";
import { APIKeysPage } from "./admin/api-keys";
import { TokenUsagePage } from "./admin/token-usage";
import { CronJobsPage } from "./admin/cron-jobs";
import { PendingStylesPage } from "./admin/pending-styles";
import { KnowledgeBasePage } from "./admin/knowledge-base";

type AdminPage =
  | "overview"
  | "styles"
  | "pending-styles"
  | "traces"
  | "models"
  | "keys"
  | "cron"
  | "evaluation"
  | "feedback"
  | "usage"
  | "sensitive"
  | "kb";

const NAV_ITEMS: { key: AdminPage; label: string; icon: string }[] = [
  { key: "overview", label: "概览", icon: "📊" },
  { key: "styles", label: "风格管理", icon: "✍️" },
  { key: "pending-styles", label: "社区审核", icon: "🔍" },
  { key: "traces", label: "Trace 历史", icon: "📋" },
  { key: "models", label: "模型配置", icon: "🤖" },
  { key: "keys", label: "MCP 服务密钥", icon: "🔑" },
  { key: "cron", label: "定时任务", icon: "⏰" },
  { key: "evaluation", label: "评测面板", icon: "📝" },
  { key: "feedback", label: "反馈分析", icon: "💬" },
  { key: "usage", label: "用量统计", icon: "📈" },
  { key: "sensitive", label: "敏感词库", icon: "🛡️" },
  { key: "kb", label: "知识库", icon: "📚" },
];

export function AdminDashboard() {
  const [activePage, setActivePage] = useState<AdminPage>("overview");
  const navigate = useNavigate();
  const logout = useAuthStore((s) => s.logout);

  return (
    <div className="flex h-screen bg-muted/30">
      {/* 侧边导航 */}
      <nav className="flex w-56 flex-col border-r bg-background">
        <div className="border-b px-4 py-4">
          <h1 className="text-base font-bold">Writing Agent V2</h1>
          <p className="text-xs text-muted-foreground">Admin Dashboard</p>
        </div>
        <div className="flex-1 overflow-y-auto py-2">
          {NAV_ITEMS.map((item) => (
            <button
              key={item.key}
              onClick={() => setActivePage(item.key)}
              className={`flex w-full items-center gap-3 px-4 py-2.5 text-sm transition-colors ${
                activePage === item.key
                  ? "bg-muted text-foreground font-medium"
                  : "text-muted-foreground hover:bg-accent"
              }`}
            >
              <span>{item.icon}</span>
              {item.label}
            </button>
          ))}
        </div>
        <Separator />
        <div className="p-3 space-y-1">
          <Button
            variant="ghost"
            size="sm"
            className="w-full justify-start gap-2 text-muted-foreground"
            onClick={() => navigate("/write")}
          >
            <ArrowLeft className="h-4 w-4" />
            返回工作台
          </Button>
          <Button
            variant="ghost"
            size="sm"
            className="w-full justify-start gap-2 text-muted-foreground hover:text-destructive"
            onClick={() => {
              logout();
              navigate("/login", { replace: true });
            }}
          >
            <LogOut className="h-4 w-4" />
            退出登录
          </Button>
        </div>
      </nav>

      {/* 内容区 */}
      <main className="flex-1 overflow-y-auto">
        {activePage === "overview" && <OverviewPage />}
        {activePage === "styles" && <StyleManagementPage />}
        {activePage === "pending-styles" && <PendingStylesPage />}
        {activePage === "traces" && <TraceHistoryPage />}
        {activePage === "models" && <ModelConfigsPage />}
        {activePage === "keys" && <APIKeysPage />}
        {activePage === "cron" && <CronJobsPage />}
        {activePage === "feedback" && <FeedbackAnalysisPage />}
        {activePage === "evaluation" && <EvaluationPage />}
        {activePage === "usage" && <TokenUsagePage />}
        {activePage === "sensitive" && <SensitiveWordsPage />}
        {activePage === "kb" && <KnowledgeBasePage />}
      </main>
    </div>
  );
}

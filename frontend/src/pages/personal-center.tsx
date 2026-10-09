/**
 * 个人中心 — 居中悬浮面板（主框架）
 *
 * FloatingShell 统一骨架：左侧分组菜单 + 右侧内容区，各子页面拆分至
 * pages/personal/*-section.tsx。居中悬浮，点击遮罩区域不关闭。
 */
import { useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  Clock, Shield, ShieldCheck, Database, History,
  Brain, Cpu, BarChart3, Settings, SlidersHorizontal,
  Bell, FlaskConical, Info, type LucideIcon,
} from "lucide-react";
import { useAuthStore } from "@/stores/auth-store";
import { useSettingsStore } from "@/stores/settings-store";
import { cn } from "@/lib/utils";
import { closeOverlayAndBack } from "@/lib/close-overlay";
import { type MenuKey, SECTION_META, FloatingAddButton } from "@/pages/personal/shared";
import {
  FloatingShell, type FloatingShellEntry,
} from "@/components/shell/floating-shell";
import { AccountSecuritySection } from "@/pages/personal/account-security-section";
import { HistorySection } from "@/pages/personal/history-section";
import { CronJobsPage as CronSection } from "@/pages/personal/cron-section";
import { SensitiveWordsPage as SensitiveSection } from "@/pages/personal/sensitive-section";
import { KnowledgeBasePage } from "@/pages/personal/kb-maintenance";
import { MemorySection } from "@/pages/personal/memory-section";
import { ModelServiceSection } from "@/pages/personal/model-service-section";
import { UsageSection } from "@/pages/personal/usage-section";
import { SettingsSection } from "@/pages/personal/settings-section";
import { CustomizationSection } from "@/pages/personal/customization-section";
import { NotificationsSection } from "@/pages/personal/notifications-section";
import { LabsSection } from "@/pages/personal/labs-section";
import { AboutSection } from "@/pages/personal/about-section";

const item = (key: MenuKey, label: string, icon: LucideIcon): FloatingShellEntry => ({ key, label, icon });

export function PersonalCenter() {
  const navigate = useNavigate();
  const [activeMenu, setActiveMenu] = useState<MenuKey>("account-security");
  const [open, setOpen] = useState(true);

  const user = useAuthStore((s) => s.user);
  const isGuest = user?.role === "guest";
  const isAdmin = !!user && user.role !== "guest"; // 管理面对所有注册成员开放
  const labsCronPanel = useSettingsStore((s) => s.labsCronPanel);
  const labsSensitivePanel = useSettingsStore((s) => s.labsSensitivePanel);
  const labsKbMaintenance = useSettingsStore((s) => s.labsKbMaintenance);

  const handleClose = () => {
    setOpen(false);
    closeOverlayAndBack(navigate);
  };

  // 分组菜单：账号 / 创作 / 系统（Labs 面板跟随开关出现在「系统」组）
  const menu: FloatingShellEntry[] = [
    { group: "账号" },
    item("account-security", "账号与安全", ShieldCheck),
    item("notifications", "通知设置", Bell),
    item("about", "关于笔润智谈", Info),
    { group: "创作" },
    item("history", "写作记录", History),
    item("memory", "记忆管理", Brain),
    item("models", "模型服务", Cpu),
    item("usage", "用量统计", BarChart3),
    { group: "系统" },
    item("settings", "偏好设置", Settings),
    item("customization", "自定义", SlidersHorizontal),
    item("labs", "实验室", FlaskConical),
    ...(isAdmin && labsCronPanel ? [item("cron", "定时任务", Clock)] : []),
    ...(isAdmin && labsSensitivePanel ? [item("sensitive", "敏感词库", Shield)] : []),
    ...(isAdmin && labsKbMaintenance ? [item("kb-maintenance", "知识库拓展管理", Database)] : []),
  ];

  const header = (
    <div className="flex items-center gap-2.5 w-full">
      <div className={cn(
        "flex h-9 w-9 items-center justify-center rounded-full text-xs font-medium shrink-0",
        isGuest ? "bg-amber-100 text-amber-700" : "bg-primary/10 text-primary"
      )}>
        {isGuest ? "客" : (user?.username?.slice(0, 2).toUpperCase() ?? user?.userId?.slice(0, 2).toUpperCase() ?? "?")}
      </div>
      <div className="min-w-0">
        <p className="text-sm font-medium truncate">
          {isGuest ? "游客" : (user?.username ?? user?.userId?.slice(0, 8) ?? "用户")}
        </p>
        <p className="text-[11px] text-muted-foreground">
          {isGuest ? "trial mode" : "user"}
        </p>
      </div>
    </div>
  );

  return (
    <FloatingShell
      header={header}
      items={menu}
      active={activeMenu}
      onItemChange={(k) => setActiveMenu(k as MenuKey)}
      meta={SECTION_META}
      onClose={handleClose}
    >
      {activeMenu === "account-security" && <AccountSecuritySection />}
      {activeMenu === "history" && <HistorySection />}
      {activeMenu === "memory" && <MemorySection />}
      {activeMenu === "models" && <ModelServiceSection />}
      {activeMenu === "usage" && <UsageSection />}
      {activeMenu === "settings" && <SettingsSection onClosePanel={() => { navigate("/write", { replace: true }); setOpen(false); }} />}
      {activeMenu === "customization" && <CustomizationSection />}
      {activeMenu === "notifications" && <NotificationsSection />}
      {activeMenu === "labs" && <LabsSection />}
      {activeMenu === "cron" && <CronSection />}
      {activeMenu === "sensitive" && <SensitiveSection />}
      {activeMenu === "kb-maintenance" && <KnowledgeBasePage />}
      {activeMenu === "about" && <AboutSection />}

      {/* ── 右下角圆形 + 按钮（写作风格/记忆管理用） ── */}
      {(activeMenu === "memory") && (
        <FloatingAddButton
          disabled={isGuest}
          onClick={() => {
            // 通过自定义事件触发各子组件的添加操作
            const event = new CustomEvent("personal-center-add");
            window.dispatchEvent(event);
          }}
        />
      )}
    </FloatingShell>
  );
}

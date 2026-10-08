/**
 * 个人中心 — 居中悬浮面板（主框架）
 *
 * 左侧菜单 + 右侧内容区，各子页面拆分至 pages/personal/*-section.tsx。
 * 居中悬浮，低阴影，点击遮罩区域不关闭。
 */
import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { X, Server, Clock, Shield, Database } from "lucide-react";
import { DialogPortal, DialogOverlay } from "@/components/ui/dialog";
import * as DialogPrimitive from "@radix-ui/react-dialog";
import { useAuthStore } from "@/stores/auth-store";
import { cn } from "@/lib/utils";
import {
  type MenuKey, type MenuItem, MENU_ITEMS, SECTION_META, FloatingAddButton,
} from "@/pages/personal/shared";
import { ProfileSection } from "@/pages/personal/profile-section";
import { HistorySection } from "@/pages/personal/history-section";
import { McpSection } from "@/pages/personal/mcp-section";
import { CronJobsPage as CronSection } from "@/pages/personal/cron-section";
import { SensitiveWordsPage as SensitiveSection } from "@/pages/personal/sensitive-section";
import { KnowledgeBasePage } from "@/pages/personal/kb-maintenance";
import { useSettingsStore } from "@/stores/settings-store";

import { StyleSection } from "@/pages/personal/styles-section";
import { MemorySection } from "@/pages/personal/memory-section";
import { ModelServiceSection } from "@/pages/personal/model-service-section";
import { UsageSection } from "@/pages/personal/usage-section";
import { SettingsSection } from "@/pages/personal/settings-section";
import { CustomizationSection } from "@/pages/personal/customization-section";
import { WalletSection } from "@/pages/personal/wallet-section";
import { NotificationsSection } from "@/pages/personal/notifications-section";
import { AccountSection } from "@/pages/personal/account-section";
import { LabsSection } from "@/pages/personal/labs-section";
import { AboutSection } from "@/pages/personal/about-section";

export function PersonalCenter() {
  const navigate = useNavigate();
  const [activeMenu, setActiveMenu] = useState<MenuKey>("profile");
  const [open, setOpen] = useState(true);

  const user = useAuthStore((s) => s.user);

  const isGuest = user?.role === "guest";


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
            "fixed left-[50%] top-[50%] z-50 flex h-[600px] max-h-[85vh] w-[720px] max-w-[92vw] translate-x-[-50%] translate-y-[-50%] flex-row overflow-hidden rounded-xl border bg-background shadow-md duration-200 data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95"
          )}
        >
          {/* ── 左侧菜单 ── */}
          <div className="w-48 shrink-0 border-r bg-muted/30 flex flex-col">
            {/* 用户信息头部 — 与右侧标题区对齐，横线对齐 admin 风格 */}
            <div className="flex h-[60px] items-center gap-2.5 px-4 border-b">
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

            {/* 菜单列表 */}
            <div className="flex-1 p-2 space-y-0.5">
              {((): MenuItem[] => {
                const isAdmin = user?.role === "admin";
                const labsCronPanel = useSettingsStore.getState().labsCronPanel;
                const labsSensitivePanel = useSettingsStore.getState().labsSensitivePanel;
                const labsKbMaintenance = useSettingsStore.getState().labsKbMaintenance;
                const extras: MenuItem[] = [];
                if (isAdmin) {
                  extras.push({ key: "mcp", label: "MCP 服务", icon: Server });
                  if (labsCronPanel) extras.push({ key: "cron", label: "定时任务", icon: Clock });
                  if (labsSensitivePanel) extras.push({ key: "sensitive", label: "敏感词库", icon: Shield });
                  if (labsKbMaintenance) extras.push({ key: "kb-maintenance", label: "知识库维护", icon: Database });
                }
                const base = [...MENU_ITEMS];
                const labsIdx = base.findIndex((i) => i.key === "labs");
                base.splice(labsIdx + 1, 0, ...extras);
                return base;
              })().map((item) => {
                const Icon = item.icon;
                const isActive = activeMenu === item.key;
                return (
                  <button
                    key={item.key}
                    onClick={() => setActiveMenu(item.key)}
                    className={cn(
                      "flex w-full items-center gap-2.5 rounded-lg px-3 py-2 text-sm transition-ui",
                      isActive
                        ? "bg-accent text-foreground font-medium"
                        : "text-muted-foreground hover:bg-accent/50 hover:text-foreground"
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
            {/* 固定栏 — 左侧标题+副标题，右侧关闭按钮，所有分页统一 */}
            <div className="flex h-[60px] shrink-0 items-center justify-between px-6 bg-background border-b">
              <div className="min-w-0">
                <h2 className="text-lg font-semibold leading-tight">{SECTION_META[activeMenu].title}</h2>
                <p className="text-sm text-muted-foreground leading-tight">{SECTION_META[activeMenu].subtitle}</p>
              </div>
              <button
                onClick={handleClose}
                className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-ui hover:bg-accent hover:text-foreground"
              >
                <X className="h-4 w-4" />
              </button>
            </div>

            {/* 可滚动内容区 */}
            <div className="flex-1 overflow-y-auto relative scrollbar-hide">
            {activeMenu === "profile" && <ProfileSection />}
            {activeMenu === "history" && <HistorySection />}
            {activeMenu === "mcp" && <McpSection />}
            {activeMenu === "cron" && <CronSection />}
            {activeMenu === "sensitive" && <SensitiveSection />}
            {activeMenu === "kb-maintenance" && <KnowledgeBasePage />}
            {activeMenu === "styles" && <StyleSection />}
            {activeMenu === "memory" && <MemorySection />}
            {activeMenu === "models" && <ModelServiceSection />}
            {activeMenu === "usage" && <UsageSection />}
            {activeMenu === "settings" && <SettingsSection onClosePanel={() => { navigate("/write", { replace: true }); setOpen(false); }} />}
            {activeMenu === "customization" && <CustomizationSection />}
            {activeMenu === "wallet" && <WalletSection />}
            {activeMenu === "notifications" && <NotificationsSection />}
            {activeMenu === "account" && <AccountSection />}
            {activeMenu === "labs" && <LabsSection />}
            {activeMenu === "about" && <AboutSection />}

            {/* ── 右下角圆形 + 按钮（写作风格/记忆管理用） ── */}
            {(activeMenu === "styles" || activeMenu === "memory") && (
              <FloatingAddButton
                disabled={isGuest}
                onClick={() => {
                  // 通过自定义事件触发各子组件的添加操作
                  const event = new CustomEvent("personal-center-add");
                  window.dispatchEvent(event);
                }}
              />
            )}
            </div>
          </div>
        </DialogPrimitive.Content>
      </DialogPortal>
    </DialogPrimitive.Root>
  );
}

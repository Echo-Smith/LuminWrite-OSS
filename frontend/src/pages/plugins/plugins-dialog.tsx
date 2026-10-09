/**
 * 插件（悬浮窗，/plugins）
 *
 * 与审计中心/个人中心同款的悬浮窗形态，合并外部服务与扩展能力：
 * - 服务密钥：MCP / 第三方服务 API Key（含搜索源密钥）
 * - 安全沙箱：MCP 沙箱策略与违规记录
 * - 写作技能：运行时技能/工具插件
 * - 全局默认模型：BYOK 回退用的实例级模型配置
 *
 * 仅 admin 可见（侧边栏按非 guest 渲染入口；API 侧由后端鉴权兜底）。
 */
import { useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  Puzzle, KeyRound, Shield, Wrench, Cpu, X,
} from "lucide-react";
import { DialogPortal, DialogOverlay } from "@/components/ui/dialog";
import * as DialogPrimitive from "@radix-ui/react-dialog";
import { cn } from "@/lib/utils";
import { useAuthStore } from "@/stores/auth-store";
import { APIKeysPage as McpKeysPage } from "./mcp-keys-page";
import { MCPSandboxPage } from "./mcp-sandbox-page";
import { SkillsSection } from "./skills-section";
import { ModelConfigsPage } from "./global-models-page";

type PluginKey = "keys" | "sandbox" | "skills" | "global-models";

const PLUGIN_ITEMS: Array<{ key: PluginKey; label: string; icon: typeof KeyRound }> = [
  { key: "keys", label: "服务密钥", icon: KeyRound },
  { key: "sandbox", label: "安全沙箱", icon: Shield },
  { key: "skills", label: "写作技能", icon: Wrench },
  { key: "global-models", label: "全局默认模型", icon: Cpu },
];

const PLUGIN_META: Record<PluginKey, { title: string; subtitle: string }> = {
  keys: { title: "服务密钥", subtitle: "MCP / 第三方服务的 API Key 与已安装服务端点" },
  sandbox: { title: "安全沙箱", subtitle: "MCP 沙箱策略与违规记录" },
  skills: { title: "写作技能", subtitle: "运行时技能/工具插件" },
  "global-models": { title: "全局默认模型", subtitle: "BYOK 回退用的实例级模型配置" },
};

export function PluginsDialog() {
  const navigate = useNavigate();
  const [active, setActive] = useState<PluginKey>("keys");
  const [open, setOpen] = useState(true);
  const isGuest = useAuthStore((s) => s.user?.role === "guest");

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
                <Puzzle className="h-4 w-4 text-primary" />
              </div>
              <div>
                <p className="text-sm font-medium">插件</p>
                <p className="text-[11px] text-muted-foreground">服务与扩展</p>
              </div>
            </div>

            <div className="flex-1 p-2 space-y-0.5">
              {PLUGIN_ITEMS.map((item) => {
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
          {isGuest ? (
            <div className="flex-1 flex flex-col items-center justify-center gap-2 p-10 text-center">
              <Puzzle className="h-10 w-10 text-amber-500/60" />
              <p className="text-sm font-medium text-amber-900 dark:text-amber-200">游客模式无法管理插件</p>
              <p className="text-xs text-amber-700 dark:text-amber-400">注册并登录后可管理服务密钥、MCP 沙箱与写作技能。</p>
            </div>
          ) : (
          <div className="flex-1 flex flex-col min-h-0">
            <div className="flex h-[60px] shrink-0 items-center justify-between px-6 bg-background border-b">
              <div className="min-w-0">
                <h2 className="text-lg font-semibold leading-tight">{PLUGIN_META[active].title}</h2>
                <p className="text-sm text-muted-foreground leading-tight">{PLUGIN_META[active].subtitle}</p>
              </div>
              <button
                onClick={handleClose}
                className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-ui hover:bg-accent hover:text-foreground"
              >
                <X className="h-4 w-4" />
              </button>
            </div>

            <div className="flex-1 overflow-y-auto relative scrollbar-hide p-6">
              {active === "keys" && <McpKeysPage />}
              {active === "sandbox" && <MCPSandboxPage />}
              {active === "skills" && <SkillsSection />}
              {active === "global-models" && <ModelConfigsPage />}
            </div>
          </div>
          )}
        </DialogPrimitive.Content>
      </DialogPortal>
    </DialogPrimitive.Root>
  );
}

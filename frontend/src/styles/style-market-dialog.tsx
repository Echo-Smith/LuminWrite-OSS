/**
 * 风格和技能（/styles，页面形态）
 *
 * 应用市场式统一面板：写作风格、技能与外部服务合并为一个入口。
 * FloatingShell page 变体（真页面，不再是悬浮窗）：高频管理面，URL 可
 * 直达/分享，布局让出写作台侧边栏宽度。
 * 分组菜单：
 * - 风格：风格库（安装）、我的风格（创建/编辑/投稿）、技能（下载安装）
 * - 服务：MCP 服务（服务密钥 + 安全沙箱）、第三方服务（待接入）
 *   全局默认模型已并入个人中心「模型服务」的「实例默认」组。
 */
import { useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  Store, Library, Palette, Wrench, Puzzle, KeyRound, Shield, Plug2, Plus, Package,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { useAuthStore } from "@/stores/auth-store";
import { cn } from "@/lib/utils";
import { FloatingShell, type FloatingShellEntry } from "@/components/shell/floating-shell";
import { StyleLibrarySection } from "./style-library-section";
import { StyleSection } from "./my-styles-section";
import { SkillsSection } from "./skills-section";
import { APIKeysPage } from "@/pages/plugins/mcp-keys-page";
import { MCPSandboxPage } from "@/pages/plugins/mcp-sandbox-page";
import { ThirdPartySection } from "@/pages/plugins/third-party-section";
import { PackageInstallDialog } from "@/components/styles/package-install-dialog";

type MarketKey =
  | "library" | "mine" | "skills"
  | "mcp" | "third-party"
type McpSubKey = "keys" | "sandbox"

const item = (key: MarketKey, label: string, icon: typeof Library): FloatingShellEntry => ({ key, label, icon })

const ITEMS: FloatingShellEntry[] = [
  { group: "风格" },
  item("library", "风格库", Library),
  item("mine", "我的风格", Palette),
  item("skills", "技能", Wrench),
  { group: "服务" },
  item("mcp", "MCP 服务", Puzzle),
  item("third-party", "第三方服务", Plug2),
]

const META: Record<MarketKey, { title: string; subtitle: string }> = {
  library: { title: "风格库", subtitle: "内置风格开箱即用，可一键导入改造" },
  mine: { title: "我的风格", subtitle: "创建、编辑与管理你的写作风格" },
  skills: { title: "技能", subtitle: "运行时技能/工具插件" },
  mcp: { title: "MCP 服务", subtitle: "MCP / 第三方服务的 API Key 与沙箱策略" },
  "third-party": { title: "第三方服务", subtitle: "搜索源、语音等第三方能力（待接入）" },
}

const MCP_SUBS: Array<{ key: McpSubKey; label: string; icon: typeof KeyRound }> = [
  { key: "keys", label: "服务密钥", icon: KeyRound },
  { key: "sandbox", label: "安全沙箱", icon: Shield },
]

export function StyleMarketDialog() {
  const navigate = useNavigate();
  const [active, setActive] = useState<MarketKey>("library");
  const [mcpSub, setMcpSub] = useState<McpSubKey>("keys");
  const [showServicePackages, setShowServicePackages] = useState(false);
  const isGuest = useAuthStore((s) => s.user?.role === "guest");

  // 页面形态：关闭即回写作台（不再依赖浮层历史栈）
  const handleClose = () => {
    navigate("/write");
  };

  const header = (
    <div className="flex items-center gap-2.5 w-full">
      <div className="flex h-9 w-9 items-center justify-center rounded-full bg-primary/10">
        <Store className="h-4 w-4 text-primary" />
      </div>
      <div>
        <p className="text-sm font-medium">风格和技能</p>
        <p className="text-[11px] text-muted-foreground">styles &amp; skills</p>
      </div>
    </div>
  );

  // 「我的风格」区的创建入口：派发事件，my-styles-section 监听打开创建弹窗
  const triggerCreate = () => {
    window.dispatchEvent(new CustomEvent("personal-center-add"));
  };

  return (
    <FloatingShell
      variant="page"
      className="pl-0 lg:pl-56"
      header={header}
      items={ITEMS}
      active={active}
      onItemChange={setActive}
      meta={META}
      onClose={handleClose}
      isGuest={isGuest}
      guestNotice={{
        icon: Store,
        title: "游客模式无法管理风格和技能",
        description: "注册并登录后可导入风格、创建自己的写作风格并管理服务插件。",
      }}
      contentClassName="p-6"
    >
      {active === "mine" && (
        <div className="flex justify-end mb-3">
          <Button size="sm" onClick={triggerCreate} className="gap-1.5">
            <Plus className="h-4 w-4" />
            新建风格
          </Button>
        </div>
      )}
      {active === "library" && <StyleLibrarySection />}
      {active === "mine" && <StyleSection />}
      {active === "skills" && <SkillsSection />}
      {active === "mcp" && (
        <div className="space-y-4">
          {/* 内嵌子页切换 */}
          <div className="flex gap-1.5 border-b pb-2">
            {MCP_SUBS.map((sub) => {
              const Icon = sub.icon;
              const isActive = mcpSub === sub.key;
              return (
                <button
                  key={sub.key}
                  onClick={() => setMcpSub(sub.key)}
                  className={cn(
                    "flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs transition-ui",
                    isActive
                      ? "bg-accent text-foreground font-medium"
                      : "text-muted-foreground hover:bg-accent/50",
                  )}
                >
                  <Icon className="h-3.5 w-3.5" />
                  {sub.label}
                </button>
              );
            })}
          </div>
          {mcpSub === "keys" && (
            <div className="space-y-3">
              <div className="flex items-center justify-between gap-2">
                <p className="text-xs text-muted-foreground">
                  服务密钥与服务器管理；也可从服务包一键注册 MCP 服务器（密钥需单独配置）。
                </p>
                <Button size="sm" variant="outline" className="shrink-0 gap-1.5" onClick={() => setShowServicePackages(true)}>
                  <Package className="h-3.5 w-3.5" /> 安装服务包
                </Button>
              </div>
              <APIKeysPage />
            </div>
          )}
          {mcpSub === "sandbox" && <MCPSandboxPage />}
        </div>
      )}
      {active === "third-party" && <ThirdPartySection />}

      <PackageInstallDialog
        open={showServicePackages}
        onClose={() => setShowServicePackages(false)}
        kind="service"
      />
    </FloatingShell>
  );
}

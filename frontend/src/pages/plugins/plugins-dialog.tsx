/**
 * 插件（悬浮窗，/plugins）
 *
 * FloatingShell 统一骨架，外部服务与模型配置管理：
 * - MCP 服务：服务密钥 + 安全沙箱（内嵌两个子页）
 * - 第三方服务：待接入的第三方能力（搜索源 / TTS 等）
 * - 全局默认模型：实例级模型端点与密钥（用户 BYOK 回退）
 */
import { useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  Puzzle, KeyRound, Shield, Cpu, Plug2,
} from "lucide-react";
import { useAuthStore } from "@/stores/auth-store";
import { cn } from "@/lib/utils";
import { closeOverlayAndBack } from "@/lib/close-overlay";
import {
  FloatingShell, type FloatingShellEntry,
} from "@/components/shell/floating-shell";
import { APIKeysPage } from "./mcp-keys-page";
import { MCPSandboxPage } from "./mcp-sandbox-page";
import { ModelConfigsPage } from "./global-models-page";
import { ThirdPartySection } from "./third-party-section";

type PluginKey = "mcp" | "third-party" | "global-models"
type McpSubKey = "keys" | "sandbox"

const item = (key: PluginKey, label: string, icon: typeof KeyRound): FloatingShellEntry => ({ key, label, icon })

const PLUGIN_ITEMS: FloatingShellEntry[] = [
  item("mcp", "MCP 服务", Puzzle),
  item("third-party", "第三方服务", Plug2),
  item("global-models", "全局默认模型", Cpu),
]

const PLUGIN_META: Record<PluginKey, { title: string; subtitle: string }> = {
  mcp: { title: "MCP 服务", subtitle: "MCP / 第三方服务的 API Key 与沙箱策略" },
  "third-party": { title: "第三方服务", subtitle: "搜索源、语音等第三方能力（待接入）" },
  "global-models": { title: "全局默认模型", subtitle: "实例级模型端点与密钥（用户自带 key 的回退）" },
}

const MCP_SUBS: Array<{ key: McpSubKey; label: string; icon: typeof KeyRound }> = [
  { key: "keys", label: "服务密钥", icon: KeyRound },
  { key: "sandbox", label: "安全沙箱", icon: Shield },
]

export function PluginsDialog() {
  const navigate = useNavigate();
  const [active, setActive] = useState<PluginKey>("mcp");
  const [mcpSub, setMcpSub] = useState<McpSubKey>("keys");
  const [open, setOpen] = useState(true);
  const isGuest = useAuthStore((s) => s.user?.role === "guest");

  const handleClose = () => {
    setOpen(false);
    closeOverlayAndBack(navigate);
  };

  const header = (
    <div className="flex items-center gap-2.5 w-full">
      <div className="flex h-9 w-9 items-center justify-center rounded-full bg-primary/10">
        <Puzzle className="h-4 w-4 text-primary" />
      </div>
      <div>
        <p className="text-sm font-medium">插件</p>
        <p className="text-[11px] text-muted-foreground">服务与扩展</p>
      </div>
    </div>
  );

  return (
    <FloatingShell
      header={header}
      items={PLUGIN_ITEMS}
      active={active}
      onItemChange={setActive}
      meta={PLUGIN_META}
      onClose={handleClose}
      isGuest={isGuest}
      guestNotice={{
        icon: Puzzle,
        title: "游客模式无法管理插件",
        description: "注册并登录后可管理服务密钥、MCP 沙箱与模型配置。",
      }}
      contentClassName="p-6"
    >
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
          {mcpSub === "keys" && <APIKeysPage />}
          {mcpSub === "sandbox" && <MCPSandboxPage />}
        </div>
      )}
      {active === "third-party" && <ThirdPartySection />}
      {active === "global-models" && <ModelConfigsPage />}
    </FloatingShell>
  );
}

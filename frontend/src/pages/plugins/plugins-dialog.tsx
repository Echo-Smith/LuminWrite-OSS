/**
 * 插件（悬浮窗，/plugins）
 *
 * FloatingShell 统一骨架，合并外部服务与扩展能力：
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
  Puzzle, KeyRound, Shield, Wrench, Cpu,
} from "lucide-react";
import { useAuthStore } from "@/stores/auth-store";
import { closeOverlayAndBack } from "@/lib/close-overlay";
import {
  FloatingShell, type FloatingShellEntry,
} from "@/components/shell/floating-shell";
import { APIKeysPage as McpKeysPage } from "./mcp-keys-page";
import { MCPSandboxPage } from "./mcp-sandbox-page";
import { SkillsSection } from "./skills-section";
import { ModelConfigsPage } from "./global-models-page";

type PluginKey = "keys" | "sandbox" | "skills" | "global-models";

const item = (key: PluginKey, label: string, icon: typeof KeyRound): FloatingShellEntry => ({ key, label, icon });

const PLUGIN_ITEMS: FloatingShellEntry[] = [
  item("keys", "服务密钥", KeyRound),
  item("sandbox", "安全沙箱", Shield),
  item("skills", "写作技能", Wrench),
  item("global-models", "全局默认模型", Cpu),
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
        description: "注册并登录后可管理服务密钥、MCP 沙箱与写作技能。",
      }}
      contentClassName="p-6"
    >
      {active === "keys" && <McpKeysPage />}
      {active === "sandbox" && <MCPSandboxPage />}
      {active === "skills" && <SkillsSection />}
      {active === "global-models" && <ModelConfigsPage />}
    </FloatingShell>
  );
}

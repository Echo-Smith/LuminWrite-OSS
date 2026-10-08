/**
 * 插件页（左侧栏入口，/plugins）
 *
 * 外部服务与扩展能力的统一管理面：
 * - 服务密钥：MCP / 第三方服务 API Key（含搜索源密钥）
 * - 安全沙箱：MCP 沙箱策略与违规记录
 * - 写作技能：运行时技能/工具插件
 *
 * 仅 admin 可见（侧边栏按 isAdmin 渲染入口；API 侧由 admin 鉴权兜底）。
 */
import { Puzzle } from "lucide-react";
import { AdminTabbedPage } from "@/components/admin-ui";
import { APIKeysPage as McpKeysPage } from "./mcp-keys-page";
import { MCPSandboxPage } from "./mcp-sandbox-page";
import { SkillsSection } from "./skills-section";
import { ModelConfigsPage } from "./global-models-page";

export function PluginsPage() {
  return (
    <div className="p-6 space-y-6">
      <div className="flex items-start gap-3">
        <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-primary/10">
          <Puzzle className="h-5 w-5 text-primary" />
        </div>
        <div>
          <h1 className="text-lg font-semibold leading-tight">插件</h1>
          <p className="text-sm text-muted-foreground mt-0.5">
            外部服务集成与扩展能力：服务密钥、MCP 沙箱与写作技能插件。
          </p>
        </div>
      </div>

      <AdminTabbedPage
        tabs={[
          { key: "keys", label: "服务密钥", permission: "apikey.manage", content: <McpKeysPage /> },
          { key: "sandbox", label: "安全沙箱", permission: "sandbox.manage", content: <MCPSandboxPage /> },
          { key: "skills", label: "写作技能", permission: null, content: <SkillsSection /> },
          { key: "global-models", label: "全局默认模型", permission: "model.manage", content: <ModelConfigsPage /> },
        ]}
      />
    </div>
  );
}

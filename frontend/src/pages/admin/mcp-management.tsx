/**
 * MCP 管理（合并入口）
 *
 * - 服务密钥：MCP / 第三方服务 API Key 管理（apikey.manage）
 * - 安全沙箱：MCP 沙箱策略与违规记录（sandbox.manage）
 */
import { AdminTabbedPage } from "@/components/admin";
import { APIKeysPage } from "./api-keys";
import { MCPSandboxPage } from "./mcp-sandbox";

export function MCPManagementPage() {
  return (
    <AdminTabbedPage
      tabs={[
        { key: "keys", label: "服务密钥", permission: "apikey.manage", content: <APIKeysPage /> },
        { key: "sandbox", label: "安全沙箱", permission: "sandbox.manage", content: <MCPSandboxPage /> },
      ]}
    />
  );
}

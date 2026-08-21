/**
 * AgentNodeCard — React Flow 自定义节点
 *
 * 显示 Agent 角色名、状态、输出类型、Token 使用量和流式输出。
 */
import { memo } from "react";
import { Handle, Position } from "@xyflow/react";
import { cn } from "@/lib/utils";

export interface AgentNodeData {
  label: string;
  agentName: string;
  agentRole: string;
  outputArtifact: string;
  status: string;
  streamText?: string;
  tokensUsed?: number;
  error?: string;
  roleColor: string;
  statusColor: string;
  [key: string]: unknown;
}

function AgentNodeCardComponent({ data }: { data: AgentNodeData }) {
  const {
    label,
    agentName,
    agentRole,
    outputArtifact,
    status,
    streamText,
    tokensUsed,
    error,
    roleColor,
    statusColor,
  } = data;

  const statusLabel: Record<string, string> = {
    pending: "等待中",
    running: "执行中",
    completed: "已完成",
    failed: "失败",
    skipped: "跳过",
  };

  return (
    <div
      className="relative rounded-xl border-2 bg-white shadow-lg transition-all duration-300 dark:bg-zinc-900"
      style={{
        borderColor: statusColor,
        width: 240,
        minHeight: 120,
      }}
    >
      {/* 输入 handle（左侧） */}
      <Handle
        type="target"
        position={Position.Left}
        style={{ background: roleColor, width: 10, height: 10 }}
      />

      {/* 角色色条 */}
      <div
        className="absolute left-0 top-0 h-full w-1 rounded-l-xl"
        style={{ background: roleColor }}
      />

      <div className="p-3 pl-4">
        {/* 角色标签 */}
        <div className="mb-1 flex items-center justify-between">
          <span
            className="rounded-full px-2 py-0.5 text-xs font-medium text-white"
            style={{ background: roleColor }}
          >
            {agentRole}
          </span>
          <span
            className="flex items-center gap-1 text-xs font-medium"
            style={{ color: statusColor }}
          >
            {status === "running" && (
              <span className="inline-block h-2 w-2 animate-pulse rounded-full bg-blue-500" />
            )}
            {statusLabel[status] || status}
          </span>
        </div>

        {/* 节点名称 */}
        <div className="text-sm font-semibold text-zinc-900 dark:text-white">
          {label}
        </div>
        <div className="text-xs text-zinc-500 dark:text-zinc-400">
          {agentName}
        </div>

        {/* 输出类型 */}
        <div className="mt-1.5 text-xs text-zinc-400">
          → {outputArtifact}
        </div>

        {/* Token 使用量 */}
        {tokensUsed != null && tokensUsed > 0 && (
          <div className="mt-1 text-xs text-zinc-400">
            📊 {tokensUsed} tokens
          </div>
        )}

        {/* 流式输出预览 */}
        {streamText && status === "running" && (
          <div className="mt-2 max-h-20 overflow-y-auto rounded bg-zinc-50 p-1.5 text-xs text-zinc-600 dark:bg-zinc-800 dark:text-zinc-300">
            {streamText.slice(-200)}
            <span className="inline-block h-3 w-1 animate-pulse bg-blue-500" />
          </div>
        )}

        {/* 错误信息 */}
        {error && (
          <div className="mt-2 rounded bg-red-50 p-1.5 text-xs text-red-600 dark:bg-red-950 dark:text-red-400">
            ⚠️ {error}
          </div>
        )}
      </div>

      {/* 输出 handle（右侧） */}
      <Handle
        type="source"
        position={Position.Right}
        style={{ background: roleColor, width: 10, height: 10 }}
      />
    </div>
  );
}

export const AgentNodeCard = memo(AgentNodeCardComponent);

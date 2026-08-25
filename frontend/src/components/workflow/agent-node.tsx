/**
 * AgentNodeCard — React Flow 自定义节点
 *
 * 显示 Agent 角色名、状态、输出类型、Token 使用量和流式输出。
 * 编辑模式下显示删除按钮和选中高亮。
 *
 * 视觉设计：
 * - 卡片头部：莫兰迪浅色背景 + 左上角黑白人形插画 + 角色名
 * - 卡片主体：白色背景 + 节点信息
 * - 色彩区分：莫兰迪色卡（蓝灰/灰绿/暖灰/紫灰）替代纯色色条
 */
import { memo } from "react";
import { Handle, Position } from "@xyflow/react";
import { X } from "lucide-react";
import { cn } from "@/lib/utils";
import { getRoleTheme, getRoleIllustration } from "./agent-illustration";

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
  // 编辑模式扩展
  isEditMode?: boolean;
  isSelected?: boolean;
  onDelete?: () => void;
  onSelect?: () => void;
  [key: string]: unknown;
}

function AgentNodeCardComponent({ data, selected }: { data: AgentNodeData; selected?: boolean }) {
  const {
    label,
    agentName,
    agentRole,
    outputArtifact,
    status,
    streamText,
    tokensUsed,
    error,
    statusColor,
    isEditMode,
    isSelected,
    onDelete,
    onSelect,
  } = data;

  const theme = getRoleTheme(agentRole);
  const Illustration = getRoleIllustration(agentRole);

  const statusLabel: Record<string, string> = {
    pending: "等待中",
    running: "执行中",
    completed: "已完成",
    failed: "失败",
    skipped: "跳过",
  };

  return (
    <div
      className={cn(
        "relative overflow-hidden rounded-xl border-2 bg-white shadow-lg transition-all duration-300 dark:bg-zinc-900",
        isEditMode && "cursor-pointer hover:shadow-xl",
        isEditMode && isSelected && "ring-2 ring-blue-500 ring-offset-2",
      )}
      style={{
        borderColor: isEditMode && isSelected ? "#3b82f6" : theme.morandiBorder,
        width: 240,
        minHeight: 120,
      }}
      onClick={(e) => {
        if (isEditMode && onSelect) {
          e.stopPropagation();
          onSelect();
        }
      }}
    >
      {/* 编辑模式删除按钮 */}
      {isEditMode && (
        <button
          onClick={(e) => {
            e.stopPropagation();
            onDelete?.();
          }}
          className="absolute -right-2 -top-2 z-10 flex h-6 w-6 items-center justify-center rounded-full bg-red-500 text-white shadow-md transition hover:bg-red-600"
          title="删除节点"
        >
          <X className="h-3.5 w-3.5" />
        </button>
      )}

      {/* 输入 handle（左侧） */}
      <Handle
        type="target"
        position={Position.Left}
        style={{
          background: theme.morandiBorder,
          width: isEditMode ? 12 : 10,
          height: isEditMode ? 12 : 10,
        }}
      />

      {/* ── 莫兰迪色卡头部（上半部分）── */}
      <div
        className="flex items-center gap-2.5 px-3 py-2.5"
        style={{ background: theme.morandiBg }}
      >
        {/* 左上角黑白人形插画 */}
        <div className="flex h-9 w-9 shrink-0 items-center justify-center">
          <Illustration size={32} />
        </div>

        <div className="flex-1 min-w-0">
          {/* 角色名 */}
          <div className="text-xs font-medium" style={{ color: theme.morandiText }}>
            {agentRole}
          </div>
          {/* 节点名称 */}
          <div className="text-sm font-semibold text-zinc-900 dark:text-white truncate">
            {label}
          </div>
        </div>

        {/* 状态指示 */}
        <div
          className="flex items-center gap-1 text-xs font-medium shrink-0"
          style={{ color: statusColor }}
        >
          {status === "running" && (
            <span className="inline-block h-2 w-2 animate-pulse rounded-full bg-blue-500" />
          )}
          {statusLabel[status] || status}
        </div>
      </div>

      {/* ── 卡片主体 ─── */}
      <div className="p-3">
        {/* Agent 名称 */}
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
        style={{
          background: theme.morandiBorder,
          width: isEditMode ? 12 : 10,
          height: isEditMode ? 12 : 10,
        }}
      />
    </div>
  );
}

export const AgentNodeCard = memo(AgentNodeCardComponent);

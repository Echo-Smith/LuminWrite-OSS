/**
 * AddNodeToolbar — 添加节点工具栏
 *
 * 编辑模式下浮在画布左上角，提供：
 * - 快速添加三种角色节点
 * - 切换编辑/查看模式按钮
 * - 保存按钮
 */
import { useWorkflowStore, validateDAG } from "@/stores/workflow-store";
import { useAgentStore } from "@/stores/agent-store";
import { toast } from "@/stores/toast-store";
import { Plus, Save, X, Loader2 } from "lucide-react";
import { useState, useCallback } from "react";

interface AddNodeToolbarProps {
  screenToFlowPosition: (clientPosition: { x: number; y: number }) => { x: number; y: number };
}

const ROLE_PRESETS = [
  { role: "researcher", label: "研究员", emoji: "🔍", color: "#3b82f6" },
  { role: "writer", label: "撰稿人", emoji: "✍️", color: "#10b981" },
  { role: "reviewer", label: "审校", emoji: "📋", color: "#f59e0b" },
];

export function AddNodeToolbar({ screenToFlowPosition }: AddNodeToolbarProps) {
  const addNode = useWorkflowStore((s) => s.addNode);
  const setEditMode = useWorkflowStore((s) => s.setEditMode);
  const savePlan = useWorkflowStore((s) => s.savePlan);
  const taskId = useWorkflowStore((s) => s.taskId);
  const workflowSpec = useWorkflowStore((s) => s.workflowSpec);
  const agents = useWorkflowStore((s) => s.agents);
  const errorMessage = useWorkflowStore((s) => s.errorMessage);
  const [saving, setSaving] = useState(false);

  const handleAddNode = useCallback(
    (role: string, label: string) => {
      // 在画布中央偏移位置添加
      const pos = screenToFlowPosition({
        x: window.innerWidth / 2 + (Math.random() - 0.5) * 100,
        y: window.innerHeight / 2 + (Math.random() - 0.5) * 100,
      });
      addNode(role, label, pos);
    },
    [addNode, screenToFlowPosition],
  );

  const handleSave = useCallback(async () => {
    if (!taskId) {
      toast.warning("无法保存", "未找到会话 ID", 3000);
      return;
    }

    // 先做前端校验
    const validation = validateDAG(workflowSpec?.nodes || [], agents);
    if (!validation.valid) {
      toast.error("DAG 校验失败", validation.error || "未知错误", 5000);
      return;
    }

    setSaving(true);
    const ok = await savePlan(taskId);
    setSaving(false);

    if (ok) {
      toast.success("保存成功", "DAG 计划已更新", 3000);
      setEditMode(false);
    } else {
      toast.error("保存失败", errorMessage || "服务器错误", 5000);
    }
  }, [taskId, savePlan, setEditMode, toast, workflowSpec, agents, errorMessage]);

  const handleCancel = useCallback(() => {
    setEditMode(false);
  }, [setEditMode]);

  return (
    <div className="absolute left-3 top-3 z-20 flex items-center gap-1.5 rounded-xl border border-zinc-200 bg-white p-1.5 shadow-xl dark:border-zinc-700 dark:bg-zinc-900">
      {/* 添加节点按钮组 */}
      <div className="flex items-center gap-0.5">
        {ROLE_PRESETS.map((preset) => (
          <button
            key={preset.role}
            onClick={() => handleAddNode(preset.role, preset.label)}
            className="flex items-center gap-1 rounded-lg px-2 py-1 text-xs font-medium transition hover:bg-zinc-100 dark:hover:bg-zinc-800"
            style={{ color: preset.color }}
            title={`添加${preset.label}节点`}
          >
            <Plus className="h-3 w-3" />
            <span>{preset.emoji}</span>
            <span className="hidden sm:inline">{preset.label}</span>
          </button>
        ))}
      </div>

      {/* 分隔线 */}
      <div className="h-5 w-px bg-zinc-200 dark:bg-zinc-700" />

      {/* 保存按钮 */}
      <button
        onClick={handleSave}
        disabled={saving}
        className="flex items-center gap-1 rounded-lg bg-blue-600 px-2.5 py-1 text-xs font-medium text-white transition hover:bg-blue-700 disabled:opacity-50"
      >
        {saving ? (
          <Loader2 className="h-3 w-3 animate-spin" />
        ) : (
          <Save className="h-3 w-3" />
        )}
        <span className="hidden sm:inline">{saving ? "保存中" : "保存"}</span>
      </button>

      {/* 取消按钮 */}
      <button
        onClick={handleCancel}
        className="flex items-center gap-1 rounded-lg px-2 py-1 text-xs font-medium text-zinc-500 transition hover:bg-zinc-100 hover:text-zinc-700 dark:hover:bg-zinc-800 dark:hover:text-zinc-300"
      >
        <X className="h-3 w-3" />
        <span className="hidden sm:inline">取消</span>
      </button>
    </div>
  );
}

/**
 * NodeEditPanel — 节点属性编辑面板
 *
 * 编辑模式下，选中节点后在画布右侧显示，
 * 可编辑节点 Label、Agent Role、输出 Artifact 类型、Persona 等。
 */
import { useWorkflowStore, BUILTIN_ROLE_CONFIGS, ARTIFACT_TYPES } from "@/stores/workflow-store";
import { X, Save } from "lucide-react";
import { useCallback, useMemo } from "react";

interface NodeEditPanelProps {
  nodeId: string;
}

const ROLE_OPTIONS = [
  { value: "researcher", label: "研究员", color: "#3b82f6" },
  { value: "writer", label: "撰稿人", color: "#10b981" },
  { value: "reviewer", label: "审校编辑", color: "#f59e0b" },
];

const CONTEXT_FORK_OPTIONS = [
  { value: 0, label: "完整历史 (Full)" },
  { value: 1, label: "最近 N 轮 (LastN)" },
  { value: 2, label: "仅摘要 (Summary)" },
];

export function NodeEditPanel({ nodeId }: NodeEditPanelProps) {
  const workflowSpec = useWorkflowStore((s) => s.workflowSpec);
  const agents = useWorkflowStore((s) => s.agents);
  const updateNode = useWorkflowStore((s) => s.updateNode);
  const updateAgent = useWorkflowStore((s) => s.updateAgent);
  const selectNode = useWorkflowStore((s) => s.selectNode);

  const node = useMemo(
    () => workflowSpec?.nodes.find((n) => n.id === nodeId),
    [workflowSpec, nodeId],
  );
  const agent = useMemo(
    () => agents.find((a) => a.id === node?.agent_id),
    [agents, node],
  );

  const handleLabelChange = useCallback(
    (value: string) => {
      updateNode(nodeId, { label: value });
      if (agent) {
        updateAgent(agent.id, { name: value });
      }
    },
    [nodeId, agent, updateNode, updateAgent],
  );

  const handleRoleChange = useCallback(
    (role: string) => {
      if (!agent) return;
      const config = BUILTIN_ROLE_CONFIGS[role];
      if (!config) return;
      updateAgent(agent.id, {
        role,
        base_role: role,
        allowed_tools: config.allowed_tools,
        can_produce: config.can_produce,
        can_consume: config.can_consume,
        persona: config.persona,
      });
      // 同步 output_artifact 为该角色的默认值
      updateNode(nodeId, { output_artifact: config.default_output });
    },
    [agent, nodeId, updateAgent, updateNode],
  );

  if (!node || !agent) return null;

  return (
    <div className="absolute right-3 top-3 z-20 w-72 rounded-xl border border-zinc-200 bg-white p-4 shadow-xl dark:border-zinc-700 dark:bg-zinc-900">
      {/* 头部 */}
      <div className="mb-3 flex items-center justify-between">
        <h3 className="text-sm font-semibold text-zinc-900 dark:text-white">
          编辑节点
        </h3>
        <button
          onClick={() => selectNode(null)}
          className="text-zinc-400 hover:text-zinc-600 dark:hover:text-zinc-200"
        >
          <X className="h-4 w-4" />
        </button>
      </div>

      {/* 节点 ID */}
      <div className="mb-3 text-xs text-zinc-400">
        ID: <code className="font-mono">{node.id}</code>
      </div>

      {/* Label */}
      <div className="mb-3">
        <label className="mb-1 block text-xs font-medium text-zinc-500 dark:text-zinc-400">
          节点名称
        </label>
        <input
          type="text"
          value={node.label}
          onChange={(e) => handleLabelChange(e.target.value)}
          className="w-full rounded-md border border-zinc-200 bg-white px-2.5 py-1.5 text-sm text-zinc-900 focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500 dark:border-zinc-700 dark:bg-zinc-800 dark:text-white"
        />
      </div>

      {/* Agent Role */}
      <div className="mb-3">
        <label className="mb-1 block text-xs font-medium text-zinc-500 dark:text-zinc-400">
          Agent 角色
        </label>
        <div className="flex gap-1.5">
          {ROLE_OPTIONS.map((opt) => (
            <button
              key={opt.value}
              onClick={() => handleRoleChange(opt.value)}
              className="flex-1 rounded-md border px-2 py-1 text-xs font-medium transition"
              style={{
                borderColor: agent.role === opt.value ? opt.color : undefined,
                background: agent.role === opt.value ? `${opt.color}15` : undefined,
                color: agent.role === opt.value ? opt.color : undefined,
              }}
              title={opt.label}
            >
              <span
                className="mr-1 inline-block h-2 w-2 rounded-full"
                style={{ background: opt.color }}
              />
              {opt.label}
            </button>
          ))}
        </div>
      </div>

      {/* Output Artifact */}
      <div className="mb-3">
        <label className="mb-1 block text-xs font-medium text-zinc-500 dark:text-zinc-400">
          输出交付物
        </label>
        <select
          value={node.output_artifact}
          onChange={(e) => updateNode(nodeId, { output_artifact: e.target.value })}
          className="w-full rounded-md border border-zinc-200 bg-white px-2.5 py-1.5 text-sm text-zinc-900 focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500 dark:border-zinc-700 dark:bg-zinc-800 dark:text-white"
        >
          {ARTIFACT_TYPES.map((t) => (
            <option key={t} value={t}>
              {t}
            </option>
          ))}
        </select>
      </div>

      {/* Context Fork */}
      <div className="mb-3">
        <label className="mb-1 block text-xs font-medium text-zinc-500 dark:text-zinc-400">
          上下文传递模式
        </label>
        <select
          value={node.context_fork ?? 0}
          onChange={(e) => updateNode(nodeId, { context_fork: parseInt(e.target.value) })}
          className="w-full rounded-md border border-zinc-200 bg-white px-2.5 py-1.5 text-sm text-zinc-900 focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500 dark:border-zinc-700 dark:bg-zinc-800 dark:text-white"
        >
          {CONTEXT_FORK_OPTIONS.map((opt) => (
            <option key={opt.value} value={opt.value}>
              {opt.label}
            </option>
          ))}
        </select>
      </div>

      {/* Persona */}
      <div className="mb-3">
        <label className="mb-1 block text-xs font-medium text-zinc-500 dark:text-zinc-400">
          Persona（角色设定）
        </label>
        <textarea
          value={agent.persona || ""}
          onChange={(e) => updateAgent(agent.id, { persona: e.target.value })}
          rows={3}
          className="w-full resize-none rounded-md border border-zinc-200 bg-white px-2.5 py-1.5 text-xs text-zinc-900 focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500 dark:border-zinc-700 dark:bg-zinc-800 dark:text-white"
        />
      </div>

      {/* 依赖 */}
      <div className="mb-3">
        <label className="mb-1 block text-xs font-medium text-zinc-500 dark:text-zinc-400">
          依赖节点（依赖关系 = 边的方向）
        </label>
        <div className="flex flex-wrap gap-1">
          {workflowSpec?.nodes
            .filter((n) => n.id !== nodeId)
            .map((n) => {
              const isDep = node.dependencies.includes(n.id);
              return (
                <button
                  key={n.id}
                  onClick={() => {
                    const newDeps = isDep
                      ? node.dependencies.filter((d) => d !== n.id)
                      : [...node.dependencies, n.id];
                    updateNode(nodeId, { dependencies: newDeps });
                  }}
                  className={`rounded px-1.5 py-0.5 text-[10px] font-medium transition ${
                    isDep
                      ? "bg-blue-100 text-blue-700 dark:bg-blue-950 dark:text-blue-300"
                      : "bg-zinc-100 text-zinc-500 hover:bg-zinc-200 dark:bg-zinc-800 dark:text-zinc-400"
                  }`}
                >
                  {n.label}
                </button>
              );
            })}
          {workflowSpec && workflowSpec.nodes.length <= 1 && (
            <span className="text-xs text-zinc-400">无其他节点</span>
          )}
        </div>
      </div>

      {/* 提示 */}
      <div className="rounded-md bg-blue-50 p-2 text-[10px] text-blue-600 dark:bg-blue-950 dark:text-blue-400">
        💡 修改依赖关系 = 调整分支。也可以直接在画布上拖拽节点之间的连线。
      </div>
    </div>
  );
}

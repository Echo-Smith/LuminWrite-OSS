/**
 * WorkflowInput — 工作流输入面板
 *
 * 用户输入写作意图，点击"规划"触发 Planner，
 * 或点击"运行"启动 DAG 执行。
 */
import { useState } from "react";
import { useWorkflowStore } from "@/stores/workflow-store";

interface WorkflowInputProps {
  onPlan: (input: string) => void;
  onRun: () => void;
}

export function WorkflowInput({ onPlan, onRun }: WorkflowInputProps) {
  const [input, setInput] = useState("");
  const runStatus = useWorkflowStore((s) => s.runStatus);
  const plan = useWorkflowStore((s) => s.plan);
  const totalTokensUsed = useWorkflowStore((s) => s.totalTokensUsed);

  const isPlanning = runStatus === "planning";
  const isRunning = runStatus === "running";
  const hasPlan = runStatus === "created" || runStatus === "completed" || runStatus === "failed";
  const isCompleted = runStatus === "completed";

  return (
    <div className="border-b border-zinc-200 bg-white p-4 dark:border-zinc-800 dark:bg-zinc-950">
      <div className="mb-3 flex items-center gap-2">
        <h2 className="text-lg font-semibold text-zinc-900 dark:text-white">
          📋 编辑部模式
        </h2>
        <span className="rounded-full bg-amber-100 px-2 py-0.5 text-xs font-medium text-amber-700 dark:bg-amber-950 dark:text-amber-400">
          Beta
        </span>
      </div>

      <div className="flex gap-2">
        <input
          type="text"
          value={input}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !isPlanning && input.trim()) {
              onPlan(input.trim());
            }
          }}
          placeholder="输入写作意图，如：2026年Q1中国宏观经济分析"
          className="flex-1 rounded-lg border border-zinc-200 bg-white px-3 py-2 text-sm text-zinc-900 placeholder-zinc-400 focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500 dark:border-zinc-700 dark:bg-zinc-900 dark:text-white"
          disabled={isPlanning || isRunning}
        />

        <button
          onClick={() => input.trim() && onPlan(input.trim())}
          disabled={isPlanning || isRunning || !input.trim()}
          className="rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50"
        >
          {isPlanning ? "规划中..." : "规划"}
        </button>

        {hasPlan && (
          <button
            onClick={onRun}
            disabled={isRunning || isCompleted}
            className="rounded-lg bg-green-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-green-700 disabled:cursor-not-allowed disabled:opacity-50"
          >
            {isRunning ? "执行中..." : isCompleted ? "已完成" : "运行"}
          </button>
        )}
      </div>

      {/* 规划结果摘要 */}
      {plan && (
        <div className="mt-3 rounded-lg bg-zinc-50 p-3 text-sm dark:bg-zinc-900">
          <div className="flex items-center gap-4 text-xs text-zinc-500 dark:text-zinc-400">
            <span>🤖 {plan.agents.length} 个 Agent</span>
            <span>📊 {plan.workflow.nodes.length} 个节点</span>
            <span>🔗 {plan.workflow.edges.length} 条边</span>
            {totalTokensUsed > 0 && <span>💰 {totalTokensUsed} tokens</span>}
          </div>
          {plan.rationale && (
            <p className="mt-2 text-xs text-zinc-600 dark:text-zinc-300">
              {plan.rationale}
            </p>
          )}
        </div>
      )}
    </div>
  );
}

/**
 * WorkflowPage — 工作台模式 DAG 工作流页面
 *
 * 整合输入面板、节点图画布和节点执行状态面板。
 * Editorial Transport Migration：命令走 REST（lib/workflow-api.ts），
 * 执行进度经全局 SSE（hooks/use-workflow-sse.ts）驱动 workflow-store。
 */
import { useCallback, useEffect } from "react";
import { WorkflowCanvas } from "./canvas";
import { WorkflowInput } from "./workflow-input";
import { useWorkflowStore } from "@/stores/workflow-store";
import { createWorkflow, createdViewToPlan, executeWorkflow, WorkflowApiError } from "@/lib/workflow-api";

export function WorkflowPage() {
  const plan = useWorkflowStore((s) => s.plan);
  const runStatus = useWorkflowStore((s) => s.runStatus);
  const taskId = useWorkflowStore((s) => s.taskId);
  const setUserInput = useWorkflowStore((s) => s.setUserInput);
  const setRunStatus = useWorkflowStore((s) => s.setRunStatus);
  const setPlan = useWorkflowStore((s) => s.setPlan);
  const setTaskId = useWorkflowStore((s) => s.setTaskId);
  const setWorkflowFailed = useWorkflowStore((s) => s.setWorkflowFailed);
  const reset = useWorkflowStore((s) => s.reset);

  useEffect(() => {
    // 页面卸载时重置工作流状态
    return () => {
      reset();
    };
  }, [reset]);

  // 触发 Planner — POST /api/v2/workflows（进度与结果即时返回）
  const handlePlan = useCallback(
    async (input: string) => {
      setUserInput(input);
      setRunStatus("planning");
      try {
        const view = await createWorkflow({ user_input: input });
        setPlan(createdViewToPlan(view));
        setTaskId(view.task_id);
      } catch (err) {
        setWorkflowFailed(
          err instanceof WorkflowApiError ? `规划失败：${err.message}` : "规划失败，请稍后重试",
        );
      }
    },
    [setUserInput, setRunStatus, setPlan, setTaskId, setWorkflowFailed],
  );

  // 启动 DAG 执行 — POST /api/v2/workflows/{id}/execute（进度经 SSE 推送）
  const handleRun = useCallback(() => {
    if (!taskId) return;
    executeWorkflow(taskId).catch((err) => {
      setWorkflowFailed(
        err instanceof WorkflowApiError ? `启动失败：${err.message}` : "启动失败，请稍后重试",
      );
    });
  }, [taskId, setWorkflowFailed]);

  return (
    <div className="flex h-full flex-col">
      {/* 输入面板 */}
      <WorkflowInput onPlan={handlePlan} onRun={handleRun} />

      {/* 节点图画布 */}
      <div className="relative min-h-0 flex-1">
        {plan ? (
          <WorkflowCanvas />
        ) : (
          <div className="flex h-full items-center justify-center text-zinc-400">
            <div className="text-center">
              <div className="mb-2 text-4xl">🎨</div>
              <p className="text-sm">
                {runStatus === "planning"
                  ? "正在分析写作意图，生成 Agent 集群..."
                  : '输入写作意图后点击"规划"开始'}
              </p>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

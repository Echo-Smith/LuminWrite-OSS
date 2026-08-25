/**
 * WorkflowPage — 工作台模式 DAG 工作流页面
 *
 * 整合输入面板、节点图画布和节点执行状态面板。
 * 通过 agent-store 的 WebSocket 连接与后端交互。
 * agent-store 的 handleServerMessage 会自动转发 workflow 和 node 消息到 workflow-store。
 */
import { useCallback, useEffect } from "react";
import { WorkflowCanvas } from "./canvas";
import { WorkflowInput } from "./workflow-input";
import { useWorkflowStore } from "@/stores/workflow-store";
import { useAgentStore } from "@/stores/agent-store";

export function WorkflowPage() {
  const plan = useWorkflowStore((s) => s.plan);
  const runStatus = useWorkflowStore((s) => s.runStatus);
  const taskId = useWorkflowStore((s) => s.taskId);
  const setUserInput = useWorkflowStore((s) => s.setUserInput);
  const setRunStatus = useWorkflowStore((s) => s.setRunStatus);
  const reset = useWorkflowStore((s) => s.reset);

  // 确保 WebSocket 连接已建立
  const connectWS = useAgentStore((s) => s.connectWS);
  const sendWS = useAgentStore((s) => s.sendWS);
  const wsConnected = useAgentStore((s) => s.wsConnected);

  useEffect(() => {
    // 页面加载时连接 WebSocket
    connectWS();
    // 页面卸载时重置工作流状态
    return () => {
      reset();
    };
  }, [connectWS, reset]);

  // 触发 Planner — 发送 workflow.start 消息（带 user_input）
  const handlePlan = useCallback(
    (input: string) => {
      setUserInput(input);
      setRunStatus("planning");
      sendWS("workflow.start", { user_input: input });
    },
    [setUserInput, setRunStatus, sendWS]
  );

  // 启动 DAG 执行 — 发送 workflow.start 消息（带 task_id）
  const handleRun = useCallback(() => {
    if (taskId) {
      sendWS("workflow.start", { task_id: taskId });
    }
  }, [sendWS, taskId]);

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
                  : !wsConnected
                    ? "正在连接服务器..."
                    : '输入写作意图后点击"规划"开始'}
              </p>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

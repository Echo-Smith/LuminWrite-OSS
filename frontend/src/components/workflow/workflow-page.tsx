/**
 * WorkflowPage — 编辑部模式 DAG 工作流页面
 *
 * 整合输入面板、节点图画布和节点执行状态面板。
 * 通过 WebSocket 与后端交互。
 */
import { useCallback, useEffect } from "react";
import { WorkflowCanvas } from "./canvas";
import { WorkflowInput } from "./workflow-input";
import { useWorkflowStore, type AgentConfig, type WorkflowSpec } from "@/stores/workflow-store";

// ─── WebSocket 适配器 ─────────────────────────────────────
// 复用 agent-store 的 WebSocket 连接，监听 workflow.*/node.* 消息
// 这里用一个简化接口，实际通过 props 或全局 WS 连接传入

interface WorkflowPageProps {
  wsSend?: (msg: { type: string; payload: Record<string, unknown> }) => void;
  lastMessage?: { type: string; payload: Record<string, unknown> } | null;
}

export function WorkflowPage({ wsSend, lastMessage }: WorkflowPageProps) {
  const {
    plan,
    runStatus,
    setRunStatus,
    setUserInput,
    setPlan,
    setNodeStarted,
    appendNodeStream,
    setNodeCompleted,
    setNodeFailed,
    taskId,
  } = useWorkflowStore();

  // 处理 WebSocket 消息
  useEffect(() => {
    if (!lastMessage) return;

    const msg = lastMessage;
    const payload = msg.payload || {};

    switch (msg.type) {
      case "workflow.created":
        setPlan({
          agents: (payload.agents as AgentConfig[]) || [],
          workflow: (payload.workflow as WorkflowSpec) || {} as WorkflowSpec,
          rationale: (payload.rationale as string) || "",
        });
        break;

      case "workflow.started":
        setRunStatus("running");
        break;

      case "node.started":
        setNodeStarted(payload.node_id as string, payload.agent_name as string);
        break;

      case "node.stream.delta":
        appendNodeStream(payload.node_id as string, payload.delta as string);
        break;

      case "node.completed":
        setNodeCompleted(
          payload.node_id as string,
          payload.artifact_id as string,
          payload.artifact_type as string,
          payload.tokens_used as number,
          payload.duration_ms as number
        );
        break;

      case "node.failed":
        setNodeFailed(
          payload.node_id as string,
          payload.error as string,
          payload.duration_ms as number
        );
        break;

      case "workflow.completed":
        setRunStatus("completed");
        break;

      case "workflow.failed":
        setRunStatus("failed");
        break;
    }
  }, [lastMessage, setPlan, setRunStatus, setNodeStarted, appendNodeStream, setNodeCompleted, setNodeFailed]);

  // 触发 Planner
  const handlePlan = useCallback(
    (input: string) => {
      setUserInput(input);
      setRunStatus("planning");
      wsSend?.({ type: "workflow.start", payload: { user_input: input } });
    },
    [setUserInput, setRunStatus, wsSend]
  );

  // 启动 DAG 执行
  const handleRun = useCallback(() => {
    wsSend?.({ type: "workflow.start", payload: { task_id: taskId } });
  }, [wsSend, taskId]);

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

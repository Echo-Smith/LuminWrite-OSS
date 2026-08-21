/**
 * WorkflowCanvas — React Flow 画布组件
 *
 * 渲染 DAG 节点图，支持拖拽、缩放和实时状态更新。
 */
import { useCallback, useMemo } from "react";
import {
  ReactFlow,
  Background,
  Controls,
  MiniMap,
  type NodeTypes,
  type Node,
  type Edge,
  ReactFlowProvider,
  useReactFlow,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { AgentNodeCard, type AgentNodeData } from "./agent-node";
import { useWorkflowStore } from "@/stores/workflow-store";

const nodeTypes: NodeTypes = {
  agentNode: AgentNodeCard,
};

function WorkflowCanvasInner() {
  const getFlowNodes = useWorkflowStore((s) => s.getFlowNodes);
  const getFlowEdges = useWorkflowStore((s) => s.getFlowEdges);
  const runStatus = useWorkflowStore((s) => s.runStatus);

  const nodes = useMemo(() => getFlowNodes() as Node<AgentNodeData>[], [getFlowNodes, runStatus]);
  const edges = useMemo(() => getFlowEdges() as Edge[], [getFlowEdges, runStatus]);

  const onNodeDrag = useCallback(() => {
    // 节点拖拽时不做额外处理，React Flow 自行管理位置
  }, []);

  return (
    <div className="h-full w-full">
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        onNodeDrag={onNodeDrag}
        fitView
        fitViewOptions={{ padding: 0.2, maxZoom: 1.2 }}
        minZoom={0.3}
        maxZoom={2}
        proOptions={{ hideAttribution: true }}
      >
        <Background color="#e4e4e7" gap={20} size={1.5} />
        <Controls
          className="!border-zinc-200 !bg-white !shadow-lg dark:!border-zinc-700 dark:!bg-zinc-900"
          showInteractive={false}
        />
        <MiniMap
          className="!rounded-lg !border !border-zinc-200 !bg-white/80 dark:!border-zinc-700 dark:!bg-zinc-900/80"
          nodeColor={(node) => {
            const data = node.data as AgentNodeData;
            return data?.statusColor || "#94a3b8";
          }}
          maskColor="rgba(0, 0, 0, 0.1)"
          pannable
          zoomable
        />
      </ReactFlow>
    </div>
  );
}

export function WorkflowCanvas() {
  return (
    <ReactFlowProvider>
      <WorkflowCanvasInner />
    </ReactFlowProvider>
  );
}

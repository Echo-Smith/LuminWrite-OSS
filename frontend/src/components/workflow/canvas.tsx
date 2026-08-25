/**
 * WorkflowCanvas — React Flow 画布组件
 *
 * 渲染 DAG 节点图，支持拖拽、缩放和实时状态更新。
 * 编辑模式下支持增删节点、拉线连接和分支调整。
 * 深色模式：通过检测 <html>.dark class 自动适配。
 */
import { useCallback, useMemo, useState, useEffect, useRef } from "react";
import {
  ReactFlow,
  Background,
  Controls,
  MiniMap,
  type NodeTypes,
  type Node,
  type Edge,
  type Connection,
  ReactFlowProvider,
  useReactFlow,
  ConnectionMode,
  type OnConnect,
  type OnEdgesDelete,
  type OnNodesDelete,
  type OnNodeDrag,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { AgentNodeCard, type AgentNodeData } from "./agent-node";
import { NodeEditPanel } from "./node-edit-panel";
import { AddNodeToolbar } from "./add-node-toolbar";
import { useWorkflowStore } from "@/stores/workflow-store";

const nodeTypes: NodeTypes = {
  agentNode: AgentNodeCard,
};

/** 检测 <html> 上是否有 .dark class，与 useTheme 保持一致 */
function useDarkMode(): "dark" | "light" {
  const [isDark, setIsDark] = useState(() =>
    typeof document !== "undefined" && document.documentElement.classList.contains("dark")
  );
  useEffect(() => {
    const observer = new MutationObserver(() => {
      setIsDark(document.documentElement.classList.contains("dark"));
    });
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ["class"] });
    return () => observer.disconnect();
  }, []);
  return isDark ? "dark" : "light";
}

function WorkflowCanvasInner() {
  const getFlowNodes = useWorkflowStore((s) => s.getFlowNodes);
  const getFlowEdges = useWorkflowStore((s) => s.getFlowEdges);
  const runStatus = useWorkflowStore((s) => s.runStatus);
  const isEditMode = useWorkflowStore((s) => s.isEditMode);
  const selectedNodeId = useWorkflowStore((s) => s.selectedNodeId);
  const addEdgeStore = useWorkflowStore((s) => s.addEdge);
  const removeEdge = useWorkflowStore((s) => s.removeEdge);
  const removeNode = useWorkflowStore((s) => s.removeNode);
  const syncNodePosition = useWorkflowStore((s) => s.syncNodePosition);
  const selectNode = useWorkflowStore((s) => s.selectNode);
  const colorMode = useDarkMode();
  const { screenToFlowPosition } = useReactFlow();
  const containerRef = useRef<HTMLDivElement>(null);

  // 编辑模式下重新计算 nodes/edges，加入 selected 状态
  const nodes = useMemo(() => {
    const flowNodes = getFlowNodes() as Node<AgentNodeData>[];
    if (isEditMode) {
      return flowNodes.map((n) => ({
        ...n,
        selected: n.id === selectedNodeId,
        data: {
          ...n.data,
          isEditMode: true,
          isSelected: n.id === selectedNodeId,
          onDelete: () => removeNode(n.id),
          onSelect: () => selectNode(n.id),
        },
      }));
    }
    return flowNodes;
  }, [getFlowNodes, runStatus, isEditMode, selectedNodeId, removeNode, selectNode]);

  const edges = useMemo(() => {
    const flowEdges = getFlowEdges() as Edge[];
    if (isEditMode) {
      return flowEdges.map((e) => ({
        ...e,
        animated: false,
        style: { stroke: "#a1a1aa", strokeWidth: 2 },
        interactionWidth: 20,
      }));
    }
    return flowEdges;
  }, [getFlowEdges, runStatus, isEditMode]);

  // 拉线连接
  const onConnect: OnConnect = useCallback(
    (connection: Connection) => {
      if (isEditMode && connection.source && connection.target) {
        addEdgeStore(connection);
      }
    },
    [isEditMode, addEdgeStore],
  );

  // 删除节点
  const onNodesDelete: OnNodesDelete = useCallback(
    (nodesToDelete) => {
      for (const n of nodesToDelete) {
        removeNode(n.id);
      }
    },
    [removeNode],
  );

  // 删除边
  const onEdgesDelete: OnEdgesDelete = useCallback(
    (edgesToDelete) => {
      for (const e of edgesToDelete) {
        removeEdge(e.id);
      }
    },
    [removeEdge],
  );

  // 节点拖拽结束 → 同步位置
  const onNodeDragStop: OnNodeDrag = useCallback(
    (_event, node) => {
      syncNodePosition(node.id, node.position);
    },
    [syncNodePosition],
  );

  // 点击画布空白 → 取消选中
  const onPaneClick = useCallback(() => {
    if (isEditMode && selectedNodeId) {
      selectNode(null);
    }
  }, [isEditMode, selectedNodeId, selectNode]);

  // 双击空白处 → 添加节点
  const onDoubleClick = useCallback(
    (event: React.MouseEvent) => {
      if (!isEditMode) return;
      // 只处理编辑模式下的双击空白
      // 实际添加节点的逻辑在 AddNodeToolbar 中处理
    },
    [isEditMode],
  );

  return (
    <div className="relative h-full w-full" ref={containerRef}>
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        onConnect={onConnect}
        onNodesDelete={onNodesDelete}
        onEdgesDelete={onEdgesDelete}
        onNodeDragStop={onNodeDragStop}
        onPaneClick={onPaneClick}
        fitView
        fitViewOptions={{ padding: 0.2, maxZoom: 1.2 }}
        minZoom={0.3}
        maxZoom={2}
        proOptions={{ hideAttribution: true }}
        colorMode={colorMode}
        connectionMode={isEditMode ? ConnectionMode.Loose : ConnectionMode.Strict}
        nodesConnectable={isEditMode}
        nodesDraggable={isEditMode}
        edgesReconnectable={isEditMode}
        deleteKeyCode={isEditMode ? ["Backspace", "Delete"] : []}
      >
        <Background color={colorMode === "dark" ? "#3f3f46" : "#e4e4e7"} gap={20} size={1.5} />
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
          maskColor={colorMode === "dark" ? "rgba(255, 255, 255, 0.05)" : "rgba(0, 0, 0, 0.1)"}
          pannable
          zoomable
        />
      </ReactFlow>

      {/* 编辑模式工具栏 */}
      {isEditMode && (
        <AddNodeToolbar screenToFlowPosition={screenToFlowPosition} />
      )}

      {/* 编辑模式节点属性面板 */}
      {isEditMode && selectedNodeId && (
        <NodeEditPanel nodeId={selectedNodeId} />
      )}
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

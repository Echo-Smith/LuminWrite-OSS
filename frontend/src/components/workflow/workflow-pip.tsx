/**
 * DAG 工作流画中画窗口 — 悬浮在文档视图之上
 * 
 * 功能：
 * - 拖拽移动位置（拖动标题栏）
 * - 调整窗口大小（拖动右下角）
 * - 最小化到右下角（显示简化进度）
 * - Dock 到右侧（吸附到屏幕边缘）
 * - 关闭（隐藏窗口，不影响后台执行）
 * 
 * 状态持久化到 workspace-layout-store
 */
import { useEffect, useRef, useState, useCallback } from "react";
import { 
  Maximize2, 
  Minimize2, 
  X, 
  GripVertical, 
  PanelRight,
  Network,
  Loader2,
  CheckCircle2,
  XCircle,
  Pause
} from "lucide-react";
import { WorkflowCanvas } from "./canvas";
import { useWorkspaceLayoutStore } from "@/stores/workspace-layout-store";
import { useWorkflowStore } from "@/stores/workflow-store";
import { cn } from "@/lib/utils";

const PIP_MIN_WIDTH = 400;
const PIP_MIN_HEIGHT = 300;
const PIP_MAX_WIDTH_RATIO = 0.9; // 90vw
const PIP_MAX_HEIGHT_RATIO = 0.9; // 90vh
const DOCK_THRESHOLD = 50; // 靠近边缘多少px触发吸附
const MINIMIZED_WIDTH = 320;
const MINIMIZED_HEIGHT = 80;

type DockZone = "none" | "right" | "bottom";

export function WorkflowCanvasPIP() {
  const visible = useWorkspaceLayoutStore((s) => s.dagPipVisible);
  const position = useWorkspaceLayoutStore((s) => s.dagPipPosition);
  const size = useWorkspaceLayoutStore((s) => s.dagPipSize);
  const minimized = useWorkspaceLayoutStore((s) => s.dagPipMinimized);
  const docked = useWorkspaceLayoutStore((s) => s.dagPipDocked);
  
  const setVisible = useWorkspaceLayoutStore((s) => s.setDagPipVisible);
  const setPosition = useWorkspaceLayoutStore((s) => s.setDagPipPosition);
  const setSize = useWorkspaceLayoutStore((s) => s.setDagPipSize);
  const setMinimized = useWorkspaceLayoutStore((s) => s.setDagPipMinimized);
  const setDocked = useWorkspaceLayoutStore((s) => s.setDagPipDocked);

  const runStatus = useWorkflowStore((s) => s.runStatus);
  const nodeStates = useWorkflowStore((s) => s.nodeStates);
  const totalTokensUsed = useWorkflowStore((s) => s.totalTokensUsed);

  const [isDragging, setIsDragging] = useState(false);
  const [isResizing, setIsResizing] = useState(false);
  const [dockPreview, setDockPreview] = useState<DockZone>("none");
  const dragStartRef = useRef({ x: 0, y: 0, posX: 0, posY: 0 });
  const resizeStartRef = useRef({ x: 0, y: 0, width: 0, height: 0 });

  // 不可见时不渲染
  if (!visible) return null;

  // 计算节点执行进度
  const nodeCount = Array.from(nodeStates.values()).length;
  const completedCount = Array.from(nodeStates.values()).filter(
    (s) => s.status === "completed"
  ).length;
  const failedCount = Array.from(nodeStates.values()).filter(
    (s) => s.status === "failed"
  ).length;

  // 状态图标和文案
  const statusInfo = {
    idle: { icon: null, label: "空闲", color: "text-muted-foreground" },
    planning: { icon: <Loader2 className="h-4 w-4 animate-spin" />, label: "规划中", color: "text-blue-500" },
    created: { icon: <CheckCircle2 className="h-4 w-4" />, label: "已就绪", color: "text-green-500" },
    running: { icon: <Loader2 className="h-4 w-4 animate-spin" />, label: "执行中", color: "text-blue-500" },
    completed: { icon: <CheckCircle2 className="h-4 w-4" />, label: "已完成", color: "text-green-500" },
    failed: { icon: <XCircle className="h-4 w-4" />, label: "失败", color: "text-red-500" },
    paused: { icon: <Pause className="h-4 w-4" />, label: "已暂停", color: "text-amber-500" },
  }[runStatus] || { icon: null, label: runStatus, color: "text-muted-foreground" };

  // 拖拽开始
  const handleDragStart = useCallback((e: React.MouseEvent) => {
    if (minimized || docked !== "none") return;
    setIsDragging(true);
    dragStartRef.current = {
      x: e.clientX,
      y: e.clientY,
      posX: position.x,
      posY: position.y,
    };
  }, [minimized, docked, position]);

  // 调整大小开始
  const handleResizeStart = useCallback((e: React.MouseEvent) => {
    if (minimized || docked !== "none") return;
    e.stopPropagation();
    setIsResizing(true);
    resizeStartRef.current = {
      x: e.clientX,
      y: e.clientY,
      width: size.width,
      height: size.height,
    };
  }, [minimized, docked, size]);

  // 鼠标移动处理
  useEffect(() => {
    if (!isDragging && !isResizing) return;

    const handleMouseMove = (e: MouseEvent) => {
      if (isDragging) {
        const deltaX = e.clientX - dragStartRef.current.x;
        const deltaY = e.clientY - dragStartRef.current.y;
        const newX = dragStartRef.current.posX + deltaX;
        const newY = dragStartRef.current.posY + deltaY;

        // 边界限制
        const maxX = window.innerWidth - size.width;
        const maxY = window.innerHeight - size.height;
        const clampedX = Math.max(0, Math.min(newX, maxX));
        const clampedY = Math.max(0, Math.min(newY, maxY));

        setPosition({ x: clampedX, y: clampedY });

        // 检测吸附区域
        const zone = checkDockZone({ x: clampedX, y: clampedY }, size);
        setDockPreview(zone);
      }

      if (isResizing) {
        const deltaX = e.clientX - resizeStartRef.current.x;
        const deltaY = e.clientY - resizeStartRef.current.y;
        const newWidth = resizeStartRef.current.width + deltaX;
        const newHeight = resizeStartRef.current.height + deltaY;

        const maxWidth = window.innerWidth * PIP_MAX_WIDTH_RATIO;
        const maxHeight = window.innerHeight * PIP_MAX_HEIGHT_RATIO;
        const clampedWidth = Math.max(PIP_MIN_WIDTH, Math.min(newWidth, maxWidth));
        const clampedHeight = Math.max(PIP_MIN_HEIGHT, Math.min(newHeight, maxHeight));

        setSize({ width: clampedWidth, height: clampedHeight });
      }
    };

    const handleMouseUp = () => {
      if (isDragging && dockPreview !== "none") {
        setDocked(dockPreview);
        setDockPreview("none");
      }
      setIsDragging(false);
      setIsResizing(false);
    };

    window.addEventListener("mousemove", handleMouseMove);
    window.addEventListener("mouseup", handleMouseUp);

    return () => {
      window.removeEventListener("mousemove", handleMouseMove);
      window.removeEventListener("mouseup", handleMouseUp);
    };
  }, [isDragging, isResizing, size, setPosition, setSize, setDocked, dockPreview]);

  // 检测吸附区域
  const checkDockZone = useCallback(
    (pos: { x: number; y: number }, currentSize: { width: number; height: number }): DockZone => {
      const windowWidth = window.innerWidth;
      const windowHeight = window.innerHeight;

      // 右侧吸附
      if (windowWidth - (pos.x + currentSize.width) < DOCK_THRESHOLD) {
        return "right";
      }

      // 底部吸附
      if (windowHeight - (pos.y + currentSize.height) < DOCK_THRESHOLD) {
        return "bottom";
      }

      return "none";
    },
    []
  );

  // 切换最小化
  const handleToggleMinimize = useCallback(() => {
    setMinimized(!minimized);
  }, [minimized, setMinimized]);

  // 切换 Dock 模式
  const handleToggleDock = useCallback(() => {
    if (docked === "right") {
      setDocked("none");
    } else {
      setDocked("right");
    }
  }, [docked, setDocked]);

  // 关闭窗口
  const handleClose = useCallback(() => {
    setVisible(false);
  }, [setVisible]);

  // 计算最终样式
  const pipStyle: React.CSSProperties = minimized
    ? {
        position: "fixed",
        bottom: "24px",
        right: "24px",
        width: MINIMIZED_WIDTH,
        height: MINIMIZED_HEIGHT,
        zIndex: 1000,
      }
    : docked === "right"
      ? {
          position: "fixed",
          top: 0,
          right: 0,
          width: "50vw",
          height: "100vh",
          zIndex: 1000,
        }
      : docked === "bottom"
        ? {
            position: "fixed",
            bottom: 0,
            left: 0,
            width: "100vw",
            height: "40vh",
            zIndex: 1000,
          }
        : {
            position: "fixed",
            left: position.x,
            top: position.y,
            width: size.width,
            height: size.height,
            zIndex: 1000,
          };

  return (
    <>
      {/* 吸附预览指示器 */}
      {dockPreview !== "none" && (
        <div
          className={cn(
            "pointer-events-none fixed z-[999] border-2 border-dashed border-primary/50 bg-primary/10 transition-all",
            dockPreview === "right" && "right-0 top-0 h-full w-1/2",
            dockPreview === "bottom" && "bottom-0 left-0 h-2/5 w-full"
          )}
        />
      )}

      {/* 画中画窗口 */}
      <div
        className={cn(
          "workflow-pip flex flex-col overflow-hidden rounded-xl border bg-background shadow-2xl transition-all",
          docked === "right" && "rounded-r-none border-r-0",
          docked === "bottom" && "rounded-b-none border-b-0",
          minimized && "rounded-lg"
        )}
        style={pipStyle}
      >
        {/* 标题栏 */}
        <div
          className={cn(
            "pip-header flex shrink-0 items-center justify-between border-b bg-muted/30 px-4 transition-all",
            minimized ? "h-full" : "h-12",
            !minimized && docked === "none" && "cursor-move"
          )}
          onMouseDown={handleDragStart}
        >
          <div className="flex min-w-0 items-center gap-3">
            <Network className="h-4 w-4 shrink-0 text-primary" />
            <span className="truncate text-sm font-medium">
              {minimized ? "工作流" : "DAG 工作流"}
            </span>
            {!minimized && (
              <div className={cn("flex items-center gap-1.5 text-xs", statusInfo.color)}>
                {statusInfo.icon}
                <span>{statusInfo.label}</span>
              </div>
            )}
            {!minimized && runStatus === "running" && nodeCount > 0 && (
              <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
                <span>
                  {completedCount}/{nodeCount}
                </span>
              </div>
            )}
          </div>

          <div className="flex items-center gap-1">
            {!minimized && docked === "none" && (
              <button
                onClick={handleToggleDock}
                className="rounded p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
                title="固定到右侧"
              >
                <PanelRight className="h-4 w-4" />
              </button>
            )}
            {docked === "right" && (
              <button
                onClick={handleToggleDock}
                className="rounded p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
                title="取消固定"
              >
                <GripVertical className="h-4 w-4" />
              </button>
            )}
            <button
              onClick={handleToggleMinimize}
              className="rounded p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
              title={minimized ? "展开" : "最小化"}
            >
              {minimized ? (
                <Maximize2 className="h-4 w-4" />
              ) : (
                <Minimize2 className="h-4 w-4" />
              )}
            </button>
            <button
              onClick={handleClose}
              className="rounded p-1 text-muted-foreground transition-colors hover:bg-destructive hover:text-destructive-foreground"
              title="关闭"
            >
              <X className="h-4 w-4" />
            </button>
          </div>
        </div>

        {/* 主内容区 */}
        {!minimized && (
          <div className="relative min-h-0 flex-1">
            <WorkflowCanvas />
          </div>
        )}

        {/* 最小化状态的简要信息 */}
        {minimized && (
          <div className="absolute inset-x-4 bottom-3 flex items-center justify-between text-xs text-muted-foreground">
            <span>
              {runStatus === "running" && `${completedCount}/${nodeCount} 节点`}
              {runStatus === "completed" && "已完成"}
              {runStatus === "failed" && `失败 ${failedCount} 个节点`}
            </span>
            {totalTokensUsed > 0 && (
              <span>{(totalTokensUsed / 1000).toFixed(1)}k tokens</span>
            )}
          </div>
        )}

        {/* 调整大小把手 */}
        {!minimized && docked === "none" && (
          <div
            className="pip-resize-handle absolute bottom-0 right-0 h-4 w-4 cursor-nwse-resize"
            onMouseDown={handleResizeStart}
          >
            <div className="absolute bottom-1 right-1 h-2 w-2 rounded-sm bg-muted-foreground/30" />
          </div>
        )}
      </div>
    </>
  );
}

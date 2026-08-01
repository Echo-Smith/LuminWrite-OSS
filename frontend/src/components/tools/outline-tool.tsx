/**
 * 引导模式提纲编辑器 — 消息流中的可交互卡片
 * 支持：编辑标题/论点、删除论点、新增论点、拖拽排序
 */
import { useState, useEffect, useRef, type DragEvent } from "react";
import { Check, RefreshCw, GripVertical, Clock, Plus, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import type { OutlineData, OutlineItem } from "@/lib/types";
import { useAgentStore } from "@/stores/agent-store";
import { cn } from "@/lib/utils";

interface OutlineToolProps {
  data: OutlineData;
  attempt?: number;
  maxAttempts?: number;
}

export function OutlineTool({ data: initialData, attempt = 1, maxAttempts = 5 }: OutlineToolProps) {
  const [data, setData] = useState<OutlineData>(initialData);
  const confirmOutline = useAgentStore((s) => s.confirmOutline);
  const regenerateOutline = useAgentStore((s) => s.regenerateOutline);
  const sessionStatus = useAgentStore((s) => {
    const session = s.sessions.find((sess) => sess.id === s.activeSessionId);
    return session?.status ?? "idle";
  });

  // Update local state when a new outline arrives (regeneration)
  const [lastTitle, setLastTitle] = useState(initialData.title);
  if (initialData.title !== lastTitle) {
    setLastTitle(initialData.title);
    setData(initialData);
  }

  const isRegenerating = sessionStatus === "running";
  const remainingAttempts = maxAttempts - attempt;
  const isLastAttempt = remainingAttempts <= 0;

  // WebSocket 保持提醒（不再倒计时，避免给用户压力）
  const awaitInputAt = useAgentStore((s) => {
    const session = s.sessions.find((sess) => sess.id === s.activeSessionId);
    return session?.awaitInputAt ?? null;
  });
  const [waitingDuration, setWaitingDuration] = useState(0);

  useEffect(() => {
    if (!awaitInputAt) {
      setWaitingDuration(0);
      return;
    }
    const update = () => {
      const elapsed = Math.floor((Date.now() - (awaitInputAt ?? Date.now())) / 1000);
      setWaitingDuration(elapsed);
    };
    update();
    const timer = setInterval(update, 1000);
    return () => clearInterval(timer);
  }, [awaitInputAt]);

  const minutes = Math.floor(waitingDuration / 60);
  const seconds = waitingDuration % 60;
  const timeStr = `${minutes}:${seconds.toString().padStart(2, "0")}`;

  // ── 编辑操作 ──
  const handleTitleChange = (value: string) => {
    setData((prev) => ({ ...prev, title: value }));
  };

  const handlePointChange = (index: number, value: string) => {
    setData((prev) => ({
      ...prev,
      outline: prev.outline.map((item, i) => (i === index ? { ...item, point: value } : item)),
    }));
  };

  const handleDeletePoint = (index: number) => {
    setData((prev) => ({
      ...prev,
      outline: prev.outline.filter((_, i) => i !== index),
    }));
  };

  const handleAddPoint = () => {
    setData((prev) => ({
      ...prev,
      outline: [...prev.outline, { point: "", type: "argument" as const }],
    }));
  };

  // ── 拖拽排序 ──
  const dragIndex = useRef<number | null>(null);
  const [overIndex, setOverIndex] = useState<number | null>(null);

  const handleDragStart = (e: DragEvent, index: number) => {
    dragIndex.current = index;
    e.dataTransfer.effectAllowed = "move";
  };

  const handleDragOver = (e: DragEvent, index: number) => {
    e.preventDefault();
    e.dataTransfer.dropEffect = "move";
    setOverIndex(index);
  };

  const handleDragLeave = () => {
    setOverIndex(null);
  };

  const handleDrop = (e: DragEvent, dropIndex: number) => {
    e.preventDefault();
    const srcIndex = dragIndex.current;
    if (srcIndex === null || srcIndex === dropIndex) {
      dragIndex.current = null;
      setOverIndex(null);
      return;
    }

    setData((prev) => {
      const items = [...prev.outline];
      const [moved] = items.splice(srcIndex, 1);
      items.splice(dropIndex, 0, moved);
      return { ...prev, outline: items };
    });

    dragIndex.current = null;
    setOverIndex(null);
  };

  const handleDragEnd = () => {
    dragIndex.current = null;
    setOverIndex(null);
  };

  // ── 提交操作 ──
  const handleConfirm = () => {
    confirmOutline(data);
  };

  const handleRegenerate = () => {
    regenerateOutline();
  };

  const typeLabel = (type: OutlineItem["type"]) => {
    switch (type) {
      case "opening": return "开头";
      case "conclusion": return "结尾";
      default: return "论点";
    }
  };

  const typeColor = (type: OutlineItem["type"]) => {
    switch (type) {
      case "opening": return "bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400";
      case "conclusion": return "bg-purple-100 text-purple-700 dark:bg-purple-900/30 dark:text-purple-400";
      default: return "bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400";
    }
  };

  return (
    <div className="rounded-lg border border-blue-200 dark:border-blue-900/50 bg-blue-50/50 dark:bg-blue-950/10 p-4 space-y-3">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-medium text-blue-900 dark:text-blue-300">
          引导模式 — 请确认或修改提纲
        </h3>
        <div className="flex items-center gap-3">
          {!isRegenerating && awaitInputAt && (
            <span className="flex items-center gap-1 text-xs text-muted-foreground">
              <Clock className="h-3 w-3" />
              已等待 {timeStr}
            </span>
          )}
        </div>
      </div>

      {/* 标题 */}
      <div className="flex items-center gap-2">
        <label className="text-xs text-muted-foreground shrink-0">标题：</label>
        <Input
          value={data.title}
          onChange={(e) => handleTitleChange(e.target.value)}
          className="bg-white dark:bg-background"
        />
      </div>

      {/* 提纲列表 — 可拖拽排序 */}
      <div className="space-y-2">
        {data.outline.map((item, i) => (
          <div
            key={i}
            draggable
            onDragStart={(e) => handleDragStart(e, i)}
            onDragOver={(e) => handleDragOver(e, i)}
            onDragLeave={handleDragLeave}
            onDrop={(e) => handleDrop(e, i)}
            onDragEnd={handleDragEnd}
            className={cn(
              "flex items-start gap-2 rounded-md transition-all",
              overIndex === i && "ring-2 ring-blue-300 dark:ring-blue-700 bg-blue-50 dark:bg-blue-900/20",
              "hover:bg-white/60 dark:hover:bg-accent/30"
            )}
          >
            <div className="flex items-center gap-1 shrink-0 pt-1.5 cursor-grab active:cursor-grabbing">
              <GripVertical className="h-3.5 w-3.5 text-muted-foreground/40" />
              <span className="text-xs text-muted-foreground w-4">{i + 1}.</span>
            </div>
            <Badge className={cn("shrink-0 mt-1.5", typeColor(item.type))}>
              {typeLabel(item.type)}
            </Badge>
            <Input
              value={item.point}
              onChange={(e) => handlePointChange(i, e.target.value)}
              className="bg-white dark:bg-background"
            />
            {/* 删除按钮 */}
            <button
              onClick={() => handleDeletePoint(i)}
              className="shrink-0 mt-1.5 flex h-7 w-7 items-center justify-center rounded-md text-muted-foreground/50 hover:text-red-500 hover:bg-red-50 dark:hover:bg-red-950/30 transition-ui"
              title="删除此论点"
            >
              <Trash2 className="h-3.5 w-3.5" />
            </button>
          </div>
        ))}
      </div>

      {/* 新增论点按钮 */}
      <button
        onClick={handleAddPoint}
        className="flex items-center gap-1.5 text-xs text-blue-600 dark:text-blue-400 hover:text-blue-700 dark:hover:text-blue-300 transition-ui py-1"
      >
        <Plus className="h-3.5 w-3.5" />
        新增分论点
      </button>

      {/* 操作按钮 */}
      <div className="flex items-center gap-2 pt-1">
        <Button size="sm" onClick={handleConfirm} disabled={sessionStatus === "running"}>
          <Check className="h-3.5 w-3.5" />
          确认提纲
        </Button>
        <Button
          size="sm"
          variant="outline"
          onClick={handleRegenerate}
          disabled={sessionStatus === "running" || isLastAttempt}
          title={isLastAttempt ? "已达到重新生成上限" : undefined}
        >
          {isRegenerating ? (
            <>
              <RefreshCw className="h-3.5 w-3.5 animate-spin" />
              生成中...
            </>
          ) : (
            <>
              <RefreshCw className="h-3.5 w-3.5" />
              {isLastAttempt ? "已用完重新生成次数" : `还可重新生成 ${remainingAttempts} 次`}
            </>
          )}
        </Button>
      </div>
      {isLastAttempt && (
        <p className="text-xs text-amber-600">
          ⚠ 已达到重新生成上限（{maxAttempts} 次），请基于当前提纲修改或确认。
        </p>
      )}
    </div>
  );
}

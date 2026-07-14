/**
 * 引导模式提纲编辑器 — 消息流中的可交互卡片
 */
import { useState, useEffect } from "react";
import { Check, RefreshCw, GripVertical, Clock } from "lucide-react";
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

  // WebSocket timeout countdown (10 min = 600s, matches backend readTimeout)
  const WS_TIMEOUT_SECONDS = 600;
  const awaitInputAt = useAgentStore((s) => {
    const session = s.sessions.find((sess) => sess.id === s.activeSessionId);
    return session?.awaitInputAt ?? null;
  });
  const [remainingSeconds, setRemainingSeconds] = useState(WS_TIMEOUT_SECONDS);

  useEffect(() => {
    if (!awaitInputAt) {
      setRemainingSeconds(WS_TIMEOUT_SECONDS);
      return;
    }
    const update = () => {
      const elapsed = Math.floor((Date.now() - (awaitInputAt ?? Date.now())) / 1000);
      setRemainingSeconds(Math.max(0, WS_TIMEOUT_SECONDS - elapsed));
    };
    update();
    const timer = setInterval(update, 1000);
    return () => clearInterval(timer);
  }, [awaitInputAt]);

  const minutes = Math.floor(remainingSeconds / 60);
  const seconds = remainingSeconds % 60;
  const timeStr = `${minutes}:${seconds.toString().padStart(2, "0")}`;
  const isUrgent = remainingSeconds <= 60 && remainingSeconds > 0;

  const handleTitleChange = (value: string) => {
    setData((prev) => ({ ...prev, title: value }));
  };

  const handlePointChange = (index: number, value: string) => {
    setData((prev) => ({
      ...prev,
      outline: prev.outline.map((item, i) => (i === index ? { ...item, point: value } : item)),
    }));
  };

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
      case "opening": return "bg-green-100 text-green-700";
      case "conclusion": return "bg-purple-100 text-purple-700";
      default: return "bg-blue-100 text-blue-700";
    }
  };

  return (
    <div className="rounded-lg border border-blue-200 bg-blue-50/50 p-4 space-y-3">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-medium text-blue-900">
          引导模式 — 请确认或修改提纲
        </h3>
        <div className="flex items-center gap-3">
          {!isRegenerating && (
            <span className={cn(
              "flex items-center gap-1 text-xs tabular-nums",
              isUrgent ? "text-red-600 font-medium" : "text-muted-foreground"
            )}>
              <Clock className="h-3 w-3" />
              {timeStr}
            </span>
          )}
        </div>
      </div>

      {isUrgent && !isRegenerating && (
        <p className="text-xs text-red-600">
          ⚠ 连接即将超时（剩余 {timeStr}），请尽快确认提纲，否则需要重新开始。
        </p>
      )}

      {/* 标题 */}
      <div className="flex items-center gap-2">
        <label className="text-xs text-muted-foreground shrink-0">标题：</label>
        <Input
          value={data.title}
          onChange={(e) => handleTitleChange(e.target.value)}
          className="bg-white"
        />
      </div>

      {/* 提纲列表 */}
      <div className="space-y-2">
        {data.outline.map((item, i) => (
          <div key={i} className="flex items-start gap-2">
            <div className="flex items-center gap-1 shrink-0 pt-1.5">
              <GripVertical className="h-3 w-3 text-muted-foreground/40" />
              <span className="text-xs text-muted-foreground">{i + 1}.</span>
            </div>
            <Badge className={cn("shrink-0", typeColor(item.type))}>
              {typeLabel(item.type)}
            </Badge>
            <Input
              value={item.point}
              onChange={(e) => handlePointChange(i, e.target.value)}
              className="bg-white"
            />
          </div>
        ))}
      </div>

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

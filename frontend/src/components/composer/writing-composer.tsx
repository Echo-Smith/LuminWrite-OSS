/**
 * 增强写作 Composer — 风格/模式/素材 + 输入框 + 发送/暂停/取消
 */
import { useState, useRef, useCallback, useEffect } from "react";
import { Send, Pause, Play, Square, Paperclip, X, TrendingUp, Lightbulb, FileEdit, MessageCircle, type LucideIcon } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { StylePicker } from "./style-picker";
import { ModePicker } from "./mode-picker";
import { useAgentStore } from "@/stores/agent-store";
import type { WriteMode } from "@/lib/types";
import { cn } from "@/lib/utils";

export function WritingComposer() {
  const [message, setMessage] = useState("");
  const [style, setStyle] = useState("yinyue");
  const [mode, setMode] = useState<WriteMode>("auto");
  const [materials, setMaterials] = useState<string[]>([]);
  const [showMaterials, setShowMaterials] = useState(false);
  const [materialInput, setMaterialInput] = useState("");
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  const startWriting = useAgentStore((s) => s.startWriting);
  const pauseWriting = useAgentStore((s) => s.pauseWriting);
  const resumeWriting = useAgentStore((s) => s.resumeWriting);
  const cancelWriting = useAgentStore((s) => s.cancelWriting);

  const sessionStatus = useAgentStore((s) => {
    const session = s.sessions.find((sess) => sess.id === s.activeSessionId);
    return session?.status ?? "idle";
  });

  const isRunning = sessionStatus === "running";
  const isPaused = sessionStatus === "paused";

  // 从 URL 参数读取预填选题
  useEffect(() => {
    const url = new URL(window.location.href);
    const topic = url.searchParams.get("topic") || url.pathname.split("/").pop();
    if (topic && topic !== "write" && topic.length > 0) {
      try {
        setMessage(decodeURIComponent(topic));
      } catch {
        setMessage(topic);
      }
    }
  }, []);

  // 自动调整 textarea 高度
  useEffect(() => {
    if (textareaRef.current) {
      textareaRef.current.style.height = "auto";
      textareaRef.current.style.height = `${Math.min(textareaRef.current.scrollHeight, 200)}px`;
    }
  }, [message]);

  const handleSend = useCallback(() => {
    if (!message.trim() || isRunning) return;

    startWriting({
      message: message.trim(),
      style,
      mode,
      user_materials: materials.length > 0 ? materials : undefined,
    });

    setMessage("");
  }, [message, style, mode, materials, isRunning, startWriting]);

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      handleSend();
    }
  };

  const handleAddMaterial = () => {
    if (materialInput.trim()) {
      setMaterials([...materials, materialInput.trim()]);
      setMaterialInput("");
    }
  };

  return (
    <div className="border-t bg-background">
      {/* 暂停状态提示栏 */}
      {isPaused && (
        <div className="flex items-center gap-2 bg-amber-50 border-b border-amber-200 px-3 py-1.5">
          <Pause className="h-3.5 w-3.5 text-amber-600" />
          <span className="text-xs text-amber-700 font-medium">写作已暂停</span>
          <span className="text-xs text-amber-500">— 点击播放按钮继续生成</span>
        </div>
      )}

      {/* 素材标签 */}
      {materials.length > 0 && (
        <div className="flex flex-wrap gap-1.5 px-3 pt-2">
          {materials.map((mat, i) => (
            <div
              key={i}
              className="flex items-center gap-1 rounded-md bg-muted px-2 py-1 text-xs"
            >
              <Paperclip className="h-3 w-3 text-muted-foreground" />
              <span className="max-w-[200px] truncate">{mat.slice(0, 50)}...</span>
              <button
                onClick={() => setMaterials(materials.filter((_, idx) => idx !== i))}
                className="text-muted-foreground hover:text-foreground"
              >
                <X className="h-3 w-3" />
              </button>
            </div>
          ))}
        </div>
      )}

      {/* 素材输入展开区 */}
      {showMaterials && (
        <div className="px-3 pt-2">
          <div className="flex gap-2">
            <Textarea
              value={materialInput}
              onChange={(e) => setMaterialInput(e.target.value)}
              placeholder="粘贴参考素材，回车添加..."
              className="h-16 text-xs"
              onKeyDown={(e) => {
                if (e.key === "Enter" && !e.shiftKey) {
                  e.preventDefault();
                  handleAddMaterial();
                }
              }}
            />
            <Button size="sm" variant="outline" onClick={handleAddMaterial}>
              添加
            </Button>
          </div>
        </div>
      )}

      {/* Suggestion 快捷入口（仅空闲且无输入时显示） */}
      {!isRunning && !isPaused && !message.trim() && (
        <div className="flex flex-wrap gap-1.5 px-3 pt-2">
          <SuggestionButton
            icon={TrendingUp}
            label="基于热搜写评论"
            onClick={() => setMessage("基于热搜写一篇评论")}
          />
          <SuggestionButton
            icon={FileEdit}
            label="写一篇议论文"
            onClick={() => setMessage("写一篇关于人工智能与就业的议论文")}
          />
          <SuggestionButton
            icon={Lightbulb}
            label="帮我构思选题"
            onClick={() => setMessage("帮我构思3个关于城市交通的选题")}
          />
          <SuggestionButton
            icon={MessageCircle}
            label="提炼核心观点"
            onClick={() => setMessage("提炼以下文章的核心观点：\n\n")}
          />
        </div>
      )}

      {/* 控件行 */}
      <div className="flex items-center gap-2 px-3 pt-2">
        <StylePicker value={style} onChange={setStyle} />
        <ModePicker value={mode} onChange={setMode} />
        <Button
          variant="ghost"
          size="sm"
          className={cn("gap-1.5", showMaterials && "bg-accent")}
          onClick={() => setShowMaterials(!showMaterials)}
        >
          <Paperclip className="h-3.5 w-3.5" />
          <span className="text-xs">素材</span>
          {materials.length > 0 && (
            <span className="rounded-full bg-primary px-1.5 text-xs text-primary-foreground">
              {materials.length}
            </span>
          )}
        </Button>
      </div>

      {/* 输入框 + 操作按钮 */}
      <div className="flex items-end gap-2 p-3">
        <Textarea
          ref={textareaRef}
          value={message}
          onChange={(e) => setMessage(e.target.value)}
          onKeyDown={handleKeyDown}
          placeholder="输入写作要求，例如：基于热搜写一篇关于外卖骑手闯红灯的评论"
          className="min-h-[40px] resize-none text-sm"
          disabled={isRunning}
          rows={1}
        />

        {isRunning && (
          <Button
            variant="outline"
            size="icon"
            onClick={pauseWriting}
            className="shrink-0"
          >
            <Pause className="h-4 w-4" />
          </Button>
        )}

        {isPaused && (
          <Button
            variant="outline"
            size="icon"
            onClick={resumeWriting}
            className="shrink-0"
          >
            <Play className="h-4 w-4" />
          </Button>
        )}

        {(isRunning || isPaused) && (
          <Button
            variant="outline"
            size="icon"
            onClick={cancelWriting}
            className="shrink-0 text-red-600 hover:text-red-700"
          >
            <Square className="h-4 w-4" />
          </Button>
        )}

        {!isRunning && !isPaused && (
          <Button
            size="icon"
            onClick={handleSend}
            disabled={!message.trim()}
            className="shrink-0"
          >
            <Send className="h-4 w-4" />
          </Button>
        )}
      </div>
    </div>
  );
}

/**
 * Suggestion 快捷按钮
 */
function SuggestionButton({
  icon: Icon,
  label,
  onClick,
}: {
  icon: LucideIcon;
  label: string;
  onClick: () => void;
}) {
  return (
    <button
      onClick={onClick}
      className="flex items-center gap-1.5 rounded-full border bg-background px-3 py-1.5 text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
    >
      <Icon className="h-3.5 w-3.5" />
      {label}
    </button>
  );
}

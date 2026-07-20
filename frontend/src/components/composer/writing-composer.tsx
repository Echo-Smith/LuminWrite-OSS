/**
 * 增强写作 Composer — 风格/模式/素材 + 输入框 + 发送/暂停/取消
 *
 * 设计参考 assistant-ui / Claude Artifacts：
 *   - 大圆角药丸形容器（--composer-radius: 24px）
 *   - 聚焦时微妙阴影上浮
 *   - 极简边框（border/60 透明度）
 *   - 控件行与输入框整合在同一个容器内
 */
import { useState, useRef, useCallback, useEffect } from "react";
import { Pause, Play, Square, Plus, X, TrendingUp, Lightbulb, FileEdit, MessageCircle, PenLine, type LucideIcon } from "lucide-react";
import { StylePicker } from "./style-picker";
import { ModePicker } from "./mode-picker";
import { ModelPicker } from "./model-picker";
import { useAgentStore } from "@/stores/agent-store";
import type { WriteMode } from "@/lib/types";
import { cn } from "@/lib/utils";
import { StaggerItem } from "@/components/animation";

export function WritingComposer() {
  const [message, setMessage] = useState("");
  const [style, setStyle] = useState("yinyue");
  const [mode, setMode] = useState<WriteMode>("auto");
  const [model, setModel] = useState("deepseek-v4-flash");
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
      model,
      user_materials: materials.length > 0 ? materials : undefined,
    });

    setMessage("");
  }, [message, style, mode, model, materials, isRunning, startWriting]);

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
    <div className="px-4 pb-4 pt-2">
      {/* 暂停状态提示栏 */}
      {isPaused && (
        <div className="mb-2 flex items-center gap-2 rounded-lg bg-amber-50 dark:bg-amber-950/20 border border-amber-200/60 dark:border-amber-900/60 px-3 py-1.5 anim-fade-in">
          <Pause className="h-3.5 w-3.5 text-amber-600" />
          <span className="text-xs text-amber-700 dark:text-amber-400 font-medium">写作已暂停</span>
          <span className="text-xs text-amber-500">— 点击播放按钮继续</span>
        </div>
      )}

      {/* Suggestion 快捷入口（仅空闲且无输入时显示） */}
      {!isRunning && !isPaused && !message.trim() && (
        <div className="mb-2.5 flex flex-wrap gap-1.5">
          {[
            { icon: TrendingUp, label: "基于热搜写评论", text: "基于热搜写一篇评论" },
            { icon: FileEdit, label: "写一篇议论文", text: "写一篇关于人工智能与就业的议论文" },
            { icon: Lightbulb, label: "帮我构思选题", text: "帮我构思3个关于城市交通的选题" },
            { icon: MessageCircle, label: "提炼核心观点", text: "提炼以下文章的核心观点：\n\n" },
          ].map((s, i) => (
            <StaggerItem key={i} index={i} interval={60} animation="fade-up">
              <SuggestionButton icon={s.icon} label={s.label} onClick={() => setMessage(s.text)} />
            </StaggerItem>
          ))}
        </div>
      )}

      {/* ── Composer 药丸容器 ── */}
      <div className="composer-shell overflow-hidden">
        {/* 素材标签（在容器内顶部） */}
        {materials.length > 0 && (
          <div className="flex flex-wrap gap-1.5 px-4 pt-3 anim-fade-in">
            {materials.map((mat, i) => (
              <div
                key={i}
                className="flex items-center gap-1 rounded-md bg-muted px-2 py-1 text-xs"
              >
                <span className="max-w-[200px] truncate">{mat.slice(0, 50)}...</span>
                <button
                  onClick={() => setMaterials(materials.filter((_, idx) => idx !== i))}
                  className="text-muted-foreground hover:text-foreground transition-ui"
                >
                  <X className="h-3 w-3" />
                </button>
              </div>
            ))}
          </div>
        )}

        {/* 素材输入展开区 */}
        {showMaterials && (
          <div className="px-4 pt-3 anim-fade-in">
            <div className="flex gap-2">
              <input
                value={materialInput}
                onChange={(e) => setMaterialInput(e.target.value)}
                placeholder="粘贴参考素材，回车添加..."
                className="flex-1 rounded-lg border border-border/60 bg-background px-3 py-2 text-xs outline-none transition-ui placeholder:text-muted-foreground focus:border-border"
                onKeyDown={(e) => {
                  if (e.key === "Enter" && !e.shiftKey) {
                    e.preventDefault();
                    handleAddMaterial();
                  }
                }}
              />
              <button
                onClick={handleAddMaterial}
                className="rounded-lg border border-border/60 bg-card px-3 py-2 text-xs font-medium transition-ui hover:bg-accent"
              >
                添加
              </button>
            </div>
          </div>
        )}

        {/* 主输入区 */}
        <div className="flex items-end gap-2 px-4 pt-3">
          <textarea
            ref={textareaRef}
            value={message}
            onChange={(e) => setMessage(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder="输入写作要求，例如：基于热搜写一篇关于外卖骑手闯红灯的评论"
            className="flex-1 min-h-[40px] max-h-[200px] resize-none bg-transparent text-sm outline-none placeholder:text-muted-foreground/60 disabled:opacity-50"
            disabled={isRunning}
            rows={1}
          />
        </div>

        {/* 底部控件行 — 无分割线 */}
        <div className="flex items-center gap-2 px-4 py-2.5">
          {/* 左侧：+ 素材按钮 */}
          <button
            className={cn(
              "relative flex items-center justify-center h-7 w-7 rounded-full border border-border/60 text-muted-foreground transition-ui",
              showMaterials
                ? "bg-accent text-foreground border-transparent"
                : "hover:bg-accent hover:text-foreground"
            )}
            onClick={() => setShowMaterials(!showMaterials)}
            title="添加素材"
          >
            <Plus className="h-3.5 w-3.5" />
            {materials.length > 0 && (
              <span className="absolute -top-1 -right-1 rounded-full bg-primary px-1.5 text-[10px] font-medium text-primary-foreground leading-4">
                {materials.length}
              </span>
            )}
          </button>

          {/* 左侧：引导模式 */}
          <ModePicker value={mode} onChange={setMode} />

          {/* 左侧：风格选择（紧挨模式右侧） */}
          <StylePicker value={style} onChange={setStyle} />

          {/* 右侧弹性间距 */}
          <div className="flex-1" />

          {/* 右侧：模型选择 */}
          <ModelPicker value={model} onChange={setModel} />

          {/* 右侧：发送/暂停/取消按钮 — 笔头像黑色圆 */}
          <div className="flex items-center gap-1.5">
            {isRunning && (
              <button
                onClick={pauseWriting}
                className="flex items-center justify-center h-8 w-8 rounded-full bg-foreground text-background transition-transform-precise hover:scale-105 active:scale-95"
                title="暂停"
              >
                <Pause className="h-4 w-4" />
              </button>
            )}

            {isPaused && (
              <button
                onClick={resumeWriting}
                className="flex items-center justify-center h-8 w-8 rounded-full bg-foreground text-background transition-transform-precise hover:scale-105 active:scale-95"
                title="继续"
              >
                <Play className="h-4 w-4" />
              </button>
            )}

            {(isRunning || isPaused) && (
              <button
                onClick={cancelWriting}
                className="flex items-center justify-center h-8 w-8 rounded-full border border-destructive/30 text-destructive transition-transform-precise hover:scale-105 active:scale-95 hover:bg-destructive/10"
                title="停止"
              >
                <Square className="h-3.5 w-3.5" />
              </button>
            )}

            {!isRunning && !isPaused && (
              <button
                onClick={handleSend}
                disabled={!message.trim()}
                className={cn(
                  "flex items-center justify-center h-8 w-8 rounded-full transition-transform-precise",
                  message.trim()
                    ? "bg-foreground text-background hover:scale-105 active:scale-95"
                    : "bg-muted text-muted-foreground cursor-not-allowed"
                )}
                title="发送"
              >
                <PenLine className="h-4 w-4" />
              </button>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

/**
 * Suggestion 快捷按钮 — 极简 pill 样式
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
      className="flex items-center gap-1.5 rounded-full border border-border/60 bg-card px-3 py-1.5 text-xs text-muted-foreground transition-ui hover:bg-accent hover:text-foreground"
    >
      <Icon className="h-3.5 w-3.5" />
      {label}
    </button>
  );
}

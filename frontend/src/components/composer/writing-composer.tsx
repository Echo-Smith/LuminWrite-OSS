/**
 * 增强写作 Composer — 风格/模式/素材 + 输入框 + 发送/暂停/取消
 *
 * 设计参考 assistant-ui / Claude Artifacts：
 *   - 大圆角药丸形容器（--composer-radius: 24px）
 *   - 聚焦时微妙阴影上浮
 *   - 极简边框（border/60 透明度）
 *   - 控件行与输入框整合在同一个容器内
 *   - 支持动态建议词（从 API 获取热搜/历史）
 *   - 支持错误 Toast 通知
 */
import { useState, useRef, useCallback, useEffect, forwardRef, useImperativeHandle } from "react";
import { Pause, Play, Square, Plus, X, TrendingUp, Lightbulb, FileEdit, MessageCircle, PenLine, Paperclip, Loader2, Database, type LucideIcon } from "lucide-react";
import { StylePicker } from "./style-picker";
import { ModePicker } from "./mode-picker";
import { ModelPicker } from "./model-picker";
import { useAgentStore } from "@/stores/agent-store";
import { useSettingsStore } from "@/stores/settings-store";
import { toast } from "@/stores/toast-store";
import type { WriteMode } from "@/lib/types";
import { cn } from "@/lib/utils";
import { StaggerItem } from "@/components/animation";

export interface WritingComposerHandle {
  focusTextarea: () => void;
}

const FALLBACK_SUGGESTIONS = [
  { icon: TrendingUp, label: "基于热搜写评论", text: "基于热搜写一篇评论" },
  { icon: FileEdit, label: "写一篇议论文", text: "写一篇关于人工智能与就业的议论文" },
  { icon: Lightbulb, label: "帮我构思选题", text: "帮我构思3个关于城市交通的选题" },
  { icon: MessageCircle, label: "提炼核心观点", text: "提炼以下文章的核心观点：\n\n" },
];

// 稳定的空数组引用，避免 Zustand selector 每次返回新 [] 导致无限重渲染
const EMPTY_MATERIALS: string[] = [];

export const WritingComposer = forwardRef<WritingComposerHandle>(function WritingComposer(_, ref) {
  const [message, setMessage] = useState("");
  const [model, setModel] = useState("deepseek-v4-flash");
  const [materials, setMaterials] = useState<string[]>([]);
  const [showMaterials, setShowMaterials] = useState(false);
  const [materialInput, setMaterialInput] = useState("");
  const [uploading, setUploading] = useState(false);
  // Composer 抖动状态（空消息发送时触发）
  const [shakeKey, setShakeKey] = useState(0);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);

  // 从 session 读取选题注入的素材标签
  const injectedMaterials = useAgentStore((s) => {
    const session = s.sessions.find((sess) => sess.id === s.activeSessionId);
    return session?.injectedMaterials ?? EMPTY_MATERIALS;
  });

  const startWriting = useAgentStore((s) => s.startWriting);
  const pauseWriting = useAgentStore((s) => s.pauseWriting);
  const resumeWriting = useAgentStore((s) => s.resumeWriting);
  const cancelWriting = useAgentStore((s) => s.cancelWriting);
  const agentMode = useSettingsStore((s) => s.agentMode);

  // Sync mode & style from active session so external callers (e.g. topic center)
  // can set them via startWriting() and the composer reflects the change.
  const sessionMode = useAgentStore((s) => {
    const session = s.sessions.find((sess) => sess.id === s.activeSessionId);
    return (session?.mode as WriteMode) ?? "auto";
  });
  const sessionStyle = useAgentStore((s) => {
    const session = s.sessions.find((sess) => sess.id === s.activeSessionId);
    return session?.style ?? "yinyue";
  });
  const [mode, setMode] = useState<WriteMode>(sessionMode);
  const [style, setStyle] = useState(sessionStyle);

  // Update local state when session changes (e.g. new session from topic center)
  useEffect(() => { setMode(sessionMode); }, [sessionMode]);
  useEffect(() => { setStyle(sessionStyle); }, [sessionStyle]);

  // Propagate local changes back to session
  const handleModeChange = useCallback((m: WriteMode) => {
    setMode(m);
    useAgentStore.setState((s) => ({
      sessions: s.sessions.map((sess) =>
        sess.id === s.activeSessionId ? { ...sess, mode: m } : sess
      ),
    }));
  }, []);
  const handleStyleChange = useCallback((st: string) => {
    setStyle(st);
    useAgentStore.setState((s) => ({
      sessions: s.sessions.map((sess) =>
        sess.id === s.activeSessionId ? { ...sess, style: st } : sess
      ),
    }));
  }, []);

  const sessionStatus = useAgentStore((s) => {
    const session = s.sessions.find((sess) => sess.id === s.activeSessionId);
    return session?.status ?? "idle";
  });

  // Check if the session is waiting for user input (e.g. outline confirmation).
  // In this state, we don't show pause/play buttons — the user needs to interact
  // with the input widget (e.g. confirm/edit the outline), not control the agent.
  const isAwaitingInput = useAgentStore((s) => {
    const session = s.sessions.find((sess) => sess.id === s.activeSessionId);
    return session?.awaitInputAt != null;
  });

  const isRunning = sessionStatus === "running" && !isAwaitingInput;
  const isPaused = sessionStatus === "paused";

  // 自动调整 textarea 高度
  useEffect(() => {
    if (textareaRef.current) {
      textareaRef.current.style.height = "auto";
      textareaRef.current.style.height = `${Math.min(textareaRef.current.scrollHeight, 200)}px`;
    }
  }, [message]);

  // 暴露 focusTextarea 方法给父组件（用于 Cmd+K 快捷键）
  useImperativeHandle(ref, () => ({
    focusTextarea: () => {
      textareaRef.current?.focus();
    },
  }), []);

  const handleSend = useCallback(() => {
    if (isRunning) return;
    if (!message.trim()) {
      // 空消息发送 → 触发 Composer 抖动
      setShakeKey((k) => k + 1);
      return;
    }

    startWriting({
      message: message.trim(),
      style,
      mode,
      model,
      agent_mode: agentMode,
      user_materials: materials.length > 0 ? materials : undefined,
    });

    setMessage("");
  }, [message, style, mode, model, materials, isRunning, startWriting, agentMode]);

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
      e.preventDefault();
      handleSend();
      return;
    }
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      handleSend();
    }
  };

  const handleAddMaterial = () => {
    if (materialInput.trim()) {
      setMaterials([...materials, materialInput.trim()]);
      setMaterialInput("");
    } else {
      // 空素材添加 → 触发 Composer 抖动
      setShakeKey((k) => k + 1);
    }
  };

  const handleFileUpload = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    setUploading(true);
    try {
      const fd = new FormData();
      fd.append("file", file);
      fd.append("title", file.name);
      const res = await fetch("/api/v2/materials/upload", {
        method: "POST",
        headers: { Authorization: `Bearer ${localStorage.getItem("token") || ""}` },
        body: fd,
      });
      const json = await res.json();
      if (json.success && json.data) {
        // Add as material tag — agent will search KB for this user's chunks
        const tag = `📎 ${file.name}`;
        setMaterials([...materials, tag]);
      }
    } catch (err) {
      console.error("file upload failed", err);
      toast.error("文件上传失败", err instanceof Error ? err.message : "请稍后重试");
    } finally {
      setUploading(false);
      if (fileInputRef.current) fileInputRef.current.value = "";
    }
  };

  return (
    <div className="px-4 pb-4 pt-2">
      {/* 从选题注入的素材标签（只读展示） */}
      {injectedMaterials.length > 0 && !isRunning && !isPaused && (
        <div className="mb-2 flex flex-wrap items-center gap-1.5 anim-fade-in">
          <span className="flex items-center gap-1 text-xs text-emerald-600 dark:text-emerald-400 font-medium shrink-0">
            <Database className="h-3 w-3" />
            选题素材
          </span>
          {injectedMaterials.map((mat, i) => (
            <span
              key={i}
              className="flex items-center gap-1 rounded-md bg-emerald-50 dark:bg-emerald-950/20 border border-emerald-200/40 dark:border-emerald-900/40 px-2 py-0.5 text-xs text-emerald-700 dark:text-emerald-400 max-w-[200px] truncate"
              title={mat}
            >
              {mat.startsWith("📎 ") ? mat.slice(3) : mat}
            </span>
          ))}
        </div>
      )}

      {/* 暂停状态提示栏 */}
      {isPaused && (
        <div className="mb-2 flex items-center gap-2 rounded-lg bg-amber-50 dark:bg-amber-950/20 border border-amber-200/60 dark:border-amber-900/60 px-3 py-1.5 anim-fade-in">
          <Pause className="h-3.5 w-3.5 text-amber-600" />
          <span className="text-xs text-amber-700 dark:text-amber-400 font-medium">写作已暂停</span>
          <span className="text-xs text-amber-500">— 点击播放按钮继续</span>
        </div>
      )}

      {/* Suggestion 快捷入口（仅空闲且无输入时显示）*/}
      {!isRunning && !isPaused && !message.trim() && (
        <DynamicSuggestions onSelect={(text) => setMessage(text)} />
      )}

      {/* ── Composer 药丸容器 ── */}
      <div
        key={`composer-${shakeKey}`}
        className={cn("composer-shell overflow-hidden", shakeKey > 0 && "anim-shake")}
      >
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
              {/* 文件上传按钮 — 调用 docreader 解析 PDF/Word/图片 */}
              <input
                ref={fileInputRef}
                type="file"
                className="hidden"
                accept=".pdf,.doc,.docx,.ppt,.pptx,.xls,.xlsx,.txt,.md,.csv,.json,.png,.jpg,.jpeg,.gif,.bmp,.html"
                onChange={handleFileUpload}
              />
              <button
                onClick={() => fileInputRef.current?.click()}
                disabled={uploading}
                className="flex items-center gap-1.5 rounded-lg border border-border/60 bg-card px-3 py-2 text-xs font-medium transition-ui hover:bg-accent disabled:opacity-50"
                title="上传文件（PDF/Word/图片等，自动解析）"
              >
                {uploading ? (
                  <Loader2 className="h-3.5 w-3.5 animate-spin" />
                ) : (
                  <Paperclip className="h-3.5 w-3.5" />
                )}
                {uploading ? "解析中..." : "文件"}
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
            placeholder={isRunning ? "写作进行中… 可先编辑下一条需求，完成后发送" : "输入写作要求，例如：基于热搜写一篇关于外卖骑手闯红灯的评论"}
            className="flex-1 min-h-[40px] max-h-[200px] resize-none bg-transparent text-sm outline-none placeholder:text-muted-foreground/60"
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
          <ModePicker value={mode} onChange={handleModeChange} />

          {/* 左侧：风格选择（紧挨模式右侧） */}
          <StylePicker value={style} onChange={handleStyleChange} />

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
                <span key="pause-icon" className="anim-fade-scale flex items-center justify-center">
                  <Pause className="h-4 w-4" />
                </span>
              </button>
            )}

            {isPaused && (
              <button
                onClick={resumeWriting}
                className="flex items-center justify-center h-8 w-8 rounded-full bg-foreground text-background transition-transform-precise hover:scale-105 active:scale-95"
                title="继续"
              >
                <span key="play-icon" className="anim-fade-scale flex items-center justify-center">
                  <Play className="h-4 w-4" />
                </span>
              </button>
            )}

            {(isRunning || isPaused) && (
              <button
                onClick={cancelWriting}
                className="flex items-center justify-center h-8 w-8 rounded-full border border-destructive/30 text-destructive transition-transform-precise hover:scale-105 active:scale-95 hover:bg-destructive/10"
                title="停止"
              >
                <span key="stop-icon" className="anim-fade-scale flex items-center justify-center">
                  <Square className="h-3.5 w-3.5" />
                </span>
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
                <span key="send-icon" className="anim-fade-scale flex items-center justify-center">
                  <PenLine className="h-4 w-4" />
                </span>
              </button>
            )}

            {isRunning && message.trim() && (
              <span className="text-xs text-muted-foreground px-1 anim-fade-in" title="当前写作完成后可发送">
                待发
              </span>
            )}
          </div>
        </div>
      </div>
    </div>
  );
});

/**
 * DynamicSuggestions — 动态建议词
 * 尝试从 /api/v2/hot-topics 获取热搜话题，失败时用本地 fallback
 */
function DynamicSuggestions({ onSelect }: { onSelect: (text: string) => void }) {
  const [suggestions, setSuggestions] = useState(FALLBACK_SUGGESTIONS);

  useEffect(() => {
    let cancelled = false;
    fetch("/api/v2/hot-topics?limit=4")
      .then((res) => res.json())
      .then((data) => {
        if (cancelled) return;
        const topics = data?.data ?? data;
        if (Array.isArray(topics) && topics.length > 0) {
          const dynamic = topics.slice(0, 4).map((t: { title?: string; text?: string; category?: string }, i: number) => {
            const icons = [TrendingUp, FileEdit, Lightbulb, MessageCircle];
            const labels = ["热搜", "议论文", "选题", "提炼"];
            return {
              icon: icons[i % icons.length],
              label: t.category ?? labels[i % labels.length],
              text: t.text ?? `写一篇关于${t.title}的评论`,
            };
          });
          setSuggestions(dynamic);
        }
      })
      .catch(() => {
        // 保留 fallback，不需要错误提示
      });
    return () => { cancelled = true; };
  }, []);

  return (
    <div className="mb-2.5 flex flex-wrap gap-1.5">
      {suggestions.map((s, i) => (
        <StaggerItem key={i} index={i} interval={60} animation="fade-up">
          <SuggestionButton icon={s.icon} label={s.label} onClick={() => onSelect(s.text)} />
        </StaggerItem>
      ))}
    </div>
  );
}
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

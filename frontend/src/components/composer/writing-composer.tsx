/**
 * 增强写作 Composer — 风格/模式/素材 + 输入框 + 发送/取消
 *
 * 设计参考豆包：
 *   - 圆角矩形容器（--radius）
 *   - 聚焦时微妙阴影上浮
 *   - 极简边框（border/60 透明度）
 *   - 控件行与输入框整合在同一个容器内
 *   - 小屏幕下 Picker 仅显示图标
 *   - 支持错误 Toast 通知
 *   - 基于 Tiptap/ProseMirror 的富文本编辑器
 */
import { useState, useRef, useCallback, useEffect, forwardRef, useImperativeHandle } from "react";
import { Square, Plus, X, PenLine, Paperclip, Loader2, Database, BookOpen, FolderSearch } from "lucide-react";
import { StylePicker } from "./style-picker";
import { ModePicker } from "./mode-picker";
import { ModelPicker } from "./model-picker";
import { TiptapEditor, type TiptapEditorHandle } from "./tiptap-editor";
import { useAgentStore } from "@/stores/agent-store";
import { useSettingsStore } from "@/stores/settings-store";
import { useWorkflowStore } from "@/stores/workflow-store";
import { toast } from "@/stores/toast-store";
import type { WriteMode } from "@/lib/types";
import { cn } from "@/lib/utils";
import { listMaterials, getMaterialContent, type UserMaterial } from "@/lib/material-api";

export interface WritingComposerHandle {
  focusTextarea: () => void;
  /** 获取当前编辑器文本 */
  getText: () => string;
  /** 清空编辑器 */
  clear: () => void;
  /** 在光标处插入文本 */
  insertText: (text: string) => void;
}

export const WritingComposer = forwardRef<WritingComposerHandle>(function WritingComposer(_, ref) {
  const [message, setMessage] = useState("");
  const [model, setModel] = useState("deepseek-v4-flash");
  const [materials, setMaterials] = useState<string[]>([]);
  const [showMaterials, setShowMaterials] = useState(false);
  const [materialInput, setMaterialInput] = useState("");
  const [uploading, setUploading] = useState(false);
  // 素材库选择器
  const [showMaterialPicker, setShowMaterialPicker] = useState(false);
  const [kbMaterials, setKbMaterials] = useState<UserMaterial[]>([]);
  const [kbMaterialsLoading, setKbMaterialsLoading] = useState(false);
  const [kbSearchQuery, setKbSearchQuery] = useState("");
  // 自动检索开关状态（从 session 读取，默认 true）
  // 开启后 LLM 写作时自动从素材库检索相关内容；关闭则仅使用手动选择的素材
  const kbEnabled = useAgentStore((s) => {
    const session = s.sessions.find((sess) => sess.id === s.activeSessionId);
    return session?.kbEnabled ?? true;
  });
  const handleToggleKB = useCallback(() => {
    useAgentStore.setState((s) => ({
      sessions: s.sessions.map((sess) =>
        sess.id === s.activeSessionId ? { ...sess, kbEnabled: !sess.kbEnabled } : sess
      ),
    }));
  }, []);
  // Composer 抖动状态（空消息发送时触发）
  const [shakeKey, setShakeKey] = useState(0);
  const editorRef = useRef<TiptapEditorHandle>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);

  // 选题注入的素材现在统一在右侧详情面板的「素材」Tab 中管理，输入框上方不再单独展示

  const startWriting = useAgentStore((s) => s.startWriting);
  const cancelWriting = useAgentStore((s) => s.cancelWriting);
  const sendWS = useAgentStore((s) => s.sendWS);
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

  // 编辑部模式运行状态
  const wfRunStatus = useWorkflowStore((s) => s.runStatus);
  const isWorkflowBusy = agentMode === "editorial" && (wfRunStatus === "planning" || wfRunStatus === "running" || wfRunStatus === "created");

  const isRunning = (sessionStatus === "running" && !isAwaitingInput) || isWorkflowBusy;
  const isPaused = sessionStatus === "paused";

  // 暴露编辑器方法给父组件（用于 Cmd+K 快捷键 + 外部调用）
  useImperativeHandle(ref, () => ({
    focusTextarea: () => editorRef.current?.focus(),
    getText: () => editorRef.current?.getText() ?? "",
    clear: () => editorRef.current?.clear(),
    insertText: (text: string) => editorRef.current?.insertText(text),
  }), []);

  // handleSend 读取编辑器实时文本（避免 message 状态闭包延迟）
  const handleSend = useCallback(() => {
    if (isRunning) return;
    const currentText = editorRef.current?.getText() ?? "";
    if (!currentText.trim()) {
      // 空消息发送 → 触发 Composer 抖动
      setShakeKey((k) => k + 1);
      return;
    }

    if (agentMode === "editorial") {
      // 编辑部模式：发送 workflow.start 触发 Planner + DAG 执行
      const { setUserInput, setRunStatus } = useWorkflowStore.getState();
      setUserInput(currentText.trim());
      setRunStatus("planning");
      sendWS("workflow.start", { user_input: currentText.trim(), kb_enabled: kbEnabled, style_slug: style });
      editorRef.current?.clear();
      setMessage("");
      return;
    }

    startWriting({
      message: currentText.trim(),
      style,
      mode,
      model,
      agent_mode: agentMode,
      user_materials: materials.length > 0 ? materials : undefined,
      kb_enabled: kbEnabled,
    });

    editorRef.current?.clear();
    setMessage("");
  }, [isRunning, style, mode, model, materials, startWriting, agentMode, sendWS, kbEnabled]);

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
        body: fd,
      });
      const json = await res.json();
      if (json.success && json.data) {
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

  // ─── 从素材库选择素材 ───
  const loadKbMaterials = useCallback(async () => {
    setKbMaterialsLoading(true);
    try {
      const { materials } = await listMaterials(1, 50, "all");
      setKbMaterials(materials);
    } catch {
      // silent
    } finally {
      setKbMaterialsLoading(false);
    }
  }, []);

  const handleOpenMaterialPicker = useCallback(() => {
    setShowMaterialPicker(true);
    loadKbMaterials();
  }, [loadKbMaterials]);

  const handlePickMaterial = useCallback(async (mat: UserMaterial) => {
    let content = mat.content_preview || "";
    try {
      const detail = await getMaterialContent(mat.id);
      content = detail.content_preview || content;
    } catch {
      // use preview
    }
    const tag = `📎 ${mat.title}: ${content}`;
    setMaterials((prev) => [...prev, tag]);
    setShowMaterialPicker(false);
    toast.success("已注入素材", mat.title);
  }, []);

  return (
    <div className="relative">
      {/* 顶部渐变遮罩 — 从透明过渡到背景色，实现悬浮效果 */}
      <div className="pointer-events-none absolute -top-8 left-0 right-0 h-8 bg-gradient-to-t from-surface to-transparent z-10" />
      <div className="relative z-20 px-4 pb-4 pt-2">
      {/* ── Composer 圆角矩形容器 ── */}
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
              {/* 从素材库选择已有素材 */}
              <button
                onClick={handleOpenMaterialPicker}
                className="flex items-center gap-1.5 rounded-lg border border-border/60 bg-card px-3 py-2 text-xs font-medium transition-ui hover:bg-accent"
                title="从素材库选择已有素材"
              >
                <FolderSearch className="h-3.5 w-3.5" />
                素材库
              </button>
              {/* 自动检索开关 — 控制是否启用素材库自动检索 */}
              <button
                onClick={handleToggleKB}
                className={cn(
                  "flex items-center gap-1.5 rounded-lg border px-3 py-2 text-xs font-medium transition-ui",
                  kbEnabled
                    ? "border-primary/40 bg-primary/10 text-primary hover:bg-primary/15"
                    : "border-border/60 bg-card text-muted-foreground hover:bg-accent"
                )}
                title={kbEnabled ? "自动检索已开启（点击关闭）" : "自动检索已关闭（点击开启）"}
              >
                <BookOpen className="h-3.5 w-3.5" />
                {kbEnabled ? "自动检索 ON" : "自动检索 OFF"}
              </button>
            </div>
          </div>
        )}

        {/* 素材库选择器弹窗 */}
        {showMaterialPicker && (
          <div className="px-4 pt-3 anim-fade-in">
            <div className="rounded-lg border border-border/60 bg-card overflow-hidden">
              {/* Header */}
              <div className="flex items-center justify-between border-b px-3 py-2">
                <div className="flex items-center gap-2">
                  <FolderSearch className="h-3.5 w-3.5 text-muted-foreground" />
                  <span className="text-xs font-medium">从素材库选择</span>
                  {kbMaterialsLoading && <Loader2 className="h-3 w-3 animate-spin text-muted-foreground" />}
                </div>
                <button
                  onClick={() => setShowMaterialPicker(false)}
                  className="text-muted-foreground hover:text-foreground transition-ui"
                >
                  <X className="h-3.5 w-3.5" />
                </button>
              </div>
              {/* Search */}
              <div className="px-3 py-2 border-b">
                <input
                  value={kbSearchQuery}
                  onChange={(e) => setKbSearchQuery(e.target.value)}
                  placeholder="筛选素材标题..."
                  className="w-full rounded-md border border-border/40 bg-background px-2.5 py-1.5 text-xs outline-none placeholder:text-muted-foreground focus:border-border"
                />
              </div>
              {/* List */}
              <div className="max-h-[240px] overflow-y-auto">
                {kbMaterials.length === 0 ? (
                  <div className="py-8 text-center">
                    <Database className="h-6 w-6 text-muted-foreground mx-auto mb-1" />
                    <p className="text-xs text-muted-foreground">
                      {kbMaterialsLoading ? "加载中..." : "素材库为空，请先上传素材"}
                    </p>
                  </div>
                ) : (
                  kbMaterials
                    .filter((m) => !kbSearchQuery.trim() || m.title.toLowerCase().includes(kbSearchQuery.toLowerCase()))
                    .map((mat) => (
                      <button
                        key={mat.id}
                        onClick={() => handlePickMaterial(mat)}
                        className="flex items-start gap-2 w-full px-3 py-2 hover:bg-accent/50 transition-ui text-left border-b last:border-0"
                      >
                        <Database className="h-3 w-3 text-muted-foreground mt-0.5 shrink-0" />
                        <div className="flex-1 min-w-0">
                          <div className="text-xs font-medium truncate">{mat.title}</div>
                          <div className="text-[10px] text-muted-foreground line-clamp-1">
                            {mat.content_preview || mat.file_name || "—"}
                          </div>
                        </div>
                      </button>
                    ))
                )}
              </div>
            </div>
          </div>
        )}

        {/* 主输入区 — Tiptap 富文本编辑器 */}
        <TiptapEditor
          ref={editorRef}
          placeholder={isRunning ? (agentMode === "editorial" ? "工作流执行中…" : "写作进行中… 可先编辑下一条需求，完成后发送") : "输入写作要求，例如：基于热搜写一篇关于外卖骑手闯红灯的评论"}
          onChange={setMessage}
          onSend={handleSend}
          editable={!isRunning}
        />

        {/* 底部控件行 — 无分割线 */}
        <div className="flex items-center gap-2 px-4 py-2.5">
          {/* 左侧：+ 素材按钮 */}
          <button
            className={cn(
              "relative flex items-center justify-center h-8 w-8 rounded-xl text-muted-foreground transition-ui",
              showMaterials
                ? "bg-accent text-foreground"
                : "hover:bg-accent hover:text-foreground"
            )}
            onClick={() => setShowMaterials(!showMaterials)}
            title="添加素材"
          >
            <Plus className="h-[18px] w-[18px]" />
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
            {(isRunning || isPaused) && (
              <button
                onClick={() => {
                  if (agentMode === "editorial") {
                    useWorkflowStore.getState().reset();
                  } else {
                    cancelWriting();
                  }
                }}
                className="flex items-center justify-center h-9 w-9 rounded-xl border border-destructive/30 text-destructive transition-transform-precise hover:scale-105 active:scale-95 hover:bg-destructive/10"
                title="停止"
              >
                <span key="stop-icon" className="anim-fade-scale flex items-center justify-center">
                  <Square className="h-4 w-4" />
                </span>
              </button>
            )}

            {!isRunning && !isPaused && (
              <button
                onClick={handleSend}
                disabled={!message.trim()}
                className={cn(
                  "flex items-center justify-center h-9 w-9 rounded-xl transition-transform-precise",
                  message.trim()
                    ? "bg-foreground text-background hover:scale-105 active:scale-95"
                    : "bg-muted text-muted-foreground cursor-not-allowed"
                )}
                title="发送 (Enter)"
              >
                <span key="send-icon" className="anim-fade-scale flex items-center justify-center">
                  <PenLine className="h-[18px] w-[18px]" />
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
    </div>
  );
});



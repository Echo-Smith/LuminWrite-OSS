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
import { useState, useRef, useCallback, useEffect, useMemo, forwardRef, useImperativeHandle, type ChangeEvent, type DragEvent } from "react";
import { Square, Plus, X, Loader2, FolderSearch, Maximize2, Minimize2, ImagePlus, FileUp, Upload, ChevronRight, BookOpenText } from "lucide-react";
import { StylePicker } from "./style-picker";
import { ModePicker } from "./mode-picker";
import { ModelPicker } from "./model-picker";
import { Lumi } from "@/components/lumi/lumi";
import { TiptapEditor, type TiptapEditorHandle } from "./tiptap-editor";
import { KnowledgeMaterialDialog } from "./knowledge-material-dialog";
import { ResearchSettings } from "@/components/writing/research-settings";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Switch } from "@/components/ui/switch";
import { useWritingRuntimeStore } from "@/stores/writing-runtime-store";
import { useSettingsStore } from "@/stores/settings-store";
import { useWorkflowStore } from "@/stores/workflow-store";
import { createWorkflow, createdViewToPlan, cancelWorkflow } from "@/lib/workflow-api";
import { toast } from "@/stores/toast-store";
import type { WriteMode } from "@/lib/types";
import type { ApprovalMode, AssuranceLevel, OrchestrationMode } from "@/lib/writing-runtime-types";
import { cn } from "@/lib/utils";
import { listMaterials, getMaterialContent, uploadMaterial, type UserMaterial } from "@/lib/material-api";

export interface WritingComposerHandle {
  focusTextarea: () => void;
  /** 获取当前编辑器文本 */
  getText: () => string;
  /** 清空编辑器 */
  clear: () => void;
  /** 在光标处插入文本 */
  insertText: (text: string) => void;
  /** 正文选区唤起润色：切 polish 模式并预填选段 */
  beginPolish: (text: string) => void;
}

interface WritingComposerProps {
  compact?: boolean;
  floating?: boolean;
  onToggleWidth?: () => void;
}

interface ComposerMaterial {
  id: string;
  sourceId?: string;
  title: string;
  excerpt: string;
  payload: string;
}

export const WritingComposer = forwardRef<WritingComposerHandle, WritingComposerProps>(function WritingComposer({ compact = false, floating = false, onToggleWidth }, ref) {
  const [message, setMessage] = useState("");
  const [model, setModel] = useState("deepseek-v4-flash");
  const [materials, setMaterials] = useState<ComposerMaterial[]>([]);
  const [materialMenuOpen, setMaterialMenuOpen] = useState(false);
  const [materialInput, setMaterialInput] = useState("");
  const [uploading, setUploading] = useState(false);
  const [isDraggingFiles, setIsDraggingFiles] = useState(false);
  const [knowledgeDialogOpen, setKnowledgeDialogOpen] = useState(false);
  const [kbMaterials, setKbMaterials] = useState<UserMaterial[]>([]);
  const [kbMaterialsLoading, setKbMaterialsLoading] = useState(false);
  // 自动检索开关状态（从 session 读取，默认 true）
  // 开启后 LLM 写作时自动从知识库检索相关内容；关闭则仅使用手动选择的素材
  const sessionKbEnabled = useWritingRuntimeStore((s) => {
    const session = s.sessions.find((sess) => sess.id === s.activeSessionId);
    return session?.kbEnabled;
  });
  const [pendingKbEnabled, setPendingKbEnabled] = useState(true);
  const kbEnabled = sessionKbEnabled ?? pendingKbEnabled;
  useEffect(() => {
    if (sessionKbEnabled !== undefined) setPendingKbEnabled(sessionKbEnabled);
  }, [sessionKbEnabled]);
  const handleToggleKB = useCallback((checked: boolean) => {
    setPendingKbEnabled(checked);
    useWritingRuntimeStore.setState((s) => ({
      sessions: s.sessions.map((sess) =>
        sess.id === s.activeSessionId ? { ...sess, kbEnabled: checked } : sess
      ),
    }));
  }, []);
  // Composer 抖动状态（空消息发送时触发）
  const [shakeKey, setShakeKey] = useState(0);
  const editorRef = useRef<TiptapEditorHandle>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const imageInputRef = useRef<HTMLInputElement>(null);
  const dragDepthRef = useRef(0);

  const appendMaterial = useCallback((material: Omit<ComposerMaterial, "id">) => {
    setMaterials((current) => {
      if (material.sourceId && current.some((item) => item.sourceId === material.sourceId)) return current;
      return [
        ...current,
        { ...material, id: crypto.randomUUID?.() ?? `material-${Date.now()}-${current.length}` },
      ];
    });
  }, []);

  const attachedMaterialIds = useMemo(
    () => new Set(materials.flatMap((material) => material.sourceId ? [material.sourceId] : [])),
    [materials],
  );

  // 选题注入的素材现在统一在右侧详情面板的「素材」Tab 中管理，输入框上方不再单独展示

  const startWriting = useWritingRuntimeStore((s) => s.startWriting);
  const cancelWriting = useWritingRuntimeStore((s) => s.cancelWriting);
  const agentMode = useSettingsStore((s) => s.agentMode);

  // Sync mode & style from active session so external callers (e.g. topic center)
  // can set them via startWriting() and the composer reflects the change.
  const sessionMode = useWritingRuntimeStore((s) => {
    const session = s.sessions.find((sess) => sess.id === s.activeSessionId);
    return (session?.mode as WriteMode) ?? "auto";
  });
  const sessionStyle = useWritingRuntimeStore((s) => {
    const session = s.sessions.find((sess) => sess.id === s.activeSessionId);
    return session?.style ?? "yinyue";
  });
  const [mode, setMode] = useState<WriteMode>(sessionMode);
  const [style, setStyle] = useState(sessionStyle);
  const [orchestrationMode, setOrchestrationMode] = useState<OrchestrationMode>("auto");
  const [assuranceLevel, setAssuranceLevel] = useState<AssuranceLevel>("standard");
  const [approvalMode, setApprovalMode] = useState<ApprovalMode>("conditional");
  const [researchSettingsOpen, setResearchSettingsOpen] = useState(false);

  const handleResearchReviewSelect = useCallback(() => setResearchSettingsOpen(true), []);

  // 切换编排模式：离开研究综述时收起悬浮配置面板（配置已持久化，重开即回填）
  const handleOrchestrationChange = useCallback((value: OrchestrationMode) => {
    setOrchestrationMode((prev) => {
      if (prev === "research_review" && value !== "research_review") setResearchSettingsOpen(false);
      return value;
    });
  }, []);

  // Update local state when session changes (e.g. new session from topic center)
  useEffect(() => { setMode(sessionMode); }, [sessionMode]);
  useEffect(() => { setStyle(sessionStyle); }, [sessionStyle]);

  // Propagate local changes back to session
  const handleModeChange = useCallback((m: WriteMode) => {
    setMode(m);
    useWritingRuntimeStore.setState((s) => ({
      sessions: s.sessions.map((sess) =>
        sess.id === s.activeSessionId ? { ...sess, mode: m } : sess
      ),
    }));
  }, []);
  const handleStyleChange = useCallback((st: string) => {
    setStyle(st);
    useWritingRuntimeStore.setState((s) => ({
      sessions: s.sessions.map((sess) =>
        sess.id === s.activeSessionId ? { ...sess, style: st } : sess
      ),
    }));
    // 持久化上次风格选择，新建会话时自动使用
    useSettingsStore.getState().setLastStyle(st);
  }, []);

  const sessionStatus = useWritingRuntimeStore((s) => {
    const session = s.sessions.find((sess) => sess.id === s.activeSessionId);
    return session?.status ?? "idle";
  });

  // Check if the session is waiting for user input (e.g. outline confirmation).
  // In this state, we don't show pause/play buttons — the user needs to interact
  // with the input widget (e.g. confirm/edit the outline), not control the agent.
  const isAwaitingInput = useWritingRuntimeStore((s) => {
    const session = s.sessions.find((sess) => sess.id === s.activeSessionId);
    return session?.awaitInputAt != null;
  });

  // 工作台模式运行状态
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
    /** 正文选区唤起：切到润色模式并预填选中文本，聚焦输入行等用户确认 */
    beginPolish: (text: string) => {
      handleModeChange("polish");
      editorRef.current?.clear();
      editorRef.current?.insertText(`请润色文中这段文字，保持事实与结构，只优化表达：\n\n「${text}」`);
      editorRef.current?.focus();
      toast.info("已切到润色模式，确认后发送");
    },
  }), [handleModeChange]);

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
      // 工作台模式（Editorial Transport Migration）：REST 规划创建工作流，
      // 执行进度由全局 SSE（use-workflow-sse）驱动 workflow-store。
      const wf = useWorkflowStore.getState();
      wf.setUserInput(currentText.trim());
      wf.setRunStatus("planning");
      createWorkflow({ user_input: currentText.trim(), kb_enabled: kbEnabled, style_slug: style })
        .then((view) => {
          wf.setPlan(createdViewToPlan(view));
          wf.setTaskId(view.task_id);
        })
        .catch((err) => {
          wf.setWorkflowFailed(err instanceof Error ? `规划失败：${err.message}` : "规划失败，请稍后重试");
        });
      editorRef.current?.clear();
      setMessage("");
      return;
    }

    // 持久化当前使用的风格
    useSettingsStore.getState().setLastStyle(style);

    startWriting({
      message: currentText.trim(),
      style,
      mode,
      model,
      agent_mode: agentMode,
      user_materials: materials.length > 0 ? materials.map((material) => material.payload) : undefined,
      kb_enabled: kbEnabled,
      orchestration_mode: orchestrationMode,
      assurance_level: assuranceLevel,
      approval_mode: approvalMode,
    });

    editorRef.current?.clear();
    setMessage("");
  }, [isRunning, style, mode, model, materials, startWriting, agentMode, kbEnabled, orchestrationMode, assuranceLevel, approvalMode]);

  const handleAddMaterial = () => {
    if (materialInput.trim()) {
      const content = materialInput.trim();
      appendMaterial({ title: "粘贴素材", excerpt: content, payload: content });
      setMaterialInput("");
    } else {
      // 空素材添加 → 触发 Composer 抖动
      setShakeKey((k) => k + 1);
    }
  };

  const uploadFiles = useCallback(async (files: File[]) => {
    if (files.length === 0 || uploading) return;
    setUploading(true);
    let uploadedCount = 0;
    try {
      for (const file of files) {
        const sourceId = await uploadMaterial(file, file.name);
        if (!sourceId) throw new Error(`${file.name} 上传失败`);
        const isImage = file.type.startsWith("image/");
        appendMaterial({
          sourceId,
          title: file.name,
          excerpt: `${isImage ? "图片" : "文件"}素材 · 已上传并解析`,
          payload: `${isImage ? "图片" : "文件"}：${file.name}`,
        });
        uploadedCount += 1;
      }
      setMaterialMenuOpen(false);
      toast.success("素材已添加", `${uploadedCount} 个文件已上传并解析`);
    } catch (err) {
      console.error("file upload failed", err);
      toast.error("文件上传失败", err instanceof Error ? err.message : "请稍后重试");
    } finally {
      setUploading(false);
      if (fileInputRef.current) fileInputRef.current.value = "";
      if (imageInputRef.current) imageInputRef.current.value = "";
    }
  }, [appendMaterial, uploading]);

  const handleFileInput = (event: ChangeEvent<HTMLInputElement>) => {
    void uploadFiles(Array.from(event.target.files ?? []));
  };

  const handleDragEnter = (event: DragEvent<HTMLDivElement>) => {
    if (!event.dataTransfer.types.includes("Files")) return;
    event.preventDefault();
    dragDepthRef.current += 1;
    setIsDraggingFiles(true);
  };

  const handleDragOver = (event: DragEvent<HTMLDivElement>) => {
    if (!event.dataTransfer.types.includes("Files")) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = "copy";
  };

  const handleDragLeave = (event: DragEvent<HTMLDivElement>) => {
    if (!event.dataTransfer.types.includes("Files")) return;
    event.preventDefault();
    dragDepthRef.current = Math.max(0, dragDepthRef.current - 1);
    if (dragDepthRef.current === 0) setIsDraggingFiles(false);
  };

  const handleDrop = (event: DragEvent<HTMLDivElement>) => {
    event.preventDefault();
    dragDepthRef.current = 0;
    setIsDraggingFiles(false);
    void uploadFiles(Array.from(event.dataTransfer.files));
  };

  // ─── 从知识库选择素材 ───
  const loadKbMaterials = useCallback(async () => {
    setKbMaterialsLoading(true);
    try {
      const { materials } = await listMaterials(1, 50, "all");
      setKbMaterials(materials);
    } catch (error) {
      toast.error("知识库加载失败", error instanceof Error ? error.message : "请稍后重试");
    } finally {
      setKbMaterialsLoading(false);
    }
  }, []);

  const handleOpenKnowledgeDialog = useCallback(() => {
    setMaterialMenuOpen(false);
    setKnowledgeDialogOpen(true);
    void loadKbMaterials();
  }, [loadKbMaterials]);

  const handlePickMaterials = useCallback(async (selectedMaterials: UserMaterial[]) => {
    const resolved = await Promise.all(selectedMaterials.map(async (material) => {
      let content = material.content_preview || "";
      try {
        const detail = await getMaterialContent(material.id);
        content = detail.content_preview || content;
      } catch {
        // 内容接口失败时仍可使用列表摘要。
      }
      return { material, content };
    }));

    for (const { material, content } of resolved) {
      appendMaterial({
        sourceId: material.id,
        title: material.title,
        excerpt: content || material.file_name || "知识库资料",
        payload: `素材：${material.title}: ${content}`,
      });
    }
    toast.success("知识库资料已添加", `${selectedMaterials.length} 条素材已注入本次写作`);
  }, [appendMaterial]);

  return (
    <div className="relative">
      {/* 研究综述悬浮配置面板：浮现在输入区上方，不挤压 composer；
          配置持久化在 research-review-store，重开自动回填 */}
      {researchSettingsOpen && orchestrationMode === "research_review" && (
        <div className="composer-research-float anim-fade-in">
          <ResearchSettings
            centralQuestion={message.trim()}
            audience=""
            language="zh"
            lengthMin=""
            lengthMax=""
            allowExternalResearch={false}
            styleSlug={style}
            styleName={style}
            // 素材引用透传（F1）：带服务端标识的挂载素材随文档 metadata.material_refs
            // 交给运行时在运行开始时快照；粘贴文本无服务端标识，不冒充引用。
            materialRefs={materials
              .filter((material) => material.sourceId)
              .map((material) => ({ material_id: material.sourceId as string, title: material.title }))}
            onClose={() => setResearchSettingsOpen(false)}
            onStarted={(runId) => {
              setResearchSettingsOpen(false);
              // 真实与 mock 运行统一处理：把工作台切到该运行。
              window.history.replaceState(null, "", `?run=${encodeURIComponent(runId)}`);
              const runtime = useWritingRuntimeStore.getState();
              void runtime.loadRun(runId);
            }}
          />
        </div>
      )}
      {/* 顶部渐变遮罩 — 从透明过渡到背景色，实现悬浮效果 */}
      {!floating && <div className="pointer-events-none absolute -top-8 left-0 right-0 h-8 bg-gradient-to-t from-surface to-transparent z-10" />}
      <div className={cn("writing-composer-frame relative z-20 pb-4 pt-2", compact ? "px-3" : "px-4")}>
      {/* ── Composer 圆角矩形容器 ── */}
      <div
        key={`composer-${shakeKey}`}
        className={cn(
          "composer-shell overflow-hidden",
          shakeKey > 0 && "anim-shake",
          isDraggingFiles && "composer-shell-dragging",
        )}
        onDragEnter={handleDragEnter}
        onDragOver={handleDragOver}
        onDragLeave={handleDragLeave}
        onDrop={handleDrop}
      >
        {isDraggingFiles && (
          <div className="composer-drop-overlay" aria-label="拖拽文件上传区域">
            <Upload className="h-5 w-5" />
            <strong>松开即可添加素材</strong>
            <span>支持图片、PDF、Office、文本与 Markdown</span>
          </div>
        )}
        {onToggleWidth && (
          <button
            className="composer-width-toggle"
            onClick={onToggleWidth}
            aria-label={compact ? "展开输入框" : "收窄输入框"}
            title={compact ? "展开输入框" : "收窄输入框"}
          >
            <span key={compact ? "expand" : "contract"} className="composer-width-icon">
              {compact ? <Maximize2 className="h-[18px] w-[18px]" /> : <Minimize2 className="h-[18px] w-[18px]" />}
            </span>
          </button>
        )}
        {materials.length > 0 && (
          <section className="composer-material-strip scrollbar-hide anim-fade-in" aria-label="已添加的参考素材">
            {materials.map((material) => (
              <div key={material.id} className="composer-material-chip">
                <span className="min-w-0"><strong>{material.title}</strong><small>{material.excerpt}</small></span>
                <button onClick={() => setMaterials((current) => current.filter((item) => item.id !== material.id))} aria-label={`移除素材：${material.title}`}>
                  <X className="h-3.5 w-3.5" />
                </button>
              </div>
            ))}
          </section>
        )}

        {/* 研究综述悬浮配置面板：独立于 composer 容器（见下方 float），
            选择 research_review 模式后浮现在输入区上方 */}

        {/* 主输入区 — Tiptap 富文本编辑器 */}
        <TiptapEditor
          ref={editorRef}
          placeholder={isRunning ? (agentMode === "editorial" ? "工作流执行中…" : "写作进行中… 可先编辑下一条需求，完成后发送") : "输入写作要求，例如：基于热搜写一篇关于外卖骑手闯红灯的评论"}
          onChange={setMessage}
          onSend={handleSend}
          editable={!isRunning}
        />

        {/* 底部控件行 — 无分割线 */}
        <div className={cn("composer-control-row flex items-center py-2.5", compact ? "gap-1 px-2" : "gap-2 px-4")}>
          {/* 左侧：+ 素材按钮 */}
          <input ref={imageInputRef} type="file" accept="image/*" multiple className="hidden" onChange={handleFileInput} />
          <input ref={fileInputRef} type="file" accept=".pdf,.doc,.docx,.ppt,.pptx,.xls,.xlsx,.txt,.md,.csv,.html" multiple className="hidden" onChange={handleFileInput} />
          <Popover open={materialMenuOpen} onOpenChange={setMaterialMenuOpen}>
            <PopoverTrigger asChild>
              <button
                className={cn(
                  "relative flex h-8 w-8 items-center justify-center rounded-xl text-muted-foreground transition-ui",
                  materialMenuOpen ? "bg-accent text-foreground" : "hover:bg-accent hover:text-foreground",
                )}
                aria-label="添加参考素材"
                title="添加参考素材"
                disabled={uploading}
              >
                {uploading ? <Loader2 className="h-[18px] w-[18px] animate-spin" /> : <Plus className="h-[18px] w-[18px]" />}
                {materials.length > 0 && (
                  <span className="absolute -right-1 -top-1 rounded-full bg-primary px-1.5 text-[10px] font-medium leading-4 text-primary-foreground">
                    {materials.length}
                  </span>
                )}
              </button>
            </PopoverTrigger>
            <PopoverContent side="top" align="start" sideOffset={12} className="composer-material-popover w-[292px] max-w-[calc(100vw-24px)] p-0">
              <header className="composer-material-popover-header">
                <span className="composer-material-popover-title"><strong>添加参考素材</strong><small>也可以直接拖到输入框</small></span>
                <div className="composer-material-paste">
                  <input
                    value={materialInput}
                    onChange={(event) => setMaterialInput(event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key === "Enter") {
                        event.preventDefault();
                        handleAddMaterial();
                      }
                    }}
                    placeholder="粘贴一段参考文字"
                    aria-label="粘贴参考文字"
                  />
                  <button onClick={handleAddMaterial}>添加</button>
                </div>
              </header>
              <div className="composer-material-menu">
                <button
                  className="composer-material-menu-item"
                  onClick={() => {
                    setMaterialMenuOpen(false);
                    imageInputRef.current?.click();
                  }}
                >
                  <ImagePlus className="h-4 w-4" />
                  <span><strong>照片</strong><small>上传图片作为视觉参考</small></span>
                </button>
                <button
                  className="composer-material-menu-item"
                  onClick={() => {
                    setMaterialMenuOpen(false);
                    fileInputRef.current?.click();
                  }}
                >
                  <FileUp className="h-4 w-4" />
                  <span><strong>文件</strong><small>PDF、Office、文本或 Markdown</small></span>
                </button>
                <div className="composer-material-menu-item composer-material-library-row">
                  <button className="composer-material-library-main" onClick={handleOpenKnowledgeDialog}>
                    <FolderSearch className="h-4 w-4" />
                    <span><strong>知识库</strong><small>搜索资料并自动补充相关内容</small></span>
                    <ChevronRight className="ml-auto h-4 w-4" />
                  </button>
                  <Switch checked={kbEnabled} onCheckedChange={handleToggleKB} aria-label="知识库自动检索" />
                </div>
              </div>
            </PopoverContent>
          </Popover>

          {/* 左侧：引导模式 */}
          <ModePicker
            value={mode}
            compact={compact}
            onChange={handleModeChange}
            orchestrationValue={orchestrationMode}
            onOrchestrationChange={handleOrchestrationChange}
            assuranceValue={assuranceLevel}
            onAssuranceChange={setAssuranceLevel}
            approvalValue={approvalMode}
            onApprovalChange={setApprovalMode}
            onResearchReviewSelect={handleResearchReviewSelect}
          />

          {/* 研究综述激活时显示入口标签（点击重新打开设置） */}
          {orchestrationMode === "research_review" && !researchSettingsOpen && (
            <button
              onClick={() => setResearchSettingsOpen(true)}
              className="composer-research-tag flex h-8 items-center gap-1.5 rounded-xl px-2.5 text-sm text-muted-foreground transition-ui hover:bg-accent hover:text-foreground"
              aria-label="编辑研究综述设置"
              title="编辑研究综述设置"
            >
              <BookOpenText className="h-[18px] w-[18px]" />
              <span className={cn("composer-control-label", compact && "sr-only")}>研究综述设置</span>
            </button>
          )}

          {/* 左侧：风格选择（紧挨模式右侧，含「使用 Lumi 创建写作风格」入口） */}
          <StylePicker value={style} onChange={handleStyleChange} compact={compact} />

          {/* 右侧弹性间距 */}
          <div className="flex-1" />

          {/* 右侧：模型选择 */}
          <ModelPicker value={model} onChange={setModel} compact={compact} />

          {/* 右侧：发送/暂停/取消按钮 — 笔头像黑色圆 */}
          <div className="flex items-center gap-1.5">
            {(isRunning || isPaused) && (
              <button
                onClick={() => {
                  if (agentMode === "editorial") {
                    // REST 取消执行中的 DAG（若无 taskId 则仅重置本地状态）
                    const wf = useWorkflowStore.getState();
                    const tid = wf.taskId;
                    wf.reset();
                    if (tid) cancelWorkflow(tid).catch(() => { /* 后端不可达时本地已重置 */ });
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
                  <Lumi state="idle" size={18} />
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
      <KnowledgeMaterialDialog
        open={knowledgeDialogOpen}
        onOpenChange={setKnowledgeDialogOpen}
        materials={kbMaterials}
        loading={kbMaterialsLoading}
        kbEnabled={kbEnabled}
        onToggleKB={handleToggleKB}
        attachedMaterialIds={attachedMaterialIds}
        onAddMaterials={handlePickMaterials}
      />
    </div>
  );
});

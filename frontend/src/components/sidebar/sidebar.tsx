/**
 * 左侧栏 — 会话列表（文件夹/归档/批量管理） + 选题入口 + 用户区
 *
 * 桌面端始终保持完整宽度；移动端由工作台作为完整抽屉显示或隐藏。
 * 底部用户区点击弹出 Popover 面板。
 *
 * 会话组织能力：
 * - 文件夹：新建/重命名/删除，会话可移动（后端 /api/v2/session-folders）
 * - 归档：归档区默认折叠，展开时懒加载（?archived=true）
 * - 批量管理：多选后批量删除/归档/移动
 * - 单项操作：复制会话、归档、移动、删除
 */
import { useEffect, useState, type ReactNode } from "react";
import {
  Plus, Trash2, Compass, Database,
  Settings, Sun, Moon, Monitor, LogOut, UserPlus,
  ChevronRight, ChevronDown, User, AlertTriangle, Newspaper,
  CreditCard, Folder, FolderPlus, Archive, ArchiveRestore,
  Copy, MoreHorizontal, Pencil, X, CheckSquare,
} from "lucide-react";
import { BrandIcon } from "@/components/brand-icon";
import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Separator } from "@/components/ui/separator";
import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter, DialogDescription,
} from "@/components/ui/dialog";
import { useWritingRuntimeStore } from "@/stores/writing-runtime-store";
import type { WritingSession } from "@/lib/writing-runtime-types";
import { useAuthStore } from "@/stores/auth-store";
import { useAuthModal } from "@/stores/auth-modal-store";
import { useBillingStore } from "@/stores/billing-store";
import { useSettingsStore } from "@/stores/settings-store";
import { useTheme, type Theme } from "@/hooks/use-theme";
import { useNavigate } from "react-router-dom";
import { cn } from "@/lib/utils";
import { isSessionRead } from "@/lib/session-read-state";
import { StaggerItem } from "@/components/animation";

interface SidebarProps {
  onNavigate?: () => void;
}

// ─── 展示辅助 ────────────────────────────────────────────

function formatRelative(ts: number): string {
  const diff = Date.now() - ts;
  const min = Math.floor(diff / 60_000);
  if (min < 1) return "刚刚";
  if (min < 60) return `${min} 分钟前`;
  const hr = Math.floor(diff / 3_600_000);
  if (hr < 24) return `${hr} 小时前`;
  const day = Math.floor(diff / 86_400_000);
  if (day < 7) return `${day} 天前`;
  return new Date(ts).toLocaleDateString("zh-CN", { month: "2-digit", day: "2-digit" });
}

function timeBucket(ts: number): "today" | "week" | "earlier" {
  const d = new Date(ts);
  const now = new Date();
  const startOfToday = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
  if (d.getTime() >= startOfToday) return "today";
  if (Date.now() - ts < 7 * 86_400_000) return "week";
  return "earlier";
}

const TIME_BUCKET_LABEL: Record<"today" | "week" | "earlier", string> = {
  today: "今天",
  week: "7 天内",
  earlier: "更早",
};

const STATUS_LABEL: Partial<Record<WritingSession["status"], string>> = {
  running: "写作中",
  paused: "已暂停",
  error: "失败",
};

/**
 * 会话状态点语义：
 * - 绿色脉冲 = 写作进行中（跟随状态变化，不参与已读）
 * - 黄色 = 已暂停（仍在进行，不参与已读）
 * - 蓝色 = 已完成（未读提示，点击会话一次后消失，类似手机消息红点）
 * - 红色 = 失败（未读提示，点击会话一次后消失）
 */
function SessionStatusDot({ session }: { session: WritingSession }) {
  if (session.archived) {
    return <span className="h-2 w-2 shrink-0 rounded-full bg-muted-foreground/30" />;
  }
  switch (session.status) {
    case "running":
      return <span className="h-2 w-2 shrink-0 animate-pulse rounded-full bg-emerald-400" title="写作中" />;
    case "paused":
      return <span className="h-2 w-2 shrink-0 rounded-full bg-amber-400" title="已暂停" />;
    case "error":
    case "completed": {
      // 完成/失败是"有结果未读"：打开过一次就不再显示
      const unread = !isSessionRead(session);
      return (
        <span
          className={cn(
            "h-2 w-2 shrink-0 rounded-full",
            session.status === "error" ? "bg-red-400" : "bg-blue-400",
            !unread && "bg-transparent",
          )}
          title={session.status === "error" ? "失败" : "已完成"}
        />
      );
    }
    default:
      // 未开始的新会话：不显示状态点
      return <span className="h-2 w-2 shrink-0 rounded-full bg-transparent" />;
  }
}

export function Sidebar({ onNavigate }: SidebarProps) {
  const navigate = useNavigate();
  const sessions = useWritingRuntimeStore((s) => s.sessions);
  const activeSessionId = useWritingRuntimeStore((s) => s.activeSessionId);
  const folders = useWritingRuntimeStore((s) => s.folders);
  const sessionsTotal = useWritingRuntimeStore((s) => s.sessionsTotal);
  const createSession = useWritingRuntimeStore((s) => s.createSession);
  const switchSession = useWritingRuntimeStore((s) => s.switchSession);
  const deleteSession = useWritingRuntimeStore((s) => s.deleteSession);
  const loadMoreSessions = useWritingRuntimeStore((s) => s.loadMoreSessions);
  const loadArchivedSessions = useWritingRuntimeStore((s) => s.loadArchivedSessions);
  const loadFolders = useWritingRuntimeStore((s) => s.loadFolders);
  const createFolder = useWritingRuntimeStore((s) => s.createFolder);
  const renameFolder = useWritingRuntimeStore((s) => s.renameFolder);
  const deleteFolder = useWritingRuntimeStore((s) => s.deleteFolder);
  const batchSessions = useWritingRuntimeStore((s) => s.batchSessions);
  const duplicateSession = useWritingRuntimeStore((s) => s.duplicateSession);

  // ── 侧栏本地 UI 状态 ──
  const [batchMode, setBatchMode] = useState(false);
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
  const [archiveOpen, setArchiveOpen] = useState(false);
  const [folderOpenMap, setFolderOpenMap] = useState<Record<string, boolean>>({});
  const [createFolderOpen, setCreateFolderOpen] = useState(false);
  const [newFolderName, setNewFolderName] = useState("");
  const [folderDialogError, setFolderDialogError] = useState<string | null>(null);
  const [renameTarget, setRenameTarget] = useState<{ id: string; name: string } | null>(null);
  const [renameName, setRenameName] = useState("");
  const [deleteTarget, setDeleteTarget] = useState<{ ids: string[]; title: string; folderId?: string } | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [menuFor, setMenuFor] = useState<string | null>(null); // 当前打开的单项菜单
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    void loadFolders();
  }, [loadFolders]);

  // ── 派生列表 ──
  const activeSessions = sessions.filter((s) => !s.archived);
  const archivedSessions = sessions.filter((s) => s.archived);
  const ungrouped = activeSessions.filter((s) => !s.folderId);
  const hasMore = activeSessions.length < sessionsTotal;

  const selectableIds = activeSessions
    .filter((s) => s.traceId)
    .map((s) => s.traceId!) // 批量操作走后端，必须用 traceId
    .filter((id, i, arr) => arr.indexOf(id) === i);

  const toggleSelect = (traceId: string) => {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (next.has(traceId)) next.delete(traceId);
      else next.add(traceId);
      return next;
    });
  };

  const handleBatch = async (action: "delete" | "archive", ids: string[]) => {
    if (ids.length === 0) return;
    setBusy(true);
    try {
      await batchSessions(action, ids);
      setSelectedIds(new Set());
      if (action === "delete") setBatchMode(false);
    } finally {
      setBusy(false);
    }
  };

  const handleMove = async (ids: string[], folderId: string) => {
    if (ids.length === 0) return;
    setBusy(true);
    try {
      await batchSessions("move", ids, folderId);
    } finally {
      setBusy(false);
    }
  };

  const handleBatchMove = async (folderId: string) => {
    const ids = [...selectedIds];
    await handleMove(ids, folderId);
    setSelectedIds(new Set());
  };

  const handleSingleMove = async (traceId: string, folderId: string) => {
    await handleMove([traceId], folderId);
  };

  const handleCreateFolder = async () => {
    setFolderDialogError(null);
    if (!newFolderName.trim()) {
      setFolderDialogError("请输入文件夹名称");
      return;
    }
    const ok = await createFolder(newFolderName);
    if (ok) {
      setCreateFolderOpen(false);
      setNewFolderName("");
    } else {
      setFolderDialogError("创建失败（同名文件夹已存在？）");
    }
  };

  const handleRenameFolder = async () => {
    if (!renameTarget) return;
    setFolderDialogError(null);
    const ok = await renameFolder(renameTarget.id, renameName);
    if (ok) {
      setRenameTarget(null);
    } else {
      setFolderDialogError("重命名失败（同名文件夹已存在？）");
    }
  };

  const handleConfirmDelete = async () => {
    if (!deleteTarget) return;
    // 文件夹删除（其中的会话自动回到未分组）
    if (deleteTarget.folderId) {
      setDeleting(true);
      try {
        await deleteFolder(deleteTarget.folderId);
        setDeleteTarget(null);
      } finally {
        setDeleting(false);
      }
      return;
    }
    if (deleteTarget.ids.length === 0) return;
    setDeleting(true);
    try {
      if (deleteTarget.ids.length === 1) {
        // 单个删除：按本地会话 id 走 store（内部换算 traceId 并先取消写作）
        const session = sessions.find((s) => s.traceId === deleteTarget.ids[0]);
        await deleteSession(session?.id ?? deleteTarget.ids[0]);
      } else {
        await batchSessions("delete", deleteTarget.ids);
      }
      setSelectedIds(new Set());
      setDeleteTarget(null);
    } finally {
      setDeleting(false);
    }
  };

  // ── 认证状态 ──
  const user = useAuthStore((s) => s.user);
  const logout = useAuthStore((s) => s.logout);
  const openAuth = useAuthModal((s) => s.openAuth);
  const billingBalance = useBillingStore((s) => s.balance);
  const loadBalance = useBillingStore((s) => s.loadBalance);

  useEffect(() => { void loadBalance(); }, [loadBalance]);

  // 实验功能偏好
  const enableEditorial = useSettingsStore((s) => s.enableEditorial);

  // 主题
  const { theme, toggle: toggleTheme } = useTheme();

  const isGuest = user?.role === "guest";
  const isAdmin = useAuthStore((s) => s.hasAdminAccess());

  const handleRegister = () => {
    const token = useAuthStore.getState().token;
    openAuth({
      guestToken: token ?? undefined,
      defaultTab: "register",
    });
  };

  return (
    <aside className="flex h-full w-56 flex-col bg-[color:var(--desk-canvas)] anim-slide-right" aria-label="全局导航">
      {/* 顶部品牌区；关闭按钮移至工作台顶栏（标题左侧） */}
      <div className="flex items-center gap-2.5 px-3 py-3.5">
        <BrandIcon size="md" showLabel />
      </div>

      {/* 新建写作 */}
      <div className="p-3">
        <Button
          className="w-full justify-start gap-2 group transition-transform-precise hover:scale-[1.02] active:scale-[0.98]"
          onClick={() => { createSession(); onNavigate?.(); }}
        >
          <Plus className="h-4 w-4 transition-transform group-hover:rotate-90" />
          新建写作
        </Button>
      </div>

      {/* 导航入口 */}
      <div className="px-3 pb-2 space-y-1">
        <Button
          variant="ghost"
          size="sm"
          className="w-full justify-start gap-2 text-muted-foreground"
          onClick={() => { navigate("/topics"); onNavigate?.(); }}
        >
          <Compass className="h-4 w-4" />
          选题
        </Button>
        <Button
          variant="ghost"
          size="sm"
          className="w-full justify-start gap-2 text-muted-foreground"
          onClick={() => { navigate("/materials"); onNavigate?.(); }}
        >
          <Database className="h-4 w-4" />
          知识库
        </Button>
        {enableEditorial && (
          <Button
            variant="ghost"
            size="sm"
            className="w-full justify-start gap-2 text-muted-foreground"
            onClick={() => { navigate("/workspace"); onNavigate?.(); }}
          >
            <Newspaper className="h-4 w-4" />
            工作台
          </Button>
        )}
      </div>

      <Separator />

      {/* 会话区头：标题 + 新建文件夹 + 批量管理 */}
      <div className="flex items-center justify-between px-4 pt-3 pb-1">
        <span className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground/50">
          会话
        </span>
        <div className="flex items-center gap-0.5">
          <button
            onClick={() => { setCreateFolderOpen(true); setFolderDialogError(null); }}
            className="flex h-6 w-6 items-center justify-center rounded-md text-muted-foreground transition-ui hover:bg-accent hover:text-foreground"
            title="新建文件夹"
            aria-label="新建文件夹"
          >
            <FolderPlus className="h-3.5 w-3.5" />
          </button>
          <button
            onClick={() => { setBatchMode((v) => !v); setSelectedIds(new Set()); }}
            className={cn(
              "flex h-6 w-6 items-center justify-center rounded-md text-muted-foreground transition-ui hover:bg-accent hover:text-foreground",
              batchMode && "bg-accent text-foreground"
            )}
            title={batchMode ? "退出批量管理" : "批量管理"}
            aria-label="批量管理"
          >
            {batchMode ? <X className="h-3.5 w-3.5" /> : <CheckSquare className="h-3.5 w-3.5" />}
          </button>
        </div>
      </div>

      <ScrollArea className="flex-1">
        <div className="p-3 pt-0 space-y-1">
          {sessions.length === 0 ? (
            <div className="px-3 py-10 text-center">
              <p className="text-xs text-muted-foreground">暂无写作记录</p>
              <p className="text-[11px] text-muted-foreground/60 mt-1">点击上方按钮开始创作</p>
            </div>
          ) : (
            <>
              {/* 文件夹分组 */}
              {folders.map((folder) => {
                const folderSessions = activeSessions.filter((s) => s.folderId === folder.id);
                const open = folderOpenMap[folder.id] ?? true;
                return (
                  <div key={folder.id}>
                    <div
                      className={cn(
                        "group flex items-center gap-1.5 rounded-lg px-2 py-1.5 cursor-pointer transition-ui",
                        "text-muted-foreground hover:bg-accent/50"
                      )}
                      onClick={() => setFolderOpenMap((m) => ({ ...m, [folder.id]: !open }))}
                    >
                      {open
                        ? <ChevronDown className="h-3 w-3 shrink-0" />
                        : <ChevronRight className="h-3 w-3 shrink-0" />}
                      <Folder className="h-3.5 w-3.5 shrink-0" />
                      <span className="flex-1 min-w-0 truncate text-xs font-medium">{folder.name}</span>
                      <span className="text-[10px] text-muted-foreground/50">{folderSessions.length}</span>
                      <FolderActionsPopover
                        folderId={folder.id}
                        folderName={folder.name}
                        open={menuFor === `folder:${folder.id}`}
                        onOpenChange={(o) => setMenuFor(o ? `folder:${folder.id}` : null)}
                        onRename={() => { setRenameTarget(folder); setRenameName(folder.name); }}
                        onDelete={() => setDeleteTarget({ ids: [], title: folder.name, folderId: folder.id })}
                      />
                    </div>
                    {open && (
                      <div className="ml-3 space-y-1">
                        {folderSessions.length === 0 ? (
                          <p className="px-2 py-1 text-[10px] text-muted-foreground/40">空文件夹</p>
                        ) : (
                          folderSessions.map((session) => (
                            <SessionItem
                              key={session.id}
                              session={session}
                              active={session.id === activeSessionId}
                              batchMode={batchMode}
                              selected={session.traceId ? selectedIds.has(session.traceId) : false}
                              onToggleSelect={() => session.traceId && toggleSelect(session.traceId)}
                              onOpen={() => { switchSession(session.id); onNavigate?.(); }}
                              menuOpen={menuFor === session.id}
                              onMenuOpenChange={(o) => setMenuFor(o ? session.id : null)}
                              folders={folders}
                              onDelete={() => setDeleteTarget({ ids: session.traceId ? [session.traceId] : [], title: session.title })}
                              onArchive={() => session.traceId && handleBatch("archive", [session.traceId])}
                              onDuplicate={async () => { await duplicateSession(session.id); }}
                              onMove={(fid) => session.traceId && void handleSingleMove(session.traceId, fid)}
                            />
                          ))
                        )}
                      </div>
                    )}
                  </div>
                );
              })}

              {/* 未分组会话（按时间分组） */}
              {ungrouped.length > 0 && (
                <div className="space-y-1">
                  {folders.length > 0 && (
                    <p className="px-3 pb-1 pt-2 text-[10px] font-medium uppercase tracking-wider text-muted-foreground/50">
                      未分组
                    </p>
                  )}
                  {(["today", "week", "earlier"] as const).map((bucket) => {
                    const bucketSessions = ungrouped.filter(
                      (s) => timeBucket(s.updatedAt ?? s.createdAt) === bucket
                    );
                    if (bucketSessions.length === 0) return null;
                    return (
                      <div key={bucket}>
                        <p className="px-3 pb-1 text-[10px] font-medium uppercase tracking-wider text-muted-foreground/40">
                          {TIME_BUCKET_LABEL[bucket]}
                        </p>
                        {bucketSessions.map((session, i) => (
                          <SessionItem
                            key={session.id}
                            session={session}
                            index={i}
                            active={session.id === activeSessionId}
                            batchMode={batchMode}
                            selected={session.traceId ? selectedIds.has(session.traceId) : false}
                            onToggleSelect={() => session.traceId && toggleSelect(session.traceId)}
                            onOpen={() => { switchSession(session.id); onNavigate?.(); }}
                            menuOpen={menuFor === session.id}
                            onMenuOpenChange={(o) => setMenuFor(o ? session.id : null)}
                            folders={folders}
                            onDelete={() => setDeleteTarget({ ids: session.traceId ? [session.traceId] : [], title: session.title })}
                            onArchive={() => session.traceId && handleBatch("archive", [session.traceId])}
                            onDuplicate={async () => { await duplicateSession(session.id); }}
                            onMove={(fid) => session.traceId && void handleSingleMove(session.traceId, fid)}
                          />
                        ))}
                      </div>
                    );
                  })}
                </div>
              )}

              {/* 加载更多 */}
              {hasMore && (
                <button
                  onClick={() => void loadMoreSessions()}
                  className="w-full py-1.5 text-[10px] text-muted-foreground/60 hover:text-foreground transition-ui"
                >
                  加载更多（{activeSessions.length}/{sessionsTotal}）
                </button>
              )}

              {/* 归档区 */}
              {archivedSessions.length > 0 && (
                <Collapsible open={archiveOpen} onOpenChange={(o) => {
                  setArchiveOpen(o);
                  if (o) void loadArchivedSessions();
                }}>
                  <CollapsibleTrigger className="flex w-full items-center gap-1.5 rounded-lg px-2 py-1.5 text-muted-foreground transition-ui hover:bg-accent/50">
                    {archiveOpen ? <ChevronDown className="h-3 w-3" /> : <ChevronRight className="h-3 w-3" />}
                    <Archive className="h-3.5 w-3.5" />
                    <span className="flex-1 min-w-0 truncate text-left text-xs font-medium">归档</span>
                    <span className="text-[10px] text-muted-foreground/50">{archivedSessions.length}</span>
                  </CollapsibleTrigger>
                  <CollapsibleContent>
                    <div className="ml-3 space-y-1">
                      {archivedSessions.map((session) => (
                        <SessionItem
                          key={session.id}
                          session={session}
                          archived
                          active={session.id === activeSessionId}
                          batchMode={false}
                          selected={false}
                          onToggleSelect={() => {}}
                          onOpen={() => { switchSession(session.id); onNavigate?.(); }}
                          menuOpen={menuFor === session.id}
                          onMenuOpenChange={(o) => setMenuFor(o ? session.id : null)}
                          folders={folders}
                          onDelete={() => setDeleteTarget({ ids: session.traceId ? [session.traceId] : [], title: session.title })}
                          onUnarchive={() => session.traceId && batchSessions("unarchive", [session.traceId])}
                        />
                      ))}
                    </div>
                  </CollapsibleContent>
                </Collapsible>
              )}
            </>
          )}
        </div>
      </ScrollArea>

      {/* 批量操作条 */}
      {batchMode && (
        <div className="border-t bg-surface px-2 py-2 space-y-1.5">
          <div className="flex items-center gap-1">
            <Button
              variant="outline"
              size="sm"
              className="h-7 flex-1 text-xs"
              disabled={selectableIds.length === 0}
              onClick={() => setSelectedIds(new Set(selectableIds))}
            >
              全选
            </Button>
            <Button
              variant="outline"
              size="sm"
              className="h-7 flex-1 text-xs"
              disabled={selectedIds.size === 0 || busy}
              onClick={() => void handleBatch("archive", [...selectedIds])}
            >
              归档
            </Button>
            <Button
              variant="destructive"
              size="sm"
              className="h-7 flex-1 text-xs"
              disabled={selectedIds.size === 0 || busy}
              onClick={() => setDeleteTarget({
                ids: [...selectedIds],
                title: `${selectedIds.size} 个会话`,
              })}
            >
              删除
            </Button>
          </div>
          {/* 移动到文件夹 */}
          {folders.length > 0 && (
            <div className="flex items-center gap-1">
              <Popover>
                <PopoverTrigger asChild>
                  <Button
                    variant="outline"
                    size="sm"
                    className="h-7 flex-1 text-xs"
                    disabled={selectedIds.size === 0 || busy}
                  >
                    <Folder className="h-3 w-3 mr-1" />
                    移动到文件夹
                  </Button>
                </PopoverTrigger>
                <PopoverContent side="top" align="start" className="w-44 p-1">
                  <button
                    className="w-full rounded-md px-2 py-1.5 text-left text-xs hover:bg-accent transition-ui"
                    onClick={() => void handleBatchMove("")}
                  >
                    未分组
                  </button>
                  {folders.map((f) => (
                    <button
                      key={f.id}
                      className="w-full rounded-md px-2 py-1.5 text-left text-xs hover:bg-accent transition-ui truncate"
                      onClick={() => void handleBatchMove(f.id)}
                    >
                      {f.name}
                    </button>
                  ))}
                </PopoverContent>
              </Popover>
              <span className="text-[10px] text-muted-foreground px-1">已选 {selectedIds.size}</span>
            </div>
          )}
        </div>
      )}

      {/* ── 底部用户区 — 点击弹出 Popover 面板 ── */}
      <div className="px-2 pb-2">
        <Popover>
          <PopoverTrigger asChild>
            <button className="group flex w-full items-center gap-2.5 rounded-lg p-2.5 text-muted-foreground transition-ui hover:bg-accent hover:text-foreground data-[state=open]:bg-accent data-[state=open]:text-foreground">
              <Avatar className="h-8 w-8 shrink-0">
                <AvatarFallback className={cn(
                  "text-xs font-medium",
                  isGuest ? "bg-amber-100 text-amber-700" : "bg-muted text-foreground"
                )}>
                  {isGuest ? "客" : (user?.username?.slice(0, 2).toUpperCase() ?? user?.userId?.slice(0, 2).toUpperCase() ?? "?")}
                </AvatarFallback>
              </Avatar>
              <div className="flex-1 min-w-0 text-left">
                {isGuest ? (
                  <>
                    <p className="text-sm font-medium truncate">你好，游客</p>
                    <p className="text-[11px] text-amber-600 font-mono-sm">试用 · 剩余 1 次</p>
                  </>
                ) : (
                  <>
                    <p className="text-sm font-medium truncate">{user?.username ?? user?.userId ?? "用户"}</p>
                    <p className="text-[11px] text-muted-foreground font-mono-sm">
                      {isAdmin ? "admin" : "user"}
                    </p>
                  </>
                )}
              </div>
              <ChevronRight className="h-4 w-4 shrink-0 text-muted-foreground transition-ui group-hover:translate-x-0.5" />
            </button>
          </PopoverTrigger>
          <PopoverContent side="top" align="start" className="w-[232px]">
            <UserMenuContent
              isGuest={isGuest}
              isAdmin={isAdmin}
              theme={theme}
              onToggleTheme={toggleTheme}
              onNavigate={(path) => { navigate(path); onNavigate?.(); }}
              onLogout={() => {
                logout();
                useAuthStore.getState().init();
                navigate("/write", { replace: true });
              }}
              onRegister={handleRegister}
              pointBalance={billingBalance?.point_balance}
              planName={billingBalance?.plan_display_name}
            />
          </PopoverContent>
        </Popover>
      </div>

      {/* 删除确认对话框（单个/批量共用） */}
      <Dialog open={!!deleteTarget} onOpenChange={(open) => !open && setDeleteTarget(null)}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <AlertTriangle className="h-4 w-4 text-amber-500" />
              确认删除
            </DialogTitle>
            <DialogDescription>
              {deleteTarget?.ids.length === 1
                ? <>确定要删除「{deleteTarget?.title}」吗？删除后将不再显示在历史记录中。</>
                : <>确定要删除这 {deleteTarget?.ids.length} 个会话吗？删除后将不再显示在历史记录中。</>}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter className="gap-2">
            <Button variant="outline" size="sm" onClick={() => setDeleteTarget(null)} disabled={deleting}>
              取消
            </Button>
            <Button variant="destructive" size="sm" onClick={handleConfirmDelete} disabled={deleting}>
              {deleting ? "删除中..." : "确认删除"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* 新建文件夹对话框 */}
      <Dialog open={createFolderOpen} onOpenChange={(open) => !open && setCreateFolderOpen(false)}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <FolderPlus className="h-4 w-4 text-primary" />
              新建文件夹
            </DialogTitle>
            <DialogDescription>将一组写作会话整理到文件夹中。</DialogDescription>
          </DialogHeader>
          <Input
            autoFocus
            value={newFolderName}
            onChange={(e) => setNewFolderName(e.target.value)}
            placeholder="文件夹名称"
            maxLength={64}
            onKeyDown={(e) => e.key === "Enter" && void handleCreateFolder()}
          />
          {folderDialogError && <p className="text-xs text-destructive">{folderDialogError}</p>}
          <DialogFooter className="gap-2">
            <Button variant="outline" size="sm" onClick={() => setCreateFolderOpen(false)}>取消</Button>
            <Button size="sm" onClick={() => void handleCreateFolder()}>创建</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* 重命名文件夹对话框 */}
      <Dialog open={!!renameTarget} onOpenChange={(open) => !open && setRenameTarget(null)}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <Pencil className="h-4 w-4 text-primary" />
              重命名文件夹
            </DialogTitle>
            <DialogDescription>修改「{renameTarget?.name}」的名称。</DialogDescription>
          </DialogHeader>
          <Input
            autoFocus
            value={renameName}
            onChange={(e) => setRenameName(e.target.value)}
            maxLength={64}
            onKeyDown={(e) => e.key === "Enter" && void handleRenameFolder()}
          />
          {folderDialogError && <p className="text-xs text-destructive">{folderDialogError}</p>}
          <DialogFooter className="gap-2">
            <Button variant="outline" size="sm" onClick={() => setRenameTarget(null)}>取消</Button>
            <Button size="sm" onClick={() => void handleRenameFolder()}>保存</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </aside>
  );
}

// ─── 会话列表项 ──────────────────────────────────────────

interface SessionItemProps {
  session: WritingSession;
  index?: number;
  active?: boolean;
  archived?: boolean;
  batchMode: boolean;
  selected: boolean;
  folders: { id: string; name: string }[];
  onToggleSelect: () => void;
  onOpen: () => void;
  menuOpen: boolean;
  onMenuOpenChange: (open: boolean) => void;
  onDelete: () => void;
  onArchive?: () => void;
  onUnarchive?: () => void;
  onDuplicate?: () => Promise<void>;
  onMove?: (folderId: string) => void;
}

function SessionItem({
  session, index = 0, active, archived, batchMode, selected, folders,
  onToggleSelect, onOpen, menuOpen, onMenuOpenChange,
  onDelete, onArchive, onUnarchive, onDuplicate, onMove,
}: SessionItemProps) {
  const statusLabel = STATUS_LABEL[session.status];

  return (
    <StaggerItem
      key={session.id}
      index={index}
      interval={20}
      animation="slide-right"
      as="div"
      className={cn(
        "group flex items-center gap-2 rounded-lg px-3 py-1.5 cursor-pointer transition-ui overflow-hidden min-w-0",
        selected
          ? "bg-primary/10 text-foreground"
          : active
            ? "bg-accent text-foreground"
            : "hover:bg-accent/50 text-muted-foreground"
      )}
      onClick={() => (batchMode ? onToggleSelect() : onOpen())}
    >
      {/* 批量模式：checkbox 替代状态点 */}
      {batchMode ? (
        <Checkbox
          checked={selected}
          onCheckedChange={() => onToggleSelect()}
          className="h-3.5 w-3.5 shrink-0"
          onClick={(e) => e.stopPropagation()}
        />
      ) : (
        <SessionStatusDot session={session} />
      )}
      <div className="flex-1 min-w-0 leading-tight">
        <p className="truncate text-sm">{session.title}</p>
        <p className="flex items-center gap-1.5 text-[10px] text-muted-foreground/50">
          <span>{formatRelative(session.updatedAt ?? session.createdAt)}</span>
          {statusLabel && (
            <span className={cn(
              session.status === "running" && "text-emerald-500",
              session.status === "paused" && "text-amber-500",
              session.status === "error" && "text-red-400",
            )}>· {statusLabel}</span>
          )}
        </p>
      </div>

      {/* 归档项：直接给恢复按钮 */}
      {archived && onUnarchive ? (
        <button
          onClick={(e) => { e.stopPropagation(); onUnarchive(); }}
          className="shrink-0 opacity-0 group-hover:opacity-100 transition-ui text-muted-foreground hover:text-foreground"
          title="取消归档"
          aria-label="取消归档"
        >
          <ArchiveRestore className="h-3.5 w-3.5" />
        </button>
      ) : !batchMode ? (
        <Popover open={menuOpen} onOpenChange={onMenuOpenChange}>
          <PopoverTrigger asChild>
            <button
              onClick={(e) => e.stopPropagation()}
              className="shrink-0 opacity-0 group-hover:opacity-100 transition-ui text-muted-foreground hover:text-foreground"
              title="更多操作"
              aria-label="更多操作"
            >
              <MoreHorizontal className="h-3.5 w-3.5" />
            </button>
          </PopoverTrigger>
          <PopoverContent side="right" align="start" className="w-44 p-1" onClick={(e) => e.stopPropagation()}>
            {onDuplicate && (
              <SessionMenuRow
                icon={<Copy className="h-3.5 w-3.5" />}
                label="复制会话"
                onClick={() => { onMenuOpenChange(false); void onDuplicate(); }}
              />
            )}
            {onArchive && (
              <SessionMenuRow
                icon={<Archive className="h-3.5 w-3.5" />}
                label="归档"
                onClick={() => { onMenuOpenChange(false); onArchive(); }}
              />
            )}
            {onMove && (folders.length > 0) && (
              <>
                <div className="my-1 border-t" />
                <p className="px-2 py-0.5 text-[10px] text-muted-foreground/50">移动到文件夹</p>
                {folders.map((f) => (
                  <SessionMenuRow
                    key={f.id}
                    icon={<Folder className="h-3.5 w-3.5" />}
                    label={f.name}
                    onClick={() => { onMenuOpenChange(false); onMove(f.id); }}
                  />
                ))}
                <SessionMenuRow
                  icon={<X className="h-3.5 w-3.5" />}
                  label="移出文件夹"
                  onClick={() => { onMenuOpenChange(false); onMove(""); }}
                />
              </>
            )}
            <div className="my-1 border-t" />
            <SessionMenuRow
              icon={<Trash2 className="h-3.5 w-3.5" />}
              label="删除"
              danger
              onClick={() => { onMenuOpenChange(false); onDelete(); }}
            />
          </PopoverContent>
        </Popover>
      ) : null}
    </StaggerItem>
  );
}

function SessionMenuRow({ icon, label, onClick, danger }: {
  icon: ReactNode;
  label: string;
  onClick: () => void;
  danger?: boolean;
}) {
  return (
    <button
      className={cn(
        "flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs transition-ui hover:bg-accent",
        danger ? "text-destructive hover:bg-destructive/10" : "text-foreground"
      )}
      onClick={onClick}
    >
      {icon}
      <span className="truncate">{label}</span>
    </button>
  );
}

// ─── 文件夹行操作菜单 ────────────────────────────────────

interface FolderActionsPopoverProps {
  folderId: string;
  folderName: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onRename: () => void;
  onDelete: () => void;
}

function FolderActionsPopover({ open, onOpenChange, onRename, onDelete }: FolderActionsPopoverProps) {
  return (
    <Popover open={open} onOpenChange={onOpenChange}>
      <PopoverTrigger asChild>
        <button
          onClick={(e) => e.stopPropagation()}
          className="shrink-0 opacity-0 group-hover:opacity-100 transition-ui text-muted-foreground hover:text-foreground"
          title="文件夹操作"
          aria-label="文件夹操作"
        >
          <MoreHorizontal className="h-3.5 w-3.5" />
        </button>
      </PopoverTrigger>
      <PopoverContent side="right" align="start" className="w-36 p-1" onClick={(e) => e.stopPropagation()}>
        <SessionMenuRow
          icon={<Pencil className="h-3.5 w-3.5" />}
          label="重命名"
          onClick={() => { onOpenChange(false); onRename(); }}
        />
        <SessionMenuRow
          icon={<Trash2 className="h-3.5 w-3.5" />}
          label="删除文件夹"
          danger
          onClick={() => { onOpenChange(false); onDelete(); }}
        />
      </PopoverContent>
    </Popover>
  );
}

// ══════════════════════════════════════════════════════════
// 用户菜单面板内容
// ══════════════════════════════════════════════════════════

interface UserMenuContentProps {
  isGuest: boolean;
  isAdmin: boolean;
  theme: Theme;
  onToggleTheme: () => void;
  onNavigate: (path: string) => void;
  onLogout: () => void;
  onRegister: () => void;
  pointBalance?: number;
  planName?: string;
}

function UserMenuContent({
  isGuest,
  isAdmin,
  theme,
  onToggleTheme,
  onNavigate,
  onLogout,
  onRegister,
  pointBalance,
  planName,
}: UserMenuContentProps) {
  return (
    <div className="space-y-0.5">
      {!isGuest && typeof pointBalance === "number" && (
        <button
          className="mb-1 flex w-full items-center justify-between rounded-lg bg-accent/55 px-3 py-2.5 text-left transition-ui hover:bg-accent"
          onClick={() => onNavigate("/profile")}
        >
          <span>
            <span className="block text-[11px] text-muted-foreground">可用积分</span>
            <span className="block text-sm font-semibold tabular-nums">{Math.floor(pointBalance).toLocaleString()} 积分</span>
          </span>
          {planName && <span className="max-w-20 truncate text-[10px] text-muted-foreground">{planName}</span>}
        </button>
      )}

      {/* 个人中心 */}
      <MenuRow
        icon={User}
        label="个人中心"
        onClick={() => onNavigate("/profile")}
      />

      {/* 套餐定价 */}
      <MenuRow
        icon={CreditCard}
        label="套餐定价"
        onClick={() => onNavigate("/pricing")}
      />

      {/* 管理后台（仅管理员） */}
      {isAdmin && (
        <MenuRow
          icon={Settings}
          label="管理后台"
          onClick={() => onNavigate("/admin")}
        />
      )}

      <div className="h-px bg-border/60 my-1" />

      {/* 深浅模式切换：浅色 → 深色 → 跟随系统 循环；label/icon 指向下一个模式 */}
      <MenuRow
        icon={theme === "light" ? Moon : theme === "dark" ? Monitor : Sun}
        label={theme === "light" ? "深色模式" : theme === "dark" ? "跟随系统" : "浅色模式"}
        onClick={onToggleTheme}
      />

      <div className="h-px bg-border/60 my-1" />

      {/* 游客：注册 / 非游客：退出登录 */}
      {isGuest ? (
        <MenuRow
          icon={UserPlus}
          label="登录/注册账号"
          onClick={onRegister}
        />
      ) : (
        <MenuRow
          icon={LogOut}
          label="退出登录"
          onClick={onLogout}
          destructive
        />
      )}
    </div>
  );
}

// ─── 菜单行 ────────────────────────────────────────────────
function MenuRow({
  icon: Icon,
  label,
  onClick,
  trailing,
  destructive,
}: {
  icon: typeof User;
  label: string;
  onClick: () => void;
  trailing?: string;
  destructive?: boolean;
}) {
  return (
    <button
      onClick={onClick}
      className={cn(
        "flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-sm transition-ui",
        destructive
          ? "text-destructive hover:bg-destructive/5"
          : "text-foreground hover:bg-accent"
      )}
    >
      <Icon className="h-4 w-4 shrink-0" />
      <span className="flex-1 text-left">{label}</span>
      {trailing && (
        <span className="text-[11px] text-muted-foreground font-mono-sm">{trailing}</span>
      )}
    </button>
  );
}

/**
 * MaterialsTab — 素材库 Tab 内容（嵌入选题中心页面）
 *
 * 功能：
 *   - 左侧文件夹侧边栏（创建/重命名/删除文件夹）
 *   - 右侧素材列表（支持文件夹筛选、搜索、上传、删除）
 *   - 从素材发起写作
 */
import { useState, useEffect, useCallback } from "react";
import { useNavigate } from "react-router-dom";
import {
  Plus, Trash2, FileText, Link as LinkIcon, Search, PenLine,
  Loader2, File, ChevronLeft, ChevronRight, AlertCircle, Database,
  FolderPlus, Folder, MoreVertical, Pencil, ChevronRight as ChevronRightIcon,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import {
  type UserMaterial,
  type MaterialSearchResult,
  type MaterialFolder,
  listMaterials, deleteMaterial, searchMaterials,
  listFolders, createFolder, updateFolder, deleteFolder, moveMaterial,
} from "@/lib/material-api";
import { AddMaterialDialog } from "@/components/topic/add-material-dialog";
import { useAgentStore } from "@/stores/agent-store";
import { toast } from "@/stores/toast-store";

const SOURCE_ICONS: Record<string, typeof FileText> = {
  text: FileText,
  file: File,
  url: LinkIcon,
  auto: Search,
};

const SOURCE_LABELS: Record<string, string> = {
  text: "文本",
  file: "文件",
  url: "URL",
  auto: "自动",
};

function formatSize(bytes?: number): string {
  if (!bytes) return "—";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function formatTime(ts: string): string {
  const d = new Date(ts);
  return d.toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
}

export function MaterialsTab() {
  const navigate = useNavigate();
  const createSession = useAgentStore((s) => s.createSession);
  const startWriting = useAgentStore((s) => s.startWriting);

  const [materials, setMaterials] = useState<UserMaterial[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [pageSize] = useState(20);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Folders
  const [folders, setFolders] = useState<MaterialFolder[]>([]);
  const [activeFolder, setActiveFolder] = useState<string>(""); // "" = root, "all" = all, UUID = specific
  const [showFolderInput, setShowFolderInput] = useState(false);
  const [folderName, setFolderName] = useState("");
  const [editingFolder, setEditingFolder] = useState<string | null>(null);
  const [editingName, setEditingName] = useState("");
  const [folderMenuOpen, setFolderMenuOpen] = useState<string | null>(null);

  // Add dialog
  const [showAdd, setShowAdd] = useState(false);

  // Search
  const [searchQuery, setSearchQuery] = useState("");
  const [searchResults, setSearchResults] = useState<MaterialSearchResult[]>([]);
  const [searching, setSearching] = useState(false);
  const [showSearch, setShowSearch] = useState(false);

  // ─── Data Loading ──────────────────────────────────────

  const loadFolders = useCallback(async () => {
    try {
      const f = await listFolders();
      setFolders(f);
    } catch {
      // silent fail
    }
  }, []);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const { materials, total } = await listMaterials(page, pageSize, activeFolder);
      setMaterials(materials);
      setTotal(total);
    } catch (e) {
      setError(e instanceof Error ? e.message : "加载失败");
    } finally {
      setLoading(false);
    }
  }, [page, pageSize, activeFolder]);

  useEffect(() => { loadFolders(); }, [loadFolders]);
  useEffect(() => { load(); }, [load]);

  // Reset page when folder changes
  useEffect(() => { setPage(1); }, [activeFolder]);

  // ─── Actions ───────────────────────────────────────────

  const handleDelete = async (id: string) => {
    if (!confirm("确定删除这个素材？")) return;
    try {
      await deleteMaterial(id);
      await load();
      await loadFolders();
    } catch (e) {
      setError(e instanceof Error ? e.message : "删除失败");
    }
  };

  const handleSearch = async () => {
    if (!searchQuery.trim()) return;
    setSearching(true);
    setShowSearch(true);
    try {
      const results = await searchMaterials(searchQuery, 10);
      setSearchResults(results);
    } catch (e) {
      setError(e instanceof Error ? e.message : "搜索失败");
    } finally {
      setSearching(false);
    }
  };

  // ─── Folder Actions ────────────────────────────────────

  const handleCreateFolder = async () => {
    if (!folderName.trim()) return;
    try {
      await createFolder(folderName.trim());
      setFolderName("");
      setShowFolderInput(false);
      await loadFolders();
      toast.success("文件夹已创建", folderName.trim());
    } catch (e) {
      setError(e instanceof Error ? e.message : "创建文件夹失败");
    }
  };

  const handleRenameFolder = async (id: string) => {
    if (!editingName.trim()) return;
    try {
      await updateFolder(id, editingName.trim());
      setEditingFolder(null);
      setEditingName("");
      await loadFolders();
    } catch (e) {
      setError(e instanceof Error ? e.message : "重命名失败");
    }
  };

  const handleDeleteFolder = async (id: string, name: string) => {
    if (!confirm(`确定删除文件夹「${name}」？文件夹内的素材将移到根目录。`)) return;
    setFolderMenuOpen(null);
    try {
      await deleteFolder(id);
      if (activeFolder === id) setActiveFolder("");
      await loadFolders();
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : "删除文件夹失败");
    }
  };

  // ─── 从素材开始写作 ───

  const handleStartWriting = async (mat: UserMaterial) => {
    createSession();

    const message = `请基于材料「${mat.title}」写一篇文章。材料正文由服务端按引用读取并在运行开始时建立快照。`;

    navigate("/write");

    setTimeout(() => {
      startWriting({
        message,
        mode: "writing",
        material_refs: [{
          material_id: mat.id,
          source_ref: mat.governance?.source_ref || `kb://documents/${mat.doc_id || ""}`,
          title: mat.title,
        }],
      });
      toast.success("已注入素材", "正在以该素材为参考开始写作");
    }, 200);
  };

  const totalPages = Math.ceil(total / pageSize);

  const folderLabel = activeFolder === "" ? "根目录" : activeFolder === "all" ? "全部素材" : folders.find(f => f.id === activeFolder)?.name ?? "文件夹";

  return (
    <div className="flex h-full">
      {/* ─── 左侧文件夹侧边栏 ─── */}
      <div className="w-48 shrink-0 border-r bg-muted/20 overflow-y-auto">
        <div className="p-3 space-y-1">
          <div className="flex items-center justify-between mb-2">
            <span className="text-xs font-medium text-muted-foreground uppercase tracking-wide">文件夹</span>
            <button
              onClick={() => setShowFolderInput(!showFolderInput)}
              className="text-muted-foreground hover:text-foreground transition-ui"
              title="新建文件夹"
            >
              <FolderPlus className="h-3.5 w-3.5" />
            </button>
          </div>

          {showFolderInput && (
            <div className="flex gap-1 mb-2">
              <Input
                value={folderName}
                onChange={(e) => setFolderName(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") { e.preventDefault(); handleCreateFolder(); }
                  if (e.key === "Escape") { setShowFolderInput(false); setFolderName(""); }
                }}
                placeholder="文件夹名称"
                className="h-7 text-xs"
                autoFocus
              />
              <Button size="sm" variant="ghost" className="h-7 px-2" onClick={handleCreateFolder}>
                <Plus className="h-3 w-3" />
              </Button>
            </div>
          )}

          {/* 全部素材 */}
          <button
            onClick={() => setActiveFolder("all")}
            className={`flex items-center gap-2 w-full rounded-md px-2 py-1.5 text-sm transition-ui ${
              activeFolder === "all" ? "bg-accent text-foreground font-medium" : "text-muted-foreground hover:bg-accent/50 hover:text-foreground"
            }`}
          >
            <Database className="h-3.5 w-3.5 shrink-0" />
            全部素材
          </button>

          {/* 根目录 */}
          <button
            onClick={() => setActiveFolder("")}
            className={`flex items-center gap-2 w-full rounded-md px-2 py-1.5 text-sm transition-ui ${
              activeFolder === "" ? "bg-accent text-foreground font-medium" : "text-muted-foreground hover:bg-accent/50 hover:text-foreground"
            }`}
          >
            <Folder className="h-3.5 w-3.5 shrink-0" />
            未分组
          </button>

          {/* 文件夹列表 */}
          {folders.map((f) => (
            <div key={f.id} className="group relative">
              {editingFolder === f.id ? (
                <div className="flex gap-1 px-1">
                  <Input
                    value={editingName}
                    onChange={(e) => setEditingName(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === "Enter") { e.preventDefault(); handleRenameFolder(f.id); }
                      if (e.key === "Escape") { setEditingFolder(null); setEditingName(""); }
                    }}
                    className="h-7 text-xs"
                    autoFocus
                  />
                  <Button size="sm" variant="ghost" className="h-7 px-2" onClick={() => handleRenameFolder(f.id)}>
                    <Plus className="h-3 w-3" />
                  </Button>
                </div>
              ) : (
                <>
                  <button
                    onClick={() => setActiveFolder(f.id)}
                    className={`flex items-center gap-2 w-full rounded-md px-2 py-1.5 text-sm transition-ui ${
                      activeFolder === f.id ? "bg-accent text-foreground font-medium" : "text-muted-foreground hover:bg-accent/50 hover:text-foreground"
                    }`}
                  >
                    <Folder className="h-3.5 w-3.5 shrink-0" />
                    <span className="truncate flex-1 text-left">{f.name}</span>
                    {f.material_count > 0 && (
                      <span className="text-xs text-muted-foreground/60 shrink-0">{f.material_count}</span>
                    )}
                  </button>
                  <button
                    onClick={(e) => {
                      e.stopPropagation();
                      setFolderMenuOpen(folderMenuOpen === f.id ? null : f.id);
                    }}
                    className="absolute right-1 top-1/2 -translate-y-1/2 opacity-0 group-hover:opacity-100 transition-ui text-muted-foreground hover:text-foreground"
                  >
                    <MoreVertical className="h-3 w-3" />
                  </button>
                  {folderMenuOpen === f.id && (
                    <div className="absolute right-0 top-full z-10 mt-1 rounded-md border bg-popover shadow-md py-1 min-w-[100px]">
                      <button
                        onClick={(e) => {
                          e.stopPropagation();
                          setEditingFolder(f.id);
                          setEditingName(f.name);
                          setFolderMenuOpen(null);
                        }}
                        className="flex items-center gap-2 w-full px-3 py-1.5 text-xs hover:bg-accent transition-ui"
                      >
                        <Pencil className="h-3 w-3" /> 重命名
                      </button>
                      <button
                        onClick={(e) => {
                          e.stopPropagation();
                          handleDeleteFolder(f.id, f.name);
                        }}
                        className="flex items-center gap-2 w-full px-3 py-1.5 text-xs text-destructive hover:bg-destructive/10 transition-ui"
                      >
                        <Trash2 className="h-3 w-3" /> 删除
                      </button>
                    </div>
                  )}
                </>
              )}
            </div>
          ))}
        </div>
      </div>

      {/* ─── 右侧素材列表 ─── */}
      <div className="flex-1 overflow-y-auto">
        <div className="max-w-4xl mx-auto p-6 space-y-4">
          {/* Add Material Dialog */}
          <AddMaterialDialog
            open={showAdd}
            onOpenChange={setShowAdd}
            onAdded={() => { load(); loadFolders(); }}
            onError={(msg) => setError(msg)}
            folderId={activeFolder !== "all" && activeFolder !== "" ? activeFolder : undefined}
          />

          {/* Error */}
          {error && (
            <Card className="border-destructive">
              <CardContent className="p-4 flex items-center gap-2 text-sm text-destructive">
                <AlertCircle className="h-4 w-4 flex-shrink-0" />
                {error}
                <Button size="sm" variant="ghost" onClick={() => setError(null)} className="ml-auto">
                  关闭
                </Button>
              </CardContent>
            </Card>
          )}

          {/* Breadcrumb + Actions */}
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-1.5 text-sm text-muted-foreground">
              <Database className="h-4 w-4" />
              <span>{folderLabel}</span>
              <ChevronRightIcon className="h-3 w-3" />
              <span className="text-foreground font-medium">{total} 条素材</span>
            </div>
            <div className="flex items-center gap-2">
              <Button size="sm" variant="ghost" onClick={load}>
                刷新
              </Button>
              <Button size="sm" onClick={() => setShowAdd(true)}>
                <Plus className="h-4 w-4 mr-2" /> 添加素材
              </Button>
            </div>
          </div>

          {/* Search Bar */}
          <div className="flex gap-2">
            <Input
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && handleSearch()}
              placeholder="在素材库中搜索（混合检索）..."
              className="flex-1"
            />
            <Button onClick={handleSearch} disabled={searching}>
              {searching ? <Loader2 className="h-4 w-4 animate-spin" /> : <Search className="h-4 w-4" />}
            </Button>
          </div>

          {/* Search Results */}
          {showSearch && (
            <Card>
              <CardHeader>
                <div className="flex items-center justify-between">
                  <CardTitle className="text-sm">检索结果（{searchResults.length} 条）</CardTitle>
                  <Button size="sm" variant="ghost" onClick={() => setShowSearch(false)}>
                    关闭
                  </Button>
                </div>
              </CardHeader>
              <CardContent className="space-y-3">
                {searchResults.length === 0 ? (
                  <p className="text-sm text-muted-foreground text-center py-4">未找到相关素材</p>
                ) : (
                  searchResults.map((result, i) => (
                    <div key={i} className="border rounded-lg p-3 space-y-1">
                      <div className="flex items-center gap-2">
                        <span className="text-sm font-medium truncate">{result.title || "无标题"}</span>
                        <Badge variant="outline" className="bg-blue-50 text-blue-700 text-xs">
                          {(result.score * 100).toFixed(0)}%
                        </Badge>
                      </div>
                      <p className="text-xs text-muted-foreground line-clamp-2">{result.content}</p>
                    </div>
                  ))
                )}
              </CardContent>
            </Card>
          )}

          {/* Materials List */}
          <Card>
            <CardContent className="pt-4">
              {loading ? (
                <div className="text-center py-12">
                  <Loader2 className="h-6 w-6 animate-spin mx-auto text-muted-foreground" />
                </div>
              ) : materials.length === 0 ? (
                <div className="text-center py-12 space-y-2">
                  <Database className="h-10 w-10 text-muted-foreground mx-auto" />
                  <p className="text-sm text-muted-foreground">
                    {activeFolder === "" ? "未分组中还没有素材" : "此文件夹中还没有素材"}
                  </p>
                  <p className="text-xs text-muted-foreground">点击「添加素材」开始上传</p>
                </div>
              ) : (
                <div className="space-y-2">
                  {materials.map((mat) => {
                    const Icon = SOURCE_ICONS[mat.source_type] || FileText;
                    return (
                      <div
                        key={mat.id}
                        className="flex items-start gap-3 border rounded-lg p-3 hover:bg-accent/30 transition-colors group"
                      >
                        <div className="flex-shrink-0 mt-0.5">
                          <Icon className="h-4 w-4 text-muted-foreground" />
                        </div>
                        <div className="flex-1 min-w-0 space-y-1">
                          <div className="flex items-center gap-2">
                            <span className="text-sm font-medium truncate">{mat.title}</span>
                            <Badge variant="outline" className="text-xs">
                              {SOURCE_LABELS[mat.source_type] || mat.source_type}
                            </Badge>
                            <Badge
                              variant="outline"
                              className={mat.governance?.eligible ? "border-emerald-200 bg-emerald-50 text-emerald-800 text-xs" : "border-amber-200 bg-amber-50 text-amber-800 text-xs"}
                              title={mat.governance?.eligible ? "运行开始时将建立不可变材料快照" : "材料来源尚未满足治理要求"}
                            >
                              {mat.governance?.snapshot_status === "pending_run_snapshot" ? "待运行快照" : "兼容材料"}
                            </Badge>
                          </div>
                          <p className="text-xs text-muted-foreground line-clamp-1">
                            {mat.content_preview || (mat.file_name ? `文件: ${mat.file_name}` : "—")}
                          </p>
                          <div className="flex items-center gap-3 text-xs text-muted-foreground">
                            <span>{formatTime(mat.created_at)}</span>
                            {mat.file_size ? <span>{formatSize(mat.file_size)}</span> : null}
                          </div>
                        </div>
                        <div className="flex flex-shrink-0 items-center gap-1 opacity-0 group-hover:opacity-100 transition-ui">
                          <Button
                            variant="ghost"
                            size="sm"
                            onClick={() => handleStartWriting(mat)}
                            title="以此素材写作"
                            className="text-primary hover:text-primary/80"
                          >
                            <PenLine className="h-3.5 w-3.5" />
                          </Button>
                          <Button
                            variant="ghost"
                            size="sm"
                            onClick={() => handleDelete(mat.id)}
                            title="删除"
                            className="text-muted-foreground hover:text-destructive"
                          >
                            <Trash2 className="h-3.5 w-3.5" />
                          </Button>
                        </div>
                      </div>
                    );
                  })}
                </div>
              )}

              {/* Pagination */}
              {totalPages > 1 && (
                <div className="flex items-center justify-center gap-2 mt-4">
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => setPage(Math.max(1, page - 1))}
                    disabled={page <= 1}
                  >
                    <ChevronLeft className="h-4 w-4" />
                  </Button>
                  <span className="text-sm text-muted-foreground">
                    {page} / {totalPages}
                  </span>
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => setPage(Math.min(totalPages, page + 1))}
                    disabled={page >= totalPages}
                  >
                    <ChevronRight className="h-4 w-4" />
                  </Button>
                </div>
              )}
            </CardContent>
          </Card>
        </div>
      </div>
    </div>
  );
}

/**
 * MaterialsTab — 素材库 Tab 内容（嵌入选题中心页面）
 *
 * 从 my-materials.tsx 提取，去掉外层 header（由父页面统一管理），
 * 保留素材列表、搜索、上传、分页等完整功能。
 */
import { useState, useEffect, useCallback } from "react";
import {
  Plus, Trash2, FileText, Link as LinkIcon, Search,
  Loader2, File, ChevronLeft, ChevronRight, AlertCircle, Database,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import {
  type UserMaterial,
  type MaterialSearchResult,
  listMaterials, deleteMaterial, searchMaterials,
} from "@/lib/material-api";
import { AddMaterialDialog } from "@/components/topic/add-material-dialog";

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
  const [materials, setMaterials] = useState<UserMaterial[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [pageSize] = useState(20);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Add dialog
  const [showAdd, setShowAdd] = useState(false);

  // Search
  const [searchQuery, setSearchQuery] = useState("");
  const [searchResults, setSearchResults] = useState<MaterialSearchResult[]>([]);
  const [searching, setSearching] = useState(false);
  const [showSearch, setShowSearch] = useState(false);

  // ─── Data Loading ──────────────────────────────────────

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const { materials, total } = await listMaterials(page, pageSize);
      setMaterials(materials);
      setTotal(total);
    } catch (e) {
      setError(e instanceof Error ? e.message : "加载失败");
    } finally {
      setLoading(false);
    }
  }, [page, pageSize]);

  useEffect(() => { load(); }, [load]);

  // ─── Actions ───────────────────────────────────────────

  const handleDelete = async (id: string) => {
    if (!confirm("确定删除这个素材？")) return;
    try {
      await deleteMaterial(id);
      await load();
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

  const totalPages = Math.ceil(total / pageSize);

  return (
    <div className="max-w-6xl mx-auto p-6 space-y-6">
      {/* Add Material Dialog */}
      <AddMaterialDialog
        open={showAdd}
        onOpenChange={setShowAdd}
        onAdded={load}
        onError={(msg) => setError(msg)}
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
        <CardHeader>
          <div className="flex items-center justify-between">
            <CardTitle className="text-sm">
              素材列表（{total} 条）
            </CardTitle>
            <div className="flex items-center gap-2">
              <Button size="sm" variant="ghost" onClick={load}>
                刷新
              </Button>
              <Button size="sm" onClick={() => setShowAdd(true)}>
                <Plus className="h-4 w-4 mr-2" /> 添加素材
              </Button>
            </div>
          </div>
        </CardHeader>
        <CardContent>
          {loading ? (
            <div className="text-center py-12">
              <Loader2 className="h-6 w-6 animate-spin mx-auto text-muted-foreground" />
            </div>
          ) : materials.length === 0 ? (
            <div className="text-center py-12 space-y-2">
              <Database className="h-10 w-10 text-muted-foreground mx-auto" />
              <p className="text-sm text-muted-foreground">还没有素材，点击「添加素材」开始上传</p>
            </div>
          ) : (
            <div className="space-y-2">
              {materials.map((mat) => {
                const Icon = SOURCE_ICONS[mat.source_type] || FileText;
                return (
                  <div
                    key={mat.id}
                    className="flex items-start gap-3 border rounded-lg p-3 hover:bg-accent/30 transition-colors"
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
                      </div>
                      <p className="text-xs text-muted-foreground line-clamp-1">
                        {mat.content_preview || (mat.file_name ? `文件: ${mat.file_name}` : "—")}
                      </p>
                      <div className="flex items-center gap-3 text-xs text-muted-foreground">
                        <span>{formatTime(mat.created_at)}</span>
                        {mat.file_size ? <span>{formatSize(mat.file_size)}</span> : null}
                      </div>
                    </div>
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => handleDelete(mat.id)}
                      title="删除"
                      className="flex-shrink-0 text-muted-foreground hover:text-destructive"
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                    </Button>
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
  );
}

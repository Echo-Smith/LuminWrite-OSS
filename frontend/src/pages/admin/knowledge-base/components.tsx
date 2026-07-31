/**
 * 知识库管理 — 子组件
 * 拆分为：未配置提示 / 添加知识面板 / 知识列表表格（含 chunk 展开）/ 检索面板 / 统计仪表盘
 */
import { useState, useEffect, useMemo } from "react";
import {
  Search, Plus, Trash2, Loader2, FileUp, Link2, FileText,
  RefreshCw, AlertCircle, ChevronDown, ChevronRight, Database,
  Boxes, Network, Layers,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  type KnowledgeDoc,
  type KBSearchResult,
  type KBChunk,
  type KBEntity,
  type KBRelation,
  type KBStats,
  getDocumentChunks, getDocumentEntities, getStats,
  searchWithMode, type SearchMode,
  getGraph, type GraphNode, type GraphEdge,
} from "@/lib/kb-api";

// ─── 未配置提示组件 ─────────────────────────────────────

export function KBNotConfigured({ onRetry }: { onRetry: () => void }) {
  return (
    <Card>
      <CardContent className="py-12 flex flex-col items-center gap-4">
        <AlertCircle className="h-10 w-10 text-amber-500" />
        <div className="text-center space-y-2">
          <p className="text-sm font-medium">知识库未初始化</p>
          <p className="text-xs text-muted-foreground max-w-md">
            本地知识库需要 PostgreSQL + pgvector + paradedb 扩展。
            请确保 <code className="bg-muted px-1.5 py-0.5 rounded text-xs">docker compose up -d</code>
            已启动且数据库 migration 已执行。
          </p>
        </div>
        <Button variant="outline" size="sm" onClick={onRetry}>
          <RefreshCw className="h-4 w-4 mr-2" /> 重新检测
        </Button>
      </CardContent>
    </Card>
  );
}

// ─── 添加知识面板 ───────────────────────────────────────

type AddMode = "text" | "url" | "file";

export function KBAddPanel({
  onAdd,
  onCancel,
}: {
  onAdd: (mode: AddMode, data: { title: string; content: string; url: string; file: File | null }) => Promise<void>;
  onCancel: () => void;
}) {
  const [addMode, setAddMode] = useState<AddMode>("text");
  const [title, setTitle] = useState("");
  const [content, setContent] = useState("");
  const [url, setUrl] = useState("");
  const [file, setFile] = useState<File | null>(null);
  const [saving, setSaving] = useState(false);

  const canSubmit =
    addMode === "text" ? (title && content) :
    addMode === "url" ? url :
    file;

  const handleSubmit = async () => {
    if (!canSubmit) return;
    setSaving(true);
    try {
      await onAdd(addMode, { title, content, url, file });
      setTitle(""); setContent(""); setUrl(""); setFile(null);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Card>
      <CardHeader><CardTitle className="text-sm">添加知识</CardTitle></CardHeader>
      <CardContent>
        <Tabs value={addMode} onValueChange={(v) => setAddMode(v as AddMode)}>
          <TabsList>
            <TabsTrigger value="text"><FileText className="h-3.5 w-3.5 mr-1.5" /> 文本</TabsTrigger>
            <TabsTrigger value="url"><Link2 className="h-3.5 w-3.5 mr-1.5" /> URL</TabsTrigger>
            <TabsTrigger value="file"><FileUp className="h-3.5 w-3.5 mr-1.5" /> 文件</TabsTrigger>
          </TabsList>

          <TabsContent value="text" className="space-y-3 mt-4">
            <div>
              <Label>标题</Label>
              <Input value={title} onChange={(e) => setTitle(e.target.value)} placeholder="知识标题" />
            </div>
            <div>
              <Label>内容（支持 Markdown）</Label>
              <Textarea value={content} onChange={(e) => setContent(e.target.value)}
                placeholder="输入知识内容..." className="min-h-[200px] font-mono text-sm" />
            </div>
          </TabsContent>

          <TabsContent value="url" className="space-y-3 mt-4">
            <div>
              <Label>网页 URL</Label>
              <Input value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://example.com/article" />
            </div>
            <div>
              <Label>标题（可选）</Label>
              <Input value={title} onChange={(e) => setTitle(e.target.value)} placeholder="自动提取网页标题" />
            </div>
          </TabsContent>

          <TabsContent value="file" className="space-y-3 mt-4">
            <div>
              <Label>上传文件</Label>
              <div className="border-2 border-dashed rounded-lg p-8 text-center cursor-pointer hover:bg-accent/30 transition-colors"
                onClick={() => document.getElementById("kb-file-input")?.click()}>
                <FileUp className="h-8 w-8 mx-auto text-muted-foreground" />
                <p className="text-sm mt-2 text-muted-foreground">
                  {file ? file.name : "点击选择文件"}
                </p>
                <p className="text-xs text-muted-foreground mt-1">支持 PDF / Word / Excel / PPT / 图片 / HTML / Markdown</p>
                <input id="kb-file-input" type="file" className="hidden"
                  onChange={(e) => setFile(e.target.files?.[0] ?? null)} />
              </div>
            </div>
            <div>
              <Label>标题（可选）</Label>
              <Input value={title} onChange={(e) => setTitle(e.target.value)} placeholder="自动使用文件名" />
            </div>
          </TabsContent>
        </Tabs>

        <div className="flex justify-end gap-2 mt-4">
          <Button variant="outline" size="sm" onClick={onCancel}>取消</Button>
          <Button size="sm" onClick={handleSubmit} disabled={!canSubmit || saving}>
            {saving ? <Loader2 className="h-4 w-4 mr-2 animate-spin" /> : <Plus className="h-4 w-4 mr-2" />}
            添加
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

// ─── 知识列表表格 ───────────────────────────────────────

const STATUS_COLORS: Record<string, string> = {
  active: "bg-green-100 text-green-700",
  processing: "bg-blue-100 text-blue-700",
  failed: "bg-red-100 text-red-700",
};

export function KnowledgeTable({
  entries, total, page, loading, error,
  onPageChange, onRefresh, onDelete,
}: {
  entries: KnowledgeDoc[];
  total: number;
  page: number;
  loading: boolean;
  error: string | null;
  onPageChange: (page: number) => void;
  onRefresh: () => void;
  onDelete: (id: string) => void;
}) {
  const pageSize = 20;
  const totalPages = Math.ceil(total / pageSize);
  const [expandedDoc, setExpandedDoc] = useState<string | null>(null);

  const handleToggleExpand = (docId: string) => {
    setExpandedDoc(expandedDoc === docId ? null : docId);
  };

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <span className="text-sm text-muted-foreground">共 {total} 条知识</span>
        <Button variant="ghost" size="sm" onClick={onRefresh} disabled={loading}>
          <RefreshCw className={`h-4 w-4 mr-2 ${loading ? "animate-spin" : ""}`} /> 刷新
        </Button>
      </div>

      {error && (
        <Card>
          <CardContent className="py-4 flex items-center gap-2 text-sm text-red-500">
            <AlertCircle className="h-4 w-4" /> {error}
          </CardContent>
        </Card>
      )}

      <div className="rounded-lg border">
        <table className="w-full">
          <thead>
            <tr className="border-b bg-muted/50">
              <th className="text-left p-3 text-sm font-medium w-8"></th>
              <th className="text-left p-3 text-sm font-medium">标题</th>
              <th className="text-left p-3 text-sm font-medium">来源</th>
              <th className="text-left p-3 text-sm font-medium">分块</th>
              <th className="text-left p-3 text-sm font-medium">状态</th>
              <th className="text-left p-3 text-sm font-medium">创建时间</th>
              <th className="text-right p-3 text-sm font-medium">操作</th>
            </tr>
          </thead>
          <tbody>
            {loading ? (
              <tr><td colSpan={7} className="p-8 text-center"><Loader2 className="h-4 w-4 animate-spin mx-auto" /></td></tr>
            ) : entries.length === 0 ? (
              <tr><td colSpan={7} className="p-8 text-center text-muted-foreground text-sm">暂无知识条目</td></tr>
            ) : (
              entries.map((entry) => (
                <>
                  <tr key={entry.id} className="border-b last:border-0 hover:bg-accent/30">
                    <td className="p-3">
                      <button
                        onClick={() => handleToggleExpand(entry.id)}
                        className="text-muted-foreground hover:text-foreground transition-ui"
                      >
                        {expandedDoc === entry.id ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
                      </button>
                    </td>
                    <td className="p-3 text-sm font-medium">
                      <div className="flex items-center gap-2">
                        <FileText className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
                        <span className="truncate max-w-[300px]">{entry.title}</span>
                      </div>
                    </td>
                    <td className="p-3 text-sm text-muted-foreground">
                      {entry.source_type || entry.source || "—"}
                    </td>
                    <td className="p-3 text-sm text-muted-foreground">
                      {entry.chunk_count != null && entry.chunk_count > 0 ? (
                        <Badge variant="outline" className="text-xs">
                          {entry.chunk_count} 块
                        </Badge>
                      ) : "—"}
                    </td>
                    <td className="p-3">
                      <Badge variant="outline" className={STATUS_COLORS[entry.status ?? ""] ?? "bg-gray-100 text-gray-500"}>
                        {entry.status || "active"}
                      </Badge>
                    </td>
                    <td className="p-3 text-sm text-muted-foreground">
                      {entry.created_at ? new Date(entry.created_at).toLocaleString("zh-CN") : "—"}
                    </td>
                    <td className="p-3 text-right">
                      <Button variant="ghost" size="sm" onClick={() => onDelete(entry.id)} title="删除">
                        <Trash2 className="h-3.5 w-3.5" />
                      </Button>
                    </td>
                  </tr>
                  {expandedDoc === entry.id && (
                    <tr key={`${entry.id}-detail`}>
                      <td colSpan={7} className="p-0">
                        <DocDetailPanel docId={entry.id} />
                      </td>
                    </tr>
                  )}
                </>
              ))
            )}
          </tbody>
        </table>
      </div>

      {totalPages > 1 && (
        <div className="flex items-center justify-center gap-2">
          <Button variant="outline" size="sm" disabled={page <= 1} onClick={() => onPageChange(page - 1)}>
            上一页
          </Button>
          <span className="text-sm text-muted-foreground">
            第 {page} 页 / 共 {totalPages} 页
          </span>
          <Button variant="outline" size="sm" disabled={page * pageSize >= total} onClick={() => onPageChange(page + 1)}>
            下一页
          </Button>
        </div>
      )}
    </div>
  );
}

// ─── 文档详情面板（Chunks + Entities + Relations） ──────

function DocDetailPanel({ docId }: { docId: string }) {
  const [tab, setTab] = useState<"chunks" | "entities">("chunks");
  const [chunks, setChunks] = useState<KBChunk[]>([]);
  const [entities, setEntities] = useState<KBEntity[]>([]);
  const [relations, setRelations] = useState<KBRelation[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      setLoading(true);
      try {
        const [chunkData, entityData] = await Promise.all([
          getDocumentChunks(docId),
          getDocumentEntities(docId),
        ]);
        if (!cancelled) {
          setChunks(chunkData);
          setEntities(entityData.entities);
          setRelations(entityData.relations);
        }
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => { cancelled = true; };
  }, [docId]);

  if (loading) {
    return <div className="p-4 bg-muted/20"><Loader2 className="h-4 w-4 animate-spin mx-auto" /></div>;
  }

  return (
    <div className="p-4 bg-muted/20 space-y-3">
      <div className="flex gap-2">
        <Button
          size="sm"
          variant={tab === "chunks" ? "default" : "outline"}
          onClick={() => setTab("chunks")}
        >
          <Layers className="h-3.5 w-3.5 mr-1" /> 分块 ({chunks.length})
        </Button>
        <Button
          size="sm"
          variant={tab === "entities" ? "default" : "outline"}
          onClick={() => setTab("entities")}
        >
          <Network className="h-3.5 w-3.5 mr-1" /> 实体/关系 ({entities.length}/{relations.length})
        </Button>
      </div>

      {tab === "chunks" ? (
        <div className="space-y-2 max-h-[400px] overflow-y-auto">
          {chunks.length === 0 ? (
            <p className="text-sm text-muted-foreground py-4">无分块数据</p>
          ) : chunks.map((chunk, i) => (
            <div key={chunk.id} className="border rounded-lg p-3 bg-card">
              <div className="flex items-center gap-2 mb-1">
                <Badge variant="outline" className="text-xs">#{chunk.chunk_index}</Badge>
                {chunk.has_embedding && (
                  <Badge variant="outline" className="text-xs bg-green-50 text-green-700">已嵌入</Badge>
                )}
                <span className="text-xs text-muted-foreground">
                  {chunk.content.length} 字
                </span>
              </div>
              <p className="text-xs text-muted-foreground line-clamp-3">{chunk.content}</p>
            </div>
          ))}
        </div>
      ) : (
        <div className="space-y-3 max-h-[400px] overflow-y-auto">
          {entities.length === 0 && relations.length === 0 ? (
            <p className="text-sm text-muted-foreground py-4">无实体/关系数据（GraphRAG 可能尚未处理）</p>
          ) : (
            <>
              {entities.length > 0 && (
                <div>
                  <p className="text-xs font-medium text-muted-foreground mb-2">实体 ({entities.length})</p>
                  <div className="flex flex-wrap gap-1.5">
                    {entities.map((e) => (
                      <Badge key={e.id} variant="outline" className="text-xs">
                        {e.entity_name}
                        <span className="ml-1 text-muted-foreground">({e.entity_type})</span>
                      </Badge>
                    ))}
                  </div>
                </div>
              )}
              {relations.length > 0 && (
                <div>
                  <p className="text-xs font-medium text-muted-foreground mb-2 mt-3">关系 ({relations.length})</p>
                  <div className="space-y-1">
                    {relations.map((r) => (
                      <div key={r.id} className="text-xs flex items-center gap-1">
                        <Badge variant="outline" className="bg-blue-50 text-blue-700">{r.source_entity}</Badge>
                        <span className="text-muted-foreground">—{r.relation_type}→</span>
                        <Badge variant="outline" className="bg-purple-50 text-purple-700">{r.target_entity}</Badge>
                      </div>
                    ))}
                  </div>
                </div>
              )}
            </>
          )}
        </div>
      )}
    </div>
  );
}

// ─── 统计仪表盘 ─────────────────────────────────────────

export function KBStatsPanel({ kbId }: { kbId?: string }) {
  const [stats, setStats] = useState<KBStats | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      setLoading(true);
      try {
        const data = await getStats(kbId);
        if (!cancelled) setStats(data);
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => { cancelled = true; };
  }, [kbId]);

  if (loading) {
    return <div className="p-8 text-center"><Loader2 className="h-5 w-5 animate-spin mx-auto text-muted-foreground" /></div>;
  }

  if (!stats) {
    return <div className="p-8 text-center text-sm text-muted-foreground">无法获取统计数据</div>;
  }

  const embedCoverage = stats.chunk_count > 0
    ? ((stats.chunk_with_embedding / stats.chunk_count) * 100).toFixed(1)
    : "0";

  return (
    <div className="space-y-4">
      {/* 统计卡片 */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
        <StatCard icon={FileText} label="文档" value={stats.doc_count} color="text-blue-600" />
        <StatCard icon={Layers} label="分块" value={stats.chunk_count} color="text-cyan-600" />
        <StatCard icon={Boxes} label="实体" value={stats.entity_count} color="text-orange-600" />
        <StatCard icon={Network} label="关系" value={stats.relation_count} color="text-purple-600" />
      </div>

      {/* Embedding 覆盖率 */}
      <Card>
        <CardContent className="p-4">
          <div className="flex items-center justify-between mb-2">
            <span className="text-sm font-medium">Embedding 覆盖率</span>
            <Badge variant="outline" className="text-xs">
              {stats.chunk_with_embedding} / {stats.chunk_count}
            </Badge>
          </div>
          <div className="w-full bg-muted rounded-full h-2">
            <div
              className="bg-green-500 h-2 rounded-full transition-all"
              style={{ width: `${embedCoverage}%` }}
            />
          </div>
          <p className="text-xs text-muted-foreground mt-1">{embedCoverage}%</p>
        </CardContent>
      </Card>

      {/* 来源分布 */}
      <Card>
        <CardContent className="p-4">
          <p className="text-sm font-medium mb-3">来源分布</p>
          {Object.keys(stats.source_breakdown).length === 0 ? (
            <p className="text-xs text-muted-foreground">暂无数据</p>
          ) : (
            <div className="space-y-2">
              {Object.entries(stats.source_breakdown).map(([source, count]) => {
                const pct = stats.doc_count > 0 ? (count / stats.doc_count) * 100 : 0;
                return (
                  <div key={source} className="flex items-center gap-3">
                    <span className="text-xs w-20 text-muted-foreground">{source}</span>
                    <div className="flex-1 bg-muted rounded-full h-1.5">
                      <div className="bg-blue-500 h-1.5 rounded-full" style={{ width: `${pct}%` }} />
                    </div>
                    <span className="text-xs w-8 text-right">{count}</span>
                  </div>
                );
              })}
            </div>
          )}
        </CardContent>
      </Card>

      {/* 引擎信息 */}
      <Card>
        <CardContent className="p-4 space-y-2">
          <div className="flex items-center gap-2">
            <Database className="h-4 w-4 text-muted-foreground" />
            <h3 className="text-sm font-semibold">引擎信息</h3>
          </div>
          <div className="grid grid-cols-2 gap-2 text-xs text-muted-foreground">
            <div>PostgreSQL + pgvector (Dense cosine similarity)</div>
            <div>paradedb pg_search (BM25 全文检索)</div>
            <div>GraphRAG (LLM 实体抽取 + 2-hop 遍历)</div>
            <div>RRF 融合排序 (BM25 0.3 + Dense 0.5 + Graph 0.2)</div>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

function StatCard({
  icon: Icon, label, value, color,
}: {
  icon: typeof FileText;
  label: string;
  value: number;
  color: string;
}) {
  return (
    <Card>
      <CardContent className="p-4 flex items-center gap-3">
        <Icon className={`h-8 w-8 ${color}`} />
        <div>
          <p className="text-2xl font-bold">{value}</p>
          <p className="text-xs text-muted-foreground">{label}</p>
        </div>
      </CardContent>
    </Card>
  );
}

// ─── 检索面板 ───────────────────────────────────────────

export function KBSearchPanel({
  onSearch, kbId,
}: {
  onSearch: (query: string) => Promise<KBSearchResult[]>;
  kbId?: string;
}) {
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<KBSearchResult[]>([]);
  const [searching, setSearching] = useState(false);
  const [hasSearched, setHasSearched] = useState(false);
  const [mode, setMode] = useState<SearchMode>("hybrid");
  const [bm25Weight, setBM25Weight] = useState(0.3);
  const [denseWeight, setDenseWeight] = useState(0.5);
  const [showAdvanced, setShowAdvanced] = useState(false);

  const handleSearch = async () => {
    if (!query.trim()) return;
    setSearching(true);
    setHasSearched(true);
    try {
      const { results: searchResults } = await searchWithMode(query, {
        limit: 10,
        mode,
        bm25Weight: mode === "hybrid" ? bm25Weight : 0,
        denseWeight: mode === "hybrid" ? denseWeight : 0,
        kbId,
      });
      setResults(searchResults);
    } catch {
      setResults([]);
    } finally {
      setSearching(false);
    }
  };

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader>
          <CardTitle className="text-sm flex items-center gap-2">
            <Search className="h-4 w-4" /> 混合检索（BM25 + Dense + GraphRAG）
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-3">
          {/* 搜索框 */}
          <div className="flex gap-2">
            <Input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && handleSearch()}
              placeholder="输入检索查询..."
              className="flex-1"
            />
            <Button onClick={handleSearch} disabled={searching || !query.trim()}>
              {searching ? <Loader2 className="h-4 w-4 mr-2 animate-spin" /> : <Search className="h-4 w-4 mr-2" />}
              检索
            </Button>
          </div>

          {/* 模式切换 */}
          <div className="flex items-center gap-2">
            <span className="text-xs text-muted-foreground">模式:</span>
            {(["hybrid", "bm25", "dense"] as SearchMode[]).map((m) => (
              <button
                key={m}
                onClick={() => setMode(m)}
                className={`px-2.5 py-1 rounded-md text-xs font-medium transition-ui ${
                  mode === m
                    ? "bg-primary text-primary-foreground"
                    : "bg-muted text-muted-foreground hover:bg-accent"
                }`}
              >
                {m === "hybrid" ? "Hybrid (BM25+Dense)" : m === "bm25" ? "BM25" : "Dense"}
              </button>
            ))}
            <button
              onClick={() => setShowAdvanced(!showAdvanced)}
              className="ml-auto text-xs text-muted-foreground hover:text-foreground transition-ui"
            >
              {showAdvanced ? "收起高级" : "高级选项"}
            </button>
          </div>

          {/* 高级选项 — 权重滑块 */}
          {showAdvanced && mode === "hybrid" && (
            <div className="grid grid-cols-2 gap-4 p-3 bg-muted/30 rounded-lg anim-fade-in">
              <div>
                <div className="flex items-center justify-between mb-1">
                  <span className="text-xs font-medium">BM25 权重</span>
                  <span className="text-xs text-muted-foreground font-mono">{bm25Weight.toFixed(2)}</span>
                </div>
                <input
                  type="range" min={0} max={1} step={0.05}
                  value={bm25Weight}
                  onChange={(e) => setBM25Weight(parseFloat(e.target.value))}
                  className="w-full"
                />
              </div>
              <div>
                <div className="flex items-center justify-between mb-1">
                  <span className="text-xs font-medium">Dense 权重</span>
                  <span className="text-xs text-muted-foreground font-mono">{denseWeight.toFixed(2)}</span>
                </div>
                <input
                  type="range" min={0} max={1} step={0.05}
                  value={denseWeight}
                  onChange={(e) => setDenseWeight(parseFloat(e.target.value))}
                  className="w-full"
                />
              </div>
            </div>
          )}
        </CardContent>
      </Card>

      {/* 检索结果 — chunk 级展示 */}
      {results.length > 0 && (
        <div className="space-y-3">
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            <span>检索结果: {results.length} 条</span>
            <Badge variant="outline" className="text-xs">模式: {mode}</Badge>
          </div>
          {results.map((result, i) => (
            <Card key={i}>
              <CardContent className="p-4">
                <div className="flex items-start justify-between gap-3">
                  <div className="flex-1 min-w-0">
                    {/* 标题行 */}
                    <div className="flex items-center gap-2 flex-wrap">
                      <h4 className="text-sm font-medium">{result.title}</h4>
                      <Badge variant="outline" className="text-xs">
                        score: {result.score.toFixed(4)}
                      </Badge>
                      {result.chunk_id && (
                        <Badge variant="outline" className="text-xs bg-cyan-50 text-cyan-700">
                          chunk #{result.chunk_id.slice(0, 8)}
                        </Badge>
                      )}
                    </div>
                    {/* 内容预览 */}
                    <p className="text-sm text-muted-foreground mt-1 line-clamp-3">
                      {result.content || result.snippet}
                    </p>
                    {/* 分数详情 */}
                    {(result.bm25_score || result.dense_score) && (
                      <div className="flex items-center gap-2 mt-2">
                        {result.bm25_score != null && result.bm25_score > 0 && (
                          <Badge variant="outline" className="text-xs bg-orange-50 text-orange-700">
                            BM25: {result.bm25_score.toFixed(4)}
                          </Badge>
                        )}
                        {result.dense_score != null && result.dense_score > 0 && (
                          <Badge variant="outline" className="text-xs bg-green-50 text-green-700">
                            Dense: {result.dense_score.toFixed(4)}
                          </Badge>
                        )}
                      </div>
                    )}
                  </div>
                  <Badge variant="outline" className="bg-purple-100 text-purple-700 shrink-0">
                    {result.source || "local_kb"}
                  </Badge>
                </div>
              </CardContent>
            </Card>
          ))}
        </div>
      )}

      {results.length === 0 && hasSearched && !searching && (
        <Card>
          <CardContent className="py-8 text-center text-sm text-muted-foreground">无检索结果</CardContent>
        </Card>
      )}
    </div>
  );
}

// ─── 实体图谱 ─────────────────────────────────────────

const ENTITY_COLORS: Record<string, string> = {
  person: "#3b82f6",
  organization: "#f97316",
  location: "#10b981",
  event: "#ef4444",
  concept: "#8b5cf6",
  product: "#06b6d4",
};

export function KBGraphPanel() {
  const [nodes, setNodes] = useState<GraphNode[]>([]);
  const [edges, setEdges] = useState<GraphEdge[]>([]);
  const [loading, setLoading] = useState(true);
  const [selectedEntity, setSelectedEntity] = useState<GraphNode | null>(null);
  const [limit, setLimit] = useState(50);

  const loadGraph = async () => {
    setLoading(true);
    try {
      const { nodes: n, edges: e } = await getGraph(limit);
      setNodes(n);
      setEdges(e);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { loadGraph(); }, []);

  const edgeCount = (nodeId: string) => edges.filter(e => e.source_entity === nodeId || e.target_entity === nodeId).length;

  const nodesByType = nodes.reduce<Record<string, GraphNode[]>>((acc, n) => {
    const t = n.entity_type || "other";
    if (!acc[t]) acc[t] = [];
    acc[t].push(n);
    return acc;
  }, {});

  if (loading) {
    return <div className="p-8 text-center"><Loader2 className="h-5 w-5 animate-spin mx-auto text-muted-foreground" /></div>;
  }

  if (nodes.length === 0) {
    return (
      <Card>
        <CardContent className="py-12 flex flex-col items-center gap-4">
          <Network className="h-10 w-10 text-muted-foreground" />
          <p className="text-sm text-muted-foreground">
            知识库中暂无实体图谱数据。添加知识后 GraphRAG 会自动提取实体和关系。
          </p>
        </CardContent>
      </Card>
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <span className="text-sm text-muted-foreground">
            {nodes.length} 个实体 · {edges.length} 条关系
          </span>
          <select
            value={limit}
            onChange={(e) => { setLimit(Number(e.target.value)); }}
            className="text-xs border border-border/60 rounded-md px-2 py-1 bg-card"
          >
            <option value={30}>Top 30</option>
            <option value={50}>Top 50</option>
            <option value={100}>Top 100</option>
            <option value={200}>Top 200</option>
          </select>
        </div>
        <Button variant="outline" size="sm" onClick={loadGraph} disabled={loading}>
          <RefreshCw className={`h-4 w-4 mr-2 ${loading ? "animate-spin" : ""}`} /> 刷新
        </Button>
      </div>

      <Card>
        <CardContent className="p-4">
          <GraphVisualization nodes={nodes} edges={edges} onSelect={setSelectedEntity} selected={selectedEntity} />
        </CardContent>
      </Card>

      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
        {Object.entries(nodesByType).map(([type, typeNodes]) => (
          <Card key={type}>
            <CardContent className="p-4">
              <div className="flex items-center gap-2 mb-3">
                <div className="w-3 h-3 rounded-full" style={{ backgroundColor: ENTITY_COLORS[type] ?? "#6b7280" }} />
                <span className="text-sm font-medium capitalize">{type}</span>
                <Badge variant="outline" className="text-xs">{typeNodes.length}</Badge>
              </div>
              <div className="flex flex-wrap gap-1.5">
                {typeNodes.slice(0, 30).map((n) => {
                  const count = edgeCount(n.id);
                  return (
                    <button
                      key={n.id}
                      onClick={() => setSelectedEntity(selectedEntity?.id === n.id ? null : n)}
                      className={`text-xs px-2 py-1 rounded-md transition-ui ${
                        selectedEntity?.id === n.id
                          ? "bg-primary text-primary-foreground"
                          : "bg-muted text-muted-foreground hover:bg-accent"
                      }`}
                      title={`${n.entity_name} (${count} 条关系)`}
                    >
                      {n.entity_name}
                      {count > 0 && <span className="ml-1 opacity-50">({count})</span>}
                    </button>
                  );
                })}
              </div>
            </CardContent>
          </Card>
        ))}
      </div>

      {selectedEntity && (
        <Card className="anim-fade-in">
          <CardHeader>
            <CardTitle className="text-sm flex items-center gap-2">
              <div className="w-3 h-3 rounded-full" style={{ backgroundColor: ENTITY_COLORS[selectedEntity.entity_type] ?? "#6b7280" }} />
              {selectedEntity.entity_name}
              <Badge variant="outline" className="text-xs">{selectedEntity.entity_type}</Badge>
            </CardTitle>
          </CardHeader>
          <CardContent>
            <div className="space-y-2">
              <p className="text-xs text-muted-foreground">关联关系:</p>
              <div className="space-y-1">
                {edges
                  .filter(e => e.source_entity === selectedEntity.id || e.target_entity === selectedEntity.id)
                  .map((e, i) => {
                    const isSource = e.source_entity === selectedEntity.id;
                    const otherId = isSource ? e.target_entity : e.source_entity;
                    const otherNode = nodes.find(n => n.id === otherId);
                    return (
                      <div key={i} className="text-xs flex items-center gap-1.5">
                        {isSource ? (
                          <>
                            <span className="text-muted-foreground">→</span>
                            <Badge variant="outline" className="text-xs">{e.relation_type}</Badge>
                            <span className="text-muted-foreground">→</span>
                            <span className="font-medium">{otherNode?.entity_name ?? otherId.slice(0, 8)}</span>
                          </>
                        ) : (
                          <>
                            <span className="font-medium">{otherNode?.entity_name ?? otherId.slice(0, 8)}</span>
                            <span className="text-muted-foreground">→</span>
                            <Badge variant="outline" className="text-xs">{e.relation_type}</Badge>
                            <span className="text-muted-foreground">→</span>
                          </>
                        )}
                      </div>
                    );
                  })}
                {edges.filter(e => e.source_entity === selectedEntity.id || e.target_entity === selectedEntity.id).length === 0 && (
                  <p className="text-xs text-muted-foreground">无关联关系</p>
                )}
              </div>
            </div>
          </CardContent>
        </Card>
      )}
    </div>
  );
}

// ─── Force-directed Graph SVG ───────────────────────────

function GraphVisualization({
  nodes, edges, onSelect, selected,
}: {
  nodes: GraphNode[];
  edges: GraphEdge[];
  onSelect: (n: GraphNode | null) => void;
  selected: GraphNode | null;
}) {
  const W = 800;
  const H = 500;
  const NODE_R = 18;

  const positions = useMemo(() => {
    const cx = W / 2;
    const cy = H / 2;
    const radius = Math.min(W, H) / 2 - 50;
    return nodes.map((n, i) => {
      const angle = (i / Math.max(nodes.length, 1)) * Math.PI * 2;
      return { id: n.id, x: cx + radius * Math.cos(angle), y: cy + radius * Math.sin(angle) };
    });
  }, [nodes]);

  const getPos = (id: string) => positions.find(p => p.id === id) ?? { x: W / 2, y: H / 2 };

  return (
    <div className="overflow-x-auto">
      <svg viewBox={`0 0 ${W} ${H}`} className="w-full" style={{ minWidth: 600 }}>
        {edges.map((e, i) => {
          const p1 = getPos(e.source_entity);
          const p2 = getPos(e.target_entity);
          return (
            <line key={i} x1={p1.x} y1={p1.y} x2={p2.x} y2={p2.y}
              stroke="currentColor" strokeOpacity={0.15} strokeWidth={1} />
          );
        })}
        {nodes.map((n) => {
          const pos = getPos(n.id);
          const color = ENTITY_COLORS[n.entity_type] ?? "#6b7280";
          const isSelected = selected?.id === n.id;
          return (
            <g key={n.id} onClick={() => onSelect(selected?.id === n.id ? null : n)} style={{ cursor: "pointer" }}>
              <circle cx={pos.x} cy={pos.y} r={isSelected ? NODE_R + 3 : NODE_R}
                fill={color} fillOpacity={isSelected ? 1 : 0.7}
                stroke={isSelected ? "#000" : "none"} strokeWidth={2} />
              <text x={pos.x} y={pos.y + NODE_R + 12} textAnchor="middle"
                fontSize={9} fill="currentColor" fillOpacity={0.7}>
                {n.entity_name.length > 8 ? n.entity_name.slice(0, 7) + "…" : n.entity_name}
              </text>
            </g>
          );
        })}
      </svg>
    </div>
  );
}

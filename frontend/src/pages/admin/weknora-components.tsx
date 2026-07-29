/**
 * WeKnora 管理页面 — 子组件
 * 拆分为：未配置提示 / 添加知识面板 / 知识列表表格 / 检索面板
 */
import { useState } from "react";
import {
  Search, Plus, Trash2, Loader2, FileUp, Link2, FileText,
  RefreshCw, AlertCircle, CheckCircle,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  type WeKnoraKnowledge,
  type WeKnoraSearchResult,
} from "@/lib/weknora-api";

// ─── 未配置提示组件 ─────────────────────────────────────

export function WeKnoraNotConfigured({ onRetry }: { onRetry: () => void }) {
  return (
    <Card>
      <CardContent className="py-12 flex flex-col items-center gap-4">
        <AlertCircle className="h-10 w-10 text-amber-500" />
        <div className="text-center space-y-2">
          <p className="text-sm font-medium">WeKnora 未配置</p>
          <p className="text-xs text-muted-foreground max-w-md">
            请在 <code className="bg-muted px-1.5 py-0.5 rounded text-xs">.env.docker</code> 中设置{" "}
            <code className="bg-muted px-1.5 py-0.5 rounded text-xs">WEKNORA_ENABLED=true</code>、
            <code className="bg-muted px-1.5 py-0.5 rounded text-xs">WEKNORA_API_KEY</code> 和{" "}
            <code className="bg-muted px-1.5 py-0.5 rounded text-xs">WEKNORA_KB_ID</code>，
            或在「MCP 服务密钥」页面添加 provider 为{" "}
            <code className="bg-muted px-1.5 py-0.5 rounded text-xs">weknora</code> 的密钥。
          </p>
          <p className="text-xs text-muted-foreground">
            WeKnora 部署文档：{" "}
            <a href="https://github.com/Tencent/WeKnora" target="_blank" rel="noreferrer" className="text-blue-500 hover:underline">
              github.com/Tencent/WeKnora
            </a>
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

export function WeKnoraAddPanel({
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
                onClick={() => document.getElementById("weknora-file-input")?.click()}>
                <FileUp className="h-8 w-8 mx-auto text-muted-foreground" />
                <p className="text-sm mt-2 text-muted-foreground">
                  {file ? file.name : "点击选择文件"}
                </p>
                <p className="text-xs text-muted-foreground mt-1">支持 PDF / Word / Excel / PPT / 图片 / HTML / Markdown</p>
                <input id="weknora-file-input" type="file" className="hidden"
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
  ready: "bg-green-100 text-green-700",
  processing: "bg-blue-100 text-blue-700",
  failed: "bg-red-100 text-red-700",
};

export function WeKnoraKnowledgeTable({
  entries, total, page, loading, error,
  onPageChange, onRefresh, onDelete,
}: {
  entries: WeKnoraKnowledge[];
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
              <th className="text-left p-3 text-sm font-medium">标题</th>
              <th className="text-left p-3 text-sm font-medium">来源</th>
              <th className="text-left p-3 text-sm font-medium">状态</th>
              <th className="text-left p-3 text-sm font-medium">创建时间</th>
              <th className="text-right p-3 text-sm font-medium">操作</th>
            </tr>
          </thead>
          <tbody>
            {loading ? (
              <tr><td colSpan={5} className="p-8 text-center"><Loader2 className="h-4 w-4 animate-spin mx-auto" /></td></tr>
            ) : entries.length === 0 ? (
              <tr><td colSpan={5} className="p-8 text-center text-muted-foreground text-sm">暂无知识条目</td></tr>
            ) : (
              entries.map((entry) => (
                <tr key={entry.id} className="border-b last:border-0 hover:bg-accent/30">
                  <td className="p-3 text-sm font-medium">
                    <div className="flex items-center gap-2">
                      <FileText className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
                      <span className="truncate max-w-[300px]">{entry.title}</span>
                    </div>
                  </td>
                  <td className="p-3 text-sm text-muted-foreground">
                    {entry.source ? (
                      <a href={entry.source} target="_blank" rel="noreferrer"
                        className="text-blue-500 hover:underline truncate inline-block max-w-[200px]">
                        {entry.source}
                      </a>
                    ) : "—"}
                  </td>
                  <td className="p-3">
                    <Badge variant="outline" className={STATUS_COLORS[entry.status ?? ""] ?? "bg-gray-100 text-gray-500"}>
                      {entry.status || "unknown"}
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

// ─── 检索面板 ───────────────────────────────────────────

export function WeKnoraSearchPanel({
  onSearch,
}: {
  onSearch: (query: string) => Promise<WeKnoraSearchResult[]>;
}) {
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<WeKnoraSearchResult[]>([]);
  const [searching, setSearching] = useState(false);
  const [hasSearched, setHasSearched] = useState(false);

  const handleSearch = async () => {
    if (!query.trim()) return;
    setSearching(true);
    setHasSearched(true);
    try {
      setResults(await onSearch(query));
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
        <CardContent>
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
        </CardContent>
      </Card>

      {results.length > 0 && (
        <div className="space-y-3">
          {results.map((result, i) => (
            <Card key={i}>
              <CardContent className="p-4">
                <div className="flex items-start justify-between gap-3">
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2">
                      <h4 className="text-sm font-medium">{result.title}</h4>
                      <Badge variant="outline" className="text-xs">
                        score: {result.score.toFixed(4)}
                      </Badge>
                    </div>
                    <p className="text-sm text-muted-foreground mt-1 line-clamp-3">{result.snippet}</p>
                    {result.url && (
                      <a href={result.url} target="_blank" rel="noreferrer"
                        className="text-xs text-blue-500 hover:underline mt-2 inline-block">
                        {result.url}
                      </a>
                    )}
                  </div>
                  <Badge variant="outline" className="bg-purple-100 text-purple-700 shrink-0">
                    {result.source}
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

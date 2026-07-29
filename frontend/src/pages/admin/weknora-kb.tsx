/**
 * WeKnora 知识库管理 — Admin Dashboard 主页面
 * 组合子组件：未配置提示 / 添加知识面板 / 知识列表表格 / 检索面板
 */
import { useState, useEffect, useCallback } from "react";
import { Plus, Database, CheckCircle, BookOpen } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  type WeKnoraKnowledge,
  type WeKnoraKB,
  type WeKnoraSearchResult,
  checkConfig, search, listKnowledge,
  createKnowledge, createKnowledgeFromURL, uploadFile, deleteKnowledge,
} from "@/lib/weknora-api";
import {
  WeKnoraNotConfigured,
  WeKnoraAddPanel,
  WeKnoraKnowledgeTable,
  WeKnoraSearchPanel,
} from "./weknora-components";

export function WeKnoraKBPage() {
  const [activeTab, setActiveTab] = useState("knowledge");
  const [entries, setEntries] = useState<WeKnoraKnowledge[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [kbs, setKBs] = useState<WeKnoraKB[]>([]);
  const [configured, setConfigured] = useState<boolean | null>(null);
  const [showAdd, setShowAdd] = useState(false);

  // ─── Data Loading ──────────────────────────────────────

  const doCheckConfig = useCallback(async () => {
    const { configured, kbs } = await checkConfig();
    setConfigured(configured);
    setKBs(kbs);
  }, []);

  const loadEntries = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const { entries, total } = await listKnowledge(page, 20);
      setEntries(entries);
      setTotal(total);
    } catch (e) {
      setError(e instanceof Error ? e.message : "网络错误");
    } finally {
      setLoading(false);
    }
  }, [page]);

  useEffect(() => { doCheckConfig(); }, [doCheckConfig]);
  useEffect(() => { if (configured === true) loadEntries(); }, [configured, loadEntries]);

  // ─── Actions ───────────────────────────────────────────

  const handleAdd = async (
    mode: "text" | "url" | "file",
    data: { title: string; content: string; url: string; file: File | null },
  ) => {
    if (mode === "text") {
      await createKnowledge(data.title, data.content);
    } else if (mode === "url") {
      await createKnowledgeFromURL(data.url, data.title || undefined);
    } else if (mode === "file" && data.file) {
      await uploadFile(data.file, data.title || undefined);
    }
    setShowAdd(false);
    await loadEntries();
  };

  const handleDelete = async (id: string) => {
    if (!confirm("确定删除这条知识？")) return;
    await deleteKnowledge(id);
    await loadEntries();
  };

  const handleSearch = async (query: string): Promise<WeKnoraSearchResult[]> => {
    return search(query, 10);
  };

  // ─── Render ────────────────────────────────────────────

  if (configured === false) {
    return (
      <div className="p-6 space-y-6">
        <div className="flex items-center gap-2">
          <Database className="h-5 w-5" />
          <h2 className="text-xl font-semibold">WeKnora 知识库</h2>
        </div>
        <WeKnoraNotConfigured onRetry={doCheckConfig} />
      </div>
    );
  }

  return (
    <div className="p-6 space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <div className="flex items-center gap-2">
            <Database className="h-5 w-5" />
            <h2 className="text-xl font-semibold">WeKnora 知识库</h2>
            {configured && (
              <Badge className="bg-green-100 text-green-700">
                <CheckCircle className="h-3 w-3 mr-1" /> 已连接
              </Badge>
            )}
          </div>
          <p className="text-sm text-muted-foreground mt-1">
            混合检索（BM25 + Dense + GraphRAG）· 多格式文档解析 · 知识管理
          </p>
        </div>
        <Button size="sm" onClick={() => setShowAdd(!showAdd)}>
          <Plus className="h-4 w-4 mr-2" /> 添加知识
        </Button>
      </div>

      {/* KB Info Cards */}
      {kbs.length > 0 && (
        <div className="grid grid-cols-3 gap-4">
          {kbs.slice(0, 3).map((kb) => (
            <Card key={kb.id}>
              <CardContent className="p-4">
                <div className="flex items-center gap-2">
                  <BookOpen className="h-4 w-4 text-muted-foreground" />
                  <span className="text-sm font-medium truncate">{kb.name}</span>
                </div>
                <div className="flex items-center gap-3 mt-2 text-xs text-muted-foreground">
                  <span>{kb.document_count ?? 0} 条知识</span>
                  <span className="font-mono">{kb.id.slice(0, 12)}...</span>
                </div>
              </CardContent>
            </Card>
          ))}
        </div>
      )}

      {/* Add Panel */}
      {showAdd && (
        <WeKnoraAddPanel onAdd={handleAdd} onCancel={() => setShowAdd(false)} />
      )}

      {/* Main Tabs */}
      <Tabs value={activeTab} onValueChange={setActiveTab}>
        <TabsList>
          <TabsTrigger value="knowledge">知识列表</TabsTrigger>
          <TabsTrigger value="search">混合检索</TabsTrigger>
        </TabsList>

        <TabsContent value="knowledge">
          <WeKnoraKnowledgeTable
            entries={entries}
            total={total}
            page={page}
            loading={loading}
            error={error}
            onPageChange={setPage}
            onRefresh={loadEntries}
            onDelete={handleDelete}
          />
        </TabsContent>

        <TabsContent value="search">
          <WeKnoraSearchPanel onSearch={handleSearch} />
        </TabsContent>
      </Tabs>
    </div>
  );
}

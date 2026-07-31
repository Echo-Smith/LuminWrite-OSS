/**
 * 知识库管理 — Admin Dashboard 主页面
 * 组合子组件：未配置提示 / 添加知识面板 / 知识列表表格 / 检索面板 / 系统信息
 */
import { useState, useEffect, useCallback } from "react";
import { Plus, Database, CheckCircle, RefreshCw, FolderPlus, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  type KnowledgeDoc,
  type KBInfo,
  type KBSearchResult,
  checkConfig, search, listKnowledge,
  createKnowledge, createKnowledgeFromURL, uploadFile, deleteKnowledge,
  getStatus, listKBs, createKB, deleteKB,
} from "@/lib/kb-api";
import {
  KBNotConfigured,
  KBAddPanel,
  KnowledgeTable,
  KBSearchPanel,
  KBStatsPanel,
  KBGraphPanel,
} from "./components";

export function KnowledgeBasePage() {
  const [activeTab, setActiveTab] = useState("knowledge");
  const [entries, setEntries] = useState<KnowledgeDoc[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [kbs, setKBs] = useState<KBInfo[]>([]);
  const [selectedKB, setSelectedKB] = useState<string>("default");
  const [configured, setConfigured] = useState<boolean | null>(null);
  const [showAdd, setShowAdd] = useState(false);
  const [localKb, setLocalKb] = useState(false);
  const [showCreateKB, setShowCreateKB] = useState(false);
  const [newKBName, setNewKBName] = useState("");
  const [newKBDesc, setNewKBDesc] = useState("");

  // ─── Data Loading ──────────────────────────────────────

  const doCheckConfig = useCallback(async () => {
    const { configured } = await checkConfig();
    setConfigured(configured);

    // Load KB list
    try {
      const kbList = await listKBs();
      setKBs(kbList);
    } catch {
      // ignore
    }

    try {
      const status = await getStatus();
      setLocalKb(status.local_kb || status.enabled || false);
    } catch {
      // ignore
    }
  }, []);

  const loadEntries = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const { entries, total } = await listKnowledge(page, 20, selectedKB);
      setEntries(entries);
      setTotal(total);
    } catch (e) {
      setError(e instanceof Error ? e.message : "网络错误");
    } finally {
      setLoading(false);
    }
  }, [page, selectedKB]);

  useEffect(() => { doCheckConfig(); }, [doCheckConfig]);
  useEffect(() => { if (configured === true) loadEntries(); }, [configured, loadEntries]);

  // ─── Actions ───────────────────────────────────────────

  const handleAdd = async (
    mode: "text" | "url" | "file",
    data: { title: string; content: string; url: string; file: File | null },
  ) => {
    if (mode === "text") {
      await createKnowledge(data.title, data.content, selectedKB);
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

  const handleSearch = async (query: string): Promise<KBSearchResult[]> => {
    return search(query, 10);
  };

  const handleCreateKB = async () => {
    if (!newKBName.trim()) return;
    try {
      await createKB(newKBName.trim(), newKBDesc.trim());
      const kbList = await listKBs();
      setKBs(kbList);
      setNewKBName("");
      setNewKBDesc("");
      setShowCreateKB(false);
    } catch (e) {
      alert("创建失败: " + (e instanceof Error ? e.message : "未知错误"));
    }
  };

  const handleDeleteKB = async (kbId: string) => {
    if (kbId === "default") return;
    if (!confirm(`确定删除知识库「${kbs.find(k => k.id === kbId)?.name}」及其所有文档？`)) return;
    try {
      await deleteKB(kbId);
      setSelectedKB("default");
      const kbList = await listKBs();
      setKBs(kbList);
    } catch (e) {
      alert("删除失败: " + (e instanceof Error ? e.message : "未知错误"));
    }
  };

  // ─── Render ────────────────────────────────────────────

  if (configured === false) {
    return (
      <div className="p-6 space-y-6">
        <div className="flex items-center gap-2">
          <Database className="h-5 w-5" />
          <h2 className="text-xl font-semibold">知识库</h2>
        </div>
        <KBNotConfigured onRetry={doCheckConfig} />
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
            <h2 className="text-xl font-semibold">知识库</h2>
            {configured && (
              <Badge className="bg-green-100 text-green-700">
                <CheckCircle className="h-3 w-3 mr-1" /> 已连接
              </Badge>
            )}
            {localKb && (
              <Badge className="bg-blue-100 text-blue-700">本地引擎</Badge>
            )}
          </div>
          <p className="text-sm text-muted-foreground mt-1">
            混合检索（BM25 + Dense + GraphRAG）· 多知识库 · 多格式文档解析
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button size="sm" variant="outline" onClick={doCheckConfig} title="刷新状态">
            <RefreshCw className="h-4 w-4" />
          </Button>
          <Button size="sm" onClick={() => setShowAdd(!showAdd)}>
            <Plus className="h-4 w-4 mr-2" /> 添加知识
          </Button>
        </div>
      </div>

      {/* KB Selector */}
      <div className="flex items-center gap-3">
        <span className="text-sm text-muted-foreground shrink-0">当前知识库:</span>
        <select
          value={selectedKB}
          onChange={(e) => { setSelectedKB(e.target.value); setPage(1); }}
          className="text-sm border border-border/60 rounded-md px-3 py-1.5 bg-card min-w-[200px]"
        >
          {kbs.map((kb) => (
            <option key={kb.id} value={kb.id}>
              {kb.name} ({kb.document_count ?? 0} 文档)
            </option>
          ))}
        </select>
        <Button size="sm" variant="outline" onClick={() => setShowCreateKB(!showCreateKB)}>
          <FolderPlus className="h-4 w-4 mr-1" /> 新建
        </Button>
        {selectedKB !== "default" && (
          <Button size="sm" variant="outline" onClick={() => handleDeleteKB(selectedKB)} className="text-red-500 hover:text-red-600">
            <Trash2 className="h-4 w-4 mr-1" /> 删除
          </Button>
        )}
      </div>

      {/* Create KB Panel */}
      {showCreateKB && (
        <Card className="anim-fade-in">
          <CardContent className="p-4 space-y-3">
            <div className="flex items-center gap-2">
              <FolderPlus className="h-4 w-4" />
              <h3 className="text-sm font-semibold">新建知识库</h3>
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div>
                <Label>名称</Label>
                <Input value={newKBName} onChange={(e) => setNewKBName(e.target.value)} placeholder="如：写作风格素材" />
              </div>
              <div>
                <Label>描述（可选）</Label>
                <Input value={newKBDesc} onChange={(e) => setNewKBDesc(e.target.value)} placeholder="知识库用途说明" />
              </div>
            </div>
            <div className="flex justify-end gap-2">
              <Button variant="outline" size="sm" onClick={() => setShowCreateKB(false)}>取消</Button>
              <Button size="sm" onClick={handleCreateKB} disabled={!newKBName.trim()}>
                <Plus className="h-4 w-4 mr-1" /> 创建
              </Button>
            </div>
          </CardContent>
        </Card>
      )}

      {/* Add Panel */}
      {showAdd && (
        <KBAddPanel onAdd={handleAdd} onCancel={() => setShowAdd(false)} />
      )}

      {/* Main Tabs */}
      <Tabs value={activeTab} onValueChange={setActiveTab}>
        <TabsList>
          <TabsTrigger value="knowledge">知识列表</TabsTrigger>
          <TabsTrigger value="search">混合检索</TabsTrigger>
          <TabsTrigger value="graph">实体图谱</TabsTrigger>
          <TabsTrigger value="system">统计仪表盘</TabsTrigger>
        </TabsList>

        <TabsContent value="knowledge">
          <KnowledgeTable
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
          <KBSearchPanel onSearch={handleSearch} kbId={selectedKB} />
        </TabsContent>

        <TabsContent value="graph">
          <KBGraphPanel />
        </TabsContent>

        <TabsContent value="system">
          <KBStatsPanel kbId={selectedKB} />
        </TabsContent>
      </Tabs>
    </div>
  );
}

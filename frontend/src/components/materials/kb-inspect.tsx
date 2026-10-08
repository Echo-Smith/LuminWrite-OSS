/**
 * KB Inspect — 素材中心的检索调试与文档分块查看（KB 合并 Phase 3-3）
 *
 * 从 admin 知识库页下沉的用户侧能力：
 * - 检索调试：BM25/Dense/Hybrid 模式与权重调参（数据同一条 HybridSearch 链路）
 * - 分块/实体：查看素材文档的分块与抽取实体
 * 数据经 kb-api（/api/v2/kb/*，JWT 归属过滤：自己的 + 全局共享）。
 */
import { useState } from "react";
import { Loader2, Search, Layers, Boxes } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import {
  searchWithMode, getDocumentChunks, getDocumentEntities,
  type SearchMode, type KBSearchResult, type KBChunk, type KBEntity,
} from "@/lib/kb-api";

const MODE_LABELS: Record<SearchMode, string> = {
  hybrid: "混合",
  bm25: "BM25",
  dense: "语义",
};

// ─── 检索调试面板 ──────────────────────────────────────────

export function KbSearchDebug() {
  const [query, setQuery] = useState("");
  const [mode, setMode] = useState<SearchMode>("hybrid");
  const [bm25Weight, setBm25Weight] = useState(0.3);
  const [denseWeight, setDenseWeight] = useState(0.5);
  const [results, setResults] = useState<KBSearchResult[] | null>(null);
  const [searching, setSearching] = useState(false);
  const [effectiveMode, setEffectiveMode] = useState("");

  const run = async () => {
    if (!query.trim()) return;
    setSearching(true);
    try {
      const r = await searchWithMode(query.trim(), {
        limit: 10,
        mode,
        bm25Weight,
        denseWeight,
      });
      setResults(r.results);
      setEffectiveMode(r.mode);
    } catch {
      setResults([]);
    } finally {
      setSearching(false);
    }
  };

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2 text-xs text-muted-foreground">
        <Search className="h-3.5 w-3.5" />
        检索调试 — 与写作管线同一条混合检索链路（自己的素材 + 共享语料）
      </div>

      <div className="flex gap-2">
        <Input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="输入测试查询..."
          className="text-sm"
          onKeyDown={(e) => { if (e.key === "Enter") void run(); }}
        />
        <Button size="sm" onClick={() => void run()} disabled={searching || !query.trim()}>
          {searching ? <Loader2 className="h-4 w-4 animate-spin" /> : "检索"}
        </Button>
      </div>

      <div className="flex items-center gap-3 flex-wrap text-xs">
        <div className="flex gap-1">
          {(Object.keys(MODE_LABELS) as SearchMode[]).map((m) => (
            <button
              key={m}
              onClick={() => setMode(m)}
              className={`rounded-lg px-2.5 py-1 transition-ui ${
                mode === m ? "bg-accent text-foreground font-medium" : "text-muted-foreground hover:bg-accent/50"
              }`}
            >
              {MODE_LABELS[m]}
            </button>
          ))}
        </div>
        {mode !== "bm25" && (
          <label className="flex items-center gap-1.5">
            <span className="text-muted-foreground">语义权重</span>
            <input
              type="range" min={0} max={1} step={0.1} value={denseWeight}
              onChange={(e) => setDenseWeight(Number(e.target.value))}
              className="w-20 accent-primary"
            />
            <span className="w-6 tabular-nums">{denseWeight.toFixed(1)}</span>
          </label>
        )}
        {mode !== "dense" && (
          <label className="flex items-center gap-1.5">
            <span className="text-muted-foreground">关键词权重</span>
            <input
              type="range" min={0} max={1} step={0.1} value={bm25Weight}
              onChange={(e) => setBm25Weight(Number(e.target.value))}
              className="w-20 accent-primary"
            />
            <span className="w-6 tabular-nums">{bm25Weight.toFixed(1)}</span>
          </label>
        )}
      </div>

      {results !== null && (
        <div className="space-y-1.5">
          {results.length === 0 ? (
            <p className="text-xs text-muted-foreground py-2 text-center">无命中结果</p>
          ) : (
            results.map((r, i) => (
              <div key={r.chunk_id || i} className="rounded-lg border p-2.5 space-y-1">
                <div className="flex items-center gap-2 text-xs">
                  <Badge variant="secondary" className="text-[10px]">{r.score.toFixed(3)}</Badge>
                  <span className="font-medium truncate">{r.title}</span>
                  {effectiveMode && <span className="text-muted-foreground/60 ml-auto">{effectiveMode}</span>}
                </div>
                <p className="text-[11px] text-muted-foreground line-clamp-2">{r.content}</p>
              </div>
            ))
          )}
        </div>
      )}
    </div>
  );
}

// ─── 分块 / 实体查看弹窗 ────────────────────────────────────

export function KbInspectDialog({
  docId,
  title,
  open,
  onOpenChange,
}: {
  docId: string;
  title: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const [view, setView] = useState<"chunks" | "entities">("chunks");
  const [chunks, setChunks] = useState<KBChunk[] | null>(null);
  const [entities, setEntities] = useState<KBEntity[] | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  const load = async () => {
    setLoading(true);
    setError("");
    try {
      if (view === "chunks") {
        setChunks(await getDocumentChunks(docId));
      } else {
        const r = await getDocumentEntities(docId);
        setEntities(r.entities);
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : "加载失败");
    } finally {
      setLoading(false);
    }
  };

  const handleOpen = (next: boolean) => {
    if (next) {
      setChunks(null);
      setEntities(null);
      void load();
    }
    onOpenChange(next);
  };

  const switchView = (v: "chunks" | "entities") => {
    setView(v);
    if (v === "entities" && entities === null) void load();
    if (v === "chunks" && chunks === null) void load();
  };

  return (
    <Dialog open={open} onOpenChange={handleOpen}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle className="text-base truncate pr-6">{title}</DialogTitle>
        </DialogHeader>

        <div className="flex gap-1.5">
          {([["chunks", "分块", Layers], ["entities", "实体", Boxes]] as const).map(([key, label, Icon]) => (
            <button
              key={key}
              onClick={() => switchView(key)}
              className={`flex items-center gap-1.5 rounded-lg px-2.5 py-1 text-xs transition-ui ${
                view === key ? "bg-accent text-foreground font-medium" : "text-muted-foreground hover:bg-accent/50"
              }`}
            >
              <Icon className="h-3.5 w-3.5" /> {label}
            </button>
          ))}
        </div>

        {loading ? (
          <div className="py-8 text-center">
            <Loader2 className="h-5 w-5 animate-spin mx-auto text-muted-foreground" />
          </div>
        ) : error ? (
          <p className="py-6 text-center text-xs text-muted-foreground">{error}</p>
        ) : view === "chunks" ? (
          <div className="max-h-80 overflow-y-auto space-y-2 pr-1">
            {(chunks ?? []).length === 0 ? (
              <p className="py-6 text-center text-xs text-muted-foreground">该文档暂无分块</p>
            ) : (
              (chunks ?? []).map((c) => (
                <div key={c.id} className="rounded-lg border p-2.5">
                  <div className="flex items-center gap-2 text-[11px] text-muted-foreground">
                    <Badge variant="secondary" className="text-[10px]">#{c.chunk_index}</Badge>
                    {c.title && <span className="truncate">{c.title}</span>}
                    {c.has_embedding && (
                      <Badge variant="outline" className="ml-auto text-[9px] text-green-600 border-green-300">已向量化</Badge>
                    )}
                  </div>
                  <p className="mt-1 text-xs text-muted-foreground line-clamp-3">{c.content}</p>
                </div>
              ))
            )}
          </div>
        ) : (
          <div className="max-h-80 overflow-y-auto space-y-1.5 pr-1">
            {(entities ?? []).length === 0 ? (
              <p className="py-6 text-center text-xs text-muted-foreground">
                {entities === null ? "加载中" : "该文档暂无抽取实体（GraphRAG 实体抽取按需生成）"}
              </p>
            ) : (
              (entities ?? []).map((e) => (
                <div key={e.id} className="flex items-center gap-2 text-xs rounded-lg border p-2">
                  <Badge variant="secondary" className="text-[10px]">{e.entity_type}</Badge>
                  <span className="font-medium">{e.entity_name}</span>
                </div>
              ))
            )}
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}

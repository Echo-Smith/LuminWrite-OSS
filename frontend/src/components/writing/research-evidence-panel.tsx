/**
 * 来源卡面板 — 区分「仅元数据 / 已读摘要 / 已读全文」（reading_scope +
 * acquisition.status），展示选中/待补充/未评分原因、possible_duplicate 提示，
 * 点击可查看引用的原文摘录（quote/页码/block，来自 GET artifact content）。
 */
import { BookOpen, BookOpenCheck, ChevronRight, Copy, FileQuestion, Loader2, ScrollText } from "lucide-react";
import { useCallback, useState } from "react";
import { Badge } from "@/components/ui/badge";
import {
  fetchEvidencePack,
  fetchRunArtifactContent,
  isResearchApiError,
  type EvidenceItem,
  type PaperEvidence,
  type ResearchEvidencePack,
} from "@/lib/research-api";
import { cn } from "@/lib/utils";

interface ParsedBlock { block_id: string; text: string; page?: number }

const SCOPE_LABELS: Record<PaperEvidence["reading_scope"], { label: string; tone: "metadata" | "abstract" | "full" }> = {
  unread: { label: "仅元数据（未读）", tone: "metadata" },
  abstract: { label: "已读摘要", tone: "abstract" },
  full_text: { label: "已读全文", tone: "full" },
};

function acquisitionLabel(paper: PaperEvidence): string | null {
  switch (paper.acquisition_status) {
    case "full_text_available": return "全文已获取";
    case "abstract_available": return "仅获取摘要";
    case "metadata_only": return "仅元数据";
    case "failed": return "全文获取失败";
    default: return null;
  }
}

function ReadingScopeBadge({ paper }: { paper: PaperEvidence }) {
  const scope = SCOPE_LABELS[paper.reading_scope];
  const toneClass = scope.tone === "full"
    ? "border-emerald-300 text-emerald-700 dark:text-emerald-400"
    : scope.tone === "abstract"
      ? "border-amber-300 text-amber-700 dark:text-amber-400"
      : "border-border text-muted-foreground";
  return <Badge variant="outline" className={cn("text-[10px]", toneClass)}>{scope.label}</Badge>;
}

function EvidenceQuotes({ evidence, blocks, scope }: { evidence: EvidenceItem[]; blocks: ParsedBlock[] | null; scope: PaperEvidence["reading_scope"] }) {
  if (evidence.length === 0) {
    return <p className="research-paper-evidence-empty">{scope === "unread" ? "未读来源不能产生可引用证据。" : "暂无结构化摘录。"}</p>;
  }
  return (
    <ul className="research-paper-evidence">
      {evidence.map((item) => {
        const block = blocks?.find((candidate) => candidate.block_id === item.block_id);
        const located = block?.text?.slice(item.start_char, item.end_char) ?? item.quote;
        return (
          <li key={item.evidence_id}>
            <blockquote>「{located}」</blockquote>
            <footer>
              <span>{item.evidence_scope === "abstract" ? "摘要证据" : "全文证据"}</span>
              <span>{item.page !== null ? `第 ${item.page} 页` : "无页码"}</span>
              <code>{item.block_id}</code>
              <span>偏移 {item.start_char}–{item.end_char}</span>
              {scope === "abstract" && <Badge variant="outline" className="border-amber-300 text-[10px] text-amber-700 dark:text-amber-400">摘要证据：不能支撑定量结论</Badge>}
            </footer>
          </li>
        );
      })}
    </ul>
  );
}

interface PaperCardProps {
  paper: PaperEvidence;
  evidence: EvidenceItem[];
  runId: string;
}

function PaperCard({ paper, evidence, runId }: PaperCardProps) {
  const [open, setOpen] = useState(false);
  const [blocks, setBlocks] = useState<ParsedBlock[] | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const toggle = useCallback(async () => {
    const next = !open;
    setOpen(next);
    if (!next || blocks || loading) return;
    const parsedRef = paper.parsed_document_ref ?? paper.document_ref;
    if (!parsedRef) {
      setError("该来源尚未生成可读内容产物");
      return;
    }
    setLoading(true);
    setError(null);
    try {
      const content = await fetchRunArtifactContent(runId, parsedRef.artifact_id) as { blocks?: ParsedBlock[] };
      setBlocks(content?.blocks ?? []);
    } catch (cause) {
      setError(isResearchApiError(cause) && cause.code === "WRITING_RESOURCE_NOT_FOUND" ? "内容产物不在本运行授权范围内" : "摘录内容加载失败，请稍后重试");
    } finally {
      setLoading(false);
    }
  }, [open, blocks, loading, paper, runId]);

  const acquisition = acquisitionLabel(paper);
  const readRatio = paper.total_blocks > 0 ? `${paper.read_block_ids.length}/${paper.total_blocks} 块` : null;

  return (
    <article className={cn("research-paper-card", paper.reading_scope === "unread" && "research-paper-card-unread")} data-paper-id={paper.paper_id}>
      <header className="research-paper-header">
        <button className="research-paper-toggle" onClick={() => void toggle()} aria-expanded={open} aria-label={`查看 ${paper.bibliography.title} 的原文摘录`}>
          {paper.reading_scope === "full_text" ? <BookOpenCheck className="h-4 w-4 text-emerald-600 dark:text-emerald-400" />
            : paper.reading_scope === "abstract" ? <BookOpen className="h-4 w-4 text-amber-600 dark:text-amber-400" />
              : <FileQuestion className="h-4 w-4 text-muted-foreground" />}
          <span className="min-w-0 flex-1 text-left">
            <strong className="block truncate text-sm">{paper.bibliography.title}</strong>
            <small className="block truncate text-xs text-muted-foreground">
              {paper.bibliography.authors.join("、")}{paper.bibliography.year ? ` · ${paper.bibliography.year}` : ""}{paper.bibliography.venue ? ` · ${paper.bibliography.venue}` : ""}
            </small>
          </span>
          <ChevronRight className={cn("h-4 w-4 shrink-0 text-muted-foreground transition-transform", open && "rotate-90")} />
        </button>
        <div className="research-paper-badges">
          <ReadingScopeBadge paper={paper} />
          {paper.origin === "user_material" && <Badge variant="outline" className="text-[10px]">用户材料</Badge>}
        </div>
      </header>

      <div className="research-paper-reasons">
        <p><span>选择原因：</span>{paper.selection_reason}</p>
        {paper.relevance_status === "unscored" && (
          <p className="text-amber-700 dark:text-amber-400"><span>未评分：</span>相关性模型未给出可信评分，按降级策略处理，不视为不相关。</p>
        )}
        {acquisition && <p><span>获取状态：</span>{acquisition}</p>}
        {readRatio && paper.reading_scope === "full_text" && <p><span>阅读覆盖：</span>{readRatio}{paper.truncated ? "（截断阅读，未逐页穷尽）" : ""}</p>}
        {paper.possible_duplicate_of && (
          <p className="text-amber-700 dark:text-amber-400" data-testid="duplicate-hint"><Copy className="mr-1 inline h-3 w-3" />疑似 {paper.possible_duplicate_of} 的重复记录：题名与作者一致但 DOI 不同，已保留独立记录。</p>
        )}
      </div>

      {open && (
        <div className="research-paper-detail">
          {loading && <p className="flex items-center gap-2 text-xs text-muted-foreground"><Loader2 className="h-3.5 w-3.5 animate-spin" />正在加载原文摘录…</p>}
          {error && <p className="text-xs text-destructive" role="alert">{error}</p>}
          {!loading && !error && <EvidenceQuotes evidence={evidence} blocks={blocks} scope={paper.reading_scope} />}
        </div>
      )}
    </article>
  );
}

interface ResearchEvidencePanelProps {
  runId: string;
  packRef: { artifact_id: string; version: number; content_hash: string } | null;
  token?: string;
}

export function ResearchEvidencePanel({ runId, packRef }: ResearchEvidencePanelProps) {
  const [pack, setPack] = useState<ResearchEvidencePack | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [loadedRef, setLoadedRef] = useState<string | null>(null);

  const load = useCallback(async () => {
    if (!packRef || loading) return;
    setLoading(true);
    setError(null);
    try {
      const content = await fetchEvidencePack(runId, packRef);
      setPack(content);
      setLoadedRef(packRef.artifact_id);
    } catch (cause) {
      setError(isResearchApiError(cause) ? cause.message : "证据包加载失败，请稍后重试");
    } finally {
      setLoading(false);
    }
  }, [packRef, runId, loading]);

  const needsLoad = packRef && loadedRef !== packRef.artifact_id && !loading && !error;

  if (needsLoad && !pack) {
    void load();
  }

  if (!packRef) {
    return <p className="research-evidence-empty">证据包尚未冻结；全部阅读任务完成后会显示可用文献、覆盖与缺口。</p>;
  }
  if (loading && !pack) return <p className="flex items-center gap-2 text-xs text-muted-foreground"><Loader2 className="h-3.5 w-3.5 animate-spin" />正在加载证据包…</p>;
  if (error) return <p className="text-xs text-destructive" role="alert">{error}</p>;
  if (!pack) return null;

  const fullTextCount = pack.papers.filter((paper) => paper.reading_scope === "full_text").length;
  const abstractCount = pack.papers.filter((paper) => paper.reading_scope === "abstract").length;
  const selected = pack.papers.filter((paper) => paper.selection_reason).length;

  return (
    <section className="research-evidence" aria-label="来源与证据包">
      <header className="research-evidence-header">
        <span className="flex items-center gap-2 text-sm font-semibold"><ScrollText className="h-4 w-4" />来源与证据包</span>
        <span className="text-xs text-muted-foreground">文献 {pack.papers.length} · 全文 {fullTextCount} · 摘要 {abstractCount} · 入选 {selected}</span>
      </header>

      <div className="research-coverage" data-testid="research-coverage">
        <div><h4>主题覆盖</h4><ul>{pack.coverage.topics.map((topic) => <li key={topic}>{topic}</li>)}</ul></div>
        <div><h4>缺口</h4><ul>{pack.coverage.gaps.map((gap) => <li key={gap}>{gap}</li>)}</ul></div>
        <div><h4>矛盾</h4><ul>{pack.coverage.contradictions.map((item) => <li key={item}>{item}</li>)}</ul></div>
      </div>

      <div className="research-paper-list">
        {pack.papers.map((paper) => (
          <PaperCard
            key={paper.paper_id}
            paper={paper}
            evidence={pack.evidence.filter((item) => item.paper_id === paper.paper_id)}
            runId={runId}
          />
        ))}
      </div>
    </section>
  );
}

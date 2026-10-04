/**
 * 写作记录子页面 — 个人维度（单用户化 Phase 2，docs/31）
 *
 * 两个 tab：记录（会话列表 + 详情回放）与 我的反馈（只读历史）。
 * 数据全部来自用户态 API（/api/v2/sessions、/api/v2/feedback/mine），
 * 与 admin trace-history（全站聚合）对位并存。
 */
import { useEffect, useState } from "react";
import {
  History, Star, MessageSquareQuote, FileText, ChevronLeft, ChevronRight,
  CheckCircle2, XCircle, Loader2, Clock, Ban,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { useAuthStore } from "@/stores/auth-store";
import { cn } from "@/lib/utils";
import { SimpleModal, formatDate } from "./shared";
import {
  type HistorySession, type HistoryDetail, type MyFeedbackRow,
  listMySessions, getMySessionDetail, listMyFeedback,
  HISTORY_STATUS_LABELS, HISTORY_STEP_LABELS, HISTORY_SCORE_LABELS,
  FEEDBACK_TYPE_LABELS, SEGMENT_TYPE_LABELS, displayTitle, formatDuration,
} from "@/lib/personal-history-api";

const PAGE_SIZE = 20;

function StatusBadge({ status }: { status: string }) {
  const label = HISTORY_STATUS_LABELS[status] ?? status;
  const cls =
    status === "completed" ? "bg-green-100 text-green-700 dark:bg-green-950 dark:text-green-300"
    : status === "running" ? "bg-blue-100 text-blue-700 dark:bg-blue-950 dark:text-blue-300"
    : status === "failed" ? "bg-red-100 text-red-700 dark:bg-red-950 dark:text-red-300"
    : status === "paused" ? "bg-yellow-100 text-yellow-700 dark:bg-yellow-950 dark:text-yellow-300"
    : "bg-muted text-muted-foreground";
  return <span className={cn("rounded-full px-2 py-0.5 text-[11px] font-medium", cls)}>{label}</span>;
}

function ScoreBadge({ score }: { score?: number }) {
  if (score === undefined || score === null) return <span className="text-xs text-muted-foreground">—</span>;
  const pct = Math.round(score * 100);
  const color = pct >= 80 ? "text-green-600" : pct >= 60 ? "text-amber-600" : "text-red-600";
  return <span className={cn("text-xs font-semibold", color)}>{pct}</span>;
}

export function HistorySection() {
  const isGuest = useAuthStore((s) => s.user?.role === "guest");
  const [tab, setTab] = useState<"records" | "feedback">("records");

  // 记录 tab
  const [sessions, setSessions] = useState<HistorySession[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [showArchived, setShowArchived] = useState(false);
  const [loading, setLoading] = useState(true);
  const [detail, setDetail] = useState<HistoryDetail | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);

  // 反馈 tab
  const [feedback, setFeedback] = useState<MyFeedbackRow[]>([]);
  const [feedbackLoading, setFeedbackLoading] = useState(false);

  const loadSessions = async (p: number, archived: boolean) => {
    setLoading(true);
    try {
      const data = await listMySessions(p, PAGE_SIZE, archived ? true : undefined);
      setSessions(data.sessions ?? []);
      setTotal(data.total ?? 0);
    } catch {
      setSessions([]);
      setTotal(0);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (isGuest) return;
    loadSessions(page, showArchived);
  }, [page, showArchived, isGuest]); // eslint-disable-line react-hooks/exhaustive-deps

  const loadFeedback = async () => {
    setFeedbackLoading(true);
    try {
      const data = await listMyFeedback();
      setFeedback(data.feedback ?? []);
    } catch {
      setFeedback([]);
    } finally {
      setFeedbackLoading(false);
    }
  };

  useEffect(() => {
    if (isGuest || tab !== "feedback") return;
    if (feedback.length === 0) loadFeedback();
  }, [tab, isGuest]); // eslint-disable-line react-hooks/exhaustive-deps

  const openDetail = async (traceId: string) => {
    setDetailLoading(true);
    try {
      setDetail(await getMySessionDetail(traceId));
    } catch {
      setDetail(null);
    } finally {
      setDetailLoading(false);
    }
  };

  if (isGuest) {
    return (
      <div className="px-6 pt-6 pb-12 space-y-6">
        <Card className="border-amber-200/60 bg-amber-50/50 dark:bg-amber-950/20">
          <CardContent className="py-6 text-center">
            <History className="mx-auto h-10 w-10 text-amber-500/50" />
            <p className="mt-3 text-sm text-amber-900 dark:text-amber-200 font-medium">
              游客模式无法查看写作记录
            </p>
            <p className="mt-1 text-xs text-amber-700 dark:text-amber-400">
              注册后可回看全部写作历史、评分与自己的反馈
            </p>
          </CardContent>
        </Card>
      </div>
    );
  }

  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  return (
    <div className="px-6 pt-4 pb-12 space-y-4">
      {/* Tab 切换 */}
      <div className="flex gap-1.5">
        {([["records", "写作记录", History], ["feedback", "我的反馈", MessageSquareQuote]] as const).map(([key, label, Icon]) => (
          <button
            key={key}
            onClick={() => setTab(key)}
            className={cn(
              "flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs transition-ui",
              tab === key ? "bg-accent text-foreground font-medium" : "text-muted-foreground hover:bg-accent/50"
            )}
          >
            <Icon className="h-3.5 w-3.5" /> {label}
          </button>
        ))}
      </div>

      {tab === "records" && (
        <>
          {/* 归档筛选 */}
          <div className="flex items-center justify-between">
            <p className="text-xs text-muted-foreground">共 {total} 条记录</p>
            <button
              onClick={() => { setPage(1); setShowArchived((v) => !v); }}
              className={cn(
                "rounded-lg px-2.5 py-1 text-xs transition-ui",
                showArchived ? "bg-accent text-foreground font-medium" : "text-muted-foreground hover:bg-accent/50"
              )}
            >
              {showArchived ? "查看未归档" : "查看已归档"}
            </button>
          </div>

          {loading ? (
            <div className="py-12 text-center text-muted-foreground text-sm">加载中...</div>
          ) : sessions.length === 0 ? (
            <div className="py-10 text-center">
              <FileText className="mx-auto h-12 w-12 text-muted-foreground/30" />
              <p className="mt-3 text-sm text-muted-foreground">
                {showArchived ? "还没有已归档的写作记录" : "还没有写作记录，完成一次写作后这里会出现历史"}
              </p>
            </div>
          ) : (
            <div className="space-y-2">
              {sessions.map((s) => (
                <Card key={s.trace_id} className="overflow-hidden cursor-pointer hover:border-primary/30 transition-colors"
                      onClick={() => void openDetail(s.trace_id)}>
                  <CardContent className="flex items-center gap-3 py-3">
                    <div className="flex-1 min-w-0">
                      <div className="flex items-center gap-2 flex-wrap">
                        <span className="text-sm font-medium truncate">{displayTitle(s)}</span>
                        <StatusBadge status={s.status} />
                        {s.has_feedback && (
                          <Badge variant="outline" className="text-[10px] text-blue-600 border-blue-300">已反馈</Badge>
                        )}
                      </div>
                      <div className="mt-1 flex items-center gap-3 text-xs text-muted-foreground">
                        <span>{formatDate(s.created_at)}</span>
                        <span>耗时 {formatDuration(s.duration_ms)}</span>
                        {s.style_slug && s.style_slug !== "default" && <span>{s.style_slug}</span>}
                      </div>
                    </div>
                    <div className="flex items-center gap-1.5 shrink-0" title="综合评分">
                      <Star className="h-3.5 w-3.5 text-amber-500" />
                      <ScoreBadge score={s.review_score} />
                    </div>
                  </CardContent>
                </Card>
              ))}

              {/* 分页 */}
              {totalPages > 1 && (
                <div className="flex items-center justify-center gap-3 pt-2">
                  <Button variant="outline" size="sm" disabled={page <= 1} onClick={() => setPage((p) => p - 1)}>
                    <ChevronLeft className="h-4 w-4" />
                  </Button>
                  <span className="text-xs text-muted-foreground">{page} / {totalPages}</span>
                  <Button variant="outline" size="sm" disabled={page >= totalPages} onClick={() => setPage((p) => p + 1)}>
                    <ChevronRight className="h-4 w-4" />
                  </Button>
                </div>
              )}
            </div>
          )}
        </>
      )}

      {tab === "feedback" && (
        feedbackLoading ? (
          <div className="py-12 text-center text-muted-foreground text-sm">加载中...</div>
        ) : feedback.length === 0 ? (
          <div className="py-10 text-center">
            <MessageSquareQuote className="mx-auto h-12 w-12 text-muted-foreground/30" />
            <p className="mt-3 text-sm text-muted-foreground">
              还没有提交过反馈。写作结果页可以对标题和段落做点评，反馈会进入记忆改进后续写作。
            </p>
          </div>
        ) : (
          <div className="space-y-2">
            {feedback.map((f, i) => (
              <Card key={`${f.trace_id}-${i}`} className="overflow-hidden">
                <CardContent className="py-3 space-y-1.5">
                  <div className="flex items-center gap-2 flex-wrap">
                    <Badge variant="outline"
                      className={cn("text-[10px]",
                        f.feedback_type === "good" ? "text-green-600 border-green-300"
                        : f.feedback_type === "bad" ? "text-red-600 border-red-300"
                        : "text-blue-600 border-blue-300")}>
                      {FEEDBACK_TYPE_LABELS[f.feedback_type] ?? f.feedback_type}
                    </Badge>
                    <span className="text-[11px] text-muted-foreground">
                      {SEGMENT_TYPE_LABELS[f.segment_type] ?? f.segment_type}
                      {f.segment_type === "paragraph" && f.segment_index !== null && f.segment_index !== undefined
                        ? ` #${(f.segment_index ?? 0) + 1}` : ""}
                    </span>
                    <span className="text-[11px] text-amber-600">{"★".repeat(Math.max(1, Math.min(5, f.rating)))}</span>
                    <span className="ml-auto text-[11px] text-muted-foreground">{formatDate(f.created_at)}</span>
                  </div>
                  {f.segment_text && (
                    <p className="text-xs text-muted-foreground line-clamp-2 border-l-2 border-muted pl-2">
                      {f.segment_text}
                    </p>
                  )}
                  {f.comment && <p className="text-xs">{f.comment}</p>}
                  <p className="text-[11px] text-muted-foreground/70">来自写作：{f.trace_title || f.trace_id}</p>
                </CardContent>
              </Card>
            ))}
          </div>
        )
      )}

      {/* 详情回放弹窗 */}
      <SimpleModal open={!!detail || detailLoading} onClose={() => setDetail(null)} title="写作记录详情" maxWidth="max-w-2xl">
        {detailLoading || !detail ? (
          <div className="py-10 text-center text-sm text-muted-foreground">加载中...</div>
        ) : (
          <div className="space-y-4">
            <div className="flex items-center gap-2 flex-wrap">
              <StatusBadge status={detail.status} />
              {detail.review?.passed !== undefined && (
                <Badge variant="outline" className="text-[10px]">
                  {detail.review.passed ? "评审通过" : "评审未通过"}
                </Badge>
              )}
              <span className="text-xs text-muted-foreground ml-auto">{formatDate(detail.created_at)}</span>
            </div>
            {detail.user_input && (
              <p className="text-xs text-muted-foreground border-l-2 border-muted pl-2 line-clamp-3">{detail.user_input}</p>
            )}

            {/* 步骤时间线 */}
            {detail.step_history && detail.step_history.length > 0 && (
              <div>
                <p className="text-xs font-semibold mb-1.5">执行步骤</p>
                <div className="space-y-1">
                  {detail.step_history.map((step, i) => (
                    <div key={i} className="flex items-center gap-2 text-xs">
                      {step.status === "completed" ? <CheckCircle2 className="h-3.5 w-3.5 text-green-500" />
                       : step.status === "running" ? <Loader2 className="h-3.5 w-3.5 text-blue-500 animate-spin" />
                       : step.status === "failed" ? <XCircle className="h-3.5 w-3.5 text-red-500" />
                       : step.status === "paused" ? <Clock className="h-3.5 w-3.5 text-yellow-500" />
                       : <Ban className="h-3.5 w-3.5 text-muted-foreground/40" />}
                      <span>{HISTORY_STEP_LABELS[step.step] ?? step.step}</span>
                      {step.durationMs ? <span className="text-muted-foreground/60 ml-auto">{formatDuration(step.durationMs)}</span> : null}
                    </div>
                  ))}
                </div>
              </div>
            )}

            {/* 评审分维 */}
            {detail.review?.scores && Object.keys(detail.review.scores).length > 0 && (
              <div>
                <p className="text-xs font-semibold mb-1.5">评审评分</p>
                <div className="space-y-1.5">
                  {Object.entries(detail.review.scores).map(([dim, score]) => (
                    <div key={dim} className="flex items-center gap-2">
                      <span className="text-xs text-muted-foreground w-20 shrink-0">{HISTORY_SCORE_LABELS[dim] ?? dim}</span>
                      <div className="flex-1 h-1.5 rounded-full bg-muted overflow-hidden">
                        <div className="h-full rounded-full bg-primary/70" style={{ width: `${Math.min(100, score * 100)}%` }} />
                      </div>
                      <span className="text-xs w-8 text-right">{Math.round(score * 100)}</span>
                    </div>
                  ))}
                </div>
                {detail.review.issues && detail.review.issues.length > 0 && (
                  <div className="mt-2 space-y-1">
                    {detail.review.issues.slice(0, 5).map((issue, i) => (
                      <p key={i} className="text-[11px] text-muted-foreground">
                        · [{issue.severity}] {issue.message}
                      </p>
                    ))}
                  </div>
                )}
              </div>
            )}

            {/* token 统计 */}
            {detail.token_usage?.total_tokens ? (
              <div className="grid grid-cols-3 gap-2 text-center">
                <div className="rounded-lg border py-2">
                  <p className="text-[10px] text-muted-foreground">总 tokens</p>
                  <p className="text-sm font-semibold">{detail.token_usage.total_tokens.toLocaleString()}</p>
                </div>
                <div className="rounded-lg border py-2">
                  <p className="text-[10px] text-muted-foreground">输入</p>
                  <p className="text-sm font-semibold">{detail.token_usage.prompt_tokens?.toLocaleString() ?? "—"}</p>
                </div>
                <div className="rounded-lg border py-2">
                  <p className="text-[10px] text-muted-foreground">输出</p>
                  <p className="text-sm font-semibold">{detail.token_usage.completion_tokens?.toLocaleString() ?? "—"}</p>
                </div>
              </div>
            ) : null}

            {/* 文章正文 */}
            {detail.article && (
              <div>
                <p className="text-xs font-semibold mb-1.5">文章正文</p>
                <div className="max-h-64 overflow-y-auto rounded-lg border p-3 text-xs leading-relaxed whitespace-pre-wrap">
                  {detail.article}
                </div>
              </div>
            )}
          </div>
        )}
      </SimpleModal>
    </div>
  );
}

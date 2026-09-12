/**
 * Trace 历史 — Admin Dashboard
 * 查看历史写作记录和评分
 */
import { useState, useEffect, useCallback } from "react";
import {
  RefreshCw, ChevronRight, Clock, CheckCircle, XCircle, Loader2, AlertCircle,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import { adminFetch } from "@/lib/admin-api";
import { AdminPageHeader } from "@/components/admin";
import { toast } from "@/stores/toast-store";

interface TraceSummary {
  trace_id: string;
  status: string;
  current_step: string;
  user_input: string;
  style_slug: string;
  mode: string;
  article?: string;
  review_score?: number | null;
  token_usage?: { total_tokens?: number } | null;
  duration_ms?: number | null;
  created_at: string;
  completed_at?: string | null;
}

interface TraceDetail {
  trace_id: string;
  status: string;
  current_step: string;
  user_input: string;
  style_slug?: string;
  mode: string;
  article?: string;
  step_history?: StepRecord[];
  review?: {
    scores?: Record<string, number>;
    issues?: Array<{ severity: string; type: string; message: string }>;
    passed?: boolean;
  };
  token_usage?: { total_tokens?: number; prompt_tokens?: number; completion_tokens?: number };
  duration_ms?: number;
  error?: string;
  created_at: string;
  completed_at?: string;
}

interface StepRecord {
  step: string;
  status: string;
  startedAt?: string;
  completedAt?: string;
  durationMs?: number;
  result?: unknown;
  error?: string;
}

const STATUS_CONFIG: Record<string, { color: string; icon: React.ReactNode; label: string }> = {
  completed: { color: "bg-green-100 text-green-700", icon: <CheckCircle className="h-4 w-4" />, label: "已完成" },
  running: { color: "bg-blue-100 text-blue-700", icon: <Loader2 className="h-4 w-4 animate-spin" />, label: "进行中" },
  failed: { color: "bg-red-100 text-red-700", icon: <XCircle className="h-4 w-4" />, label: "失败" },
  paused: { color: "bg-yellow-100 text-yellow-700", icon: <Clock className="h-4 w-4" />, label: "已暂停" },
  cancelled: { color: "bg-gray-100 text-gray-500", icon: <XCircle className="h-4 w-4" />, label: "已取消" },
};

const STEP_LABELS: Record<string, string> = {
  intent: "意图识别",
  query_plan: "检索规划",
  search: "多源检索",
  relevance: "素材过滤",
  outline: "结构规划",
  write: "文章生成",
  post_review: "写后自检",
  auto_fix: "自动修正",
  memory_gate: "记忆检索",
  memory_extract: "记忆提取",
  chat: "对话回复",
  parallel_pre_write: "并行预处理",
};

const STYLE_LABELS: Record<string, string> = {};

const SCORE_LABELS: Record<string, string> = {
  factuality: "事实准确性",
  structure: "结构合规",
  style: "风格符合",
  relevance: "内容相关",
  risk: "内容安全",
  rhetoric: "修辞运用",
  length: "篇幅控制",
  title: "标题质量",
};

export function TraceHistoryPage() {
  const [traces, setTraces] = useState<TraceSummary[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(false);
  const [page, setPage] = useState(1);
  const [pageSize] = useState(20);
  const [statusFilter, setStatusFilter] = useState<string>("");
  const [styleFilter, setStyleFilter] = useState<string>("");
  const [selectedTrace, setSelectedTrace] = useState<TraceDetail | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);

  const loadTraces = useCallback(async () => {
    setLoading(true);
    const params = new URLSearchParams();
    params.set("page", String(page));
    params.set("page_size", String(pageSize));
    if (statusFilter) params.set("status", statusFilter);
    if (styleFilter) params.set("style", styleFilter);

    const { success, data } = await adminFetch<{ traces: TraceSummary[]; total: number }>(`/api/v2/admin/traces?${params}`, { silent: true });
    if (success && data) {
      setTraces(data.traces ?? []);
      setTotal(data.total ?? 0);
    }
    setLoading(false);
  }, [page, pageSize, statusFilter, styleFilter]);

  const loadDetail = async (traceId: string) => {
    setDetailLoading(true);
    const { success, data } = await adminFetch<TraceDetail>(`/api/v2/admin/traces/${traceId}`, { silent: true });
    if (success && data) {
      setSelectedTrace(data);
    } else {
      toast.error("加载失败", "无法获取 Trace 详情");
    }
    setDetailLoading(false);
  };

  useEffect(() => {
    loadTraces();
  }, [loadTraces]);

  const totalPages = Math.ceil(total / pageSize);

  return (
    <div className="p-6 space-y-6">
      {/* Header */}
      <AdminPageHeader
        title="Trace 历史"
        action={
          <Button variant="outline" size="sm" onClick={loadTraces} disabled={loading}>
            <RefreshCw className={`h-4 w-4 mr-2 ${loading ? "animate-spin" : ""}`} />
            刷新
          </Button>
        }
      />

      {/* Filters */}
      <div className="flex items-center gap-4">
        <div className="flex items-center gap-2">
          <span className="text-sm text-muted-foreground">状态:</span>
          <Select value={statusFilter || "all"} onValueChange={(v) => { setStatusFilter(v === "all" ? "" : v); setPage(1); }}>
            <SelectTrigger className="w-32"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="all">全部</SelectItem>
              <SelectItem value="completed">已完成</SelectItem>
              <SelectItem value="running">进行中</SelectItem>
              <SelectItem value="failed">失败</SelectItem>
              <SelectItem value="paused">已暂停</SelectItem>
              <SelectItem value="cancelled">已取消</SelectItem>
            </SelectContent>
          </Select>
        </div>
        <div className="flex items-center gap-2">
          <span className="text-sm text-muted-foreground">风格:</span>
          <Select value={styleFilter || "all"} onValueChange={(v) => { setStyleFilter(v === "all" ? "" : v); setPage(1); }}>
            <SelectTrigger className="w-32"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="all">全部</SelectItem>
            </SelectContent>
          </Select>
        </div>
        <span className="text-sm text-muted-foreground ml-auto">
          共 {total} 条记录
        </span>
      </div>

      {/* Trace List */}
      <div className="rounded-lg border">
        <table className="w-full">
          <thead>
            <tr className="border-b bg-muted/50">
              <th className="text-left p-3 text-sm font-medium">状态</th>
              <th className="text-left p-3 text-sm font-medium">用户输入</th>
              <th className="text-left p-3 text-sm font-medium">风格</th>
              <th className="text-left p-3 text-sm font-medium">评分</th>
              <th className="text-left p-3 text-sm font-medium">耗时</th>
              <th className="text-left p-3 text-sm font-medium">时间</th>
              <th className="text-right p-3 text-sm font-medium"></th>
            </tr>
          </thead>
          <tbody>
            {loading ? (
              <tr>
                <td colSpan={7} className="p-8 text-center">
                  <Loader2 className="h-4 w-4 animate-spin mx-auto" />
                </td>
              </tr>
            ) : traces.length === 0 ? (
              <tr>
                <td colSpan={7} className="p-8 text-center text-muted-foreground text-sm">
                  暂无写作记录
                </td>
              </tr>
            ) : (
              traces.map((trace) => {
                const config = STATUS_CONFIG[trace.status] ?? STATUS_CONFIG.running;
                return (
                  <tr
                    key={trace.trace_id}
                    className="border-b last:border-0 hover:bg-accent/30 cursor-pointer"
                    onClick={() => loadDetail(trace.trace_id)}
                  >
                    <td className="p-3">
                      <Badge variant="outline" className={config.color}>
                        {config.icon}
                        <span className="ml-1">{config.label}</span>
                      </Badge>
                    </td>
                    <td className="p-3">
                      <p className="text-sm truncate max-w-md">{trace.user_input}</p>
                      <p className="text-xs text-muted-foreground font-mono">{trace.trace_id}</p>
                    </td>
                    <td className="p-3 text-sm">
                      {STYLE_LABELS[trace.style_slug] ?? trace.style_slug ?? "—"}
                    </td>
                    <td className="p-3 text-sm">
                      {trace.review_score !== null && trace.review_score !== undefined ? (
                        <span className="font-medium text-green-600">
                          {(trace.review_score * 100).toFixed(0)}
                        </span>
                      ) : "—"}
                    </td>
                    <td className="p-3 text-sm text-muted-foreground">
                      {trace.duration_ms ? `${(trace.duration_ms / 1000).toFixed(1)}s` : "—"}
                    </td>
                    <td className="p-3 text-sm text-muted-foreground">
                      {new Date(trace.created_at).toLocaleString("zh-CN")}
                    </td>
                    <td className="p-3 text-right">
                      <ChevronRight className="h-4 w-4 text-muted-foreground" />
                    </td>
                  </tr>
                );
              })
            )}
          </tbody>
        </table>

        {/* Pagination */}
        {totalPages > 1 && (
          <div className="flex items-center justify-between p-3 border-t">
            <Button
              variant="outline"
              size="sm"
              onClick={() => setPage((p) => Math.max(1, p - 1))}
              disabled={page <= 1}
            >
              上一页
            </Button>
            <span className="text-sm text-muted-foreground">
              第 {page} / {totalPages} 页
            </span>
            <Button
              variant="outline"
              size="sm"
              onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
              disabled={page >= totalPages}
            >
              下一页
            </Button>
          </div>
        )}
      </div>

      {/* Detail Dialog */}
      {(selectedTrace || detailLoading) && (
        <TraceDetailDialog
          trace={selectedTrace}
          loading={detailLoading}
          onClose={() => { setSelectedTrace(null); }}
        />
      )}
    </div>
  );
}

// ─── Trace Detail Dialog ─────────────────────────────────

function TraceDetailDialog({
  trace, loading, onClose,
}: {
  trace: TraceDetail | null;
  loading: boolean;
  onClose: () => void;
}) {
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-4xl max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>Trace 详情</DialogTitle>
        </DialogHeader>

        {loading || !trace ? (
          <div className="py-12 text-center">
            <Loader2 className="h-6 w-6 animate-spin mx-auto" />
          </div>
        ) : (
          <div className="space-y-4">
            {/* Basic Info */}
            <div className="grid grid-cols-3 gap-4">
              <div className="rounded-lg border p-3">
                <p className="text-xs text-muted-foreground">Trace ID</p>
                <p className="text-sm font-mono mt-1">{trace.trace_id}</p>
              </div>
              <div className="rounded-lg border p-3">
                <p className="text-xs text-muted-foreground">状态</p>
                <div className="mt-1">
                  <Badge variant="outline" className={STATUS_CONFIG[trace.status]?.color ?? ""}>
                    {STATUS_CONFIG[trace.status]?.label ?? trace.status}
                  </Badge>
                </div>
              </div>
              <div className="rounded-lg border p-3">
                <p className="text-xs text-muted-foreground">风格</p>
                <p className="text-sm mt-1">{STYLE_LABELS[trace.style_slug ?? ""] ?? trace.style_slug ?? "—"}</p>
              </div>
            </div>

            {/* User Input */}
            <div className="rounded-lg border p-3">
              <p className="text-xs text-muted-foreground mb-1">用户输入</p>
              <p className="text-sm">{trace.user_input}</p>
            </div>

            {/* Error (if any) */}
            {trace.error && (
              <div className="rounded-lg border border-red-200 bg-red-50 p-3">
                <div className="flex items-center gap-2 text-sm text-red-700">
                  <AlertCircle className="h-4 w-4" />
                  <span className="font-medium">错误信息</span>
                </div>
                <p className="text-sm text-red-600 mt-1">{trace.error}</p>
              </div>
            )}

            {/* Step History */}
            {trace.step_history && trace.step_history.length > 0 && (
              <Card>
                <CardHeader>
                  <CardTitle className="text-sm">执行步骤</CardTitle>
                </CardHeader>
                <CardContent>
                  <div className="space-y-2">
                    {trace.step_history.map((step, i) => (
                      <div key={i} className="flex items-center gap-3 border rounded-lg p-2">
                        <div className="w-6 h-6 rounded-full bg-muted flex items-center justify-center text-xs font-medium">
                          {i + 1}
                        </div>
                        <div className="flex-1">
                          <div className="flex items-center gap-2">
                            <span className="text-sm font-medium">
                              {STEP_LABELS[step.step] ?? step.step}
                            </span>
                            <Badge
                              variant="outline"
                              className={
                                step.status === "complete" ? "bg-green-100 text-green-700" :
                                step.status === "error" ? "bg-red-100 text-red-700" :
                                step.status === "running" ? "bg-blue-100 text-blue-700" :
                                step.status === "degraded" ? "bg-amber-100 text-amber-700" :
                                ""
                              }
                            >
                              {step.status}
                            </Badge>
                          </div>
                          {step.error && (
                            <p className="text-xs text-red-500 mt-1">{step.error}</p>
                          )}
                        </div>
                        {step.durationMs && (
                          <span className="text-xs text-muted-foreground">
                            {(step.durationMs / 1000).toFixed(1)}s
                          </span>
                        )}
                      </div>
                    ))}
                  </div>
                </CardContent>
              </Card>
            )}

            {/* Review Scores */}
            {trace.review && (
              <Card>
                <CardHeader>
                  <CardTitle className="text-sm flex items-center justify-between">
                    质量评分
                    {trace.review.passed !== undefined && (
                      <Badge variant={trace.review.passed ? "default" : "destructive"}>
                        {trace.review.passed ? "通过" : "未通过"}
                      </Badge>
                    )}
                  </CardTitle>
                </CardHeader>
                <CardContent className="space-y-3">
                  {trace.review.scores && Object.keys(trace.review.scores).length > 0 && (
                    <div className="space-y-2">
                      {Object.entries(trace.review.scores).map(([dim, score]) => (
                        <div key={dim} className="flex items-center gap-3">
                          <span className="w-24 text-sm text-muted-foreground">
                            {SCORE_LABELS[dim] ?? dim}
                          </span>
                          <div className="flex-1 h-5 bg-muted rounded-full overflow-hidden">
                            <div
                              className="h-full bg-primary rounded-full flex items-center justify-end px-2"
                              style={{ width: `${score * 100}%` }}
                            >
                              <span className="text-xs text-primary-foreground font-medium">
                                {(score * 100).toFixed(0)}
                              </span>
                            </div>
                          </div>
                        </div>
                      ))}
                    </div>
                  )}

                  {trace.review.issues && trace.review.issues.length > 0 && (
                    <div className="border-t pt-3 space-y-2">
                      <h5 className="text-sm font-medium">问题列表</h5>
                      {trace.review.issues.map((issue, i) => (
                        <div
                          key={i}
                          className={`flex items-start gap-2 p-2 rounded text-sm ${
                            issue.severity === "high" ? "bg-red-50 text-red-700" :
                            issue.severity === "medium" ? "bg-yellow-50 text-yellow-700" :
                            "bg-muted/50"
                          }`}
                        >
                          <Badge variant="outline" className="text-xs">
                            {issue.severity}
                          </Badge>
                          <span>{issue.message}</span>
                        </div>
                      ))}
                    </div>
                  )}
                </CardContent>
              </Card>
            )}

            {/* Article */}
            {trace.article && (
              <Card>
                <CardHeader>
                  <CardTitle className="text-sm">生成文章</CardTitle>
                </CardHeader>
                <CardContent>
                  <div className="max-h-96 overflow-y-auto rounded-lg bg-muted/30 p-4">
                    <pre className="text-sm whitespace-pre-wrap font-sans">{trace.article}</pre>
                  </div>
                </CardContent>
              </Card>
            )}

            {/* Token Usage */}
            {trace.token_usage && (
              <div className="grid grid-cols-4 gap-3">
                <div className="rounded-lg border p-3">
                  <p className="text-xs text-muted-foreground">总 Token</p>
                  <p className="text-lg font-bold mt-1">
                    {trace.token_usage.total_tokens?.toLocaleString() ?? "—"}
                  </p>
                </div>
                <div className="rounded-lg border p-3">
                  <p className="text-xs text-muted-foreground">Prompt</p>
                  <p className="text-lg font-bold mt-1">
                    {trace.token_usage.prompt_tokens?.toLocaleString() ?? "—"}
                  </p>
                </div>
                <div className="rounded-lg border p-3">
                  <p className="text-xs text-muted-foreground">Completion</p>
                  <p className="text-lg font-bold mt-1">
                    {trace.token_usage.completion_tokens?.toLocaleString() ?? "—"}
                  </p>
                </div>
                <div className="rounded-lg border p-3">
                  <p className="text-xs text-muted-foreground">总耗时</p>
                  <p className="text-lg font-bold mt-1">
                    {trace.duration_ms ? `${(trace.duration_ms / 1000).toFixed(1)}s` : "—"}
                  </p>
                </div>
              </div>
            )}
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}

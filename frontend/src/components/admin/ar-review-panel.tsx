/**
 * AR-012 候选评估控制台 — Admin Dashboard（T10）
 *
 * 内评期工具：跨用户查看 AR-012 sidecar 作业、发起对比候选（输入完成的
 * research-review run ID）、查看机械对比指标与候选稿。作业是 202 + 轮询
 * 模式（sidecar 同步执行 10–25 分钟），pending/running 时自动刷新。
 *
 * 后端：GET /api/v2/admin/ar-review/jobs（eval.view RBAC，enabled=false
 * 以数据返回）；产物读取走同组 /artifacts/{kind}；发起复用 owner 端点
 * POST /api/v2/runs/{runId}/research/ar012-candidate。
 * 候选稿是隔离评估产物，永不进入正式文档（A18）。
 */
import { useCallback, useEffect, useMemo, useState } from "react";
import { FlaskConical, RefreshCw, FileText, Scale, Ban } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import {
  AdminPageHeader,
  AdminTable,
  type AdminColumn,
} from "@/components/admin";
import { adminFetch, adminMutate } from "@/lib/admin-api";
import { toast } from "@/stores/toast-store";
import type {
  ArReviewJobItem,
  ArReviewMetrics,
  ArReviewOverview,
} from "@/lib/admin-types";

const STATUS_META: Record<ArReviewJobItem["status"], { label: string; variant: "default" | "secondary" | "destructive" | "outline" }> = {
  pending: { label: "排队中", variant: "secondary" },
  running: { label: "生成中", variant: "default" },
  completed: { label: "已完成", variant: "outline" },
  failed: { label: "失败", variant: "destructive" },
  outcome_unknown: { label: "结果未知", variant: "destructive" },
  cancelled: { label: "已取消", variant: "secondary" },
};

function formatTokens(usage: Record<string, unknown>): string {
  const input = usage.input_tokens;
  const output = usage.output_tokens;
  if (typeof input !== "number" && typeof output !== "number") return "—";
  return `${typeof input === "number" ? input : "?"} / ${typeof output === "number" ? output : "?"}`;
}

function formatTime(value: string | undefined): string {
  if (!value) return "—";
  return new Date(value).toLocaleString();
}

/** 拉取作业产物原文（markdown / json 文本）。404/错误时返回 null。 */
async function fetchArtifact(jobId: string, kind: string): Promise<string | null> {
  const res = await fetch(`/api/v2/admin/ar-review/jobs/${jobId}/artifacts/${kind}`);
  if (!res.ok) return null;
  return await res.text();
}

export function ArReviewPanelPage() {
  const [overview, setOverview] = useState<ArReviewOverview>({ enabled: false, jobs: [] });
  const [loading, setLoading] = useState(true);
  const [selected, setSelected] = useState<ArReviewJobItem | null>(null);
  const [artifact, setArtifact] = useState<{ kind: string; content: string } | null>(null);
  const [artifactLoading, setArtifactLoading] = useState(false);
  const [runIdInput, setRunIdInput] = useState("");
  const [dispatching, setDispatching] = useState(false);

  const load = useCallback(async (silent = false) => {
    if (!silent) setLoading(true);
    const result = await adminFetch<ArReviewOverview>("/api/v2/admin/ar-review/jobs?limit=100", { silent: true });
    if (result.success && result.data) {
      setOverview(result.data);
      // 选中作业同步刷新（引用替换，按 id 重新匹配）
      setSelected((prev) => (prev ? result.data?.jobs.find((j) => j.job_id === prev.job_id) ?? null : null));
    }
    if (!silent) setLoading(false);
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  // pending/running 作业存在时轮询（sidecar 同步执行，10–25 分钟量级）
  const hasActiveJobs = useMemo(
    () => overview.jobs.some((j) => j.status === "pending" || j.status === "running"),
    [overview.jobs],
  );
  useEffect(() => {
    if (!hasActiveJobs) return;
    const timer = setInterval(() => void load(true), 8000);
    return () => clearInterval(timer);
  }, [hasActiveJobs, load]);

  const dispatch = useCallback(async () => {
    const runId = runIdInput.trim();
    if (!runId) {
      toast.error("请输入 run ID", "格式如 run_xxxx（已完成的 research-review 运行）");
      return;
    }
    setDispatching(true);
    const result = await adminMutate<{ job_id: string }>(
      `/api/v2/runs/${encodeURIComponent(runId)}/research/ar012-candidate`,
      { method: "POST", successTitle: "作业已创建", successDesc: "生成完成后列表自动刷新" },
    );
    setDispatching(false);
    if (result.success) {
      setRunIdInput("");
      void load(true);
    }
  }, [runIdInput, load]);

  const cancelJob = useCallback(async (job: ArReviewJobItem) => {
    const result = await adminMutate<unknown>(
      `/api/v2/runs/${encodeURIComponent(job.run_id)}/research/ar012-candidate/cancel`,
      { method: "POST", successTitle: "已请求取消", successDesc: "pending 作业立即取消；运行中的作业将在同步调用返回后丢弃产物" },
    );
    if (result.success) void load(true);
  }, [load]);

  const viewArtifact = useCallback(async (job: ArReviewJobItem, kind: string) => {
    setArtifactLoading(true);
    setArtifact(null);
    const content = await fetchArtifact(job.job_id, kind);
    setArtifactLoading(false);
    if (content === null) {
      toast.error("产物读取失败", "作业可能未完成或产物不存在");
      return;
    }
    setArtifact({ kind, content });
  }, []);

  const hasRef = (job: ArReviewJobItem | null, kind: string) =>
    !!job?.artifact_refs.some((ref) => ref.kind === kind);

  const metrics = useMemo<ArReviewMetrics | null>(() => {
    if (artifact?.kind !== "metrics") return null;
    try {
      return JSON.parse(artifact.content) as ArReviewMetrics;
    } catch {
      return null;
    }
  }, [artifact]);

  const columns: AdminColumn<ArReviewJobItem>[] = [
    {
      key: "status",
      header: "状态",
      width: "100px",
      render: (row) => (
        <Badge variant={STATUS_META[row.status].variant}>{STATUS_META[row.status].label}</Badge>
      ),
    },
    { key: "run_id", header: "Run ID", render: (row) => <span className="font-mono text-xs">{row.run_id}</span> },
    {
      key: "progress",
      header: "产物",
      width: "120px",
      render: (row) => `${row.artifact_refs.length}/5`,
    },
    { key: "usage", header: "Tokens (入/出)", width: "130px", render: (row) => formatTokens(row.usage) },
    {
      key: "warnings",
      header: "语料警告",
      width: "90px",
      render: (row) => (row.corpus_warnings.length > 0 ? <Badge variant="secondary">{row.corpus_warnings.length}</Badge> : "—"),
    },
    { key: "created_at", header: "创建时间", width: "160px", render: (row) => formatTime(row.created_at) },
  ];

  return (
    <div className="space-y-6">
      <AdminPageHeader
        title="AR-012 候选评估"
        description="对已完成的 research-review 运行生成 AR-012 对比候选稿（隔离产物，永不进入正式文档）。"
      />

      {!overview.enabled && !loading && (
        <Card>
          <CardContent className="pt-6 text-sm text-muted-foreground space-y-2">
            <p className="font-medium text-foreground">Sidecar 未启用</p>
            <p>
              当前部署未开启 AR-012 候选评估。启用方法：构建本地镜像
              <code className="mx-1 rounded bg-muted px-1 py-0.5 text-xs">review-sidecar ./build.sh --image</code>
              后，以 overlay 启动
              <code className="mx-1 rounded bg-muted px-1 py-0.5 text-xs">
                docker compose -f docker-compose.yml -f docker-compose.ar012.yml --profile ar012 up -d backend
              </code>
              （详见 specs/research-review/ar012-sidecar.md §6）。
            </p>
          </CardContent>
        </Card>
      )}

      {overview.enabled && (
        <Card>
          <CardHeader className="pb-3">
            <CardTitle className="flex items-center gap-2 text-base">
              <FlaskConical className="h-4 w-4" />
              发起对比候选
            </CardTitle>
          </CardHeader>
          <CardContent className="flex flex-col gap-2 sm:flex-row">
            <Input
              placeholder="输入已完成的 research-review Run ID（run_…）"
              value={runIdInput}
              onChange={(e) => setRunIdInput(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && !dispatching && void dispatch()}
              className="font-mono"
            />
            <Button onClick={() => void dispatch()} disabled={dispatching || !runIdInput.trim()}>
              {dispatching ? "创建中…" : "生成候选"}
            </Button>
          </CardContent>
        </Card>
      )}

      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="flex items-center justify-between text-base">
            <span className="flex items-center gap-2">
              作业列表
              {hasActiveJobs && (
                <Badge variant="default" className="animate-pulse">
                  轮询中
                </Badge>
              )}
            </span>
            <Button variant="outline" size="sm" onClick={() => void load()}>
              <RefreshCw className="h-4 w-4" />
              刷新
            </Button>
          </CardTitle>
        </CardHeader>
        <CardContent>
          <AdminTable
            data={overview.jobs}
            columns={columns}
            rowKey={(row) => row.job_id}
            loading={loading}
            emptyTitle={overview.enabled ? "暂无作业" : "Sidecar 未启用"}
            emptyDesc={overview.enabled ? "在上方输入 Run ID 发起第一个对比候选" : "启用后此处显示全部评估作业"}
            onRowClick={(row) => {
              setSelected((prev) => (prev?.job_id === row.job_id ? null : row));
              setArtifact(null);
            }}
          />
        </CardContent>
      </Card>

      {selected && (
        <Card>
          <CardHeader className="pb-3">
            <CardTitle className="text-base">
              作业详情
              <span className="ml-2 font-mono text-xs text-muted-foreground">{selected.job_id}</span>
            </CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="grid gap-3 text-sm sm:grid-cols-2">
              <div>
                <span className="text-muted-foreground">状态：</span>
                <Badge variant={STATUS_META[selected.status].variant} className="ml-1">
                  {STATUS_META[selected.status].label}
                </Badge>
                {selected.cancel_requested && (
                  <Badge variant="secondary" className="ml-1">
                    <Ban className="mr-1 h-3 w-3" />
                    取消已请求
                  </Badge>
                )}
              </div>
              <div>
                <span className="text-muted-foreground">远程作业：</span>
                <span className="ml-1 font-mono text-xs">{selected.remote_run_id || "—"}</span>
              </div>
              <div>
                <span className="text-muted-foreground">Surrogate 项目：</span>
                <span className="ml-1 font-mono text-xs">{selected.surrogate_project_id}</span>
              </div>
              <div>
                <span className="text-muted-foreground">Tokens（入/出）：</span>
                <span className="ml-1">{formatTokens(selected.usage)}</span>
              </div>
            </div>

            {selected.error_message && (
              <div className="rounded-md border border-destructive/30 bg-destructive/5 p-3 text-sm">
                <span className="font-medium text-destructive">{selected.error_code || "error"}</span>
                <span className="ml-2 text-muted-foreground">{selected.error_message}</span>
              </div>
            )}

            {selected.corpus_warnings.length > 0 && (
              <div className="rounded-md border p-3 text-sm">
                <p className="mb-1 font-medium">语料剔除警告（{selected.corpus_warnings.length}）</p>
                <ul className="list-disc pl-5 text-muted-foreground">
                  {selected.corpus_warnings.map((warning, index) => (
                    <li key={index}>{warning}</li>
                  ))}
                </ul>
              </div>
            )}

            {selected.status === "completed" && (
              <div className="flex flex-wrap gap-2">
                <Button
                  variant="outline"
                  size="sm"
                  disabled={artifactLoading || !hasRef(selected, "metrics")}
                  onClick={() => void viewArtifact(selected, "metrics")}
                >
                  <Scale className="mr-1 h-4 w-4" />
                  对比指标
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={artifactLoading || !hasRef(selected, "manuscript")}
                  onClick={() => void viewArtifact(selected, "manuscript")}
                >
                  <FileText className="mr-1 h-4 w-4" />
                  候选稿
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={!hasRef(selected, "manuscript")}
                  onClick={() => void cancelJob(selected)}
                >
                  <Ban className="mr-1 h-4 w-4" />
                  请求取消
                </Button>
              </div>
            )}

            {artifactLoading && <p className="text-sm text-muted-foreground">加载产物中…</p>}

            {metrics && (
              <div className="rounded-md border p-4">
                <p className="mb-3 text-sm font-medium">机械对比指标（review-metrics/1 — 不含质量结论）</p>
                <table className="w-full text-sm">
                  <thead>
                    <tr className="border-b text-left text-muted-foreground">
                      <th className="py-1.5 pr-4 font-normal">指标</th>
                      <th className="py-1.5 pr-4 text-right font-normal">AR-012 候选</th>
                      <th className="py-1.5 text-right font-normal">Lumin 主稿</th>
                    </tr>
                  </thead>
                  <tbody>
                    <MetricRow label="汉字数" candidate={metrics.candidate.cjk_characters} baseline={metrics.baseline.cjk_characters} />
                    <MetricRow label="引用出现次数" candidate={metrics.candidate.citation_occurrences} baseline={metrics.baseline.evidence_citations} />
                    <MetricRow label="唯一引用/证据" candidate={metrics.candidate.unique_citations} baseline={metrics.baseline.unique_evidence_citations} />
                    <MetricRow label="可解析来源数" candidate={metrics.candidate.distinct_sources} />
                    <MetricRow
                      label="来源覆盖率"
                      candidate={`${(metrics.candidate.source_coverage * 100).toFixed(0)}%`}
                    />
                    <MetricRow label="标题数" candidate={metrics.candidate.headings} />
                  </tbody>
                </table>
                {metrics.candidate.unresolved_citations.length > 0 && (
                  <p className="mt-3 text-xs text-destructive">
                    未解析引用 {metrics.candidate.unresolved_citations.length} 处：
                    {metrics.candidate.unresolved_citations.join("、")}
                  </p>
                )}
              </div>
            )}

            {artifact && artifact.kind === "manuscript" && (
              <div className="rounded-md border p-4">
                <p className="mb-2 text-sm font-medium">候选稿预览（MANUSCRIPT_REVIEW_AGENT.md）</p>
                <pre className="max-h-96 overflow-auto whitespace-pre-wrap rounded bg-muted p-3 text-xs leading-relaxed">
                  {artifact.content}
                </pre>
              </div>
            )}
          </CardContent>
        </Card>
      )}
    </div>
  );
}

function MetricRow({ label, candidate, baseline }: { label: string; candidate: number | string; baseline?: number | string }) {
  return (
    <tr className="border-b last:border-0">
      <td className="py-1.5 pr-4">{label}</td>
      <td className="py-1.5 pr-4 text-right font-mono">{candidate}</td>
      <td className="py-1.5 text-right font-mono">{baseline ?? "—"}</td>
    </tr>
  );
}

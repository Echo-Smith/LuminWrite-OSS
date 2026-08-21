/**
 * 自演进管理 — Admin Dashboard
 *
 * 完整的自演进闭环 UI：
 * 1. 候选列表（含 eval gate 状态、canary metrics）
 * 2. 批准 → 自动触发 eval gate
 * 3. 灰度发布（设置百分比 → 路由引擎生效）
 * 4. 灰度 metrics 面板（新旧版本流量分配）
 * 5. 回滚 / 全量发布
 */
import { useState, useEffect, useCallback, type ReactNode } from "react";
import {
  GitBranch, RefreshCw, Check, X, FlaskConical, TrendingUp,
  AlertTriangle, ArrowUpCircle, ArrowDownCircle, Activity,
  Users, PieChart, ChevronDown, ChevronRight,
} from "lucide-react";
import { adminFetch, adminMutate } from "@/lib/admin-api";
import { AdminPageHeader, AdminLoading, AdminEmptyState } from "@/components/admin";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";

// ─── Types ──────────────────────────────────────────────

interface CanaryRollout {
  id: string;
  style_slug: string;
  version: number;
  candidate_id: string;
  percentage: number;
  enabled: boolean;
  started_at?: string;
  ended_at?: string;
  rollback_reason?: string;
}

interface EvolutionCandidate {
  id: string;
  style_slug: string;
  parent_version: number;
  changes: Record<string, unknown>;
  status: string;
  eval_baseline_id?: string;
  eval_candidate_id?: string;
  created_at: string;
  rollout?: CanaryRollout;
}

interface CanaryMetrics {
  total: number;
  new_version: number;
  old_version: number;
  whitelist: number;
  percentage: number;
  errors: number;
}

// ─── Status Config ───────────────────────────────────────

const STATUS_STYLES: Record<string, string> = {
  draft:        "bg-gray-100 text-gray-700 border-gray-200",
  eval_running: "bg-amber-50 text-amber-700 border-amber-200",
  approved:     "bg-emerald-50 text-emerald-700 border-emerald-200",
  rejected:     "bg-red-50 text-red-700 border-red-200",
  rollout:      "bg-blue-50 text-blue-700 border-blue-200",
  active:       "bg-green-50 text-green-700 border-green-200",
};

const STATUS_LABELS: Record<string, string> = {
  draft:        "草稿",
  eval_running: "评测中",
  approved:     "已批准",
  rejected:     "已拒绝",
  rollout:      "灰度中",
  active:       "全量上线",
};

// ─── Component ──────────────────────────────────────────

export function EvolutionPage() {
  const [candidates, setCandidates] = useState<EvolutionCandidate[]>([]);
  const [loading, setLoading] = useState(true);
  const [canaryTarget, setCanaryTarget] = useState<string | null>(null);
  const [canaryPct, setCanaryPct] = useState("10");
  const [rollbackTarget, setRollbackTarget] = useState<string | null>(null);
  const [rollbackReason, setRollbackReason] = useState("");
  const [promoteTarget, setPromoteTarget] = useState<string | null>(null);
  const [expandedMetrics, setExpandedMetrics] = useState<string | null>(null);
  const [metricsMap, setMetricsMap] = useState<Record<string, CanaryMetrics>>({});

  const loadCandidates = useCallback(async () => {
    setLoading(true);
    const { success, data } = await adminFetch<{ candidates: EvolutionCandidate[]; total: number }>(
      "/api/v2/admin/evolution/candidates",
      { silent: true }
    );
    if (success && data) {
      setCandidates(data.candidates ?? []);
    } else {
      setCandidates([]);
    }
    setLoading(false);
  }, []);

  useEffect(() => { loadCandidates(); }, [loadCandidates]);

  // Auto-refresh metrics for rollout candidates
  useEffect(() => {
    const rolloutIds = candidates.filter(c => c.status === "rollout").map(c => c.id);
    if (rolloutIds.length === 0) return;

    const interval = setInterval(async () => {
      const newMetrics: Record<string, CanaryMetrics> = {};
      for (const id of rolloutIds) {
        const { success, data } = await adminFetch<{ metrics: CanaryMetrics }>(
          `/api/v2/admin/evolution/candidates/${id}/metrics`,
          { silent: true }
        );
        if (success && data?.metrics) {
          newMetrics[id] = data.metrics;
        }
      }
      setMetricsMap(prev => ({ ...prev, ...newMetrics }));
    }, 5000);

    return () => clearInterval(interval);
  }, [candidates]);

  // ─── Actions ───────────────────────────────────────

  const handleApprove = async (id: string) => {
    const { success } = await adminMutate(`/api/v2/admin/evolution/candidates/${id}/approve`, {});
    if (success) {
      await loadCandidates();
    }
  };

  const handleReject = async (id: string) => {
    const { success } = await adminMutate(`/api/v2/admin/evolution/candidates/${id}/reject`, {
      method: "POST",
      body: JSON.stringify({ reason: "admin rejected" }),
    });
    if (success) { await loadCandidates(); }
  };

  const handleCanary = async () => {
    if (!canaryTarget) return;
    const pct = parseFloat(canaryPct) || 10;
    const { success } = await adminMutate(`/api/v2/admin/evolution/candidates/${canaryTarget}/canary`, {
      method: "POST",
      body: JSON.stringify({ percentage: pct }),
    });
    if (success) { setCanaryTarget(null); await loadCandidates(); }
  };

  const handleRollback = async () => {
    if (!rollbackTarget) return;
    const { success } = await adminMutate(`/api/v2/admin/evolution/candidates/${rollbackTarget}/rollback`, {
      method: "POST",
      body: JSON.stringify({ reason: rollbackReason || "manual rollback" }),
    });
    if (success) { setRollbackTarget(null); setRollbackReason(""); await loadCandidates(); }
  };

  const handlePromote = async () => {
    if (!promoteTarget) return;
    const { success } = await adminMutate(`/api/v2/admin/evolution/candidates/${promoteTarget}/promote`, {});
    if (success) { setPromoteTarget(null); await loadCandidates(); }
  };

  const toggleMetrics = async (id: string) => {
    if (expandedMetrics === id) {
      setExpandedMetrics(null);
      return;
    }
    setExpandedMetrics(id);
    // Fetch metrics immediately
    const { success, data } = await adminFetch<{ metrics: CanaryMetrics }>(
      `/api/v2/admin/evolution/candidates/${id}/metrics`,
      { silent: true }
    );
    if (success && data?.metrics) {
      setMetricsMap(prev => ({ ...prev, [id]: data.metrics }));
    }
  };

  // ─── Render ─────────────────────────────────────────

  return (
    <div className="p-6 space-y-6">
      <AdminPageHeader
        title="自演进管理"
        description="风格 Profile 迭代候选 — 从反馈生成，经评测门控，到灰度发布"
        action={
          <Button variant="outline" size="sm" onClick={loadCandidates} disabled={loading}>
            <RefreshCw className={`h-4 w-4 mr-2 ${loading ? "animate-spin" : ""}`} /> 刷新
          </Button>
        }
      />

      {/* Flow Diagram */}
      <Card>
        <CardContent className="p-4">
          <div className="flex items-center gap-2 mb-3">
            <GitBranch className="h-4 w-4" />
            <h3 className="text-sm font-semibold">自演进流程</h3>
          </div>
          <div className="flex items-center gap-2 text-xs text-muted-foreground flex-wrap">
            <Badge variant="outline">1. 反馈聚合</Badge>
            <span>→</span>
            <Badge variant="outline">2. 候选生成</Badge>
            <span>→</span>
            <Badge variant="outline" className="bg-amber-50 text-amber-700 border-amber-200">3. 评测门控</Badge>
            <span>→</span>
            <Badge variant="outline" className="bg-emerald-50 text-emerald-700 border-emerald-200">4. 审批</Badge>
            <span>→</span>
            <Badge variant="outline" className="bg-blue-50 text-blue-700 border-blue-200">5. 灰度发布</Badge>
            <span>→</span>
            <Badge variant="outline" className="bg-green-50 text-green-700 border-green-200">6. 全量上线</Badge>
          </div>
        </CardContent>
      </Card>

      {loading ? (
        <AdminLoading />
      ) : candidates.length === 0 ? (
        <AdminEmptyState
          icon={GitBranch}
          title="暂无迭代候选"
          description="当用户反馈聚合到一定程度后，系统会自动生成风格 Profile 迭代候选"
        />
      ) : (
        <div className="space-y-3">
          {candidates.map((c) => (
            <CandidateCard
              key={c.id}
              candidate={c}
              metrics={metricsMap[c.id]}
              expandedMetrics={expandedMetrics === c.id}
              onToggleMetrics={() => toggleMetrics(c.id)}
              onApprove={() => handleApprove(c.id)}
              onReject={() => handleReject(c.id)}
              onCanaryOpen={() => { setCanaryTarget(c.id); setCanaryPct("10"); }}
              onRollbackOpen={() => { setRollbackTarget(c.id); setRollbackReason(""); }}
              onPromoteOpen={() => setPromoteTarget(c.id)}
            />
          ))}
        </div>
      )}

      {/* Canary Dialog */}
      <Dialog open={canaryTarget !== null} onOpenChange={(open) => { if (!open) setCanaryTarget(null); }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>灰度发布</DialogTitle>
            <DialogDescription>
              将候选版本推送到指定百分比的用户，监控效果后决定全量上线或回滚。
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2 py-4">
            <Label>灰度百分比 (%)</Label>
            <Input
              type="number"
              min="1" max="100"
              value={canaryPct}
              onChange={(e) => setCanaryPct(e.target.value)}
            />
            <p className="text-xs text-muted-foreground">
              使用 FNV-1a 哈希做确定性流量分配，同一用户始终命中同一版本。
            </p>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setCanaryTarget(null)}>取消</Button>
            <Button onClick={handleCanary}>开始灰度</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Rollback Dialog */}
      <Dialog open={rollbackTarget !== null} onOpenChange={(open) => { if (!open) setRollbackTarget(null); }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <AlertTriangle className="h-5 w-5 text-red-500" />
              回滚灰度发布
            </DialogTitle>
            <DialogDescription>
              回滚后将禁用灰度路由，所有用户恢复使用旧版本。
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2 py-4">
            <Label>回滚原因</Label>
            <Textarea
              value={rollbackReason}
              onChange={(e) => setRollbackReason(e.target.value)}
              placeholder="说明回滚原因，便于后续分析"
              rows={3}
            />
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setRollbackTarget(null)}>取消</Button>
            <Button variant="destructive" onClick={handleRollback}>确认回滚</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Promote Dialog */}
      <Dialog open={promoteTarget !== null} onOpenChange={(open) => { if (!open) setPromoteTarget(null); }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <ArrowUpCircle className="h-5 w-5 text-green-500" />
              全量发布
            </DialogTitle>
            <DialogDescription>
              确认灰度效果良好后，将候选版本全量推送到 100% 用户。
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setPromoteTarget(null)}>取消</Button>
            <Button onClick={handlePromote}>全量发布</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

// ─── Candidate Card Sub-component ────────────────────────

interface CandidateCardProps {
  candidate: EvolutionCandidate;
  metrics?: CanaryMetrics;
  expandedMetrics: boolean;
  onToggleMetrics: () => void;
  onApprove: () => void;
  onReject: () => void;
  onCanaryOpen: () => void;
  onRollbackOpen: () => void;
  onPromoteOpen: () => void;
}

function CandidateCard({
  candidate: c,
  metrics,
  expandedMetrics,
  onToggleMetrics,
  onApprove,
  onReject,
  onCanaryOpen,
  onRollbackOpen,
  onPromoteOpen,
}: CandidateCardProps) {
  const isRollout = c.status === "rollout";
  const isActive = c.status === "active";
  const isEvalRunning = c.status === "eval_running";

  return (
    <Card>
      <CardContent className="p-4">
        <div className="flex items-start justify-between gap-4">
          {/* Left: Info */}
          <div className="flex-1 min-w-0 space-y-2">
            <div className="flex items-center gap-2 flex-wrap">
              <span className="font-medium text-sm">{c.style_slug}</span>
              <Badge variant="outline">v{c.parent_version} → v{c.parent_version + 1}</Badge>
              <Badge className={STATUS_STYLES[c.status] ?? "bg-gray-100 text-gray-700"}>
                {isEvalRunning && <FlaskConical className="h-3 w-3 mr-1 animate-pulse" />}
                {isActive && <Check className="h-3 w-3 mr-1" />}
                {isRollout && <TrendingUp className="h-3 w-3 mr-1" />}
                {STATUS_LABELS[c.status] ?? c.status}
              </Badge>
              {c.rollout && (
                <Badge variant="outline" className="text-xs">
                  {c.rollout.percentage > 0 && c.rollout.percentage < 1
                    ? `${(c.rollout.percentage * 100).toFixed(0)}%`
                    : `${c.rollout.percentage}%`} 流量
                </Badge>
              )}
            </div>

            <div className="text-xs text-muted-foreground">
              ID: <span className="font-mono">{c.id.slice(0, 8)}</span>
              <span className="mx-2">·</span>
              创建于 {new Date(c.created_at).toLocaleString("zh-CN")}
            </div>

            {/* Changes preview */}
            {Object.keys(c.changes).length > 0 && (
              <div className="mt-2 p-2 bg-muted/30 rounded text-xs">
                <span className="text-muted-foreground">变更内容：</span>
                <pre className="mt-1 whitespace-pre-wrap font-mono text-muted-foreground max-h-32 overflow-y-auto">
                  {JSON.stringify(c.changes, null, 2).slice(0, 800)}
                </pre>
              </div>
            )}

            {/* Canary rollout info */}
            {c.rollout && (
              <div className="text-xs text-muted-foreground flex items-center gap-3">
                {c.rollout.started_at && (
                  <span>开始: {new Date(c.rollout.started_at).toLocaleString("zh-CN")}</span>
                )}
                {c.rollout.rollback_reason && (
                  <span className="text-red-500">回滚原因: {c.rollout.rollback_reason}</span>
                )}
              </div>
            )}

            {/* Metrics panel */}
            {isRollout && (
              <div className="mt-2">
                <button
                  onClick={onToggleMetrics}
                  className="flex items-center gap-1 text-xs text-blue-600 hover:underline"
                >
                  {expandedMetrics ? <ChevronDown className="h-3 w-3" /> : <ChevronRight className="h-3 w-3" />}
                  <Activity className="h-3 w-3" /> 灰度 Metrics
                </button>
                {expandedMetrics && metrics && (
                  <MetricsPanel metrics={metrics} />
                )}
                {expandedMetrics && !metrics && (
                  <p className="text-xs text-muted-foreground mt-1">加载中...</p>
                )}
              </div>
            )}
          </div>

          {/* Right: Actions */}
          <div className="flex flex-col gap-2 shrink-0 min-w-[120px]">
            {(c.status === "draft" || c.status === "rejected") && (
              <>
                <Button size="sm" variant="outline" className="gap-1.5" onClick={onApprove}>
                  <Check className="h-3.5 w-3.5" /> 批准
                </Button>
                {c.status === "draft" && (
                  <Button size="sm" variant="outline" className="gap-1.5 text-red-600" onClick={onReject}>
                    <X className="h-3.5 w-3.5" /> 拒绝
                  </Button>
                )}
              </>
            )}

            {isEvalRunning && (
              <div className="flex items-center gap-1.5 text-xs text-amber-600">
                <FlaskConical className="h-3.5 w-3.5 animate-pulse" />
                评测运行中...
              </div>
            )}

            {c.status === "approved" && (
              <Button size="sm" variant="outline" className="gap-1.5" onClick={onCanaryOpen}>
                <TrendingUp className="h-3.5 w-3.5" /> 灰度发布
              </Button>
            )}

            {isRollout && (
              <>
                <Button size="sm" variant="outline" className="gap-1.5 text-green-600" onClick={onPromoteOpen}>
                  <ArrowUpCircle className="h-3.5 w-3.5" /> 全量发布
                </Button>
                <Button size="sm" variant="outline" className="gap-1.5 text-red-600" onClick={onRollbackOpen}>
                  <ArrowDownCircle className="h-3.5 w-3.5" /> 回滚
                </Button>
              </>
            )}

            {isActive && (
              <Badge className="bg-green-50 text-green-700 border-green-200 gap-1.5 justify-center">
                <Check className="h-3.5 w-3.5" /> 已全量上线
              </Badge>
            )}
          </div>
        </div>
      </CardContent>
    </Card>
  );
}

// ─── Metrics Panel Sub-component ──────────────────────────

function MetricsPanel({ metrics }: { metrics: CanaryMetrics }) {
  const total = metrics.total || 1; // avoid div by zero
  const newPct = (metrics.new_version / total * 100).toFixed(1);
  const oldPct = (metrics.old_version / total * 100).toFixed(1);

  return (
    <div className="mt-2 p-3 bg-muted/20 rounded-lg space-y-2">
      <div className="grid grid-cols-4 gap-3 text-xs">
        <MetricBox icon={Activity} label="总请求" value={metrics.total} color="text-gray-600" />
        <MetricBox icon={TrendingUp} label="新版流量" value={metrics.new_version} sub={`${newPct}%`} color="text-blue-600" />
        <MetricBox icon={ArrowDownCircle} label="旧版流量" value={metrics.old_version} sub={`${oldPct}%`} color="text-gray-500" />
        <MetricBox icon={AlertTriangle} label="错误" value={metrics.errors} color={metrics.errors > 0 ? "text-red-500" : "text-gray-400"} />
      </div>
      {/* Traffic bar */}
      <div className="flex h-2 rounded-full overflow-hidden bg-muted">
        <div className="bg-blue-500" style={{ width: `${newPct}%` }} />
        <div className="bg-gray-300" style={{ width: `${oldPct}%` }} />
      </div>
      <div className="flex justify-between text-xs text-muted-foreground">
        <span className="flex items-center gap-1">
          <span className="w-2 h-2 rounded-full bg-blue-500" /> 新版 v{(metrics.new_version > 0) ? "+" : ""}1
        </span>
        <span className="flex items-center gap-1">
          <span className="w-2 h-2 rounded-full bg-gray-300" /> 旧版 (fallback)
        </span>
      </div>
    </div>
  );
}

function MetricBox({
  icon: Icon,
  label,
  value,
  sub,
  color,
}: {
  icon: typeof Activity;
  label: string;
  value: number;
  sub?: string;
  color: string;
}) {
  return (
    <div className="flex flex-col gap-0.5">
      <div className="flex items-center gap-1 text-muted-foreground">
        <Icon className="h-3 w-3" />
        {label}
      </div>
      <div className={`text-lg font-semibold ${color}`}>{value.toLocaleString()}</div>
      {sub && <div className="text-xs text-muted-foreground">{sub}</div>}
    </div>
  );
}

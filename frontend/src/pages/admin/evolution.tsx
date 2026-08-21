/**
 * 自演进管理 — Admin Dashboard
 *
 * 完整的自演进闭环门控 UI：
 * 1. 候选列表（含 eval gate 状态、eval 结果、canary metrics）
 * 2. 批准 → 自动触发 eval gate → 结果持久化
 * 3. 灰度发布（设置百分比 → 路由引擎生效）
 * 4. 灰度 metrics 面板（新旧版本流量分配）
 * 5. 回滚 / 全量发布
 * 6. 门控配置面板（阈值、自动回滚/自动全量）
 * 7. 门控事件审计时间线
 * 8. Canary 健康快照历史
 */
import { useState, useEffect, useCallback } from "react";
import {
  GitBranch, RefreshCw, Check, X, FlaskConical, TrendingUp,
  AlertTriangle, ArrowUpCircle, ArrowDownCircle, Activity,
  ChevronDown, ChevronRight, Settings, History, Shield,
  Clock, User, Zap, type LucideIcon,
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
  // Gate result fields
  eval_run_id?: string;
  eval_score?: number | null;
  eval_passed?: boolean | null;
  eval_completed_at?: string | null;
  rejected_reason?: string;
  approved_by?: string;
  approved_at?: string | null;
}

interface CanaryMetrics {
  total: number;
  new_version: number;
  old_version: number;
  whitelist: number;
  percentage: number;
  errors: number;
}

interface GateConfig {
  style_slug?: string;
  min_eval_score?: number;
  max_regression_drop?: number;
  error_rate_threshold?: number;
  min_sample_size?: number;
  observation_window_min?: number;
  auto_rollback_enabled?: boolean;
  auto_promote_enabled?: boolean;
  auto_promote_min_uptime?: number;
  auto_promote_window_min?: number;
  is_default?: boolean;
}

interface GateEvent {
  id: string;
  candidate_id: string;
  event_type: string;
  actor_id: string;
  actor_type: string;
  detail: string;
  metadata: Record<string, unknown>;
  created_at: string;
}

interface HealthSnapshot {
  id: string;
  candidate_id: string;
  style_slug: string;
  total_requests: number;
  new_version_hits: number;
  old_version_hits: number;
  error_count: number;
  error_rate: number;
  uptime_pct: number;
  triggered_rollback: boolean;
  rollback_reason?: string;
  captured_at: string;
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

const EVENT_LABELS: Record<string, string> = {
  approve_triggered: "批准触发",
  eval_passed:        "评测通过",
  eval_rejected:      "评测未通过",
  eval_failed:        "评测失败",
  manual_reject:      "手动拒绝",
  canary_enabled:     "灰度启动",
  manual_rollback:    "手动回滚",
  auto_rollback:      "自动回滚",
  promoted:           "全量发布",
  auto_promote:       "自动全量",
};

const EVENT_COLORS: Record<string, string> = {
  approve_triggered: "text-blue-600",
  eval_passed:        "text-emerald-600",
  eval_rejected:      "text-red-600",
  eval_failed:        "text-red-600",
  manual_reject:      "text-red-600",
  canary_enabled:     "text-blue-600",
  manual_rollback:    "text-orange-600",
  auto_rollback:      "text-red-600 font-semibold",
  promoted:           "text-green-600",
  auto_promote:       "text-green-600",
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
  const [gateConfigTarget, setGateConfigTarget] = useState<string | null>(null);
  const [gateConfigs, setGateConfigs] = useState<Record<string, GateConfig>>({});
  const [eventsMap, setEventsMap] = useState<Record<string, GateEvent[]>>({});
  const [expandedEvents, setExpandedEvents] = useState<string | null>(null);
  const [healthMap, setHealthMap] = useState<Record<string, HealthSnapshot[]>>({});
  const [expandedHealth, setExpandedHealth] = useState<string | null>(null);

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
    const { success, data } = await adminFetch<{ metrics: CanaryMetrics }>(
      `/api/v2/admin/evolution/candidates/${id}/metrics`,
      { silent: true }
    );
    if (success && data?.metrics) {
      setMetricsMap(prev => ({ ...prev, [id]: data.metrics }));
    }
  };

  const toggleEvents = async (id: string) => {
    if (expandedEvents === id) {
      setExpandedEvents(null);
      return;
    }
    setExpandedEvents(id);
    if (!eventsMap[id]) {
      const { success, data } = await adminFetch<{ events: GateEvent[]; total: number }>(
        `/api/v2/admin/evolution/candidates/${id}/events`,
        { silent: true }
      );
      if (success && data) {
        setEventsMap(prev => ({ ...prev, [id]: data.events ?? [] }));
      }
    }
  };

  const toggleHealth = async (id: string) => {
    if (expandedHealth === id) {
      setExpandedHealth(null);
      return;
    }
    setExpandedHealth(id);
    if (!healthMap[id]) {
      const { success, data } = await adminFetch<{ snapshots: HealthSnapshot[]; total: number }>(
        `/api/v2/admin/evolution/candidates/${id}/health`,
        { silent: true }
      );
      if (success && data) {
        setHealthMap(prev => ({ ...prev, [id]: data.snapshots ?? [] }));
      }
    }
  };

  const loadGateConfig = async (slug: string) => {
    const { success, data } = await adminFetch<GateConfig>(
      `/api/v2/admin/evolution/gate-config/${slug}`,
      { silent: true }
    );
    if (success && data) {
      setGateConfigs(prev => ({ ...prev, [slug]: data }));
    }
  };

  // ─── Render ─────────────────────────────────────────

  return (
    <div className="p-6 space-y-6">
      <AdminPageHeader
        title="自演进管理"
        description="风格 Profile 迭代候选 — 从反馈生成，经评测门控，到灰度发布与自动回滚"
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
          <div className="mt-2 flex items-center gap-2 text-xs text-muted-foreground flex-wrap">
            <Shield className="h-3 w-3" />
            <span>自动回滚监控：每 30s 检查错误率，超阈值自动回滚</span>
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
              events={eventsMap[c.id]}
              healthSnapshots={healthMap[c.id]}
              expandedMetrics={expandedMetrics === c.id}
              expandedEvents={expandedEvents === c.id}
              expandedHealth={expandedHealth === c.id}
              onToggleMetrics={() => toggleMetrics(c.id)}
              onToggleEvents={() => toggleEvents(c.id)}
              onToggleHealth={() => toggleHealth(c.id)}
              onApprove={() => handleApprove(c.id)}
              onReject={() => handleReject(c.id)}
              onCanaryOpen={() => { setCanaryTarget(c.id); setCanaryPct("10"); }}
              onRollbackOpen={() => { setRollbackTarget(c.id); setRollbackReason(""); }}
              onPromoteOpen={() => setPromoteTarget(c.id)}
              onGateConfigOpen={() => { setGateConfigTarget(c.style_slug); loadGateConfig(c.style_slug); }}
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

      {/* Gate Config Dialog */}
      <GateConfigDialog
        slug={gateConfigTarget}
        config={gateConfigTarget ? gateConfigs[gateConfigTarget] : undefined}
        onClose={() => setGateConfigTarget(null)}
        onSaved={(slug) => loadGateConfig(slug)}
      />
    </div>
  );
}

// ─── Candidate Card Sub-component ────────────────────────

interface CandidateCardProps {
  candidate: EvolutionCandidate;
  metrics?: CanaryMetrics;
  events?: GateEvent[];
  healthSnapshots?: HealthSnapshot[];
  expandedMetrics: boolean;
  expandedEvents: boolean;
  expandedHealth: boolean;
  onToggleMetrics: () => void;
  onToggleEvents: () => void;
  onToggleHealth: () => void;
  onApprove: () => void;
  onReject: () => void;
  onCanaryOpen: () => void;
  onRollbackOpen: () => void;
  onPromoteOpen: () => void;
  onGateConfigOpen: () => void;
}

function CandidateCard({
  candidate: c,
  metrics,
  events,
  healthSnapshots,
  expandedMetrics,
  expandedEvents,
  expandedHealth,
  onToggleMetrics,
  onToggleEvents,
  onToggleHealth,
  onApprove,
  onReject,
  onCanaryOpen,
  onRollbackOpen,
  onPromoteOpen,
  onGateConfigOpen,
}: CandidateCardProps) {
  const isRollout = c.status === "rollout";
  const isActive = c.status === "active";
  const isEvalRunning = c.status === "eval_running";
  const hasEvalResult = c.eval_completed_at != null;
  const isRejected = c.status === "rejected";

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
                  {c.rollout.percentage.toFixed(0)}% 流量
                </Badge>
              )}
            </div>

            <div className="text-xs text-muted-foreground">
              ID: <span className="font-mono">{c.id.slice(0, 8)}</span>
              <span className="mx-2">·</span>
              创建于 {new Date(c.created_at).toLocaleString("zh-CN")}
            </div>

            {/* Eval Gate Result */}
            {hasEvalResult && (
              <div className="mt-2 p-2 bg-muted/20 rounded text-xs space-y-1">
                <div className="flex items-center gap-2">
                  {c.eval_passed ? (
                    <Check className="h-3.5 w-3.5 text-emerald-500" />
                  ) : (
                    <X className="h-3.5 w-3.5 text-red-500" />
                  )}
                  <span className="font-medium">
                    评测{c.eval_passed ? "通过" : "未通过"}
                    {c.eval_score != null && (
                      <span className="ml-1 text-muted-foreground">
                        评分: <span className={c.eval_score >= 3.0 ? "text-emerald-600" : "text-red-600"}>
                          {c.eval_score.toFixed(2)}
                        </span>
                      </span>
                    )}
                  </span>
                  {c.eval_completed_at && (
                    <span className="text-muted-foreground">
                      · {new Date(c.eval_completed_at).toLocaleString("zh-CN")}
                    </span>
                  )}
                </div>
                {c.eval_run_id && (
                  <div className="text-muted-foreground">
                    评测运行 ID: <span className="font-mono">{c.eval_run_id.slice(0, 8)}</span>
                  </div>
                )}
                {c.approved_by && c.approved_at && (
                  <div className="text-muted-foreground">
                    批准者: {c.approved_by} · {new Date(c.approved_at).toLocaleString("zh-CN")}
                  </div>
                )}
              </div>
            )}

            {/* Rejected reason */}
            {isRejected && c.rejected_reason && (
              <div className="mt-1 p-2 bg-red-50 border border-red-200 rounded text-xs text-red-700">
                <AlertTriangle className="h-3 w-3 inline mr-1" />
                拒绝原因: {c.rejected_reason}
              </div>
            )}

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

            {/* Gate Events Timeline */}
            <div className="mt-2">
              <button
                onClick={onToggleEvents}
                className="flex items-center gap-1 text-xs text-blue-600 hover:underline"
              >
                {expandedEvents ? <ChevronDown className="h-3 w-3" /> : <ChevronRight className="h-3 w-3" />}
                <History className="h-3 w-3" /> 门控事件
              </button>
              {expandedEvents && events && (
                <GateEventTimeline events={events} />
              )}
              {expandedEvents && !events && (
                <p className="text-xs text-muted-foreground mt-1">加载中...</p>
              )}
            </div>

            {/* Health Snapshots */}
            {isRollout && (
              <div className="mt-1">
                <button
                  onClick={onToggleHealth}
                  className="flex items-center gap-1 text-xs text-blue-600 hover:underline"
                >
                  {expandedHealth ? <ChevronDown className="h-3 w-3" /> : <ChevronRight className="h-3 w-3" />}
                  <Shield className="h-3 w-3" /> 健康快照
                </button>
                {expandedHealth && healthSnapshots && (
                  <HealthSnapshotsList snapshots={healthSnapshots} />
                )}
                {expandedHealth && !healthSnapshots && (
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

            <Button size="sm" variant="ghost" className="gap-1.5 text-xs" onClick={onGateConfigOpen}>
              <Settings className="h-3.5 w-3.5" /> 门控配置
            </Button>
          </div>
        </div>
      </CardContent>
    </Card>
  );
}

// ─── Metrics Panel Sub-component ──────────────────────────

function MetricsPanel({ metrics }: { metrics: CanaryMetrics }) {
  const total = metrics.total || 1;
  const newPct = (metrics.new_version / total * 100).toFixed(1);
  const oldPct = (metrics.old_version / total * 100).toFixed(1);
  const errorRate = (metrics.errors / total * 100).toFixed(2);

  return (
    <div className="mt-2 p-3 bg-muted/20 rounded-lg space-y-2">
      <div className="grid grid-cols-5 gap-3 text-xs">
        <MetricBox icon={Activity} label="总请求" value={metrics.total} color="text-gray-600" />
        <MetricBox icon={TrendingUp} label="新版流量" value={metrics.new_version} sub={`${newPct}%`} color="text-blue-600" />
        <MetricBox icon={ArrowDownCircle} label="旧版流量" value={metrics.old_version} sub={`${oldPct}%`} color="text-gray-500" />
        <MetricBox icon={AlertTriangle} label="错误" value={metrics.errors} sub={`${errorRate}%`} color={metrics.errors > 0 ? "text-red-500" : "text-gray-400"} />
        <MetricBox icon={Zap} label="错误率" value={parseFloat(errorRate)} sub="%" color={parseFloat(errorRate) > 10 ? "text-red-500" : "text-emerald-500"} />
      </div>
      <div className="flex h-2 rounded-full overflow-hidden bg-muted">
        <div className="bg-blue-500" style={{ width: `${newPct}%` }} />
        <div className="bg-gray-300" style={{ width: `${oldPct}%` }} />
      </div>
      <div className="flex justify-between text-xs text-muted-foreground">
        <span className="flex items-center gap-1">
          <span className="w-2 h-2 rounded-full bg-blue-500" /> 新版 (灰度候选)
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
  icon: LucideIcon;
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

// ─── Gate Event Timeline ─────────────────────────────────

function GateEventTimeline({ events }: { events: GateEvent[] }) {
  if (events.length === 0) {
    return <p className="text-xs text-muted-foreground mt-1">暂无事件记录</p>;
  }

  return (
    <div className="mt-2 space-y-2">
      {events.map((e, i) => (
        <div key={e.id} className="flex gap-2 text-xs">
          {/* Timeline dot */}
          <div className="flex flex-col items-center">
            <div className={`w-2 h-2 rounded-full mt-1 ${
              e.event_type.includes("rollback") || e.event_type.includes("reject") || e.event_type.includes("failed")
                ? "bg-red-500"
                : e.event_type.includes("pass") || e.event_type.includes("promote")
                ? "bg-emerald-500"
                : "bg-blue-500"
            }`} />
            {i < events.length - 1 && <div className="w-px h-full min-h-[16px] bg-muted" />}
          </div>
          {/* Content */}
          <div className="flex-1 pb-2">
            <div className="flex items-center gap-2 flex-wrap">
              <span className={`font-medium ${EVENT_COLORS[e.event_type] ?? "text-gray-600"}`}>
                {EVENT_LABELS[e.event_type] ?? e.event_type}
              </span>
              <span className="text-muted-foreground flex items-center gap-1">
                <Clock className="h-3 w-3" />
                {new Date(e.created_at).toLocaleString("zh-CN")}
              </span>
              {e.actor_type === "admin" ? (
                <Badge variant="outline" className="text-xs py-0 px-1">
                  <User className="h-2.5 w-2.5 mr-0.5" /> {e.actor_id.slice(0, 12)}
                </Badge>
              ) : (
                <Badge variant="outline" className="text-xs py-0 px-1 bg-amber-50 text-amber-700">
                  <Zap className="h-2.5 w-2.5 mr-0.5" /> 自动
                </Badge>
              )}
            </div>
            <p className="text-muted-foreground mt-0.5">{e.detail}</p>
            {e.metadata && Object.keys(e.metadata).length > 0 && (
              <pre className="mt-0.5 text-muted-foreground/70 font-mono whitespace-pre-wrap">
                {JSON.stringify(e.metadata, null, 2).slice(0, 300)}
              </pre>
            )}
          </div>
        </div>
      ))}
    </div>
  );
}

// ─── Health Snapshots List ───────────────────────────────

function HealthSnapshotsList({ snapshots }: { snapshots: HealthSnapshot[] }) {
  if (snapshots.length === 0) {
    return <p className="text-xs text-muted-foreground mt-1">暂无健康快照</p>;
  }

  return (
    <div className="mt-2 space-y-1">
      {snapshots.slice(0, 20).map((s) => (
        <div key={s.id} className="flex items-center gap-3 p-2 bg-muted/20 rounded text-xs">
          <span className="text-muted-foreground whitespace-nowrap">
            {new Date(s.captured_at).toLocaleString("zh-CN")}
          </span>
          <span>请求: {s.total_requests}</span>
          <span className="text-blue-600">新版: {s.new_version_hits}</span>
          <span className="text-gray-500">旧版: {s.old_version_hits}</span>
          <span className={s.error_rate > 10 ? "text-red-500 font-medium" : "text-muted-foreground"}>
            错误率: {s.error_rate.toFixed(2)}%
          </span>
          <span className={s.uptime_pct < 99 ? "text-red-500" : "text-emerald-500"}>
            可用率: {s.uptime_pct.toFixed(2)}%
          </span>
          {s.triggered_rollback && (
            <Badge variant="outline" className="text-xs py-0 px-1 bg-red-50 text-red-700 border-red-200">
              <AlertTriangle className="h-2.5 w-2.5 mr-0.5" /> 触发回滚
            </Badge>
          )}
        </div>
      ))}
    </div>
  );
}

// ─── Gate Config Dialog ─────────────────────────────────

function GateConfigDialog({
  slug,
  config,
  onClose,
  onSaved,
}: {
  slug: string | null;
  config?: GateConfig;
  onClose: () => void;
  onSaved: (slug: string) => void;
}) {
  const [form, setForm] = useState<GateConfig>({});
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (config) {
      setForm(config);
    } else {
      setForm({});
    }
  }, [config]);

  const handleSave = async () => {
    if (!slug) return;
    setSaving(true);
    const { success } = await adminMutate(
      `/api/v2/admin/evolution/gate-config/${slug}`,
      {
        method: "PUT",
        body: JSON.stringify(form),
      }
    );
    if (success) {
      onSaved(slug);
      onClose();
    }
    setSaving(false);
  };

  const update = (key: keyof GateConfig, value: string | number | boolean) => {
    setForm(prev => ({ ...prev, [key]: value }));
  };

  return (
    <Dialog open={slug !== null} onOpenChange={(open) => { if (!open) onClose(); }}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Settings className="h-5 w-5" />
            门控配置 — {slug}
          </DialogTitle>
          <DialogDescription>
            配置评测门控阈值和灰度自动回滚/全量策略。留空使用默认值。
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4 py-2 max-h-[60vh] overflow-y-auto">
          {/* Eval Gate Section */}
          <div className="space-y-2">
            <Label className="text-sm font-semibold">评测门控</Label>
            <div className="grid grid-cols-2 gap-3">
              <div>
                <Label className="text-xs text-muted-foreground">最低评测分</Label>
                <Input
                  type="number"
                  step="0.1"
                  min="0" max="5"
                  value={form.min_eval_score ?? 3.0}
                  onChange={(e) => update("min_eval_score", parseFloat(e.target.value) || 3.0)}
                />
              </div>
              <div>
                <Label className="text-xs text-muted-foreground">最大回归降幅</Label>
                <Input
                  type="number"
                  step="0.05"
                  min="0" max="1"
                  value={form.max_regression_drop ?? 0.3}
                  onChange={(e) => update("max_regression_drop", parseFloat(e.target.value) || 0.3)}
                />
              </div>
            </div>
          </div>

          {/* Auto-Rollback Section */}
          <div className="space-y-2">
            <Label className="text-sm font-semibold flex items-center gap-2">
              <Shield className="h-3.5 w-3.5" /> 自动回滚
            </Label>
            <div className="grid grid-cols-2 gap-3">
              <div>
                <Label className="text-xs text-muted-foreground">错误率阈值 (%)</Label>
                <Input
                  type="number"
                  step="1"
                  min="0" max="100"
                  value={form.error_rate_threshold ?? 10}
                  onChange={(e) => update("error_rate_threshold", parseFloat(e.target.value) || 10)}
                />
              </div>
              <div>
                <Label className="text-xs text-muted-foreground">最小样本量</Label>
                <Input
                  type="number"
                  min="1"
                  value={form.min_sample_size ?? 50}
                  onChange={(e) => update("min_sample_size", parseInt(e.target.value) || 50)}
                />
              </div>
              <div>
                <Label className="text-xs text-muted-foreground">观察窗口 (分钟)</Label>
                <Input
                  type="number"
                  min="1"
                  value={form.observation_window_min ?? 10}
                  onChange={(e) => update("observation_window_min", parseInt(e.target.value) || 10)}
                />
              </div>
              <div className="flex items-end">
                <label className="flex items-center gap-2 text-xs cursor-pointer">
                  <input
                    type="checkbox"
                    checked={form.auto_rollback_enabled ?? true}
                    onChange={(e) => update("auto_rollback_enabled", e.target.checked)}
                    className="rounded border-gray-300"
                  />
                  启用自动回滚
                </label>
              </div>
            </div>
          </div>

          {/* Auto-Promote Section */}
          <div className="space-y-2">
            <Label className="text-sm font-semibold flex items-center gap-2">
              <ArrowUpCircle className="h-3.5 w-3.5" /> 自动全量
            </Label>
            <div className="grid grid-cols-2 gap-3">
              <div>
                <Label className="text-xs text-muted-foreground">最低可用率 (%)</Label>
                <Input
                  type="number"
                  step="0.1"
                  min="0" max="100"
                  value={form.auto_promote_min_uptime ?? 99}
                  onChange={(e) => update("auto_promote_min_uptime", parseFloat(e.target.value) || 99)}
                />
              </div>
              <div>
                <Label className="text-xs text-muted-foreground">观察窗口 (分钟)</Label>
                <Input
                  type="number"
                  min="1"
                  value={form.auto_promote_window_min ?? 30}
                  onChange={(e) => update("auto_promote_window_min", parseInt(e.target.value) || 30)}
                />
              </div>
              <div className="flex items-end col-span-2">
                <label className="flex items-center gap-2 text-xs cursor-pointer">
                  <input
                    type="checkbox"
                    checked={form.auto_promote_enabled ?? false}
                    onChange={(e) => update("auto_promote_enabled", e.target.checked)}
                    className="rounded border-gray-300"
                  />
                  启用自动全量发布（灰度稳定后自动提升至100%）
                </label>
              </div>
            </div>
          </div>

          {config?.is_default && (
            <p className="text-xs text-muted-foreground bg-amber-50 border border-amber-200 rounded p-2">
              当前使用默认配置，保存后将创建自定义配置。
            </p>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button onClick={handleSave} disabled={saving}>
            {saving ? <RefreshCw className="h-4 w-4 mr-2 animate-spin" /> : null}
            保存配置
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
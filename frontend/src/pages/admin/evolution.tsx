import { useState, useEffect, useCallback } from "react";
import { GitBranch, RefreshCw, Check, X, FlaskConical, TrendingUp } from "lucide-react";
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

interface EvolutionCandidate {
  id: string;
  style_slug: string;
  parent_version: number;
  changes: Record<string, unknown>;
  status: string;
  eval_baseline_id?: string;
  eval_candidate_id?: string;
  created_at: string;
}

const STATUS_STYLES: Record<string, string> = {
  draft: "bg-gray-100 text-gray-700 border-gray-200",
  approved: "bg-emerald-50 text-emerald-700 border-emerald-200",
  rejected: "bg-red-50 text-red-700 border-red-200",
  eval_running: "bg-amber-50 text-amber-700 border-amber-200",
  rollout: "bg-blue-50 text-blue-700 border-blue-200",
};

const STATUS_LABELS: Record<string, string> = {
  draft: "草稿",
  approved: "已批准",
  rejected: "已拒绝",
  eval_running: "评测中",
  rollout: "灰度发布",
};

export function EvolutionPage() {
  const [candidates, setCandidates] = useState<EvolutionCandidate[]>([]);
  const [loading, setLoading] = useState(true);
  const [canaryTarget, setCanaryTarget] = useState<string | null>(null);
  const [canaryPct, setCanaryPct] = useState("10");

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

  const handleApprove = async (id: string) => {
    const { success } = await adminMutate(`/api/v2/admin/evolution/candidates/${id}/approve`, {});
    if (success) { await loadCandidates(); }
  };

  const handleReject = async (id: string) => {
    const { success } = await adminMutate(`/api/v2/admin/evolution/candidates/${id}/reject`, { method: "POST", body: JSON.stringify({ reason: "admin rejected" }) });
    if (success) { await loadCandidates(); }
  };

  const handleCanary = async () => {
    if (!canaryTarget) return;
    const pct = parseFloat(canaryPct) || 10;
    const { success } = await adminMutate(`/api/v2/admin/evolution/candidates/${canaryTarget}/canary`, { method: "POST", body: JSON.stringify({ percentage: pct }) });
    if (success) { setCanaryTarget(null); await loadCandidates(); }
  };

  return (
    <div className="p-6 space-y-6">
      <AdminPageHeader
        title="自演进管理"
        description="风格 Profile 迭代候选 — 从反馈生成，经评测门控，到灰度发布"
        action={<Button variant="outline" size="sm" onClick={loadCandidates} disabled={loading}>
          <RefreshCw className={`h-4 w-4 mr-2 ${loading ? "animate-spin" : ""}`} /> 刷新
        </Button>}
      />

      {/* 流程说明 */}
      <Card>
        <CardContent className="p-4">
          <div className="flex items-center gap-2 mb-3">
            <GitBranch className="h-4 w-4" />
            <h3 className="text-sm font-semibold">自演进流程</h3>
          </div>
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            <Badge variant="outline">1. 反馈聚合</Badge>
            <span>→</span>
            <Badge variant="outline">2. 候选生成</Badge>
            <span>→</span>
            <Badge variant="outline">3. 评测门控</Badge>
            <span>→</span>
            <Badge variant="outline">4. 审批</Badge>
            <span>→</span>
            <Badge variant="outline">5. 灰度发布</Badge>
            <span>→</span>
            <Badge variant="outline">6. 全量上线</Badge>
          </div>
        </CardContent>
      </Card>

      {loading ? <AdminLoading /> : candidates.length === 0 ? (
        <AdminEmptyState
          icon={GitBranch}
          title="暂无迭代候选"
          description="当用户反馈聚合到一定程度后，系统会自动生成风格 Profile 迭代候选"
        />
      ) : (
        <div className="space-y-3">
          {candidates.map((c) => (
            <Card key={c.id}>
              <CardContent className="p-4">
                <div className="flex items-start justify-between gap-4">
                  <div className="flex-1 min-w-0 space-y-2">
                    <div className="flex items-center gap-2">
                      <span className="font-medium text-sm">{c.style_slug}</span>
                      <Badge variant="outline">v{c.parent_version} → v{c.parent_version + 1}</Badge>
                      <Badge className={STATUS_STYLES[c.status] ?? "bg-gray-100 text-gray-700"}>
                        {STATUS_LABELS[c.status] ?? c.status}
                      </Badge>
                    </div>
                    <div className="text-xs text-muted-foreground">
                      ID: <span className="font-mono">{c.id.slice(0, 8)}</span>
                      <span className="mx-2">·</span>
                      创建于 {new Date(c.created_at).toLocaleString("zh-CN")}
                    </div>
                    {Object.keys(c.changes).length > 0 && (
                      <div className="mt-2 p-2 bg-muted/30 rounded text-xs">
                        <span className="text-muted-foreground">变更内容：</span>
                        <pre className="mt-1 whitespace-pre-wrap font-mono text-muted-foreground">
                          {JSON.stringify(c.changes, null, 2).slice(0, 500)}
                        </pre>
                      </div>
                    )}
                  </div>

                  {/* 操作按钮 */}
                  <div className="flex flex-col gap-2 shrink-0">
                    {c.status === "draft" && (
                      <>
                        <Button size="sm" variant="outline" className="gap-1.5" onClick={() => handleApprove(c.id)}>
                          <Check className="h-3.5 w-3.5" /> 批准
                        </Button>
                        <Button size="sm" variant="outline" className="gap-1.5 text-red-600" onClick={() => handleReject(c.id)}>
                          <X className="h-3.5 w-3.5" /> 拒绝
                        </Button>
                      </>
                    )}
                    {c.status === "approved" && (
                      <Dialog open={canaryTarget === c.id} onOpenChange={(open) => {
                        if (open) { setCanaryTarget(c.id); setCanaryPct("10"); }
                        else { setCanaryTarget(null); }
                      }}>
                        <DialogTrigger asChild>
                          <Button size="sm" variant="outline" className="gap-1.5">
                            <TrendingUp className="h-3.5 w-3.5" /> 灰度发布
                          </Button>
                        </DialogTrigger>
                        <DialogContent>
                          <DialogHeader>
                            <DialogTitle>灰度发布 — {c.style_slug} v{c.parent_version + 1}</DialogTitle>
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
                          </div>
                          <DialogFooter>
                            <Button variant="outline" onClick={() => setCanaryTarget(null)}>取消</Button>
                            <Button onClick={handleCanary}>开始灰度</Button>
                          </DialogFooter>
                        </DialogContent>
                      </Dialog>
                    )}
                    {c.status === "rollout" && (
                      <Badge className="bg-blue-50 text-blue-700 border-blue-200 gap-1.5">
                        <FlaskConical className="h-3.5 w-3.5" /> 灰度中
                      </Badge>
                    )}
                  </div>
                </div>
              </CardContent>
            </Card>
          ))}
        </div>
      )}
    </div>
  );
}

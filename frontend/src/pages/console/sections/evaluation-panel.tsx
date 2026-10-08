import { useCallback, useEffect, useRef, useState } from "react";
import {
  EvalBadcases,
  EvalCandidates,
  EvalCenterShell,
  EvalDatasets,
  EvalErrorState,
  EvalLoadingState,
  EvalOverview,
  EvalRelease,
  EvalReviews,
  EvalRuns,
} from "@/components/evaluation";
import { useAdminPermission } from "@/hooks/use-admin-permission";
import { adminFetch, adminMutate } from "@/lib/admin-api";
import type {
  WABenchBadcaseItem,
  WABenchCandidateItem,
  WABenchListResponse,
  WABenchOverview,
  WABenchReleaseItem,
  WABenchReviewItem,
  WABenchRunItem,
  WABenchSuiteItem,
} from "@/lib/admin-types";
import type { WABenchWorkspace } from "@/lib/wabench-eval";

interface EvalCenterData {
  overview: WABenchOverview;
  suites: WABenchSuiteItem[];
  candidates: WABenchCandidateItem[];
  runs: WABenchRunItem[];
  reviews: WABenchReviewItem[];
  badcases: WABenchBadcaseItem[];
  releases: WABenchReleaseItem[];
}

const emptyOverview: WABenchOverview = {
  suiteCount: 0,
  candidateCount: 0,
  runCount: 0,
  reviewCount: 0,
  runningCount: 0,
  failedRunCount: 0,
  averageScore: null,
  hardFailureRate: null,
  acceptanceRate: null,
  modificationBurden: null,
  p50LatencyMs: null,
  p95LatencyMs: null,
  sourceBoundaryFailureCount: 0,
  latestGateDecision: "",
  costStatus: "unavailable",
};

const initialData: EvalCenterData = {
  overview: emptyOverview,
  suites: [],
  candidates: [],
  runs: [],
  reviews: [],
  badcases: [],
  releases: [],
};

export function EvaluationPage() {
  const { can, isAdmin } = useAdminPermission();
  const canViewEvaluation = can("evaluation", "view");
  const [workspace, setWorkspace] = useState<WABenchWorkspace>("overview");
  const [data, setData] = useState<EvalCenterData>(initialData);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState("");
  const [permissionDenied, setPermissionDenied] = useState(false);
  const [seeding, setSeeding] = useState(false);
  const initialLoadStarted = useRef(false);

  const loadCenter = useCallback(async (initial = false) => {
    if (!canViewEvaluation) {
      setPermissionDenied(true);
      setLoading(false);
      return;
    }
    if (initial) setLoading(true); else setRefreshing(true);
    setError("");
    const [overview, suites, candidates, runs, reviews, badcases, releases] = await Promise.all([
      adminFetch<WABenchOverview>("/api/v2/admin/evaluation/wabench/overview", { silent: true }),
      adminFetch<WABenchListResponse<WABenchSuiteItem>>("/api/v2/admin/evaluation/wabench/suites?limit=200", { silent: true }),
      adminFetch<WABenchListResponse<WABenchCandidateItem>>("/api/v2/admin/evaluation/wabench/candidates?limit=200", { silent: true }),
      adminFetch<WABenchListResponse<WABenchRunItem>>("/api/v2/admin/evaluation/wabench/runs?limit=200", { silent: true }),
      adminFetch<WABenchListResponse<WABenchReviewItem>>("/api/v2/admin/evaluation/wabench/reviews?limit=500", { silent: true }),
      adminFetch<WABenchListResponse<WABenchBadcaseItem>>("/api/v2/admin/evaluation/wabench/badcases?limit=300", { silent: true }),
      adminFetch<WABenchListResponse<WABenchReleaseItem>>("/api/v2/admin/evaluation/wabench/releases?limit=100", { silent: true }),
    ]);
    const results = [overview, suites, candidates, runs, reviews, badcases, releases];
    const denied = results.some((result) => result.error?.code === "forbidden" || result.error?.code === "unauthorized");
    if (denied) {
      setPermissionDenied(true);
    } else if (results.some((result) => !result.success)) {
      const firstFailure = results.find((result) => !result.success);
      setError(firstFailure?.error?.message ?? "无法读取 WABench 数据，请检查数据库和 Admin 权限。");
    } else {
      setData({
        overview: overview.data ?? emptyOverview,
        suites: suites.data?.items ?? [],
        candidates: candidates.data?.items ?? [],
        runs: runs.data?.items ?? [],
        reviews: reviews.data?.items ?? [],
        badcases: badcases.data?.items ?? [],
        releases: releases.data?.items ?? [],
      });
    }
    setLoading(false);
    setRefreshing(false);
  }, [canViewEvaluation]);

  useEffect(() => {
    if (initialLoadStarted.current) return;
    initialLoadStarted.current = true;
    void loadCenter(true);
  }, [loadCenter]);

  const createRun = async (suiteId: string, candidateId: string, environment: string) => {
    if (!isAdmin) {
      setError("当前角色只能查看评测证据，无法启动运行。");
      return;
    }
    const result = await adminMutate<WABenchRunItem>("/api/v2/admin/evaluation/wabench/runs", {
      method: "POST",
      body: JSON.stringify({ suiteId, candidateId, environment }),
      successTitle: "Shadow Run 已启动",
      successDesc: `${candidateId} → ${suiteId}`,
    });
    if (result.success) await loadCenter(false);
  };

  const seedRedTeam = async () => {
    if (!isAdmin || seeding) return;
    setSeeding(true);
    try {
      const result = await adminMutate("/api/v2/admin/evaluation/wabench/red-team/seed", {
        method: "POST",
        successTitle: "红队套件已校验",
        successDesc: "独立 20 条红队样本已就绪",
      });
      if (result.success) await loadCenter(false);
    } finally { setSeeding(false); }
  };

  const gateDecision = data.releases[0]?.decision ?? data.overview.latestGateDecision;
  const renderWorkspace = () => {
    if (loading) return <EvalLoadingState />;
    if (permissionDenied) return <EvalErrorState permissionDenied message="请使用具有 evaluation:view 权限的管理员账号。" />;
    if (error) return <EvalErrorState message={error} onRetry={() => void loadCenter(false)} />;
    switch (workspace) {
      case "overview": return <EvalOverview overview={data.overview} runs={data.runs} releases={data.releases} onNavigate={setWorkspace} />;
      case "datasets": return <EvalDatasets items={data.suites} onSeedRedTeam={() => void seedRedTeam()} seeding={seeding} canMutate={isAdmin} />;
      case "candidates": return <EvalCandidates items={data.candidates} />;
      case "runs": return <EvalRuns runs={data.runs} suites={data.suites} candidates={data.candidates} onCreateRun={createRun} canMutate={isAdmin} />;
      case "reviews": return <EvalReviews items={data.reviews} onImported={() => loadCenter(false)} canMutate={isAdmin} />;
      case "badcases": return <EvalBadcases items={data.badcases} />;
      case "release": return <EvalRelease items={data.releases} />;
    }
  };

  return (
    <EvalCenterShell
      workspace={workspace}
      onWorkspaceChange={setWorkspace}
      onRefresh={() => void loadCenter(false)}
      refreshing={refreshing}
      candidate={data.candidates[0]}
      latestRun={data.runs[0]}
      gateDecision={gateDecision}
    >
      {renderWorkspace()}
    </EvalCenterShell>
  );
}

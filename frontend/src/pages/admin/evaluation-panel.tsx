/**
 * 评测面板页面 — Admin Dashboard
 */
import { useState, useEffect, useCallback } from "react";
import { Play, Plus, FileText, CheckCircle, Clock, XCircle, Loader2, Shield, ShieldAlert, FlaskConical, Zap } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter, DialogTrigger,
} from "@/components/ui/dialog";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { adminFetch, adminMutate } from "@/lib/admin-api";
import { AdminPageHeader } from "@/components/admin";

interface EvalSet {
  id: string;
  name: string;
  style_slug: string;
  description: string;
  status: string;
  sample_count: number;
  created_at: string;
}

interface EvalRun {
  id: string;
  set_id: string;
  profile_slug: string;
  profile_version: number;
  trigger_type: string;
  trigger_detail: string;
  status: string;
  total_samples: number;
  completed_count: number;
  overall_score: number;
  dimension_scores: Record<string, number>;
  results: Array<{
    sample_id: string;
    topic: string;
    scores: Record<string, number>;
    weighted: number;
    judge_feedback: string;
    article_preview: string;
  }>;
  started_at: string | null;
  completed_at: string | null;
  created_at: string;
}

const STYLES = ["yinyue", "shenlun", "xiaohongshu"];

interface RedTeamCase {
  id: string;
  category: string;
  attack_vector: string;
  input_prompt: string;
  expected_behavior: string;
  pass_criteria: string[];
  fail_indicators: string[];
  severity: string;
}

const SEVERITY_COLORS: Record<string, string> = {
  critical: "bg-red-100 text-red-700 border-red-200",
  high: "bg-orange-100 text-orange-700 border-orange-200",
  medium: "bg-yellow-100 text-yellow-700 border-yellow-200",
  low: "bg-blue-100 text-blue-700 border-blue-200",
};

const CATEGORY_LABELS: Record<string, string> = {
  prompt_injection: "Prompt 注入",
  search_injection: "搜索结果注入",
  info_extraction: "信息提取",
  instruction_override: "指令覆盖",
  content_policy: "内容策略违规",
  tool_misuse: "工具滥用",
};

export function EvaluationPage() {
  const [sets, setSets] = useState<EvalSet[]>([]);
  const [selectedSet, setSelectedSet] = useState<EvalSet | null>(null);
  const [runs, setRuns] = useState<EvalRun[]>([]);
  const [selectedRun, setSelectedRun] = useState<EvalRun | null>(null);
  const [loading, setLoading] = useState(false);
  const [creating, setCreating] = useState(false);

  // Create dialog state
  const [newSetName, setNewSetName] = useState("");
  const [newSetStyle, setNewSetStyle] = useState("yinyue");
  const [newSetDesc, setNewSetDesc] = useState("");
  const [newSetSamples, setNewSetSamples] = useState("");

  // Red-team state
  const [showRedTeam, setShowRedTeam] = useState(false);
  const [redTeamCases, setRedTeamCases] = useState<RedTeamCase[]>([]);
  const [redTeamRunning, setRedTeamRunning] = useState(false);
  const [redTeamSeeding, setRedTeamSeeding] = useState(false);
  const [redTeamMessage, setRedTeamMessage] = useState("");

  const loadSets = useCallback(async () => {
    setLoading(true);
    const { success, data } = await adminFetch<{ sets: EvalSet[] }>("/api/v2/evaluation/sets", { silent: true });
    if (success && data) setSets(data.sets ?? []);
    setLoading(false);
  }, []);

  const loadRuns = async (setId: string) => {
    const { success, data } = await adminFetch<{ runs: EvalRun[] }>(`/api/v2/evaluation/runs?set_id=${setId}`, { silent: true });
    if (success && data) setRuns(data.runs ?? []);
  };

  const handleCreateSet = async () => {
    setCreating(true);
    const samples = newSetSamples
      .split("\n")
      .filter((s) => s.trim())
      .map((s) => {
        const [topic, prompt] = s.split("|").map((p) => p.trim());
        return { topic: topic || s.trim(), input_prompt: prompt || topic || s.trim(), style_slug: newSetStyle };
      });

    const { success } = await adminMutate("/api/v2/evaluation/sets", {
      method: "POST",
      body: JSON.stringify({
        name: newSetName,
        style_slug: newSetStyle,
        description: newSetDesc,
        samples,
      }),
      successTitle: "评测集已创建",
      successDesc: newSetName,
    });
    if (success) {
      await loadSets();
      setNewSetName("");
      setNewSetDesc("");
      setNewSetSamples("");
    }
    setCreating(false);
  };

  const handleRunEvaluation = async (setId: string) => {
    const { success } = await adminMutate("/api/v2/evaluation/runs", {
      method: "POST",
      body: JSON.stringify({
        set_id: setId,
        profile_slug: selectedSet?.style_slug ?? "yinyue",
        profile_version: 1,
        trigger_type: "manual",
      }),
      successTitle: "评测已启动",
      successDesc: "结果将在运行完成后显示",
    });
    if (success) setTimeout(() => loadRuns(setId), 2000);
  };

  const handleSelectSet = async (set: EvalSet) => {
    setSelectedSet(set);
    setSelectedRun(null);
    await loadRuns(set.id);
  };

  const handleSelectRun = async (runId: string) => {
    const { success, data } = await adminFetch<EvalRun>(`/api/v2/evaluation/runs/${runId}`, { silent: true });
    if (success && data) setSelectedRun(data);
  };

  const loadRedTeamCases = useCallback(async () => {
    const { success, data } = await adminFetch<{ cases: RedTeamCase[] }>("/api/v2/evaluation/redteam/cases", { silent: true });
    if (success && data) setRedTeamCases(data.cases ?? []);
  }, []);

  const handleSeedRedTeam = async () => {
    setRedTeamSeeding(true);
    setRedTeamMessage("");
    const { success, error } = await adminMutate("/api/v2/evaluation/redteam/seed", {
      method: "POST",
      successTitle: "红队评估集已写入",
    });
    if (success) {
      setRedTeamMessage("红队评估集已成功写入数据库");
      await loadSets();
    } else {
      setRedTeamMessage("写入失败: " + (error?.message ?? "未知错误"));
    }
    setRedTeamSeeding(false);
    setTimeout(() => setRedTeamMessage(""), 5000);
  };

  const handleRunRedTeam = async () => {
    setRedTeamRunning(true);
    setRedTeamMessage("");
    const { success, data, error } = await adminMutate<{ cases: number }>("/api/v2/evaluation/redteam/run", {
      method: "POST",
      body: JSON.stringify({}),
      successTitle: "红队评估已启动",
    });
    if (success) {
      setRedTeamMessage(`红队评估已启动，共 ${data?.cases ?? 20} 个测试用例，预计运行 ~15 分钟，结果请查看后端日志`);
    } else {
      setRedTeamMessage("启动失败: " + (error?.message ?? "未知错误"));
    }
    setRedTeamRunning(false);
    setTimeout(() => setRedTeamMessage(""), 8000);
  };

  useEffect(() => {
    loadSets();
  }, [loadSets]);

  useEffect(() => {
    if (showRedTeam && redTeamCases.length === 0) {
      loadRedTeamCases();
    }
  }, [showRedTeam, redTeamCases.length, loadRedTeamCases]);

  return (
    <div className="p-6 space-y-6">
      <AdminPageHeader
        title="评测面板"
        action={
          <div className="flex items-center gap-2">
            <Button
              size="sm"
              variant={showRedTeam ? "default" : "outline"}
              onClick={() => setShowRedTeam(!showRedTeam)}
            >
              <Shield className="h-4 w-4 mr-2" />
              {showRedTeam ? "返回常规评测" : "红队安全评估"}
            </Button>
            {!showRedTeam && (
              <Dialog>
                <DialogTrigger asChild>
                  <Button size="sm">
                    <Plus className="h-4 w-4 mr-2" />
                    创建评测集
                  </Button>
                </DialogTrigger>
                <DialogContent className="max-w-2xl">
                  <DialogHeader>
                    <DialogTitle>创建评测集</DialogTitle>
                  </DialogHeader>
                  <div className="space-y-4">
                    <div className="grid grid-cols-2 gap-4">
                      <div>
                        <Label>名称</Label>
                        <Input value={newSetName} onChange={(e) => setNewSetName(e.target.value)} placeholder="如：预设评论风格-标准评测" />
                      </div>
                      <div>
                        <Label>风格</Label>
                        <Select value={newSetStyle} onValueChange={setNewSetStyle}>
                          <SelectTrigger><SelectValue /></SelectTrigger>
                          <SelectContent>
                            {STYLES.map((s) => <SelectItem key={s} value={s}>{s}</SelectItem>)}
                          </SelectContent>
                        </Select>
                      </div>
                    </div>
                    <div>
                      <Label>描述</Label>
                      <Input value={newSetDesc} onChange={(e) => setNewSetDesc(e.target.value)} placeholder="评测集用途说明" />
                    </div>
                    <div>
                      <Label>评测样本（每行一条，格式：题目 | 完整输入提示）</Label>
                      <Textarea
                        value={newSetSamples}
                        onChange={(e) => setNewSetSamples(e.target.value)}
                        placeholder={"写一篇关于外卖骑手的评论 | 写一篇关于外卖骑手闯红灯现象的评论文章，1000-1500字\n写一篇关于城市垃圾分类的评论 | 写一篇关于城市垃圾分类政策的评论文章"}
                        rows={6}
                      />
                    </div>
                  </div>
                  <DialogFooter>
                    <Button onClick={handleCreateSet} disabled={!newSetName || creating}>
                      {creating ? "创建中..." : "创建"}
                    </Button>
                  </DialogFooter>
                </DialogContent>
              </Dialog>
            )}
          </div>
        }
      />

      {/* Red-Team Security Evaluation Panel */}
      {showRedTeam ? (
        <div className="space-y-4">
          {/* Red-team action bar */}
          <Card>
            <CardContent className="p-4">
              <div className="flex flex-wrap items-center gap-3">
                <div className="flex items-center gap-2 text-sm text-muted-foreground">
                  <ShieldAlert className="h-4 w-4 text-red-500" />
                  <span>红队安全评估 — 对抗性测试覆盖 Prompt 注入、信息泄露、内容策略违规等攻击场景</span>
                </div>
                <div className="flex-1" />
                <Button
                  size="sm"
                  variant="outline"
                  onClick={handleSeedRedTeam}
                  disabled={redTeamSeeding}
                >
                  <FlaskConical className="h-4 w-4 mr-2" />
                  {redTeamSeeding ? "写入中..." : "写入评估集"}
                </Button>
                <Button
                  size="sm"
                  variant="destructive"
                  onClick={handleRunRedTeam}
                  disabled={redTeamRunning}
                >
                  <Zap className="h-4 w-4 mr-2" />
                  {redTeamRunning ? "启动中..." : "运行红队评估"}
                </Button>
              </div>
              {redTeamMessage && (
                <div className="mt-3 text-sm text-muted-foreground bg-muted/50 rounded-md px-3 py-2">
                  {redTeamMessage}
                </div>
              )}
            </CardContent>
          </Card>

          {/* Red-team cases grid */}
          <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
            {redTeamCases.map((tc) => (
              <Card key={tc.id} className="overflow-hidden">
                <CardContent className="p-4 space-y-2">
                  <div className="flex items-center justify-between">
                    <div className="flex items-center gap-2">
                      <Badge variant="outline" className="text-xs font-mono">{tc.id}</Badge>
                      <Badge
                        variant="outline"
                        className={`text-xs ${SEVERITY_COLORS[tc.severity] ?? ""}`}
                      >
                        {tc.severity}
                      </Badge>
                    </div>
                    <span className="text-xs text-muted-foreground">
                      {CATEGORY_LABELS[tc.category] ?? tc.category}
                    </span>
                  </div>
                  <div>
                    <p className="text-xs font-medium text-muted-foreground">攻击向量</p>
                    <p className="text-sm font-mono mt-0.5">{tc.attack_vector}</p>
                  </div>
                  <div>
                    <p className="text-xs font-medium text-muted-foreground">对抗输入</p>
                    <p className="text-sm mt-0.5 line-clamp-2 text-foreground/80">{tc.input_prompt}</p>
                  </div>
                  <div>
                    <p className="text-xs font-medium text-muted-foreground">期望行为</p>
                    <p className="text-xs mt-0.5 text-foreground/70">{tc.expected_behavior}</p>
                  </div>
                  <div className="flex flex-wrap gap-1">
                    {tc.pass_criteria?.map((c, i) => (
                      <span key={i} className="text-xs px-1.5 py-0.5 rounded bg-green-50 text-green-700 border border-green-200">
                        ✓ {c}
                      </span>
                    ))}
                  </div>
                  {tc.fail_indicators && tc.fail_indicators.length > 0 && (
                    <div className="flex flex-wrap gap-1">
                      {tc.fail_indicators.map((f, i) => (
                        <span key={i} className="text-xs px-1.5 py-0.5 rounded bg-red-50 text-red-700 border border-red-200">
                          ✗ {f}
                        </span>
                      ))}
                    </div>
                  )}
                </CardContent>
              </Card>
            ))}
          </div>

          {redTeamCases.length === 0 && (
            <div className="text-center py-12 text-muted-foreground">
              <Shield className="h-8 w-8 mx-auto mb-2 opacity-50" />
              <p className="text-sm">加载红队用例中...</p>
            </div>
          )}
        </div>
      ) : (
      <div className="grid grid-cols-12 gap-6">
        {/* 左侧：评测集列表 */}
        <div className="col-span-4 space-y-2">
          <h3 className="text-sm font-medium text-muted-foreground mb-2">评测集</h3>
          {loading && sets.length === 0 ? (
            <div className="flex items-center justify-center py-8 text-muted-foreground">
              <Loader2 className="h-4 w-4 animate-spin mr-2" /> 加载中...
            </div>
          ) : sets.length === 0 ? (
            <div className="text-center py-8 text-muted-foreground text-sm">
              暂无评测集，点击"创建评测集"添加
            </div>
          ) : (
            sets.map((set) => (
              <Card
                key={set.id}
                className={`cursor-pointer transition-colors ${selectedSet?.id === set.id ? "ring-2 ring-primary" : ""}`}
                onClick={() => handleSelectSet(set)}
              >
                <CardContent className="p-3">
                  <div className="flex items-start justify-between">
                    <div>
                      <p className="text-sm font-medium">{set.name}</p>
                      <p className="text-xs text-muted-foreground">{set.style_slug} · {set.sample_count} 样本</p>
                    </div>
                    <Badge variant="outline">{set.status}</Badge>
                  </div>
                </CardContent>
              </Card>
            ))
          )}
        </div>

        {/* 右侧：运行记录 + 详情 */}
        <div className="col-span-8 space-y-4">
          {selectedSet && (
            <div className="flex items-center justify-between">
              <div>
                <h3 className="font-medium">{selectedSet.name}</h3>
                <p className="text-xs text-muted-foreground">{selectedSet.description || "无描述"}</p>
              </div>
              <Button size="sm" onClick={() => handleRunEvaluation(selectedSet.id)}>
                <Play className="h-4 w-4 mr-2" />
                运行评测
              </Button>
            </div>
          )}

          {/* 运行记录列表 */}
          {runs.length > 0 && (
            <div className="space-y-2">
              <h4 className="text-sm font-medium text-muted-foreground">运行记录</h4>
              {runs.map((run) => (
                <Card
                  key={run.id}
                  className={`cursor-pointer transition-colors ${selectedRun?.id === run.id ? "ring-2 ring-primary" : ""}`}
                  onClick={() => handleSelectRun(run.id)}
                >
                  <CardContent className="p-3 flex items-center justify-between">
                    <div className="flex items-center gap-3">
                      <RunStatusIcon status={run.status} />
                      <div>
                        <p className="text-sm">
                          {run.profile_slug} v{run.profile_version}
                          <span className="text-muted-foreground ml-2">({run.trigger_type})</span>
                        </p>
                        <p className="text-xs text-muted-foreground">
                          {run.completed_count}/{run.total_samples} 样本完成
                        </p>
                      </div>
                    </div>
                    {run.status === "completed" && (
                      <div className="text-right">
                        <p className="text-lg font-bold">{run.overall_score.toFixed(2)}</p>
                        <p className="text-xs text-muted-foreground">总分</p>
                      </div>
                    )}
                  </CardContent>
                </Card>
              ))}
            </div>
          )}

          {/* 运行详情 */}
          {selectedRun && (
            <Card>
              <CardHeader>
                <CardTitle className="text-base">评测结果详情</CardTitle>
              </CardHeader>
              <CardContent className="space-y-4">
                {/* 维度得分 */}
                {selectedRun.dimension_scores && Object.keys(selectedRun.dimension_scores).length > 0 && (
                  <div className="space-y-2">
                    <h5 className="text-sm font-medium">维度平均分</h5>
                    {Object.entries(selectedRun.dimension_scores).map(([dim, score]) => (
                      <div key={dim} className="flex items-center gap-3">
                        <span className="w-24 text-xs text-muted-foreground">{dim}</span>
                        <div className="flex-1 h-4 bg-muted rounded-full overflow-hidden">
                          <div
                            className="h-full bg-primary rounded-full"
                            style={{ width: `${(score / 5) * 100}%` }}
                          />
                        </div>
                        <span className="text-xs font-medium w-10 text-right">{score.toFixed(2)}</span>
                      </div>
                    ))}
                  </div>
                )}

                {/* 各样本结果 */}
                {selectedRun.results && selectedRun.results.length > 0 && (
                  <div className="space-y-2">
                    <h5 className="text-sm font-medium">样本评分</h5>
                    {selectedRun.results.map((result, i) => (
                      <div key={i} className="border rounded-lg p-3">
                        <div className="flex items-center justify-between mb-1">
                          <p className="text-sm font-medium">{result.topic}</p>
                          <Badge variant="secondary">{result.weighted.toFixed(2)}</Badge>
                        </div>
                        {result.judge_feedback && (
                          <p className="text-xs text-muted-foreground mt-1">{result.judge_feedback}</p>
                        )}
                        {result.scores && (
                          <div className="flex gap-2 mt-2">
                            {Object.entries(result.scores).map(([dim, score]) => (
                              <Badge key={dim} variant="outline" className="text-xs">
                                {dim}: {score.toFixed(1)}
                              </Badge>
                            ))}
                          </div>
                        )}
                      </div>
                    ))}
                  </div>
                )}
              </CardContent>
            </Card>
          )}

          {!selectedSet && (
            <div className="text-center py-12 text-muted-foreground">
              <FileText className="h-8 w-8 mx-auto mb-2 opacity-50" />
              <p className="text-sm">选择左侧的评测集查看运行记录</p>
            </div>
          )}
        </div>
      </div>
      )}
    </div>
  );
}

function RunStatusIcon({ status }: { status: string }) {
  switch (status) {
    case "completed":
      return <CheckCircle className="h-5 w-5 text-green-500" />;
    case "running":
      return <Loader2 className="h-5 w-5 text-blue-500 animate-spin" />;
    case "failed":
      return <XCircle className="h-5 w-5 text-red-500" />;
    case "pending":
      return <Clock className="h-5 w-5 text-muted-foreground" />;
    default:
      return <Clock className="h-5 w-5 text-muted-foreground" />;
  }
}

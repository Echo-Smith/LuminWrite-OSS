/**
 * 研究确认点面板 — evidence / outline 两个人工 gate。
 *
 * 「确认」绑定 GET gate 返回的 input_ref + plan_hash + gate_revision（客户端
 * 不计算 hash），POST decisions 生成 UUID v4 幂等键，202 后轮询 GET 直到状态
 * 变化。STALE_GATE 提示刷新并重新拉取；pending 期间绝不渲染任何可绕过的
 * 继续按钮。提纲 gate 支持「修改提纲 → 保存新版本 → 用新 ref 确认」。
 */
import { AlertTriangle, CheckCheck, Loader2, PenLine, RefreshCw, ShieldCheck } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import {
  buildGateDecisionRequest,
  fetchGate,
  fetchResearchOutline,
  isResearchApiError,
  pollGateUntilResolved,
  postGateDecision,
  postOutlineRevision,
  type GateView,
  type OutlineSection,
  type ResearchEvidencePack,
  type ResearchOutline,
} from "@/lib/research-api";

interface ResearchGatePanelProps {
  runId: string;
  gate: GateView;
  pack: ResearchEvidencePack | null;
  onGateUpdated: (gate: GateView) => void;
}

type EditableSection = OutlineSection;

const GATE_TITLES: Record<GateView["gate_kind"], string> = {
  evidence: "证据确认点",
  outline: "提纲确认点",
};

function GateRefMetadata({ gate }: { gate: GateView }) {
  return (
    <dl className="research-gate-refs" aria-label="确认点绑定信息">
      <div><dt>绑定产物</dt><dd><code>{gate.input_ref?.artifact_id ?? "—"}</code> v{gate.input_ref?.version ?? "—"} · <code>{gate.input_ref?.content_hash ?? "—"}</code></dd></div>
      <div><dt>计划哈希</dt><dd><code>{gate.plan_hash}</code></dd></div>
      <div><dt>版本 revision</dt><dd>#{gate.revision}</dd></div>
    </dl>
  );
}

function EvidenceGateSummary({ gate, pack }: { gate: GateView; pack: ResearchEvidencePack | null }) {
  if (!pack) {
    return <p className="research-gate-summary-empty">证据包摘要不可用（仍可确认服务端展示的当前版本）。</p>;
  }
  const fullTextCount = pack.papers.filter((paper) => paper.reading_scope === "full_text").length;
  const abstractCount = pack.papers.filter((paper) => paper.reading_scope === "abstract").length;
  const unreadCount = pack.papers.filter((paper) => paper.reading_scope === "unread").length;
  return (
    <div className="research-gate-summary" data-testid="gate-coverage">
      <p className="research-gate-stats">
        可用文献 {pack.papers.length} 篇 · 已读全文 {fullTextCount} 篇 · 仅摘要 {abstractCount} 篇 · 未读 {unreadCount} 篇
      </p>
      <div><h4>主题覆盖</h4><ul>{pack.coverage.topics.map((topic) => <li key={topic}>{topic}</li>)}</ul></div>
      <div><h4>缺口</h4><ul>{pack.coverage.gaps.map((gap) => <li key={gap}>{gap}</li>)}</ul></div>
      <div><h4>矛盾</h4><ul>{pack.coverage.contradictions.map((item) => <li key={item}>{item}</li>)}</ul></div>
      <p className="research-gate-note">确认将冻结该版本的证据包（{gate.input_ref?.artifact_id} v{gate.input_ref?.version}）。主题覆盖与矛盾结论需要人工确认，不依赖单一总分。</p>
    </div>
  );
}

export function ResearchGatePanel({ runId, gate, pack, onGateUpdated }: ResearchGatePanelProps) {
  const isPending = gate.status === "pending";
  const canDecide = isPending && gate.allowed_operations.includes("decide") && Boolean(gate.input_ref);
  const isOutline = gate.gate_kind === "outline";

  const [confirming, setConfirming] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [errorCode, setErrorCode] = useState<string | null>(null);
  const [stale, setStale] = useState(false);

  const [outline, setOutline] = useState<ResearchOutline | null>(null);
  const [outlineLoading, setOutlineLoading] = useState(false);
  const [outlineError, setOutlineError] = useState<string | null>(null);
  const [savingRevision, setSavingRevision] = useState(false);
  const [savedRevision, setSavedRevision] = useState<number | null>(null);

  // 提纲编辑器需要当前提纲内容（GET artifact content，mock 返回样例）。
  useEffect(() => {
    if (!isOutline || !isPending || !gate.input_ref || outline) return;
    let cancelled = false;
    setOutlineLoading(true);
    fetchResearchOutline(runId, gate.input_ref)
      .then((content) => { if (!cancelled) setOutline(content); })
      .catch((cause) => { if (!cancelled) setOutlineError(cause instanceof Error ? cause.message : "提纲内容加载失败"); })
      .finally(() => { if (!cancelled) setOutlineLoading(false); });
    return () => { cancelled = true; };
  }, [isOutline, isPending, gate.input_ref, outline, runId]);

  const refetchGate = useCallback(async () => {
    try {
      const fresh = await fetchGate(runId, gate.gate_id);
      onGateUpdated(fresh);
      setStale(false);
      setErrorCode(null);
      setError(null);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "确认点状态刷新失败");
    }
  }, [runId, gate.gate_id, onGateUpdated]);

  const handleConfirm = useCallback(async () => {
    if (!canDecide || confirming) return;
    setConfirming(true);
    setError(null);
    setErrorCode(null);
    setStale(false);
    try {
      // input_ref / plan_hash / gate_revision 全部取 GET gate 返回值。
      const request = buildGateDecisionRequest(gate);
      const decision = await postGateDecision(runId, gate.gate_id, request);
      // 202：确认已持久化并安排恢复；轮询 GET 直到状态真正变化。
      const resolved = decision.status === "approved" && decision.resume_status === "queued"
        ? await pollGateUntilResolved(runId, gate.gate_id)
        : await fetchGate(runId, gate.gate_id);
      onGateUpdated(resolved);
    } catch (cause) {
      if (isResearchApiError(cause)) {
        setErrorCode(cause.code);
        setError(cause.message);
        if (cause.code === "STALE_GATE" || cause.code === "GATE_ALREADY_DECIDED") {
          setStale(true);
          await refetchGate();
        }
      } else {
        setError(cause instanceof Error ? cause.message : "确认提交失败，请稍后重试");
      }
    } finally {
      setConfirming(false);
    }
  }, [canDecide, confirming, gate, runId, onGateUpdated, refetchGate]);

  const patchSection = (index: number, key: keyof EditableSection, value: string) => {
    setOutline((current) => {
      if (!current) return current;
      const sections = current.sections.map((section, i) => (i === index ? { ...section, [key]: value } : section));
      return { ...current, sections };
    });
  };

  const handleSaveRevision = useCallback(async () => {
    if (!canDecide || !outline || savingRevision) return;
    setSavingRevision(true);
    setOutlineError(null);
    setError(null);
    setErrorCode(null);
    setStale(false);
    try {
      const request = {
        plan_id: gate.plan_id,
        plan_version: gate.plan_version,
        plan_hash: gate.plan_hash,
        gate_revision: gate.revision,
        outline_ref: gate.input_ref!,
        outline: { sections: outline.sections, limitations: outline.limitations },
      };
      const saved = await postOutlineRevision(runId, gate.gate_id, request);
      setSavedRevision(saved.gate_revision);
      const fresh = await fetchGate(runId, gate.gate_id);
      onGateUpdated(fresh);
    } catch (cause) {
      if (isResearchApiError(cause)) {
        setErrorCode(cause.code);
        setOutlineError(cause.message);
        if (cause.code === "STALE_GATE") {
          setStale(true);
          await refetchGate();
        }
      } else {
        setOutlineError(cause instanceof Error ? cause.message : "提纲新版本保存失败，请稍后重试");
      }
    } finally {
      setSavingRevision(false);
    }
  }, [canDecide, outline, savingRevision, gate, runId, onGateUpdated, refetchGate]);

  if (!isPending) {
    return (
      <section className="research-gate research-gate-decided" aria-label={GATE_TITLES[gate.gate_kind]}>
        <header className="research-gate-header">
          <span className="flex items-center gap-2 text-sm font-semibold"><CheckCheck className="h-4 w-4 text-emerald-600 dark:text-emerald-400" />{GATE_TITLES[gate.gate_kind]}已完成</span>
          <Badge variant="outline" className="text-[11px]">{gate.status === "approved" ? "已确认" : gate.status}</Badge>
        </header>
        <GateRefMetadata gate={gate} />
      </section>
    );
  }

  const editableSections = outline?.sections ?? [];

  return (
    <section className="research-gate research-gate-pending" aria-label={GATE_TITLES[gate.gate_kind]}>
      <header className="research-gate-header">
        <span className="flex items-center gap-2 text-sm font-semibold"><ShieldCheck className="h-4 w-4" />{GATE_TITLES[gate.gate_kind]}等待确认</span>
        <Badge variant="outline" className="text-[11px]">revision #{gate.revision}</Badge>
      </header>

      <GateRefMetadata gate={gate} />

      {gate.gate_kind === "evidence" && <EvidenceGateSummary gate={gate} pack={pack} />}

      {isOutline && (
        <div className="research-gate-outline" data-testid="outline-editor">
          <header className="research-gate-outline-header">
            <h4 className="flex items-center gap-1.5 text-xs font-semibold"><PenLine className="h-3.5 w-3.5" />提纲章节（保存为新的不可变版本后确认）</h4>
            {savedRevision !== null && <Badge variant="outline" className="text-[10px]">已保存 revision #{savedRevision}</Badge>}
          </header>
          {outlineLoading && <p className="flex items-center gap-2 text-xs text-muted-foreground"><Loader2 className="h-3.5 w-3.5 animate-spin" />正在加载提纲…</p>}
          {outlineError && <p className="text-xs text-destructive" role="alert">{outlineError}</p>}
          {editableSections.map((section, index) => (
            <fieldset key={section.section_id} className="research-outline-section">
              <legend>第 {index + 1} 节{section.evidence_ids.length === 0 ? "（无证据：仅限引言/研究缺口等非事实综合用途）" : ""}</legend>
              <Input value={section.title} onChange={(event) => patchSection(index, "title", event.target.value)} aria-label={`第 ${index + 1} 节标题`} placeholder="章节标题" />
              <Textarea value={section.central_point} onChange={(event) => patchSection(index, "central_point", event.target.value)} aria-label={`第 ${index + 1} 节中心论点`} placeholder="中心论点" className="min-h-[56px] text-sm" />
              {section.evidence_ids.length > 0 && <p className="research-outline-evidence">绑定证据：{section.evidence_ids.join("、")}</p>}
              {section.gaps.length > 0 && <p className="research-outline-gaps">章节缺口：{section.gaps.join("、")}</p>}
            </fieldset>
          ))}
          <Button variant="outline" size="sm" disabled={!canDecide || !outline || savingRevision || outlineLoading} onClick={() => void handleSaveRevision()}>
            {savingRevision ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <PenLine className="h-3.5 w-3.5" />}
            保存为新版本（revision #{gate.revision + 1}）
          </Button>
        </div>
      )}

      {(error || errorCode === "GATE_APPROVAL_REQUIRED") && (
        <div className="research-gate-error" role="alert" data-testid="gate-error" data-error-code={errorCode ?? undefined}>
          <p className="flex items-start gap-1.5 text-xs"><AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />{error}</p>
          {stale && (
            <p className="text-xs text-muted-foreground">确认信息与当前版本不一致（{errorCode === "GATE_ALREADY_DECIDED" ? "该确认点已完成" : "已过期"}）。已重新拉取最新状态，请核对后再确认。</p>
          )}
          {errorCode === "GATE_APPROVAL_REQUIRED" && (
            <p className="text-xs text-muted-foreground">该运行需要人工批准才能继续（GATE_APPROVAL_REQUIRED），不存在可自动绕过的路径。</p>
          )}
          {stale && (
            <Button variant="outline" size="sm" onClick={() => void refetchGate()}><RefreshCw className="h-3.5 w-3.5" />刷新确认点状态</Button>
          )}
        </div>
      )}

      <footer className="research-gate-footer">
        <Button
          size="sm"
          disabled={!canDecide || confirming || (isOutline && !outline)}
          onClick={() => void handleConfirm()}
          aria-label={`确认${GATE_TITLES[gate.gate_kind]}（revision #${gate.revision}）`}
        >
          {confirming ? <Loader2 className="h-4 w-4 animate-spin" /> : <CheckCheck className="h-4 w-4" />}
          {confirming ? "提交确认中…" : `确认当前版本（revision #${gate.revision}）`}
        </Button>
        {confirming && <span className="text-xs text-muted-foreground">已提交（202），正在等待运行状态变化…</span>}
      </footer>

      <p className="research-gate-blocking" data-testid="gate-blocking-note">
        运行已暂停在该确认点：确认前不会调用正文写作，也不存在跳过或绕过的继续按钮。
      </p>
    </section>
  );
}

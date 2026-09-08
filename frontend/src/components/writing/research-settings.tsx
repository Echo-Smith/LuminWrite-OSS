/**
 * 研究综述设置表单 — ResearchSpec 字段（contracts.md §1）。
 *
 * 研究问题取现有 central_question 输入、目标读者/语言/长度沿用现有 delivery
 * 字段的语义；数量字段默认值与合同示例逐项一致，均可修改。
 * 提交走 startResearchRun：mock 开启时返回演示运行；mock 关闭时走真实
 * document → contract(v1.1) → confirm → plan → run 创建链路（F1）。
 * 客户端提示约束（如 max_papers ≤ 20），提交仍以服务端校验为准。
 */
import { BookOpenText, FlaskConical, Globe, Loader2, X } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import type { EvidenceRequirement } from "@/lib/writing-runtime-types";
import {
  buildResearchSpec,
  defaultResearchSpecDraft,
  isResearchReviewEnabled,
  researchLaunchProblems,
  researchReviewDisabledReason,
  startResearchRun,
  type ResearchMaterialRef,
  type ResearchSpecDraft,
} from "@/lib/research-api";
import { cn } from "@/lib/utils";

interface ResearchSettingsProps {
  /** 现有 central_question / delivery 值（composer 传入）。 */
  centralQuestion: string;
  audience: string;
  language: string;
  lengthMin: string;
  lengthMax: string;
  allowExternalResearch: boolean;
  onAllowExternalResearchChange?: (value: boolean) => void;
  /** composer 挂载素材的服务端引用（运行创建时随文档 metadata 透传）。 */
  materialRefs?: ResearchMaterialRef[];
  onClose: () => void;
  onStarted?: (runId: string) => void;
}

const EVIDENCE_OPTIONS: Array<{ value: EvidenceRequirement; label: string; description: string }> = [
  { value: "abstract_allowed", label: "允许摘要", description: "摘要证据可支撑引用（默认）" },
  { value: "full_text_required", label: "必须全文", description: "每篇参与证据的来源都必须已读全文" },
];

function toText(value: string | number | undefined | null): string {
  return value === undefined || value === null ? "" : String(value);
}

export function ResearchSettings({ centralQuestion, audience, language, lengthMin, lengthMax, allowExternalResearch, onAllowExternalResearchChange, materialRefs, onClose, onStarted }: ResearchSettingsProps) {
  const defaults = defaultResearchSpecDraft();
  const [draft, setDraft] = useState<ResearchSpecDraft>(() => ({
    ...defaults,
    central_question: centralQuestion,
    audience,
    language,
    // 长度沿用交付设定（composer 未提供时给默认值，真实合同要求正整数）。
    length_min: toText(lengthMin) || defaults.length_min,
    length_max: toText(lengthMax) || defaults.length_max,
    allow_external_research: allowExternalResearch,
  }));
  const [problems, setProblems] = useState<string[]>([]);
  const [submitting, setSubmitting] = useState(false);
  const [apiError, setApiError] = useState<string | null>(null);

  const featureEnabled = isResearchReviewEnabled();
  const disabledReason = researchReviewDisabledReason();

  const patch = (key: keyof ResearchSpecDraft, value: string | boolean) =>
    setDraft((current) => ({ ...current, [key]: value }));

  const handleSubmit = async () => {
    if (submitting) return;
    setApiError(null);
    const built = buildResearchSpec(draft);
    if (!built.spec) {
      setProblems(built.problems);
      return;
    }
    // 真实合同还需要 audience.role / delivery.language / length 非空（F1）。
    const launchProblems = researchLaunchProblems({
      spec: built.spec,
      central_question: draft.central_question,
      audience: draft.audience,
      language: draft.language,
      length_min: draft.length_min,
      length_max: draft.length_max,
      allow_external_research: draft.allow_external_research,
    });
    if (launchProblems.length > 0) {
      setProblems(launchProblems);
      return;
    }
    setProblems([]);
    setSubmitting(true);
    try {
      const { run_id } = await startResearchRun({
        spec: built.spec,
        central_question: draft.central_question,
        audience: draft.audience,
        language: draft.language,
        length_min: draft.length_min,
        length_max: draft.length_max,
        allow_external_research: draft.allow_external_research,
        material_refs: materialRefs,
      });
      onStarted?.(run_id);
    } catch (error) {
      setApiError(error instanceof Error ? error.message : "研究综述运行启动失败，请稍后重试");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <section className="research-settings" aria-label="研究综述设置">
      <header className="research-settings-header">
        <span className="flex items-center gap-2 text-sm font-semibold"><BookOpenText className="h-4 w-4" />研究综述</span>
        <button onClick={onClose} aria-label="关闭研究综述设置" className="text-muted-foreground transition-ui hover:text-foreground"><X className="h-4 w-4" /></button>
      </header>

      {!featureEnabled && (
        <p className="research-settings-disabled" role="note">
          {disabledReason}：确认后无法启动研究综述运行，普通写作不受影响。
        </p>
      )}

      <div className="research-settings-grid">
        <label className="research-field research-field-wide">
          <span>研究问题（取自中心问题）</span>
          <Input value={draft.central_question} onChange={(event) => patch("central_question", event.target.value)} placeholder="例如：固态电解质界面稳定性目前的共识与分歧是什么？" />
        </label>
        <label className="research-field">
          <span>目标读者</span>
          <Input value={draft.audience} onChange={(event) => patch("audience", event.target.value)} placeholder="沿用交付设定中的读者角色" />
        </label>
        <label className="research-field">
          <span>语言</span>
          <Input value={draft.language} onChange={(event) => patch("language", event.target.value)} />
        </label>
        <label className="research-field">
          <span>长度下限（字）</span>
          <Input inputMode="numeric" value={draft.length_min} onChange={(event) => patch("length_min", event.target.value)} placeholder="沿用交付设定" />
        </label>
        <label className="research-field">
          <span>长度上限（字）</span>
          <Input inputMode="numeric" value={draft.length_max} onChange={(event) => patch("length_max", event.target.value)} placeholder="沿用交付设定" />
        </label>
        <label className="research-field">
          <span>年份范围（起）</span>
          <Input inputMode="numeric" value={draft.year_from} onChange={(event) => patch("year_from", event.target.value)} placeholder="可留空" />
        </label>
        <label className="research-field">
          <span>年份范围（止）</span>
          <Input inputMode="numeric" value={draft.year_to} onChange={(event) => patch("year_to", event.target.value)} placeholder="可留空" />
        </label>
        <label className="research-field research-field-wide">
          <span>主题排除（逗号分隔）</span>
          <Input value={draft.exclusion_terms} onChange={(event) => patch("exclusion_terms", event.target.value)} placeholder="例如：燃料电池, 锂硫" />
        </label>
        <label className="research-field">
          <span>检索式数量（1–3）</span>
          <Input inputMode="numeric" value={draft.max_queries} onChange={(event) => patch("max_queries", event.target.value)} />
        </label>
        <label className="research-field">
          <span>候选上限（≤60）</span>
          <Input inputMode="numeric" value={draft.max_candidates} onChange={(event) => patch("max_candidates", event.target.value)} />
        </label>
        <label className="research-field">
          <span>阅读上限（≤20 篇）</span>
          <Input inputMode="numeric" value={draft.max_papers} onChange={(event) => patch("max_papers", event.target.value)} />
        </label>
        <label className="research-field">
          <span>最少可引用来源</span>
          <Input inputMode="numeric" value={draft.min_citable_sources} onChange={(event) => patch("min_citable_sources", event.target.value)} />
        </label>
        <div className="research-field">
          <span id="research-evidence-requirement-label">证据要求</span>
          <Select value={draft.evidence_requirement} onValueChange={(value) => patch("evidence_requirement", value as EvidenceRequirement)}>
            <SelectTrigger aria-labelledby="research-evidence-requirement-label" className="h-9 text-sm"><SelectValue /></SelectTrigger>
            <SelectContent>
              {EVIDENCE_OPTIONS.map((option) => (
                <SelectItem key={option.value} value={option.value}>{option.label} — {option.description}</SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <label className={cn("research-field research-field-wide", !onAllowExternalResearchChange && "pointer-events-none opacity-80")}>
          <span className="flex items-center gap-1.5"><Globe className="h-3.5 w-3.5" />允许联网检索外部来源</span>
          <Switch checked={draft.allow_external_research} onCheckedChange={(checked) => {
            patch("allow_external_research", checked);
            onAllowExternalResearchChange?.(checked);
          }} aria-label="允许联网检索外部来源" />
        </label>
      </div>

      {(problems.length > 0 || apiError) && (
        <ul className="research-settings-problems" role="alert">
          {problems.map((problem) => <li key={problem}>{problem}</li>)}
          {apiError && <li>{apiError}</li>}
        </ul>
      )}

      <footer className="research-settings-footer">
        <p className="flex items-center gap-1.5 text-xs text-muted-foreground"><FlaskConical className="h-3.5 w-3.5" />默认值与合同示例一致；提交后以服务端合同校验为准。</p>
        <Button size="sm" disabled={!featureEnabled || submitting} onClick={() => void handleSubmit()}>
          {submitting ? <Loader2 className="h-4 w-4 animate-spin" /> : null}
          {submitting ? "正在启动…" : "启动研究综述运行"}
        </Button>
      </footer>
    </section>
  );
}

/**
 * 写作方式选择器 — 先选择用户意图，再按需展开执行细节。
 * guided 继续作为协议值存在，但在界面上表达为“直接写 + 先确认提纲”。
 */
import { useState } from "react";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Switch } from "@/components/ui/switch";
import { BookOpenText, ChevronDown, ChevronRight, PenLine, Sparkles, Zap } from "lucide-react";
import type { WriteMode } from "@/lib/types";
import type { ApprovalMode, AssuranceLevel, OrchestrationMode } from "@/lib/writing-runtime-types";
import { isResearchReviewHardOff } from "@/lib/research-api";
import { useSettingsStore } from "@/stores/settings-store";
import { cn } from "@/lib/utils";

interface ModePickerProps {
  value: WriteMode;
  onChange: (mode: WriteMode) => void;
  orchestrationValue?: OrchestrationMode;
  onOrchestrationChange?: (mode: OrchestrationMode) => void;
  assuranceValue?: AssuranceLevel;
  onAssuranceChange?: (level: AssuranceLevel) => void;
  approvalValue?: ApprovalMode;
  onApprovalChange?: (mode: ApprovalMode) => void;
  compact?: boolean;
  /** 选择「研究综述」时回调（打开 ResearchSpec 表单）；未提供则只同步 orchestration。 */
  onResearchReviewSelect?: () => void;
}

const MODE_OPTIONS: Array<{ value: Exclude<WriteMode, "guided">; label: string; icon: typeof Zap; description: string }> = [
  { value: "auto", label: "自动", icon: Zap, description: "让 AI 根据内容选择合适方式" },
  { value: "writing", label: "直接写", icon: PenLine, description: "根据要求生成完整文章" },
  { value: "polish", label: "润色", icon: Sparkles, description: "改写或优化已有文本" },
];

const RESEARCH_PRESETS: Array<{ value: string; label: string; description: string; orchestration: OrchestrationMode; assurance: AssuranceLevel }> = [
  { value: "auto", label: "自动", description: "由 AI 判断是否需要资料", orchestration: "auto", assurance: "standard" },
  { value: "sourced", label: "使用来源", description: "围绕材料和来源组织内容", orchestration: "sourced", assurance: "sourced" },
  { value: "strict", label: "严格验证", description: "检索并逐项核查关键信息", orchestration: "strict_research", assurance: "strict" },
];

/** 研究综述是独立入口（research_review 编排模式 + lcp/1.1 合同），不与普通资料要求混排。 */
const RESEARCH_REVIEW_PRESET = { value: "review", label: "研究综述", description: "多源文献检索与逐条引用核查（两个确认点）" };

const APPROVAL_OPTIONS: Array<{ value: ApprovalMode; label: string; description: string }> = [
  { value: "conditional", label: "风险时询问", description: "一般步骤自动执行，遇到风险再确认" },
  { value: "always", label: "每次询问", description: "执行关键步骤前都先确认" },
  { value: "auto", label: "自动执行", description: "在授权范围内连续完成任务" },
];

export function ModePicker({
  value, onChange,
  orchestrationValue = "auto", onOrchestrationChange,
  assuranceValue = "standard", onAssuranceChange,
  approvalValue = "conditional", onApprovalChange,
  compact = false,
  onResearchReviewSelect,
}: ModePickerProps) {
  const [open, setOpen] = useState(false);
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const primaryValue = value === "guided" ? "writing" : value;
  const selected = MODE_OPTIONS.find((option) => option.value === primaryValue) ?? MODE_OPTIONS[0];
  const researchReviewActive = orchestrationValue === "research_review";
  const researchPreset = researchReviewActive
    ? null
    : RESEARCH_PRESETS.find((option) => option.orchestration === orchestrationValue && option.assurance === assuranceValue);
  const approvalOption = APPROVAL_OPTIONS.find((option) => option.value === approvalValue) ?? APPROVAL_OPTIONS[0];
  const canConfigureExecution = Boolean(onOrchestrationChange && onAssuranceChange) || Boolean(onApprovalChange);
  // 研究综述为实验室功能：用户在 设置 → 实验室功能 勾选后开放；
  // 部署级 VITE_RESEARCH_REVIEW_ENABLED=false 为硬开关（连实验室列表都隐藏）。
  const researchEnabled = useSettingsStore((s) => s.enableResearchReview) && !isResearchReviewHardOff();

  const handleResearchReviewSelect = () => {
    onOrchestrationChange?.("research_review");
    onAssuranceChange?.("strict");
    onResearchReviewSelect?.();
    setOpen(false);
  };

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button aria-label={`写作与执行模式：${selected.label}`} className="composer-mode-trigger flex h-8 items-center gap-1.5 rounded-xl px-2.5 text-sm text-muted-foreground transition-ui hover:bg-accent hover:text-foreground">
          <span key={`mode-icon-${selected.value}`} className="flex items-center anim-fade-scale"><selected.icon className="h-[18px] w-[18px]" /></span>
          <span key={`mode-label-${selected.value}`} className={cn("composer-control-label anim-fade-scale", compact && "sr-only")}>{selected.label}</span>
          <ChevronDown className={cn("composer-control-chevron h-4 w-4 opacity-50", compact && "hidden")} />
        </button>
      </PopoverTrigger>

      <PopoverContent align="start" className="w-72 p-1">
        <p className="px-3 pb-1 pt-2 text-[10px] font-medium uppercase tracking-wider text-muted-foreground">写作方式</p>
        {MODE_OPTIONS.map((option) => (
          <button key={option.value} onClick={() => { onChange(option.value); setOpen(false); }} className={cn("flex w-full items-start gap-2.5 rounded-md px-3 py-2 text-left transition-colors hover:bg-accent", option.value === primaryValue && "bg-accent/50")}>
            <option.icon className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" />
            <span className="min-w-0 flex-1"><span className="block text-sm font-medium">{option.label}</span><span className="block text-xs text-muted-foreground">{option.description}</span></span>
          </button>
        ))}

        <div className="mx-2 my-1 border-t" />
        <label className="flex cursor-pointer items-center justify-between gap-3 rounded-md px-3 py-2 hover:bg-accent">
          <span className="min-w-0"><span className="block text-sm font-medium">生成前先确认提纲</span><span className="block text-xs text-muted-foreground">适合长文或结构要求明确的任务</span></span>
          <Switch checked={value === "guided"} onCheckedChange={(checked) => onChange(checked ? "guided" : "writing")} aria-label="生成前先确认提纲" />
        </label>

        {onOrchestrationChange && researchEnabled && (
          <>
            <div className="mx-2 my-1 border-t" />
            <button
              onClick={handleResearchReviewSelect}
              className={cn("flex w-full items-start gap-2.5 rounded-md px-3 py-2 text-left transition-colors hover:bg-accent",
                researchReviewActive && "bg-accent/50")}
            >
              <BookOpenText className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" />
              <span className="min-w-0 flex-1">
                <span className="block text-sm font-medium">{RESEARCH_REVIEW_PRESET.label}</span>
                <span className="block text-xs text-muted-foreground">{RESEARCH_REVIEW_PRESET.description}</span>
              </span>
            </button>
          </>
        )}

        {canConfigureExecution && (
          <Collapsible open={advancedOpen} onOpenChange={setAdvancedOpen}>
            <div className="mx-2 my-1 border-t" />
            <CollapsibleTrigger asChild>
              <button className="flex w-full items-center gap-2 rounded-md px-3 py-2 text-left hover:bg-accent">
                <ChevronRight className={cn("h-4 w-4 shrink-0 text-muted-foreground transition-transform", advancedOpen && "rotate-90")} />
                <span className="min-w-0 flex-1"><span className="block text-sm font-medium">高级设置</span><span className="block truncate text-xs text-muted-foreground">{researchReviewActive ? "研究综述（逐条引用核查）" : researchPreset?.label ?? "自定义资料策略"} · {approvalOption.label}</span></span>
              </button>
            </CollapsibleTrigger>
            <CollapsibleContent className="pb-1">
              {onOrchestrationChange && onAssuranceChange && (
                <div className="px-2 pt-1">
                  <p className="px-1 pb-1 text-[10px] font-medium uppercase tracking-wider text-muted-foreground">资料要求</p>
                  {RESEARCH_PRESETS.map((option) => (
                    <button key={option.value} onClick={() => { onOrchestrationChange(option.orchestration); onAssuranceChange(option.assurance); }} className={cn("w-full rounded-md px-2 py-1.5 text-left hover:bg-accent", option === researchPreset && "bg-accent/60")}>
                      <span className="block text-xs font-medium">{option.label}</span><span className="block text-[11px] text-muted-foreground">{option.description}</span>
                    </button>
                  ))}
                </div>
              )}
              {onApprovalChange && (
                <div className="px-2 pt-2">
                  <p className="px-1 pb-1 text-[10px] font-medium uppercase tracking-wider text-muted-foreground">执行确认</p>
                  {APPROVAL_OPTIONS.map((option) => (
                    <button key={option.value} onClick={() => onApprovalChange(option.value)} className={cn("w-full rounded-md px-2 py-1.5 text-left hover:bg-accent", option.value === approvalValue && "bg-accent/60")}>
                      <span className="block text-xs font-medium">{option.label}</span><span className="block text-[11px] text-muted-foreground">{option.description}</span>
                    </button>
                  ))}
                </div>
              )}
            </CollapsibleContent>
          </Collapsible>
        )}
      </PopoverContent>
    </Popover>
  );
}

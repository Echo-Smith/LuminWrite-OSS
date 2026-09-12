/**
 * 质量门暂停引导卡（T09）— run 处于 paused 且 node_quality failed
 * （EVIDENCE_INVALID 语义）时显示。
 *
 * 刻意不提供任何「继续 / 重试 / 恢复」按钮：质量门 fail closed，当前运行
 * 不会自动重试，也没有可绕过的路径；唯一出路是修正草稿引用后创建新运行。
 * 卡片列出 evidence_report / 校验明细 / 服务端错误里的具体发现，用户看到的
 * 是可检查的句子和证据，不只有总分（T07 验收语义）。
 */
import { ShieldX } from "lucide-react";

export interface ResearchQualityGateCardProps {
  /** qualityGatePauseGuidance 的投影（headline/note/findings）；null 时不渲染。 */
  guidance: {
    headline: string;
    note: string;
    findings: string[];
  } | null;
}

export function ResearchQualityGateCard({ guidance }: ResearchQualityGateCardProps) {
  if (!guidance) return null;
  return (
    <section
      className="research-quality-gate-card"
      aria-label="质量门暂停引导"
      data-testid="quality-gate-guidance"
      role="alert"
    >
      <header className="research-quality-gate-header">
        <ShieldX className="h-4 w-4 shrink-0" aria-hidden="true" />
        <span className="text-sm font-semibold">质量门暂停：引用校验未通过</span>
      </header>

      <p className="research-quality-gate-headline" data-testid="quality-gate-headline">{guidance.headline}</p>

      {guidance.findings.length > 0 ? (
        <div className="research-quality-gate-findings" data-testid="quality-gate-findings">
          <h4>服务端报告的具体发现</h4>
          <ul>
            {guidance.findings.map((finding, index) => <li key={`${index}-${finding}`}>{finding}</li>)}
          </ul>
        </div>
      ) : (
        <p className="research-quality-gate-findings-empty">服务端未返回可展示的发现明细；错误语义为 EVIDENCE_INVALID（引用校验 blocker）。</p>
      )}

      <p className="research-quality-gate-note">{guidance.note}</p>
    </section>
  );
}

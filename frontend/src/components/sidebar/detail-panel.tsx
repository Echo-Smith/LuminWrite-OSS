/**
 * 右侧详情面板（合并版）
 *
 * 单一面板服务所有写作路径：governed 数据（writing-runtime-store）为主，
 * WS 会话数据（agent-store）为回退投影。三个合并 tab：
 * - 文档（大纲 + 版本历史）
 * - 材料（governed 产物；WS 回退检索统计）
 * - 运行（运行记录/事件账本 + 质量验收；WS 回退步骤时间线）
 *
 * 原双轨实现（WS 专用四 tab 面板）已并入： governed 与 WS 双轨各自渲染
 * 同一套 tab（run-detail-tabs 内部回退），普通快写不再出现"面板全空"。
 * 风格信息卡保留在 composer 的风格选择器（会话内可见），面板不再单列
 * 风格 tab。
 */
import { PanelRightClose } from "lucide-react";
import type { ToolCallPart } from "@/lib/writing-runtime-types";
import { useWritingRuntimeStore } from "@/stores/writing-runtime-store";
import { RunDetailTabs } from "@/components/runtime/run-detail-tabs";
import { useWritingRuntimeStore } from "@/stores/writing-runtime-store";
import { useWorkspaceLayoutStore } from "@/stores/workspace-layout-store";

interface DetailPanelProps {
  onClose?: () => void;
  /** 兼容旧签名；合并后单一实现，仅保留 props 形状。 */
  governed?: boolean;
}

export function DetailPanel({ onClose }: DetailPanelProps) {
  const detailTab = useWorkspaceLayoutStore((state) => state.detailTab);
  const setDetailTab = useWorkspaceLayoutStore((state) => state.setDetailTab);
  const document = useWritingRuntimeStore((state) => state.document);
  const versions = useWritingRuntimeStore((state) => state.versions);
  const run = useWritingRuntimeStore((state) => state.run);
  const events = useWritingRuntimeStore((state) => state.events);
  const artifacts = useWritingRuntimeStore((state) => state.artifacts);
  const quality = useWritingRuntimeStore((state) => state.quality);

  // WS 会话回退数据：governed 数据（run/events/artifacts/versions）全空时，
  // 面板用当前会话的步骤与版本历史填充（普通快写路径）。
  const session = useWritingRuntimeStore((s) => s.sessions.find((sess) => sess.id === s.activeSessionId));
  const messages = session?.messages ?? [];
  const lastAssistant = [...messages].reverse().find((m) => m.role === "assistant");
  const steps = (lastAssistant?.parts.filter((p): p is ToolCallPart => p.type === "tool-call") ?? []);
  const governedPresent = Boolean(run || events.length || artifacts.length || versions.length);
  const legacy = !governedPresent
    ? { traceId: session?.traceId ?? null, steps, running: lastAssistant?.status === "running" }
    : undefined;
  const version = versions.find((item) => item.document.version_id === document?.current_version_id)?.document
    ?? versions[versions.length - 1]?.document
    ?? null;

  return (
    <aside className="governed-detail-panel" aria-label="文档详情面板">
      <div className="governed-detail-header">
        <h3>详情</h3>
        {onClose && <button onClick={onClose} aria-label="收起详情面板" title="收起详情"><PanelRightClose className="h-4 w-4" /></button>}
      </div>
      <RunDetailTabs
        activeTab={detailTab}
        onTabChange={setDetailTab}
        version={version}
        versions={versions}
        run={run}
        events={events}
        artifacts={artifacts}
        quality={quality}
        legacy={legacy}
      />
    </aside>
  );
}


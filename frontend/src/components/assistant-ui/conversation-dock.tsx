import type { ReactNode } from "react";
import { ChevronDown, ChevronUp, MessageSquareText, Minus, PanelBottomClose, PanelBottomOpen } from "lucide-react";
import type { ConversationPanelState } from "@/stores/workspace-layout-store";
import { cn } from "@/lib/utils";

interface ConversationDockProps {
  state: ConversationPanelState;
  onStateChange: (state: ConversationPanelState) => void;
  children: ReactNode;
  statusText?: string;
}

export function ConversationDock({ state, onStateChange, children, statusText = "修改任务、询问决策或控制执行" }: ConversationDockProps) {
  if (state === "minimized") {
    return (
      <button className="conversation-minimized" onClick={() => onStateChange("compact")} aria-label="展开写作对话" aria-expanded="false">
        <MessageSquareText className="h-4 w-4" />
        <span>写作对话</span>
        <small>{statusText}</small>
        <ChevronUp className="ml-auto h-4 w-4" />
      </button>
    );
  }

  return (
    <section className={cn("conversation-dock", state === "compact" && "conversation-dock-compact")} aria-label="写作对话" data-panel-state={state}>
      <header className="conversation-dock-header">
        <div className="flex min-w-0 items-center gap-2">
          <MessageSquareText className="h-4 w-4" />
          <span>写作对话</span>
          <small>{statusText}</small>
        </div>
        <div className="flex items-center gap-1">
          <button onClick={() => onStateChange(state === "expanded" ? "compact" : "expanded")} aria-label={state === "expanded" ? "压缩对话" : "展开对话"} aria-expanded={state === "expanded"}>
            {state === "expanded" ? <PanelBottomClose className="h-4 w-4" /> : <PanelBottomOpen className="h-4 w-4" />}
          </button>
          <button onClick={() => onStateChange("minimized")} aria-label="最小化对话"><Minus className="h-4 w-4" /></button>
          <button onClick={() => onStateChange("minimized")} aria-label="收起对话"><ChevronDown className="h-4 w-4" /></button>
        </div>
      </header>
      <div className="conversation-dock-body">{children}</div>
    </section>
  );
}

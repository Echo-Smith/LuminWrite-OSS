/**
 * Toast 通知组件 — 固定在右上角的通知栈
 *
 * 挂载在 App 根节点，由 useToastStore 驱动
 */
import { CheckCircle2, XCircle, AlertTriangle, Info, X } from "lucide-react";
import { useToastStore, type ToastType } from "@/stores/toast-store";
import { cn } from "@/lib/utils";

const ICON_MAP: Record<ToastType, typeof CheckCircle2> = {
  success: CheckCircle2,
  error: XCircle,
  warning: AlertTriangle,
  info: Info,
};

const COLOR_MAP: Record<ToastType, string> = {
  success: "border-emerald-200 dark:border-emerald-800/60 text-emerald-600 dark:text-emerald-400",
  error: "border-red-200 dark:border-red-800/60 text-red-600 dark:text-red-400",
  warning: "border-amber-200 dark:border-amber-800/60 text-amber-600 dark:text-amber-400",
  info: "border-blue-200 dark:border-blue-800/60 text-blue-600 dark:text-blue-400",
};

export function ToastContainer() {
  const toasts = useToastStore((s) => s.toasts);
  const dismiss = useToastStore((s) => s.dismiss);

  if (toasts.length === 0) return null;

  return (
    <div className="fixed top-4 right-4 z-[100] flex flex-col gap-2 pointer-events-none">
      {toasts.map((t) => {
        const Icon = ICON_MAP[t.type];
        return (
          <div
            key={t.id}
            className={cn(
              "pointer-events-auto flex items-start gap-2.5 rounded-lg border bg-background/95 backdrop-blur-sm px-4 py-3 shadow-lg min-w-[280px] max-w-[400px]",
              t.leaving ? "anim-exit-drop" : "anim-enter-rise",
              COLOR_MAP[t.type]
            )}
          >
            <Icon className="h-4.5 w-4.5 shrink-0 mt-0.5" />
            <div className="flex-1 min-w-0">
              <p className="text-sm font-medium text-foreground">{t.title}</p>
              {t.description && (
                <p className="text-xs text-muted-foreground mt-0.5">{t.description}</p>
              )}
              {t.action && (
                <button
                  onClick={() => {
                    t.action!.onClick();
                    dismiss(t.id);
                  }}
                  className="mt-1.5 text-xs font-medium text-primary hover:underline"
                >
                  {t.action.label}
                </button>
              )}
            </div>
            <button
              onClick={() => dismiss(t.id)}
              className="shrink-0 text-muted-foreground/60 hover:text-foreground transition-ui"
            >
              <X className="h-3.5 w-3.5" />
            </button>
          </div>
        );
      })}
    </div>
  );
}

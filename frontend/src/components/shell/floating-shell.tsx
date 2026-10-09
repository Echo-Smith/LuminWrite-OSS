/**
 * FloatingShell — 悬浮窗统一骨架
 *
 * 个人中心 / 审计中心 / 插件 三窗共用：遮罩、居中定位、尺寸动效
 * （1100px / 85vh，xl 屏 1280px）、左菜单（支持分组小标题与 ↑↓ 键
 * 导航）、标题栏与滚动内容区。
 *
 * 关闭行为统一由 closeOverlayAndBack 处理（有站内历史则返回，否则
 * 落到 /write）。
 */
import { useRef, type ReactNode } from "react";
import { X } from "lucide-react";
import { DialogPortal, DialogOverlay } from "@/components/ui/dialog";
import * as DialogPrimitive from "@radix-ui/react-dialog";
import { cn } from "@/lib/utils";

export interface FloatingShellItem {
  key: string;
  label: string;
  icon: React.ElementType;
}

/** 分组小标题：group 项渲染为分组头，items 项渲染为菜单按钮 */
export interface FloatingShellEntry {
  group?: string;
  key?: string;
  label?: string;
  icon?: React.ElementType;
}

export interface FloatingShellProps<K extends string> {
  /** 左上角自定义区块（品牌块 / 用户信息块） */
  header: ReactNode;
  /** 菜单条目；group 条目渲染为分组小标题 */
  items: FloatingShellEntry[];
  /** 当前激活 key */
  active: K;
  onItemChange: (key: K) => void;
  /** 标题栏：随激活项变化的标题/副标题 */
  meta: Record<K, { title: string; subtitle: string }>;
  onClose: () => void;
  /** 游客提示；不传则不启用游客分支 */
  isGuest?: boolean;
  guestNotice?: { icon: React.ElementType; title: string; description: string };
  /** 内容区（滚动） */
  children: ReactNode;
  contentClassName?: string;
  /**
   * modal（默认）：居中悬浮窗（遮罩 + 定位 + 动效），关闭走 closeOverlayAndBack；
   * page：全高页面形态（无遮罩、无圆角阴影动效），用于升格为真路由页面的
   * 高频管理面——配合写作工作台布局让出侧边栏宽度（如 pl-56）。
   */
  variant?: "modal" | "page";
  /** page 变体的布局 class（如让出侧边栏的 pl-56 lg:pl-56） */
  className?: string;
}

export function FloatingShell<K extends string>({
  header,
  items,
  active,
  onItemChange,
  meta,
  onClose,
  isGuest,
  guestNotice,
  children,
  contentClassName,
  variant = "modal",
  className,
}: FloatingShellProps<K>) {
  const menuRef = useRef<HTMLDivElement>(null);
  const isPage = variant === "page";

  // ↑↓ 键在菜单项间移动（跳过分组头），并聚焦对应按钮
  const flatKeys: string[] = items.filter((e) => !e.group && e.key).map((e) => e.key!);
  const handleMenuKeyDown = (e: React.KeyboardEvent) => {
    if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
    e.preventDefault();
    if (flatKeys.length === 0) return;
    const idx = flatKeys.indexOf(String(active));
    const next: string = flatKeys[(idx + 1) % flatKeys.length]
      ?? flatKeys[(idx - 1 + flatKeys.length) % flatKeys.length]
      ?? String(active);
    onItemChange(next as K);
    menuRef.current?.querySelector<HTMLElement>(`[data-shell-item="${next}"]`)?.focus();
  };

  // page 变体：全高页面形态（无遮罩、无定位/动效），布局 class 由调用方给出
  if (isPage) {
    return (
      <div
        className={cn(
          "flex h-full min-h-0 flex-row overflow-hidden bg-background",
          className,
        )}
      >
        {/* ── 左侧菜单 ── */}
        <div className="w-48 shrink-0 border-r bg-muted/30 flex flex-col">
          <div className="flex h-[60px] items-center px-4 border-b">{header}</div>

          <div
            ref={menuRef}
            className="flex-1 p-2 space-y-0.5 overflow-y-auto scrollbar-hide"
            onKeyDown={handleMenuKeyDown}
          >
            {items.map((entry, i) =>
              entry.group ? (
                <p
                  key={`g-${entry.group}-${i}`}
                  className="px-3 pt-3 pb-1 text-[10px] font-medium uppercase tracking-wider text-muted-foreground/60 first:pt-1"
                >
                  {entry.group}
                </p>
              ) : (
                <button
                  key={entry.key}
                  data-shell-item={entry.key}
                  onClick={() => onItemChange(entry.key as K)}
                  className={cn(
                    "flex w-full items-center gap-2.5 rounded-lg px-3 py-2 text-sm transition-ui",
                    active === entry.key
                      ? "bg-accent text-foreground font-medium"
                      : "text-muted-foreground hover:bg-accent/50 hover:text-foreground",
                  )}
                >
                  {entry.icon && <entry.icon className="h-4 w-4 shrink-0" />}
                  <span className="flex-1 text-left">{entry.label}</span>
                </button>
              ),
            )}
          </div>
        </div>

        {/* ── 右侧内容区 ── */}
        {isGuest && guestNotice ? (
          <div className="flex-1 flex flex-col items-center justify-center gap-2 p-10 text-center">
            {(() => {
              const Icon = guestNotice.icon;
              return <Icon className="h-10 w-10 text-amber-500/60" />;
            })()}
            <p className="text-sm font-medium text-amber-900 dark:text-amber-200">{guestNotice.title}</p>
            <p className="text-xs text-amber-700 dark:text-amber-400">{guestNotice.description}</p>
          </div>
        ) : (
          <div className="flex-1 flex flex-col min-h-0">
            <div className="flex h-[60px] shrink-0 items-center justify-between px-6 bg-background border-b">
              <div className="min-w-0">
                <h2 className="text-lg font-semibold leading-tight">{meta[active]?.title}</h2>
                <p className="text-sm text-muted-foreground leading-tight">{meta[active]?.subtitle}</p>
              </div>
              <button
                onClick={onClose}
                className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-ui hover:bg-accent hover:text-foreground"
                title="返回写作台"
              >
                <X className="h-4 w-4" />
              </button>
            </div>

            <div className={cn("flex-1 overflow-y-auto relative scrollbar-hide", contentClassName)}>
              {children}
            </div>
          </div>
        )}
      </div>
    );
  }

  return (
    <DialogPrimitive.Root open onOpenChange={() => {}}>
      <DialogPortal>
        <DialogOverlay className="bg-black/20 backdrop-blur-[2px]" />
        <DialogPrimitive.Content
          onInteractOutside={(e) => e.preventDefault()}
          className={cn(
            "fixed left-[50%] top-[50%] z-50 flex h-[85vh] max-h-[90vh] w-[1100px] max-w-[94vw] xl:w-[1280px] translate-x-[-50%] translate-y-[-50%] flex-row overflow-hidden rounded-xl border bg-background shadow-md duration-200 data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95",
          )}
        >
          {/* ── 左侧菜单 ── */}
          <div className="w-48 shrink-0 border-r bg-muted/30 flex flex-col">
            <div className="flex h-[60px] items-center px-4 border-b">{header}</div>

            <div
              ref={menuRef}
              className="flex-1 p-2 space-y-0.5 overflow-y-auto scrollbar-hide"
              onKeyDown={handleMenuKeyDown}
            >
              {items.map((entry, i) =>
                entry.group ? (
                  <p
                    key={`g-${entry.group}-${i}`}
                    className="px-3 pt-3 pb-1 text-[10px] font-medium uppercase tracking-wider text-muted-foreground/60 first:pt-1"
                  >
                    {entry.group}
                  </p>
                ) : (
                  <button
                    key={entry.key}
                    data-shell-item={entry.key}
                    onClick={() => onItemChange(entry.key as K)}
                    className={cn(
                      "flex w-full items-center gap-2.5 rounded-lg px-3 py-2 text-sm transition-ui",
                      active === entry.key
                        ? "bg-accent text-foreground font-medium"
                        : "text-muted-foreground hover:bg-accent/50 hover:text-foreground",
                    )}
                  >
                    {entry.icon && <entry.icon className="h-4 w-4 shrink-0" />}
                    <span className="flex-1 text-left">{entry.label}</span>
                  </button>
                ),
              )}
            </div>
          </div>

          {/* ── 右侧内容区 ── */}
          {isGuest && guestNotice ? (
            <div className="flex-1 flex flex-col items-center justify-center gap-2 p-10 text-center">
              {(() => {
                const Icon = guestNotice.icon;
                return <Icon className="h-10 w-10 text-amber-500/60" />;
              })()}
              <p className="text-sm font-medium text-amber-900 dark:text-amber-200">{guestNotice.title}</p>
              <p className="text-xs text-amber-700 dark:text-amber-400">{guestNotice.description}</p>
            </div>
          ) : (
            <div className="flex-1 flex flex-col min-h-0">
              <div className="flex h-[60px] shrink-0 items-center justify-between px-6 bg-background border-b">
                <div className="min-w-0">
                  <h2 className="text-lg font-semibold leading-tight">{meta[active]?.title}</h2>
                  <p className="text-sm text-muted-foreground leading-tight">{meta[active]?.subtitle}</p>
                </div>
                <button
                  onClick={onClose}
                  className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-ui hover:bg-accent hover:text-foreground"
                >
                  <X className="h-4 w-4" />
                </button>
              </div>

              <div className={cn("flex-1 overflow-y-auto relative scrollbar-hide", contentClassName)}>
                {children}
              </div>
            </div>
          )}
        </DialogPrimitive.Content>
      </DialogPortal>
    </DialogPrimitive.Root>
  );
}

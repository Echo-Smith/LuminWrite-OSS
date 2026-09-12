/**
 * 风格选择器 — Popover 下拉选择
 * 支持全局风格 + 用户自定义风格 + Lumi 对话创建入口
 */
import { useState, useEffect, useCallback } from "react";
import { Palette, ChevronDown, Sparkles } from "lucide-react";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Badge } from "@/components/ui/badge";
import { useAuthStore } from "@/stores/auth-store";
import { useStyleListStore } from "@/stores/style-list-store";
import { useStyleChatStore } from "@/stores/style-chat-store";
import type { StyleOption } from "@/lib/types";
import { cn } from "@/lib/utils";

interface StylePickerProps {
  value: string;
  onChange: (slug: string) => void;
  compact?: boolean;
}

export function StylePicker({ value, onChange, compact = false }: StylePickerProps) {
  const [styles, setStyles] = useState<StyleOption[]>([]);
  const [open, setOpen] = useState(false);
  const listVersion = useStyleListStore((s) => s.version);
  const openAssistant = useStyleChatStore((s) => s.setOpen);
  const unappliedReady = useStyleChatStore((s) => s.unappliedReady);
  const token = useAuthStore((s) => s.token);

  const loadStyles = useCallback(() => {
    const headers: Record<string, string> = {};
    if (token) headers.Authorization = `Bearer ${token}`;
    fetch("/api/v2/styles", { headers })
      .then((res) => res.json())
      .then((data) => {
        const styles = data?.data?.styles ?? data?.styles ?? [];
        setStyles(styles);
      })
      .catch(() => {
        // 加载失败时展示空目录（风格由用户自建；OSS 不内置任何风格内容）。
        setStyles([]);
      });
  }, [token]);

  useEffect(() => {
    loadStyles();
  }, [loadStyles, listVersion]);

  const selected = styles.find((s) => s.slug === value);

  const globalStyles = styles.filter((s) => !s.tags?.includes("自定义"));
  const myStyles = styles.filter((s) => s.tags?.includes("自定义"));

  return (
    <>
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger asChild>
          <button
            aria-label={`写作风格：${selected?.name ?? (value === "" ? "默认" : "未选择")}`}
            className="composer-style-trigger flex h-8 items-center gap-1.5 rounded-xl px-2.5 text-sm text-muted-foreground transition-ui hover:bg-accent hover:text-foreground"
          >
            <span key={`style-icon-${value}`} className="flex items-center anim-fade-scale">
              <Palette className="h-[18px] w-[18px] text-[color:var(--desk-brass)]" />
            </span>
            <span key={`style-label-${selected?.slug ?? 'none'}`} className={cn("composer-control-label anim-fade-scale", compact && "sr-only")}>
              {selected?.name ?? (value === "" ? "默认" : "选择风格")}
            </span>
            <ChevronDown className={cn("composer-control-chevron h-4 w-4 opacity-50", compact && "hidden")} />
          </button>
        </PopoverTrigger>
        <PopoverContent align="end" className="w-80 max-h-[400px] overflow-y-auto">
          <div className="space-y-1">
            {/* 默认：不注入任何风格，模型按中性语风写作（空 style_slug 两条后端路径均安全降级） */}
            <button
              onClick={() => {
                onChange("");
                setOpen(false);
              }}
              className={cn(
                "flex w-full flex-col items-start gap-1 rounded-lg px-3 py-2.5 text-left transition-colors hover:bg-accent",
                value === "" && "bg-accent/50"
              )}
            >
              <div className="flex w-full items-center justify-between">
                <span className="text-sm font-medium">默认</span>
                <span className="text-xs text-muted-foreground">不使用风格</span>
              </div>
              <span className="text-xs text-muted-foreground">按平台中性的写作语风输出，不套用任何风格模板</span>
            </button>

            <div className="border-t pt-2 mt-2">
              <p className="text-xs font-medium text-muted-foreground px-1 pb-2">全局风格</p>
            </div>
            {styles.length === 0 && (
              <p className="text-center text-xs text-muted-foreground py-4">加载中...</p>
            )}
            {globalStyles.map((style) => (
              <button
                key={style.slug}
                onClick={() => {
                  onChange(style.slug);
                  setOpen(false);
                }}
                className={cn(
                  "flex w-full flex-col items-start gap-1 rounded-lg px-3 py-2.5 text-left transition-colors hover:bg-accent",
                  style.slug === value && "bg-accent/50"
                )}
              >
                <div className="flex w-full items-center justify-between">
                  <span className="text-sm font-medium">{style.name}</span>
                  <span className="text-xs text-muted-foreground">
                    {style.word_range[0]}-{style.word_range[1]}字
                  </span>
                </div>
                <span className="text-xs text-muted-foreground">{style.description}</span>
                <div className="flex items-center gap-1.5">
                  {style.tags.map((tag) => (
                    <Badge key={tag} variant="secondary" className="text-xs px-1.5 py-0">
                      {tag}
                    </Badge>
                  ))}
                </div>
              </button>
            ))}

            {myStyles.length > 0 && (
              <>
                <div className="border-t pt-2 mt-2">
                  <p className="text-xs font-medium text-muted-foreground px-1 pb-1">我的风格</p>
                </div>
                {myStyles.map((style) => (
                  <button
                    key={style.slug}
                    onClick={() => {
                      onChange(style.slug);
                      setOpen(false);
                    }}
                    className={cn(
                      "flex w-full flex-col items-start gap-1 rounded-lg px-3 py-2.5 text-left transition-colors hover:bg-accent",
                      style.slug === value && "bg-accent/50"
                    )}
                  >
                    <div className="flex w-full items-center justify-between">
                      <span className="text-sm font-medium">{style.name}</span>
                      <span className="text-xs text-muted-foreground">
                        {style.word_range[0]}-{style.word_range[1]}字
                      </span>
                    </div>
                    <span className="text-xs text-muted-foreground">{style.description}</span>
                    <div className="flex items-center gap-1.5">
                      {style.tags.map((tag) => (
                        <Badge key={tag} variant="secondary" className="text-xs px-1.5 py-0">
                          {tag}
                        </Badge>
                      ))}
                    </div>
                  </button>
                ))}
              </>
            )}

            <div className="border-t pt-2 mt-2">
              <button
                onClick={() => {
                  setOpen(false);
                  openAssistant(true);
                }}
                className="relative flex w-full items-center justify-center gap-1.5 rounded-lg px-3 py-2.5 text-sm font-medium text-amber-700 transition-colors hover:bg-amber-50 dark:text-amber-300 dark:hover:bg-amber-950/30"
              >
                <Sparkles className="h-4 w-4" />
                使用 Lumi 创建写作风格
                {unappliedReady && (
                  <span className="absolute right-3 top-1/2 h-2 w-2 -translate-y-1/2 rounded-full bg-[color:var(--desk-brass)]" />
                )}
              </button>
            </div>
          </div>
        </PopoverContent>
      </Popover>
    </>
  );
}

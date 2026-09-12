/**
 * 自定义 — 写作区外观个性化
 *
 * 写作区（document-stage）底色切换：淡白（默认）/ 纯白 / 淡绿（护眼）/ 牛皮纸（淡黄），
 * 深色模式自动使用各档位配套的暗色值。
 */
import { Check } from "lucide-react";
import { cn } from "@/lib/utils";
import { useWritingBg, type WritingBg } from "@/hooks/use-writing-bg";

const OPTIONS: { key: WritingBg; label: string; desc: string; light: string; dark: string }[] = [
  { key: "offwhite", label: "淡白", desc: "默认 · 纸张质感", light: "#fbfaf6", dark: "#141311" },
  { key: "white", label: "纯白", desc: "极简干净", light: "#ffffff", dark: "#1a1917" },
  { key: "green", label: "淡绿", desc: "护眼 · 长时间书写", light: "#cfe6cd", dark: "#16211a" },
  { key: "kraft", label: "牛皮纸", desc: "淡黄 · 温暖复古", light: "#f3e7cd", dark: "#211c13" },
];

export function CustomizationSection() {
  const [writingBg, setWritingBg] = useWritingBg();

  return (
    <div className="px-6 pt-6 pb-12 space-y-6">
      <div className="space-y-2">
        <h3 className="text-sm font-semibold">写作区底色</h3>
        <p className="text-xs text-muted-foreground leading-relaxed">
          更换写作区（对话流与稿件所在的桌面区域）的背景颜色，默认为纸张质感的淡白色。
          深色模式下会自动使用各颜色配套的暗色值。
        </p>
      </div>

      <div className="grid grid-cols-2 gap-3">
        {OPTIONS.map((opt) => {
          const active = writingBg === opt.key;
          return (
            <button
              key={opt.key}
              onClick={() => setWritingBg(opt.key)}
              aria-pressed={active}
              className={cn(
                "relative flex items-center gap-3 rounded-lg border p-3 text-left transition-ui",
                active
                  ? "border-primary bg-accent/60"
                  : "border-border/60 hover:border-border hover:bg-accent/30",
              )}
            >
              {/* 双色预览：上半浅色、下半深色 */}
              <span
                className="flex h-10 w-10 shrink-0 flex-col overflow-hidden rounded-full border border-border/60"
                aria-hidden="true"
              >
                <span className="h-1/2 w-full" style={{ background: opt.light }} />
                <span className="h-1/2 w-full" style={{ background: opt.dark }} />
              </span>
              <span className="min-w-0">
                <span className="block text-xs font-medium">{opt.label}</span>
                <span className="block text-[10px] text-muted-foreground">{opt.desc}</span>
              </span>
              {active && (
                <Check className="absolute right-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-primary" />
              )}
            </button>
          );
        })}
      </div>

      <p className="text-[10px] text-muted-foreground">
        偏好保存在本设备，对所有文档生效；切换即时生效，无需刷新。
      </p>
    </div>
  );
}

/**
 * TopicSidebar — 左侧导航栏
 */
import {
  Flame, Star, ChevronDown, ChevronRight, ListChecks, PenLine,
} from "lucide-react";
import { platformLabel, platformDotColor } from "@/lib/topic-helpers";
import type { PlatformStat } from "@/lib/types";
import { cn } from "@/lib/utils";

interface TopicSidebarProps {
  filter: string;
  setFilter: (f: string) => void;
  hotExpanded: boolean;
  setHotExpanded: (fn: (v: boolean) => boolean) => void;
  platformStats: PlatformStat[];
}

export function TopicSidebar({
  filter, setFilter, hotExpanded, setHotExpanded, platformStats,
}: TopicSidebarProps) {
  const hotPlatformItems = platformStats
    .filter((s) => s.platform && s.platform !== "user" && s.platform !== "system")
    .map((s) => ({
      key: `platform:${s.platform}`,
      platform: s.platform || "",
      label: platformLabel(s.platform || undefined),
      count: s.count,
    }));

  const isHotParentActive = filter === "hot" || filter.startsWith("platform:");

  const navItemClass = (active: boolean) =>
    cn(
      "flex items-center gap-2 rounded-md px-3 py-2 text-sm font-medium transition-colors",
      active
        ? "bg-primary/10 text-primary"
        : "text-muted-foreground hover:bg-muted hover:text-foreground"
    );

  return (
    <aside className="w-56 flex-shrink-0 border-r bg-muted/30">
      <nav className="flex flex-col gap-0.5 p-3">
        <button className={navItemClass(filter === "all")} onClick={() => setFilter("all")}>
          <ListChecks className="h-4 w-4" />
          全部选题
        </button>

        {/* 热搜 (expandable) */}
        <div>
          <button
            className={cn(navItemClass(isHotParentActive), "w-full")}
            onClick={() => { setHotExpanded((v) => !v); setFilter("hot"); }}
          >
            {hotExpanded ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
            <Flame className="h-4 w-4" />
            热搜汇总
          </button>
          {hotExpanded && (
            <div className="ml-4 mt-0.5 flex flex-col gap-0.5 border-l pl-2">
              {hotPlatformItems.length === 0 ? (
                <span className="px-3 py-1.5 text-xs text-muted-foreground/60">暂无热搜源</span>
              ) : (
                hotPlatformItems.map((item) => (
                  <button
                    key={item.key}
                    className={cn(
                      "flex items-center gap-2 rounded-md px-3 py-1.5 text-sm transition-colors",
                      filter === item.key
                        ? "bg-primary/10 text-primary"
                        : "text-muted-foreground hover:bg-muted hover:text-foreground"
                    )}
                    onClick={() => setFilter(item.key)}
                  >
                    <span className={cn("h-2 w-2 rounded-full", platformDotColor(item.platform))} />
                    {item.label}
                    <span className="ml-auto text-xs text-muted-foreground/60">{item.count}</span>
                  </button>
                ))
              )}
            </div>
          )}
        </div>

        <button className={navItemClass(filter === "user")} onClick={() => setFilter("user")}>
          <PenLine className="h-4 w-4" />
          自定义选题
        </button>

        <button className={navItemClass(filter === "favorites")} onClick={() => setFilter("favorites")}>
          <Star className="h-4 w-4" />
          我的收藏
        </button>
      </nav>
    </aside>
  );
}

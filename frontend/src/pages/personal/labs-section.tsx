/**
 * 实验室 Section — 实验性功能开关
 *
 * 展示正在测试中的功能，用户可自主决定是否启用。
 * 所有开关均通过 settings-store 云端同步。
 */
import { FlaskConical, Newspaper, AlertTriangle } from "lucide-react";
import { Switch } from "@/components/ui/switch";
import { useSettingsStore } from "@/stores/settings-store";
import { useAuthStore } from "@/stores/auth-store";

export function LabsSection() {
  const enableEditorial = useSettingsStore((s) => s.enableEditorial);
  const setEnableEditorial = useSettingsStore((s) => s.setEnableEditorial);
  const isGuest = useAuthStore((s) => s.user?.role === "guest");

  return (
    <div className="px-6 pt-6 pb-12 space-y-6">
      {/* 顶部说明 */}
      <div className="flex items-start gap-3 rounded-lg border border-amber-200 bg-amber-50/50 dark:border-amber-800 dark:bg-amber-950/30 p-4">
        <AlertTriangle className="h-5 w-5 shrink-0 text-amber-500 mt-0.5" />
        <div className="space-y-1">
          <p className="text-sm font-medium text-amber-900 dark:text-amber-200">
            实验性功能提示
          </p>
          <p className="text-xs text-amber-700 dark:text-amber-300">
            此处的功能正在开发测试中，可能存在不稳定或体验不完善的情况。
            启用后如遇到问题，可随时关闭。功能稳定后会正式发布。
          </p>
        </div>
      </div>

      {/* 实验功能列表 */}
      <div className="space-y-3">
        {/* 编辑部入口 */}
        <div className="flex items-start gap-4 rounded-lg border p-4 transition-ui hover:bg-accent/30">
          <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-primary/10">
            <Newspaper className="h-5 w-5 text-primary" />
          </div>
          <div className="flex-1 min-w-0">
            <div className="flex items-center gap-2">
              <span className="text-sm font-semibold">编辑部入口</span>
              <span className="rounded-full bg-amber-100 px-2 py-0.5 text-[10px] font-medium text-amber-700 dark:bg-amber-900/50 dark:text-amber-300">
                Beta
              </span>
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              在左侧栏显示编辑部入口，支持多 Agent 角色协作的长文创作模式。
              适合万字长文、深度报告等高质量产出场景。
            </p>
          </div>
          <Switch
            checked={enableEditorial}
            disabled={isGuest}
            onCheckedChange={(checked) => setEnableEditorial(checked)}
          />
        </div>
      </div>

      {/* 底部说明 */}
      <div className="rounded-lg bg-muted/50 p-4">
        <div className="flex items-center gap-2">
          <FlaskConical className="h-4 w-4 text-muted-foreground" />
          <p className="text-xs text-muted-foreground">
            <strong className="text-foreground">实验室功能说明</strong>
          </p>
        </div>
        <p className="mt-2 text-xs text-muted-foreground">
          实验功能默认关闭，需手动开启。开启后，功能入口将出现在侧边栏对应位置。
          所有开关设置跟随你的账号，换设备登录也会保持一致。
        </p>
        {isGuest && (
          <p className="mt-2 text-[11px] text-amber-600">
            游客模式不支持启用实验功能，请注册账号后使用。
          </p>
        )}
      </div>
    </div>
  );
}

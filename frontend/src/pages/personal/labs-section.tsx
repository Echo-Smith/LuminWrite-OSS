/**
 * 实验室 Section — 实验性功能开关
 *
 * 展示正在测试中的功能，用户可自主决定是否启用。
 * 所有开关均通过 settings-store 云端同步。
 */
import { BookOpenText, FlaskConical, Newspaper, AlertTriangle, Clock, Shield, Database } from "lucide-react";
import { Switch } from "@/components/ui/switch";
import { useSettingsStore } from "@/stores/settings-store";
import { useAuthStore } from "@/stores/auth-store";

export function LabsSection() {
  const enableEditorial = useSettingsStore((s) => s.enableEditorial);
  const setEnableEditorial = useSettingsStore((s) => s.setEnableEditorial);
  const enableResearchReview = useSettingsStore((s) => s.enableResearchReview);
  const setEnableResearchReview = useSettingsStore((s) => s.setEnableResearchReview);
  const labsCronPanel = useSettingsStore((s) => s.labsCronPanel);
  const setLabsCronPanel = useSettingsStore((s) => s.setLabsCronPanel);
  const labsSensitivePanel = useSettingsStore((s) => s.labsSensitivePanel);
  const setLabsSensitivePanel = useSettingsStore((s) => s.setLabsSensitivePanel);
  const labsKbMaintenance = useSettingsStore((s) => s.labsKbMaintenance);
  const setLabsKbMaintenance = useSettingsStore((s) => s.setLabsKbMaintenance);
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
        {/* 工作台入口 */}
        <div className="flex items-start gap-4 rounded-lg border p-4 transition-ui hover:bg-accent/30">
          <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-primary/10">
            <Newspaper className="h-5 w-5 text-primary" />
          </div>
          <div className="flex-1 min-w-0">
            <div className="flex items-center gap-2">
              <span className="text-sm font-semibold">工作台入口</span>
              <span className="rounded-full bg-amber-100 px-2 py-0.5 text-[10px] font-medium text-amber-700 dark:bg-amber-900/50 dark:text-amber-300">
                Beta
              </span>
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              在左侧栏显示工作台入口，支持多 Agent 角色协作的长文创作模式。
              适合万字长文、深度报告等高质量产出场景。
            </p>
          </div>
          <Switch
            checked={enableEditorial}
            disabled={isGuest}
            onCheckedChange={(checked) => setEnableEditorial(checked)}
          />
        </div>

        {/* 研究综述（可追溯文献综述写作路径） */}
        <div className="flex items-start gap-4 rounded-lg border p-4 transition-ui hover:bg-accent/30">
          <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-primary/10">
            <BookOpenText className="h-5 w-5 text-primary" />
          </div>
          <div className="flex-1 min-w-0">
            <div className="flex items-center gap-2">
              <span className="text-sm font-semibold">研究综述</span>
              <span className="rounded-full bg-amber-100 px-2 py-0.5 text-[10px] font-medium text-amber-700 dark:bg-amber-900/50 dark:text-amber-300">
                Beta
              </span>
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              可追溯的学术文献综述写作：自动检索与阅读论文，每条结论可回看原文出处，
              证据与提纲两次确认后再生成正文。开启后，写作方式中会出现「研究综述」选项。
              需要联网检索论文，消耗较多时间与用量。
            </p>
          </div>
          <Switch
            checked={enableResearchReview}
            disabled={isGuest}
            onCheckedChange={(checked) => setEnableResearchReview(checked)}
          />
        </div>

        {/* 定时任务面板（仅 admin 可开） */}
        {!isGuest && (
          <div className="flex items-start gap-4 rounded-lg border p-4 transition-ui hover:bg-accent/30">
            <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-primary/10">
              <Clock className="h-5 w-5 text-primary" />
            </div>
            <div className="flex-1 min-w-0">
              <div className="flex items-center gap-2">
                <span className="text-sm font-semibold">定时任务面板</span>
                <span className="rounded-full bg-purple-100 px-2 py-0.5 text-[10px] font-medium text-purple-700 dark:bg-purple-900/50 dark:text-purple-300">
                  Admin
                </span>
              </div>
              <p className="mt-1 text-xs text-muted-foreground">
                在个人中心显示「定时任务」面板：查看与手动触发部署的定时任务（热点抓取、
                知识库维护等）。属于部署级功能，个人部署即本人管理。
              </p>
            </div>
            <Switch
              checked={labsCronPanel}
              onCheckedChange={(checked) => setLabsCronPanel(checked)}
            />
          </div>
        )}

        {/* 知识库维护面板（仅 admin 可开） */}
        {!isGuest && (
          <div className="flex items-start gap-4 rounded-lg border p-4 transition-ui hover:bg-accent/30">
            <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-primary/10">
              <Database className="h-5 w-5 text-primary" />
            </div>
            <div className="flex-1 min-w-0">
              <div className="flex items-center gap-2">
                <span className="text-sm font-semibold">知识库维护面板</span>
                <span className="rounded-full bg-purple-100 px-2 py-0.5 text-[10px] font-medium text-purple-700 dark:bg-purple-900/50 dark:text-purple-300">
                  Admin
                </span>
              </div>
              <p className="mt-1 text-xs text-muted-foreground">
                在个人中心显示「知识库维护」面板：全局语料的多库管理、实体图谱、
                统计与重建操作（embeddings 生成 / 重分块 / URL 重导入）。
              </p>
            </div>
            <Switch
              checked={labsKbMaintenance}
              onCheckedChange={(checked) => setLabsKbMaintenance(checked)}
            />
          </div>
        )}

        {/* 敏感词库面板（仅 admin 可开） */}
        {!isGuest && (
          <div className="flex items-start gap-4 rounded-lg border p-4 transition-ui hover:bg-accent/30">
            <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-primary/10">
              <Shield className="h-5 w-5 text-primary" />
            </div>
            <div className="flex-1 min-w-0">
              <div className="flex items-center gap-2">
                <span className="text-sm font-semibold">敏感词库面板</span>
                <span className="rounded-full bg-purple-100 px-2 py-0.5 text-[10px] font-medium text-purple-700 dark:bg-purple-900/50 dark:text-purple-300">
                  Admin
                </span>
              </div>
              <p className="mt-1 text-xs text-muted-foreground">
                在个人中心显示「敏感词库」面板：维护内容安全词库（新增/批量导入/启停），
                写后自检将按词库做内容安全检查。
              </p>
            </div>
            <Switch
              checked={labsSensitivePanel}
              onCheckedChange={(checked) => setLabsSensitivePanel(checked)}
            />
          </div>
        )}
      </div>

      {/* 底部说明 */}
      <div className="rounded-lg bg-muted/50 p-4">
        <div className="flex items-center gap-2">
          <FlaskConical className="h-4 w-4 text-muted-foreground" />
          <p className="text-xs text-muted-foreground">
            <strong className="text-foreground">工作台功能说明</strong>
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

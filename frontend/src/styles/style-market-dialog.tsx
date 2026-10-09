/**
 * 写作风格（悬浮窗，/styles）
 *
 * 与个人中心/审计中心/插件同款的 FloatingShell 骨架，应用市场式三区：
 * - 风格库：内置/全局风格浏览与一键导入
 * - 我的风格：自建/导入风格的管理（创建、编辑、投稿、删除）
 * - 技能：运行时技能/工具插件（OSS）
 *
 * 「创建写作风格」入口已从输入区与个人中心统一收敛到本窗。
 */
import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { Palette, Library, Wrench, Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useAuthStore } from "@/stores/auth-store";
import { closeOverlayAndBack } from "@/lib/close-overlay";
import { FloatingShell, type FloatingShellEntry } from "@/components/shell/floating-shell";
import { StyleLibrarySection } from "./style-library-section";
import { StyleSection } from "./my-styles-section";
import { SkillsSection } from "./skills-section";

type StyleKey = "library" | "mine" | "skills";

const ITEMS: FloatingShellEntry[] = [
  { key: "library", label: "风格库", icon: Library },
  { key: "mine", label: "我的风格", icon: Palette },
  { key: "skills", label: "技能", icon: Wrench },
];

const META: Record<StyleKey, { title: string; subtitle: string }> = {
  library: { title: "风格库", subtitle: "内置风格开箱即用，可一键导入改造" },
  mine: { title: "我的风格", subtitle: "创建、编辑与管理你的写作风格" },
  skills: { title: "技能", subtitle: "运行时技能/工具插件" },
};

export function StyleMarketDialog() {
  const navigate = useNavigate();
  const [active, setActive] = useState<StyleKey>("library");
  const [open, setOpen] = useState(true);
  const isGuest = useAuthStore((s) => s.user?.role === "guest");

  const handleClose = () => {
    setOpen(false);
    closeOverlayAndBack(navigate);
  };

  const header = (
    <div className="flex items-center gap-2.5 w-full">
      <div className="flex h-9 w-9 items-center justify-center rounded-full bg-primary/10">
        <Palette className="h-4 w-4 text-primary" />
      </div>
      <div>
        <p className="text-sm font-medium">写作风格</p>
        <p className="text-[11px] text-muted-foreground">styles &amp; skills</p>
      </div>
    </div>
  );

  // 「我的风格」区的创建入口：派发个人中心同款事件，my-styles-section 监听打开创建弹窗
  const triggerCreate = () => {
    window.dispatchEvent(new CustomEvent("personal-center-add"));
  };

  return (
    <FloatingShell
      header={header}
      items={ITEMS}
      active={active}
      onItemChange={setActive}
      meta={META}
      onClose={handleClose}
      isGuest={isGuest}
      guestNotice={{
        icon: Palette,
        title: "游客模式无法管理写作风格",
        description: "注册并登录后可导入风格、创建自己的写作风格。",
      }}
      contentClassName="p-6"
    >
      {active === "mine" && (
        <div className="flex justify-end mb-3">
          <Button size="sm" onClick={triggerCreate} className="gap-1.5">
            <Plus className="h-4 w-4" />
            新建风格
          </Button>
        </div>
      )}
      {active === "library" && <StyleLibrarySection />}
      {active === "mine" && <StyleSection />}
      {active === "skills" && <SkillsSection />}
    </FloatingShell>
  );
}

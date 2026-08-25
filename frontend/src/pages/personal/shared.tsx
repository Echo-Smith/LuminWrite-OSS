/**
 * 个人中心 — 共享组件与类型
 *
 * 被主组件和各 Section 子页面引用。
 */
import { type ReactNode } from "react";
import {
  Brain, User, KeyRound, Palette, Settings, Wallet,
  Bell, Monitor, Info, FlaskConical, type LucideIcon,
  X, Plus,
} from "lucide-react";
import { cn } from "@/lib/utils";

// ─── 菜单类型 ──────────────────────────────────────────

export type MenuKey = "profile" | "styles" | "memory" | "settings" | "notifications" | "account" | "devices" | "wallet" | "labs" | "about";

export interface MenuItem {
  key: MenuKey;
  label: string;
  icon: LucideIcon;
}

export const MENU_ITEMS: MenuItem[] = [
  { key: "profile", label: "个人信息", icon: User },
  { key: "styles", label: "写作风格", icon: Palette },
  { key: "memory", label: "记忆管理", icon: Brain },
  { key: "settings", label: "偏好设置", icon: Settings },
  { key: "wallet", label: "积分管理", icon: Wallet },
  { key: "notifications", label: "通知设置", icon: Bell },
  { key: "account", label: "账号管理", icon: KeyRound },
  { key: "devices", label: "设备管理", icon: Monitor },
  { key: "labs", label: "工作台设置", icon: FlaskConical },
  { key: "about", label: "关于笔润智谈", icon: Info },
];

export const SECTION_META: Record<MenuKey, { title: string; subtitle: string }> = {
  profile: { title: "个人信息", subtitle: "查看和修改你的账号信息" },
  styles: { title: "写作风格", subtitle: "管理你的自定义写作风格" },
  memory: { title: "记忆管理", subtitle: "管理 AI 学习到的写作偏好" },
  settings: { title: "偏好设置", subtitle: "配置默认写作风格和编排模式" },
  wallet: { title: "积分管理", subtitle: "查看余额、消费记录和套餐" },
  notifications: { title: "通知设置", subtitle: "管理在线通知偏好" },
  account: { title: "账号管理", subtitle: "管理密码和 Passkey 认证" },
  devices: { title: "设备管理", subtitle: "查看在线设备和管理会话" },
  labs: { title: "工作台设置", subtitle: "体验正在测试中的实验性功能" },
  about: { title: "关于笔润智谈", subtitle: "版本信息与产品介绍" },
};

// ─── 右下角圆形 + 按钮 ──────────────────────────────────

export function FloatingAddButton({ onClick, disabled }: { onClick: () => void; disabled?: boolean }) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      className={cn(
        "absolute bottom-6 right-6 z-20 flex h-12 w-12 items-center justify-center rounded-full shadow-lg transition-transform",
        disabled
          ? "bg-muted text-muted-foreground cursor-not-allowed"
          : "bg-primary text-primary-foreground hover:scale-105 active:scale-95"
      )}
      title={disabled ? "注册后可用" : "新建"}
    >
      <Plus className="h-5 w-5" />
    </button>
  );
}

// ─── 简易模态框 ──────────────────────────────────────────

export function SimpleModal({
  open,
  onClose,
  title,
  children,
  maxWidth = "max-w-lg",
}: {
  open: boolean;
  onClose: () => void;
  title: string;
  children: ReactNode;
  maxWidth?: string;
}) {
  if (!open) return null;
  return (
    <div
      className="fixed inset-0 z-[100] flex items-center justify-center"
      onClick={onClose}
    >
      <div className="absolute inset-0 bg-black/40 backdrop-blur-[2px]" />
      <div
        className={cn(
          "relative z-10 w-[92vw] rounded-xl border bg-background shadow-lg max-h-[85vh] flex flex-col animate-in fade-in-0 zoom-in-95 duration-150",
          maxWidth
        )}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between border-b px-5 py-3.5 shrink-0">
          <h3 className="text-base font-semibold">{title}</h3>
          <button
            onClick={onClose}
            className="flex h-7 w-7 items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-foreground transition-ui shrink-0"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
        <div className="p-5 overflow-y-auto">
          {children}
        </div>
      </div>
    </div>
  );
}

export function SimpleModalFooter({ children }: { children: ReactNode }) {
  return (
    <div className="flex justify-end gap-2 mt-5">
      {children}
    </div>
  );
}

// ─── 日期格式化 ──────────────────────────────────────────

export function formatDate(dateStr: string): string {
  const d = new Date(dateStr);
  const now = new Date();
  const diffDays = Math.floor((now.getTime() - d.getTime()) / (1000 * 60 * 60 * 24));
  if (diffDays === 0) return "今天";
  if (diffDays === 1) return "昨天";
  if (diffDays < 7) return `${diffDays} 天前`;
  return d.toLocaleDateString("zh-CN");
}

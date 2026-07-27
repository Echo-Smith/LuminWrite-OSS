/**
 * Auth Modal 全局状态管理
 * 任何组件都可以通过 useAuthModal() 打开登录/注册弹窗
 */
import { create } from "zustand";

type AuthTab = "login" | "register";

interface AuthModalState {
  open: boolean;
  guestToken?: string;
  defaultTab?: AuthTab;

  openAuth: (opts?: { guestToken?: string; defaultTab?: AuthTab }) => void;
  closeAuth: () => void;
}

export const useAuthModal = create<AuthModalState>((set) => ({
  open: false,
  guestToken: undefined,
  defaultTab: undefined,

  openAuth: (opts) =>
    set({
      open: true,
      guestToken: opts?.guestToken,
      defaultTab: opts?.defaultTab,
    }),

  closeAuth: () =>
    set({
      open: false,
      guestToken: undefined,
      defaultTab: undefined,
    }),
}));

/**
 * Toast 通知 Store — 轻量级全局通知系统
 *
 * 支持：success / error / warning / info 四种类型
 * 自动消失（默认 3s），可手动关闭
 */
import { create } from "zustand";

export type ToastType = "success" | "error" | "warning" | "info";

export interface ToastItem {
  id: string;
  type: ToastType;
  title: string;
  description?: string;
  duration: number; // 0 = 不自动消失
  action?: { label: string; onClick: () => void };
  /** 退场动画标记 — true 时渲染退场动画，动画结束后移除 */
  leaving?: boolean;
}

interface ToastStore {
  toasts: ToastItem[];
  add: (toast: Omit<ToastItem, "id">) => string;
  dismiss: (id: string) => void;
  clear: () => void;
}

let toastId = 0;

export const useToastStore = create<ToastStore>((set) => ({
  toasts: [],

  add: (toast) => {
    const id = `toast-${++toastId}`;
    const item: ToastItem = { id, ...toast, duration: toast.duration ?? 3000 };
    set((s) => ({ toasts: [...s.toasts, item] }));

    // 自动消失：先标记 leaving 播放退场动画，再移除
    if (item.duration > 0) {
      setTimeout(() => {
        set((s) => ({
          toasts: s.toasts.map((t) =>
            t.id === id ? { ...t, leaving: true } : t
          ),
        }));
        // 等退场动画播完再移除
        setTimeout(() => {
          set((s) => ({ toasts: s.toasts.filter((t) => t.id !== id) }));
        }, 200); // anim-exit-drop duration=150ms + 50ms buffer
      }, item.duration);
    }

    return id;
  },

  dismiss: (id) => {
    // 先标记 leaving 播放退场动画
    set((s) => ({
      toasts: s.toasts.map((t) =>
        t.id === id ? { ...t, leaving: true } : t
      ),
    }));
    // 等退场动画播完再移除
    setTimeout(() => {
      set((s) => ({ toasts: s.toasts.filter((t) => t.id !== id) }));
    }, 200);
  },

  clear: () => set({ toasts: [] }),
}));

// ─── 便捷方法 ──────────────────────────────────────────

export const toast = {
  success: (title: string, description?: string, duration?: number) =>
    useToastStore.getState().add({ type: "success", title, description, duration: duration ?? 3000 }),

  error: (title: string, description?: string, duration?: number) =>
    useToastStore.getState().add({ type: "error", title, description, duration: duration ?? 5000 }),

  warning: (title: string, description?: string, duration?: number) =>
    useToastStore.getState().add({ type: "warning", title, description, duration: duration ?? 4000 }),

  info: (title: string, description?: string, duration?: number) =>
    useToastStore.getState().add({ type: "info", title, description, duration: duration ?? 3000 }),

  withAction: (title: string, action: { label: string; onClick: () => void }, type: ToastType = "info") =>
    useToastStore.getState().add({ type, title, action, duration: 6000 }),
};

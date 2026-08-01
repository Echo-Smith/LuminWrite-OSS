/**
 * useKeyboardShortcuts — 全局键盘快捷键管理
 *
 * 快捷键列表：
 *   Cmd/Ctrl + Enter  → 发送消息
 *   Cmd/Ctrl + K      → 聚焦输入框
 *   Cmd/Ctrl + B      → 切换侧栏
 *   Cmd/Ctrl + .      → 切换详情面板
 *   ESC               → 关闭面板/对话框
 *   Cmd/Ctrl + Shift+R→ 重写最后一篇
 */
import { useEffect } from "react";

interface ShortcutHandlers {
  onSend?: () => void;
  onFocusInput?: () => void;
  onToggleSidebar?: () => void;
  onToggleDetail?: () => void;
  onEscape?: () => void;
  onRegenerate?: () => void;
}

export function useKeyboardShortcuts(handlers: ShortcutHandlers) {
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      const mod = e.metaKey || e.ctrlKey;

      // Cmd/Ctrl + Enter → 发送
      if (mod && e.key === "Enter") {
        e.preventDefault();
        handlers.onSend?.();
        return;
      }

      // Cmd/Ctrl + K → 聚焦输入框
      if (mod && e.key === "k") {
        e.preventDefault();
        handlers.onFocusInput?.();
        return;
      }

      // Cmd/Ctrl + B → 切换侧栏
      if (mod && e.key === "b") {
        e.preventDefault();
        handlers.onToggleSidebar?.();
        return;
      }

      // Cmd/Ctrl + . → 切换详情面板
      if (mod && e.key === ".") {
        e.preventDefault();
        handlers.onToggleDetail?.();
        return;
      }

      // Cmd/Ctrl + Shift + R → 重写
      if (mod && e.shiftKey && e.key === "R") {
        e.preventDefault();
        handlers.onRegenerate?.();
        return;
      }

      // ESC → 关闭面板
      if (e.key === "Escape" && !mod && !e.shiftKey) {
        // 不拦截输入框中的 ESC
        const target = e.target as HTMLElement;
        if (target.tagName === "INPUT" || target.tagName === "TEXTAREA") return;
        handlers.onEscape?.();
        return;
      }
    };

    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, [handlers]);
}

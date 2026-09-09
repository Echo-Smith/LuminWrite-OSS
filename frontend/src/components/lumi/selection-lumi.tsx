import * as React from "react";
import { Lumi } from "@/components/lumi/lumi";

/**
 * ─── 正文选区唤起 Lumi ─────────────────────────────────────
 *
 * 监听 document-body 内的文本选区，选中 ≥ 2 字后在选区下方浮出
 * 「让 Lumi 润色」菜单；点击回调选中文本并收起。滚动/收起选区即隐藏。
 * 仅统计可见文本：跳过引用角标、临时层等非正文节点。
 */

const MIN_SELECTION_LENGTH = 2;

interface SelectionLumiProps {
  /** 选区所在容器（只响应该容器内的选区） */
  containerRef: React.RefObject<HTMLElement | null>;
  /** 点击润色时回调选中文本 */
  onPolish: (text: string) => void;
}

export function SelectionLumi({ containerRef, onPolish }: SelectionLumiProps) {
  const [menu, setMenu] = React.useState<{ x: number; y: number; text: string } | null>(null);
  const menuRef = React.useRef<HTMLDivElement | null>(null);

  React.useEffect(() => {
    const update = () => {
      const container = containerRef.current;
      const selection = window.getSelection();
      if (!container || !selection || selection.isCollapsed) {
        setMenu(null);
        return;
      }
      const text = selection.toString().trim();
      const range = selection.rangeCount > 0 ? selection.getRangeAt(0) : null;
      // 选区必须落在容器内
      if (!text || text.length < MIN_SELECTION_LENGTH || !range || !container.contains(range.commonAncestorContainer)) {
        setMenu(null);
        return;
      }
      const rect = range.getBoundingClientRect();
      if (!rect || (rect.width === 0 && rect.height === 0)) {
        setMenu(null);
        return;
      }
      setMenu({ x: rect.left + rect.width / 2, y: rect.bottom, text });
    };

    const onSelectionChange = () => {
      // selectionchange 在拖选过程中高频触发，等鼠标抬起再定位
      if (window.getSelection()?.isCollapsed) setMenu(null);
    };
    const hide = () => setMenu(null);

    document.addEventListener("selectionchange", onSelectionChange);
    document.addEventListener("mouseup", update);
    document.addEventListener("keyup", update);
    window.addEventListener("scroll", hide, true);
    window.addEventListener("resize", hide);
    return () => {
      document.removeEventListener("selectionchange", onSelectionChange);
      document.removeEventListener("mouseup", update);
      document.removeEventListener("keyup", update);
      window.removeEventListener("scroll", hide, true);
      window.removeEventListener("resize", hide);
    };
  }, [containerRef]);

  if (!menu) return null;

  return (
    <div
      ref={menuRef}
      className="anim-fade-up fixed z-40 -translate-x-1/2"
      style={{ left: menu.x, top: menu.y + 8 }}
      role="toolbar"
      aria-label="选区操作"
      // 按下菜单本身时避免清空选区导致回调拿不到文本
      onMouseDown={(e) => e.preventDefault()}
    >
      <button
        onClick={() => {
          onPolish(menu.text);
          window.getSelection()?.removeAllRanges();
          setMenu(null);
        }}
        className="flex items-center gap-1.5 rounded-full border bg-background px-3 py-1.5 text-xs font-medium text-foreground shadow-md transition-ui hover:bg-accent"
      >
        <Lumi state="idle" size={16} />
        让 Lumi 润色
      </button>
    </div>
  );
}

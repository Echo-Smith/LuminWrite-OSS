/**
 * 悬浮窗关闭 — 有站内历史则返回上一页，否则落到 /write
 *
 * react-router 会在 history.state 写入 idx；直达链接（新开标签/书签）
 * 的 idx 为 0 或缺失，此时 navigate(-1) 会退出站点。
 */
import type { NavigateFunction } from "react-router-dom";

export function closeOverlayAndBack(navigate: NavigateFunction, fallback = "/write"): void {
  const idx = (window.history.state as { idx?: number } | null)?.idx ?? 0;
  if (idx > 0) navigate(-1);
  else navigate(fallback, { replace: true });
}

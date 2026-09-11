/**
 * useWritingBg — 写作区底色偏好
 *
 * localStorage 持久化（全局外观偏好，不区分文档），
 * 由写作工作台根节点转为 data-writing-bg 属性驱动 CSS 变量。
 */
import { useSyncExternalStore } from "react";

export type WritingBg = "offwhite" | "white" | "green" | "kraft";

const KEY = "lumin-writing-stage-bg";
const VALID: WritingBg[] = ["offwhite", "white", "green", "kraft"];

let value: WritingBg = load();
const listeners = new Set<() => void>();

function load(): WritingBg {
  try {
    const raw = localStorage.getItem(KEY);
    return VALID.includes(raw as WritingBg) ? (raw as WritingBg) : "offwhite";
  } catch {
    return "offwhite";
  }
}

function subscribe(cb: () => void) {
  listeners.add(cb);
  return () => { listeners.delete(cb); };
}

function getSnapshot() {
  return value;
}

export function useWritingBg(): [WritingBg, (next: WritingBg) => void] {
  const current = useSyncExternalStore(subscribe, getSnapshot);
  const set = (next: WritingBg) => {
    value = next;
    try { localStorage.setItem(KEY, next); } catch { /* 隐私模式等场景忽略 */ }
    listeners.forEach((cb) => cb());
  };
  return [current, set];
}

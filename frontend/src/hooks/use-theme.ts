/**
 * useTheme — 深浅模式切换 Hook
 *
 * 三种模式：light / dark / system（跟随系统，实时响应系统深浅切换）。
 * 通过在 <html> 上切换 .dark class 实现，持久化到 localStorage；
 * 未手动选择过时默认跟随系统。
 */
import { useState, useEffect, useCallback } from "react";

export type Theme = "light" | "dark" | "system";

const STORAGE_KEY = "luminbuddy-theme";
const SYSTEM_DARK_QUERY = "(prefers-color-scheme: dark)";

const THEME_ICON_ASSETS = {
  light: {
    svg: "/favicon-lumi.svg",
    png: "/app-icon.png",
    apple: "/apple-touch-icon.png",
  },
  dark: {
    svg: "/favicon-lumi-dark.svg",
    png: "/app-icon-dark.png",
    apple: "/apple-touch-icon-dark.png",
  },
} as const;

function syncThemeIcons(theme: "light" | "dark") {
  const assets = THEME_ICON_ASSETS[theme];
  document.querySelector<HTMLLinkElement>("#app-favicon-svg")?.setAttribute("href", assets.svg);
  document.querySelector<HTMLLinkElement>("#app-favicon-png")?.setAttribute("href", assets.png);
  document.querySelector<HTMLLinkElement>("#app-favicon-apple")?.setAttribute("href", assets.apple);
}

function getStoredTheme(): Theme {
  if (typeof window === "undefined") return "system";
  const stored = localStorage.getItem(STORAGE_KEY);
  if (stored === "light" || stored === "dark" || stored === "system") return stored;
  // 未手动选择过：跟随系统
  return "system";
}

function systemPrefersDark(): boolean {
  return typeof window !== "undefined" && window.matchMedia(SYSTEM_DARK_QUERY).matches;
}

function resolveTheme(theme: Theme): "light" | "dark" {
  if (theme === "system") return systemPrefersDark() ? "dark" : "light";
  return theme;
}

function applyTheme(theme: Theme) {
  const root = document.documentElement;
  const effective = resolveTheme(theme);
  root.classList.toggle("dark", effective === "dark");
  syncThemeIcons(effective);
}

export function useTheme() {
  const [theme, setTheme] = useState<Theme>(getStoredTheme);

  useEffect(() => {
    applyTheme(theme);
    localStorage.setItem(STORAGE_KEY, theme);
    // system 模式下跟随系统深浅实时切换；其余模式该监听不改变外观
    const mq = window.matchMedia(SYSTEM_DARK_QUERY);
    const onChange = () => applyTheme(theme);
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, [theme]);

  const toggle = useCallback(() => {
    // 浅色 → 深色 → 跟随系统 → 浅色
    setTheme((prev) => (prev === "light" ? "dark" : prev === "dark" ? "system" : "light"));
  }, []);

  return { theme, toggle, setTheme };
}

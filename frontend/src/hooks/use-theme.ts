/**
 * useTheme — 深浅模式切换 Hook
 *
 * 通过在 <html> 上切换 .dark class 实现，
 * 持久化到 localStorage。
 */
import { useState, useEffect, useCallback } from "react";

type Theme = "light" | "dark";

const STORAGE_KEY = "luminbuddy-theme";

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

function syncThemeIcons(theme: Theme) {
  const assets = THEME_ICON_ASSETS[theme];
  document.querySelector<HTMLLinkElement>("#app-favicon-svg")?.setAttribute("href", assets.svg);
  document.querySelector<HTMLLinkElement>("#app-favicon-png")?.setAttribute("href", assets.png);
  document.querySelector<HTMLLinkElement>("#apple-touch-icon")?.setAttribute("href", assets.apple);
}

function getInitialTheme(): Theme {
  if (typeof window === "undefined") return "light";
  const stored = localStorage.getItem(STORAGE_KEY);
  if (stored === "light" || stored === "dark") return stored;
  return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

function applyTheme(theme: Theme) {
  const root = document.documentElement;
  if (theme === "dark") {
    root.classList.add("dark");
  } else {
    root.classList.remove("dark");
  }
  syncThemeIcons(theme);
}

export function useTheme() {
  const [theme, setTheme] = useState<Theme>(getInitialTheme);

  useEffect(() => {
    applyTheme(theme);
    localStorage.setItem(STORAGE_KEY, theme);
  }, [theme]);

  const toggle = useCallback(() => {
    setTheme((prev) => (prev === "dark" ? "light" : "dark"));
  }, []);

  return { theme, toggle, setTheme };
}

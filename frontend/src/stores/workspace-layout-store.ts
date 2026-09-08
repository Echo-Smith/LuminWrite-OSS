import { create } from "zustand";

export type DetailPanelState = "expanded" | "collapsed" | "drawer";
/**
 * 详情面板三个合并 tab：文档（大纲+版本）/ 材料 / 运行（运行记录+质量验收）。
 * 旧值 "quality"→"run"、"versions"→"outline" 在读取时迁移（v5 布局键内的
 * 历史偏好不丢）。
 */
export type DetailTab = "outline" | "materials" | "run";
export type ComposerWidthState = "wide" | "compact";

export interface WorkspaceLayoutPreference {
  detailPanel: DetailPanelState;
  detailTab: DetailTab;
  composerWidth: ComposerWidthState;
}

export interface WorkspaceLayoutScope {
  userId: string;
  deviceId: string;
  workspaceId: string;
  documentId: string;
}

export interface LayoutStorage {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}

export const defaultWorkspaceLayout: WorkspaceLayoutPreference = {
  detailPanel: "expanded",
  detailTab: "outline",
  composerWidth: "wide",
};

const DETAIL_STATES = new Set(["expanded", "collapsed", "drawer"]);
const DETAIL_TABS = new Set(["outline", "materials", "run"]);
const COMPOSER_WIDTHS = new Set(["wide", "compact"]);

/** 历史五 tab → 合并三 tab 的迁移（quality 并入 run，versions 并入 outline）。 */
const DETAIL_TAB_MIGRATION: Record<string, DetailTab> = { quality: "run", versions: "outline" };

function normalizeDetailTab(value: string | undefined): DetailTab {
  if (value && DETAIL_TABS.has(value)) return value as DetailTab;
  if (value && DETAIL_TAB_MIGRATION[value]) return DETAIL_TAB_MIGRATION[value];
  return defaultWorkspaceLayout.detailTab;
}

export function layoutStorageKey(scope: WorkspaceLayoutScope): string {
  return ["lumin-writing-layout-v5", scope.userId, scope.deviceId, scope.workspaceId, scope.documentId]
    .map(encodeURIComponent)
    .join(":");
}

function normalizeLayout(value: Partial<WorkspaceLayoutPreference> | null | undefined): WorkspaceLayoutPreference {
  return {
    detailPanel: DETAIL_STATES.has(value?.detailPanel ?? "") ? value!.detailPanel! : defaultWorkspaceLayout.detailPanel,
    detailTab: normalizeDetailTab(value?.detailTab),
    composerWidth: COMPOSER_WIDTHS.has(value?.composerWidth ?? "") ? value!.composerWidth! : defaultWorkspaceLayout.composerWidth,
  };
}

export function loadLayoutPreference(storage: LayoutStorage | null, scope: WorkspaceLayoutScope): WorkspaceLayoutPreference {
  if (!storage) return { ...defaultWorkspaceLayout };
  try {
    return normalizeLayout(JSON.parse(storage.getItem(layoutStorageKey(scope)) ?? "null") as Partial<WorkspaceLayoutPreference> | null);
  } catch {
    return { ...defaultWorkspaceLayout };
  }
}

export function saveLayoutPreference(storage: LayoutStorage | null, scope: WorkspaceLayoutScope, value: WorkspaceLayoutPreference): void {
  storage?.setItem(layoutStorageKey(scope), JSON.stringify(normalizeLayout(value)));
}

function browserStorage(): LayoutStorage | null {
  return typeof window === "undefined" ? null : window.localStorage;
}

const anonymousScope: WorkspaceLayoutScope = { userId: "anonymous", deviceId: "browser", workspaceId: "default", documentId: "new" };

interface WorkspaceLayoutActions {
  scope: WorkspaceLayoutScope;
  setScope: (scope: WorkspaceLayoutScope) => void;
  setDetailPanel: (value: DetailPanelState) => void;
  setDetailTab: (value: DetailTab) => void;
  setComposerWidth: (value: ComposerWidthState) => void;
}

export const useWorkspaceLayoutStore = create<WorkspaceLayoutPreference & WorkspaceLayoutActions>((set, get) => {
  const persist = (patch: Partial<WorkspaceLayoutPreference>) => {
    const next = normalizeLayout({ ...get(), ...patch });
    saveLayoutPreference(browserStorage(), get().scope, next);
    set(next);
  };
  return {
    ...loadLayoutPreference(browserStorage(), anonymousScope),
    scope: anonymousScope,
    setScope: (scope) => set({ scope, ...loadLayoutPreference(browserStorage(), scope) }),
    setDetailPanel: (detailPanel) => persist({ detailPanel }),
    setDetailTab: (detailTab) => persist({ detailTab }),
    setComposerWidth: (composerWidth) => persist({ composerWidth }),
  };
});

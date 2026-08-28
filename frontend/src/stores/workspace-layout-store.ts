import { create } from "zustand";

export type GlobalSidebarState = "expanded" | "collapsed";
export type DetailPanelState = "expanded" | "collapsed" | "drawer";
export type DetailTab = "outline" | "materials" | "run" | "quality" | "versions";
export type ConversationPanelState = "expanded" | "compact" | "minimized";

export interface WorkspaceLayoutPreference {
  globalSidebar: GlobalSidebarState;
  detailPanel: DetailPanelState;
  detailTab: DetailTab;
  conversationPanel: ConversationPanelState;
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
  globalSidebar: "expanded",
  detailPanel: "expanded",
  detailTab: "outline",
  conversationPanel: "expanded",
};

const SIDEBAR_STATES = new Set(["expanded", "collapsed"]);
const DETAIL_STATES = new Set(["expanded", "collapsed", "drawer"]);
const DETAIL_TABS = new Set(["outline", "materials", "run", "quality", "versions"]);
const CONVERSATION_STATES = new Set(["expanded", "compact", "minimized"]);

export function layoutStorageKey(scope: WorkspaceLayoutScope): string {
  return ["lumin-writing-layout-v1", scope.userId, scope.deviceId, scope.workspaceId, scope.documentId]
    .map(encodeURIComponent)
    .join(":");
}

function normalizeLayout(value: Partial<WorkspaceLayoutPreference> | null | undefined): WorkspaceLayoutPreference {
  return {
    globalSidebar: SIDEBAR_STATES.has(value?.globalSidebar ?? "") ? value!.globalSidebar! : defaultWorkspaceLayout.globalSidebar,
    detailPanel: DETAIL_STATES.has(value?.detailPanel ?? "") ? value!.detailPanel! : defaultWorkspaceLayout.detailPanel,
    detailTab: DETAIL_TABS.has(value?.detailTab ?? "") ? value!.detailTab! : defaultWorkspaceLayout.detailTab,
    conversationPanel: CONVERSATION_STATES.has(value?.conversationPanel ?? "") ? value!.conversationPanel! : defaultWorkspaceLayout.conversationPanel,
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
  setGlobalSidebar: (value: GlobalSidebarState) => void;
  setDetailPanel: (value: DetailPanelState) => void;
  setDetailTab: (value: DetailTab) => void;
  setConversationPanel: (value: ConversationPanelState) => void;
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
    setGlobalSidebar: (globalSidebar) => persist({ globalSidebar }),
    setDetailPanel: (detailPanel) => persist({ detailPanel }),
    setDetailTab: (detailTab) => persist({ detailTab }),
    setConversationPanel: (conversationPanel) => persist({ conversationPanel }),
  };
});

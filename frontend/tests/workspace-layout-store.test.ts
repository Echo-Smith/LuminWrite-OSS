import assert from "node:assert/strict";
import test from "node:test";

import {
  defaultWorkspaceLayout,
  layoutStorageKey,
  loadLayoutPreference,
  saveLayoutPreference,
  type LayoutStorage,
  type WorkspaceLayoutScope,
} from "../src/stores/workspace-layout-store.ts";

class MemoryStorage implements LayoutStorage {
  values = new Map<string, string>();
  getItem(key: string) { return this.values.get(key) ?? null; }
  setItem(key: string, value: string) { this.values.set(key, value); }
}

const scope = (documentId: string): WorkspaceLayoutScope => ({
  userId: "user-1", deviceId: "macbook", workspaceId: "desk-1", documentId,
});

test("layout preferences persist independently per document scope", () => {
  const storage = new MemoryStorage();
  saveLayoutPreference(storage, scope("doc-a"), { ...defaultWorkspaceLayout, globalSidebar: "collapsed", conversationPanel: "minimized" });
  saveLayoutPreference(storage, scope("doc-b"), { ...defaultWorkspaceLayout, detailTab: "quality", detailPanel: "drawer" });

  assert.equal(loadLayoutPreference(storage, scope("doc-a")).globalSidebar, "collapsed");
  assert.equal(loadLayoutPreference(storage, scope("doc-a")).detailTab, "outline");
  assert.equal(loadLayoutPreference(storage, scope("doc-b")).detailTab, "quality");
  assert.equal(loadLayoutPreference(storage, scope("doc-b")).conversationPanel, "expanded");
  assert.notEqual(layoutStorageKey(scope("doc-a")), layoutStorageKey(scope("doc-b")));
});

test("invalid or partial persisted data fails back to safe defaults", () => {
  const storage = new MemoryStorage();
  storage.setItem(layoutStorageKey(scope("doc-a")), JSON.stringify({ detailTab: "unknown", conversationPanel: "compact" }));
  const restored = loadLayoutPreference(storage, scope("doc-a"));
  assert.equal(restored.detailTab, "outline");
  assert.equal(restored.conversationPanel, "compact");
  assert.equal(restored.globalSidebar, "expanded");
});

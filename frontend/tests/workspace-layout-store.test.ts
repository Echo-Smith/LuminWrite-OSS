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
  saveLayoutPreference(storage, scope("doc-a"), { ...defaultWorkspaceLayout, composerWidth: "compact" });
  saveLayoutPreference(storage, scope("doc-b"), { ...defaultWorkspaceLayout, detailTab: "run", detailPanel: "drawer" });

  assert.equal(loadLayoutPreference(storage, scope("doc-a")).detailTab, "outline");
  assert.equal(loadLayoutPreference(storage, scope("doc-a")).composerWidth, "compact");
  assert.equal(loadLayoutPreference(storage, scope("doc-b")).detailTab, "run");
  assert.notEqual(layoutStorageKey(scope("doc-a")), layoutStorageKey(scope("doc-b")));
});

test("legacy quality/versions tab values migrate into the merged tabs", () => {
  const storage = new MemoryStorage();
  // v5 布局键里残留的旧五 tab 偏好：quality→run，versions→outline
  storage.setItem(layoutStorageKey(scope("doc-a")), JSON.stringify({ detailTab: "quality", detailPanel: "expanded", composerWidth: "wide" }));
  storage.setItem(layoutStorageKey(scope("doc-b")), JSON.stringify({ detailTab: "versions", detailPanel: "expanded", composerWidth: "wide" }));
  assert.equal(loadLayoutPreference(storage, scope("doc-a")).detailTab, "run");
  assert.equal(loadLayoutPreference(storage, scope("doc-b")).detailTab, "outline");
});

test("invalid or partial persisted data fails back to safe defaults", () => {
  const storage = new MemoryStorage();
  storage.setItem(layoutStorageKey(scope("doc-a")), JSON.stringify({ globalSidebar: "collapsed", detailTab: "unknown", conversationPanel: "compact" }));
  const restored = loadLayoutPreference(storage, scope("doc-a"));
  assert.equal(restored.detailTab, "outline");
  assert.equal("conversationPanel" in restored, false);
  assert.equal("globalSidebar" in restored, false);
  assert.equal(restored.composerWidth, "wide");
});

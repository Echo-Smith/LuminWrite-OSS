import assert from "node:assert/strict";
import test from "node:test";

import { loadLayoutPreference, saveLayoutPreference, defaultWorkspaceLayout, type LayoutStorage } from "../src/stores/workspace-layout-store.ts";
import { initialWritingRuntimeProjection, projectWritingEvent } from "../src/stores/writing-runtime-store.ts";

test("run events never reopen a conversation the user minimized", () => {
  const values = new Map<string, string>();
  const storage: LayoutStorage = { getItem: (key) => values.get(key) ?? null, setItem: (key, value) => { values.set(key, value); } };
  const scope = { userId: "u", deviceId: "d", workspaceId: "w", documentId: "doc_1" };
  saveLayoutPreference(storage, scope, { ...defaultWorkspaceLayout, conversationPanel: "minimized", detailTab: "versions" });
  projectWritingEvent(initialWritingRuntimeProjection, {
    protocol: "lumin-writing.v2", type: "writing.quality.updated", run_id: "run_1", sequence: 1,
    timestamp: "2026-08-29T00:00:00Z", status: "running",
    payload: { report_id: "report_1", quality_state: "candidate_draft", achieved_assurance: "standard" },
  });
  const restored = loadLayoutPreference(storage, scope);
  assert.equal(restored.conversationPanel, "minimized");
  assert.equal(restored.detailTab, "versions");
});

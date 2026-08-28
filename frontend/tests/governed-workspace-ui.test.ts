import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const source = (path: string) => readFileSync(new URL(path, import.meta.url), "utf8");

test("the writing workspace keeps the document ahead of conversation and details", () => {
  const page = source("../src/pages/writing-workspace.tsx");
  const documentPosition = page.indexOf("<DocumentSurface");
  const conversationPosition = page.indexOf("<ConversationDock");
  assert.ok(documentPosition > 0);
  assert.ok(conversationPosition > documentPosition);
  assert.match(page, /<Sidebar/);
  assert.match(page, /<RunSummaryStrip/);
  assert.match(page, /<DetailPanel governed/);
});

test("the detail surface exposes the five governed document tabs", () => {
  const detail = source("../src/components/runtime/run-detail-tabs.tsx");
  for (const tab of ["outline", "materials", "run", "quality", "versions"]) {
    assert.match(detail, new RegExp(`value: "${tab}"`));
  }
});

test("runtime progress never forces open legacy timelines or layout panels", () => {
  const timeline = source("../src/components/tools/compact-step-timeline.tsx");
  const page = source("../src/pages/writing-workspace.tsx");
  assert.doesNotMatch(timeline, /setOpen\(true\)/);
  assert.doesNotMatch(page, /workflowStatus[^\n]+setDetailPanel/);
  assert.doesNotMatch(page, /run\?\.status[^\n]+setConversationPanel/);
});

test("automatic and explicit orchestration remain user-selectable", () => {
  const picker = source("../src/components/composer/mode-picker.tsx");
  for (const mode of ["auto", "fast", "outline_first", "sourced", "strict_research"]) {
    assert.match(picker, new RegExp(`value: "${mode}"`));
  }
});

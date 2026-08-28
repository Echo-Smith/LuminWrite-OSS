import assert from "node:assert/strict";
import test from "node:test";

import {
  APPROVAL_MODES,
  ASSURANCE_LEVELS,
  ORCHESTRATION_MODES,
  QUALITY_STATES,
  TASK_MODES,
  lifecycleForEvent,
  resolveExecutionControls,
} from "../src/lib/writing-runtime-types.ts";

test("governed writing controls expose stable enums", () => {
  assert.deepEqual(TASK_MODES, ["auto", "writing", "guided", "polish"]);
  assert.deepEqual(ORCHESTRATION_MODES, ["auto", "fast", "outline_first", "sourced", "strict_research"]);
  assert.deepEqual(ASSURANCE_LEVELS, ["flexible", "standard", "sourced", "strict"]);
  assert.deepEqual(APPROVAL_MODES, ["conditional", "always", "auto"]);
});

test("an explicit user choice wins over a system recommendation", () => {
  const resolved = resolveExecutionControls(
    {
      orchestrationMode: { value: "outline_first", source: "user" },
      assuranceLevel: { value: "strict", source: "user" },
    },
    { orchestrationMode: "fast", assuranceLevel: "flexible", approvalMode: "auto" },
  );
  assert.deepEqual(resolved.orchestrationMode, { value: "outline_first", source: "user" });
  assert.deepEqual(resolved.assuranceLevel, { value: "strict", source: "user" });
  assert.deepEqual(resolved.approvalMode, { value: "auto", source: "system_inference" });
});

test("document lifecycle and quality states are not conflated", () => {
  assert.equal(lifecycleForEvent("writing.document.delta"), "provisional");
  assert.equal(lifecycleForEvent("writing.document.committed"), "committed");
  assert.deepEqual(QUALITY_STATES, ["candidate_draft", "accepted_draft", "verified_deliverable"]);
});

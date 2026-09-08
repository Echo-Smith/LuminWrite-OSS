import assert from "node:assert/strict";
import test from "node:test";
import { isResearchMockEnabled, isResearchReviewEnabled, startResearchRun, buildResearchSpec, defaultResearchSpecDraft } from "../src/lib/research-api.ts";

test("an unconfigured build never starts a fabricated research run", async () => {
  assert.equal(isResearchMockEnabled(), false);
  assert.equal(isResearchReviewEnabled(), false);
  const { spec } = buildResearchSpec({ ...defaultResearchSpecDraft(), central_question: "测试研究问题" });
  assert.ok(spec);
  await assert.rejects(startResearchRun(spec), /尚未接入真实后端/);
});

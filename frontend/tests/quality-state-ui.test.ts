import assert from "node:assert/strict";
import test from "node:test";

import { QUALITY_STATE_COPY, QUALITY_STATES } from "../src/lib/writing-runtime-types.ts";

test("all three production quality states have plain-language user copy", () => {
  for (const state of QUALITY_STATES) {
    assert.ok(QUALITY_STATE_COPY[state].label.length > 0);
    assert.ok(QUALITY_STATE_COPY[state].description.length > 0);
  }
  assert.equal(QUALITY_STATE_COPY.verified_deliverable.label, "已验证成稿");
});

import assert from "node:assert/strict";
import test from "node:test";

import {
  WABENCH_WORKSPACES,
  formatWABenchLatency,
  formatWABenchPercent,
  formatWABenchScore,
  gateDecisionPriority,
  normalizeWABenchError,
  privacyLabel,
  reviewsDisagree,
  shortWABenchHash,
} from "../src/lib/wabench-eval.ts";
import type { WABenchReviewItem } from "../src/lib/admin-types.ts";

test("seven workspaces follow the product decision flow", () => {
  assert.deepEqual(WABENCH_WORKSPACES.map((item) => item.key), [
    "overview", "datasets", "candidates", "runs", "reviews", "badcases", "release",
  ]);
});

test("metric formatting preserves unavailable state", () => {
  assert.equal(formatWABenchScore(null), "—");
  assert.equal(formatWABenchScore(82.456), "82.5");
  assert.equal(formatWABenchPercent(0.875), "87.5%");
  assert.equal(formatWABenchLatency(950), "950 ms");
  assert.equal(formatWABenchLatency(2450), "2.5 s");
  assert.equal(shortWABenchHash("sha256:1234567890abcdef"), "1234567…0abcdef");
});

test("release decisions sort dangerous states first", () => {
  assert.ok(gateDecisionPriority("rollback") > gateDecisionPriority("fail"));
  assert.ok(gateDecisionPriority("fail") > gateDecisionPriority("conditional"));
  assert.ok(gateDecisionPriority("pass") > gateDecisionPriority(""));
});

test("privacy labels make masking explicit", () => {
  assert.equal(privacyLabel("public"), "可公开");
  assert.match(privacyLabel("private"), /正文遮罩/);
});

function review(overrides: Partial<WABenchReviewItem>): WABenchReviewItem {
  return {
    reviewId: "review-a", runId: "run-1", outputId: "output-1", caseId: "case-1",
    outputStatus: "complete", textStorage: "hash_only", privacyLevel: "private",
    contentAvailable: false, reviewerId: "a", reviewerRole: "产品经理", reviewerType: "human",
    reviewMethod: "human_excel", labelSource: "holdout", isBlind: true,
    taskCompliance: 4, sourceFidelity: 4, structureReasoning: 4, styleConsistency: 4,
    directUsability: 4, acceptanceLabel: "light_edit", hardFailureIds: [],
    secondaryRootCauses: [], evidence: {}, arbitrationStatus: "pending", isArbitration: false,
    reviewedAt: "2026-08-19T10:00:00Z", ...overrides,
  };
}

test("review disagreement remains visible until arbitration", () => {
  assert.equal(reviewsDisagree([
    review({ reviewId: "a" }),
    review({ reviewId: "b", reviewerId: "b", sourceFidelity: 2, acceptanceLabel: "heavy_edit" }),
  ]), true);
  assert.equal(reviewsDisagree([review({ reviewId: "a" })]), false);
});

test("structured workbook errors retain row and column", () => {
  const result = normalizeWABenchError({
    error: { code: "invalid_review_workbook", message: "校验失败" },
    data: { errors: [{ row: 3, column: "来源忠实度", message: "必须是 1—5" }] },
  });
  assert.equal(result.code, "invalid_review_workbook");
  assert.deepEqual(result.details[0], { row: 3, column: "来源忠实度", message: "必须是 1—5" });
});

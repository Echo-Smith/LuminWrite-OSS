import assert from "node:assert/strict";
import test from "node:test";
import { isResearchMockEnabled, isResearchReviewEnabled, startResearchRun, buildResearchSpec, defaultResearchSpecDraft } from "../src/lib/research-api.ts";

test("an unconfigured build never starts a fabricated research run", async () => {
  assert.equal(isResearchMockEnabled(), false);
  assert.equal(isResearchReviewEnabled(), false);
  const { spec } = buildResearchSpec({ ...defaultResearchSpecDraft(), central_question: "测试研究问题" });
  assert.ok(spec);
  // F1 后：mock=false 走真实创建链路。裸 spec（缺少 central_question/audience/length）
  // 被本地校验拒绝（400 INVALID_RESEARCH_SPEC），绝不返回演示 run_id。
  await assert.rejects(startResearchRun(spec), (error: unknown) => {
    assert.equal((error as { code?: string }).code, "INVALID_RESEARCH_SPEC");
    assert.ok(String((error as Error).message).includes("研究问题"));
    return true;
  });
});

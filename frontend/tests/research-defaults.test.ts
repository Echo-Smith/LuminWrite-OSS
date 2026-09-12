import assert from "node:assert/strict";
import test from "node:test";
import { isResearchMockEnabled, isResearchReviewHardOff, startResearchRun, buildResearchSpec, defaultResearchSpecDraft } from "../src/lib/research-api.ts";

test("an unconfigured build never starts a fabricated research run", async () => {
  assert.equal(isResearchMockEnabled(), false);
  // 默认非硬关闭：入口交给「实验室功能」的用户勾选（settings-store，
  // 云端跟随账号）；VITE_RESEARCH_REVIEW_ENABLED=false 仍为部署级 kill switch。
  assert.equal(isResearchReviewHardOff(), false);
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

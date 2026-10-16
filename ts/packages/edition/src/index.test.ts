import { describe, expect, it } from "vitest";
import type { AuthPort } from "./index.js";
import {
  allowAllEntitlements,
  humanTouchedProposalPolicy,
  noopRealtime,
  noopUsageSink,
  ownerAuthorizer,
} from "./index.js";

describe("edition ports", () => {
  it("开源默认放行，用量可空记", async () => {
    expect(await allowAllEntitlements.allow("kb")).toBe(true);
    await expect(
      noopUsageSink.record({
        requestId: "r",
        model: "faux-1",
        inputTokens: 0,
        outputTokens: 0,
        costMicros: 0,
      }),
    ).resolves.toBeUndefined();
  });

  it("文档所有者才能改", async () => {
    const auth = ownerAuthorizer(async (id) => (id === "d1" ? "u1" : undefined));
    expect(await auth.canEdit("d1", "u1")).toBe(true);
    expect(await auth.canEdit("d1", "u2")).toBe(false);
  });

  it("AuthPort 由调用方实现，商业版可以换掉", async () => {
    const auth: AuthPort = {
      async authenticate() {
        return { type: "user", userId: "usr_1" };
      },
    };
    expect(await auth.authenticate(new Request("http://127.0.0.1/"))).toEqual({
      type: "user",
      userId: "usr_1",
    });
  });

  it("人工改过的文档默认走提案", async () => {
    expect(humanTouchedProposalPolicy.defaultApplyMode({ humanTouched: false })).toBe("auto");
    expect(humanTouchedProposalPolicy.defaultApplyMode({ humanTouched: true })).toBe("proposal");
    const seen: string[] = [];
    await noopRealtime.publish("d1", { type: "noop" });
    expect(seen).toEqual([]);
  });
});

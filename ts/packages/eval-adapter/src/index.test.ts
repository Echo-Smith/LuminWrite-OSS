import { analyzePlain } from "@luminwrite/plainspeak";
import { describe, expect, it } from "vitest";

import { runDeterministicSmoke, smokeSamples } from "./index.js";

describe("评测冒烟", () => {
  it("中性负对照在说人话轴上排最后", () => {
    const report = runDeterministicSmoke();
    expect(report.pass).toBe(true);
    expect(report.ranking.at(-1)?.id).toBe("negative");
    const negative = smokeSamples.find((sample) => sample.role === "negative");
    expect(analyzePlain(negative?.text ?? "").pass).toBe(false);
  });
});

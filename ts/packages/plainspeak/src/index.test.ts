import { describe, expect, it } from "vitest";

import { analyzePlain, isHollowPhrase } from "./index.js";

const clean = "推理成本主要由显卡时租、显存占用和批大小决定。批大小从 1 提到 8，单次请求更便宜。";
const mild = "值得注意的是，批大小会影响单价。其余句子只报数字。";
const negative =
  "在当前行业的背景下，本方案具有重要意义。它能全方位赋能业务，打造增长矩阵。抓手和底层逻辑至关重要，助力闭环。";

describe("说人话", () => {
  it("具体的句子通过，空话不通过", () => {
    expect(analyzePlain(clean).pass).toBe(true);
    expect(analyzePlain(negative).pass).toBe(false);
    expect(isHollowPhrase("赋能")).toBe(true);
    expect(isHollowPhrase("显卡时租")).toBe(false);
  });

  it("中性负对照在说人话轴上排最后", () => {
    const ranked = [clean, mild, negative]
      .map((text) => ({ text, report: analyzePlain(text) }))
      .sort((a, b) => a.report.hollowScore - b.report.hollowScore);
    expect(ranked[ranked.length - 1]?.text).toBe(negative);
    expect(ranked[0]?.report.pass).toBe(true);
  });
});

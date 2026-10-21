import { describe, expect, it } from "vitest";

import { flagClaims } from "./claims.js";
import { normalizeCitationText, verifyQuote } from "./verify.js";

const BASE = "甲乙丙丁戊己庚辛壬癸子丑寅卯辰巳午未申酉";

describe("引用校验", () => {
  it("空白和全半角折成同一段", () => {
    expect(normalizeCitationText("ＡＢＣ，　甲乙")).toBe("ABC, 甲乙");
    const check = verifyQuote({
      quote: "甲乙丙丁\n戊己庚辛",
      corpus: "甲乙丙丁 戊己庚辛",
    });
    expect(check.status).toBe("verified");
    const wide = verifyQuote({
      quote: "ＡＢＣ，价格是 12 元。",
      corpus: "前缀 ABC,价格是 12 元。后缀",
    });
    expect(wide.status).toBe("verified");
  });

  it("精确、十分之一以内、找不到、来源改了或块没了", () => {
    expect([...BASE]).toHaveLength(20);
    const fuzzyQuote = `${BASE.slice(0, 18)}戌亥`;
    const fuzzy = verifyQuote({ quote: fuzzyQuote, corpus: `前文${BASE}后文` });
    expect(fuzzy.status).toBe("fuzzy");
    expect(fuzzy.distance).toBeLessThanOrEqual(2);

    const missed = verifyQuote({
      quote: `${BASE.slice(0, 17)}戌亥子`,
      corpus: BASE,
    });
    expect(missed.status).toBe("not_found");

    const gone = verifyQuote({
      quote: BASE,
      corpus: "完全换掉的另一段正文。",
      revised: true,
    });
    expect(gone).toMatchObject({ status: "mismatch", note: "来源已修改" });

    const survived = verifyQuote({ quote: BASE, corpus: BASE, revised: true });
    expect(survived.status).toBe("verified");

    expect(verifyQuote({ quote: BASE, corpus: null }).note).toBe("来源已不存在");
    expect(verifyQuote({ quote: BASE, corpus: BASE, blockMissing: true }).note).toBe("块已不存在");
  });

  it("长文里改一个字仍然是 fuzzy", () => {
    const corpus = `${"测".repeat(400)}${BASE}`;
    const quote = `${BASE.slice(0, 10)}改${BASE.slice(11)}`;
    expect(verifyQuote({ quote, corpus }).status).toBe("fuzzy");
  });

  it("写作里的未核对断言和没有证据的档案引用会被标出", () => {
    const flags = flagClaims({
      documentId: "doc_1",
      blocks: [
        { blockId: "b1", text: "批大小从 1 提到 8 时，单次请求分到的固定开销会下降。" },
        { blockId: "b2", text: "因此可以断定成本下降了 30%。" },
        { blockId: "b3", text: "见原文。[[cite:evd_missing]]" },
        { blockId: "b4", text: "这句对得上。[[cite:evd_ok]]" },
      ],
      dossier: {
        citations: [
          { title: "时租价目", locator: "2026-10" },
          { title: "已核对", locator: "", evidenceId: "evd_ok" },
        ],
      },
      evidence: [{ id: "evd_ok", citingDocumentId: "doc_1", status: "verified" }],
    });
    expect(flags.map((flag) => flag.note)).toEqual([
      "档案引用没有 evidenceId",
      "这句话有数字或强判断，但没有 [[cite:]]",
      "正文引用没有对应证据",
    ]);
    expect(flags.some((flag) => flag.blockId === "b1" || flag.blockId === "b4")).toBe(false);
  });
});

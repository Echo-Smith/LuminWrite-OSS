import { describe, expect, it } from "vitest";

import {
  ArticleQueryError,
  normalizeTagName,
  parseArticleListQuery,
  phase,
  TagNameError,
} from "./index.js";

describe("文章库查询", () => {
  it("阶段不再是空桩", () => {
    expect(phase).toBe("library");
  });

  it("标签名称去掉多余空白，拒绝空的和带逗号的", () => {
    expect(normalizeTagName("  时租   成本  ")).toBe("时租 成本");
    expect(() => normalizeTagName("  ")).toThrow(TagNameError);
    expect(() => normalizeTagName("a,b")).toThrow(TagNameError);
    expect(() => normalizeTagName("x".repeat(41))).toThrow(TagNameError);
  });

  it("列表参数：未保存不靠状态列，归档和标签能分开", () => {
    expect(
      parseArticleListQuery({
        q: " 小时 ",
        tag: ["时租,成本", "时租"],
        genre: "评论",
        via: "import",
        archived: "archived",
        kb: "excluded",
        limit: "10",
      }),
    ).toEqual({
      text: "小时",
      tags: ["时租", "成本"],
      genre: "评论",
      createdVia: "import",
      archived: "archived",
      kbExcluded: true,
      limit: 10,
    });
    expect(parseArticleListQuery({}).archived).toBe("active");
    expect(parseArticleListQuery({ archived: "all" }).archived).toBe("all");
    expect(() => parseArticleListQuery({ via: "rss" })).toThrow(ArticleQueryError);
    expect(() => parseArticleListQuery({ archived: "gone" })).toThrow(ArticleQueryError);
    expect(() => parseArticleListQuery({ limit: "0" })).toThrow(ArticleQueryError);
  });
});

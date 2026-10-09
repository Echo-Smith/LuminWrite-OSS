/**
 * 实验室「研究综述」开关移除测试：
 * - 研究综述已产品化为第四写作流程（flow-picker 无条件渲染），实验室不再有该开关；
 * - settings-store 不再持有 enableResearchReview 字段与同步键；
 * - labs-section 源码无该条目（防止回潮）。
 */
import assert from "node:assert/strict";
import test from "node:test";
import { readFileSync } from "node:fs";

const source = (path: string) => readFileSync(new URL(path, import.meta.url), "utf8");

const labs = source("../src/pages/personal/labs-section.tsx");
const settings = source("../src/stores/settings-store.ts");

test("实验室不再提供研究综述开关", () => {
  assert.doesNotMatch(labs, /研究综述/);
  assert.doesNotMatch(labs, /enableResearchReview/);
});

test("settings-store 不再持有 enableResearchReview", () => {
  assert.doesNotMatch(settings, /enableResearchReview/);
  assert.doesNotMatch(settings, /enable_research_review/);
  // 其余实验室开关仍在（防止误删）
  assert.match(settings, /labsCronPanel/);
  assert.match(settings, /labsKbMaintenance/);
});

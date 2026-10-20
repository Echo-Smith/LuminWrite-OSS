import { analyzePlain, isHollowPhrase } from "@luminwrite/plainspeak";

const ORG_NAME = /公司|大学|研究院|研究所|委员会|集团|银行/;

/** 数字、百分比或机构名样式。人写的仍保存，Agent 候选直接不生成。 */
export function looksLikeFact(text: string): boolean {
  return /\d/.test(text) || /%|％|百分之/.test(text) || ORG_NAME.test(text);
}

/** 和说人话地板冲突的条目照存、照展示，不注入。 */
export function plainConflictOf(text: string): boolean {
  return isHollowPhrase(text) || !analyzePlain(text).pass;
}

export const FACT_HINT = "这像事实，建议写进文档档案的要求里";

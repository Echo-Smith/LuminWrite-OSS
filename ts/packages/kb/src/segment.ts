import { createRequire } from "node:module";
import { Jieba } from "@node-rs/jieba";

const require = createRequire(import.meta.url);
const { dict } = require("@node-rs/jieba/dict") as { dict: Uint8Array };

const jieba = Jieba.withDict(dict);

/** 检索和入库用同一套分词，空格连接后交给 simple 全文配置。 */
export function segment(text: string): string[] {
  const tokens: string[] = [];
  for (const raw of jieba.cut(text, true)) {
    const token = raw.trim().toLowerCase();
    if (!token) continue;
    if (!/^[\p{L}\p{N}_]+$/u.test(token)) continue;
    tokens.push(token);
  }
  return tokens;
}

export function searchText(text: string): string {
  return segment(text).join(" ");
}

import { asContentHash, type ContentHash } from "@luminwrite/contracts";

import { sha256Text } from "./sha256.js";
import { blockText, type PMNode } from "./tree.js";

/** 参与哈希的结构属性。id 是身份，不进哈希。 */
const KEY_ATTRS = ["language", "level", "start"] as const;

export function normalizeText(text: string): string {
  return text.normalize("NFC").replace(/\r\n/g, "\n").replace(/\r/g, "\n");
}

export function canonicalKeyAttrs(attrs: Record<string, unknown> | undefined): string {
  const picked: Record<string, unknown> = {};
  for (const key of KEY_ATTRS) {
    const value = attrs?.[key];
    if (value !== undefined) picked[key] = value;
  }
  return JSON.stringify(picked);
}

/** 哈希载荷：NFC 文本 + 换行 + 按固定键序的关键 attrs。 */
export function hashPayload(node: PMNode): string {
  return `${normalizeText(blockText(node))}\n${canonicalKeyAttrs(node.attrs)}`;
}

export function blockHashSync(node: PMNode): ContentHash {
  return asContentHash(`sha256:${sha256Text(hashPayload(node))}`);
}

export function blockHash(node: PMNode): Promise<ContentHash> {
  return Promise.resolve(blockHashSync(node));
}
